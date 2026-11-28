package interp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// namesSeen is a call interceptor that notes each native it passes through.
type namesSeen struct{ names []string }

func (n *namesSeen) InterceptCall(ctx context.Context, name string, args []vm.Value, call vm.Callable) (vm.Value, error) {
	n.names = append(n.names, name)
	return call(ctx, args)
}

type ReplayRunSuite struct {
	suite.Suite
	seen   *namesSeen
	opened int
	closed int
	// ended, restarted and closeErr are what the log saw and what it returns on Close.
	ended, restarted bool
	closeErr         error
	recorded         []ScriptRecord
	runner           *ScriptRunner
}

func TestReplayRun(t *testing.T) { suite.Run(t, new(ReplayRunSuite)) }

func (s *ReplayRunSuite) SetupTest() {
	s.seen, s.opened, s.closed = &namesSeen{}, 0, 0
	s.ended, s.restarted, s.closeErr = false, false, nil
	s.recorded = nil
	s.runner = &ScriptRunner{
		Open:    (&opens{}).open,
		Adopted: func(context.Context) bool { return false },
		Quiet:   true,
		Phase:   func(string) func() { return func() {} },
		Host: ScriptHost{
			RegisterModules: func(_ context.Context, sess *buzz.Session, _ io.Writer) {
				mod := vm.NewMap()
				mod.MapSet("ping", vm.DirectValue("probe.ping", func(_ context.Context, args []vm.Value) (vm.Value, error) {
					return args[0], nil
				}))
				mod.MapSet("fail", vm.DirectValue("probe.fail", func(context.Context, []vm.Value) (vm.Value, error) {
					return vm.Null, errors.New("native refused")
				}))
				sess.SetNativeModule("probe", mod)
			},
			RegisterSpellDecls:     func(*buzz.Session) {},
			RegisterMagusNamespace: func(context.Context, *buzz.Session) {},
			DeclareMagusTypes:      func(*buzz.Session) error { return nil },
			RegisterSpellImports:   func(*buzz.Session) {},
		},
		Record: func(_ context.Context, _ string, rec ScriptRecord) error {
			s.recorded = append(s.recorded, rec)
			return nil
		},
		Replay: func(_ context.Context, _ string, _ ScriptRecord, restart bool) (*ReplayLog, error) {
			s.restarted = restart
			s.opened++
			return &ReplayLog{
				Interceptor: s.seen,
				DiedIn: func(error) string {
					return "#1 probe.fail"
				},
				Close: func(ended bool, _ error) error {
					s.closed++
					s.ended = ended
					return s.closeErr
				},
			}, nil
		},
	}
}

const pingScript = `
import "probe";
fun main(args: [str]) > void !> any {
    probe\ping("one");
    probe\ping("two");
}
`

func (s *ReplayRunSuite) TestARecordedRunRoutesItsCallsThroughTheLog() {
	s.Require().NoError(s.runner.RunSource(s.T().Context(), s.T().TempDir(), ScriptOptions{Record: true}, "probe.buzz", pingScript, []string{"--x"}))

	s.Equal([]string{"probe.ping", "probe.ping"}, s.seen.names)
	s.Equal(1, s.closed, "the log closes once the run ends")
	s.Require().Len(s.recorded, 1)
	s.Empty(s.recorded[0].DiedIn, "a run that succeeded died nowhere")
}

func (s *ReplayRunSuite) TestAFailedRunNamesTheCallItDiedIn() {
	const failing = `
import "probe";
fun main(args: [str]) > void !> any {
    probe\ping("one");
    probe\fail();
}
`
	err := s.runner.RunSource(s.T().Context(), s.T().TempDir(), ScriptOptions{Record: true}, "probe.buzz", failing, nil)
	s.Require().Error(err)
	s.Require().Len(s.recorded, 1)
	s.Equal("#1 probe.fail", s.recorded[0].DiedIn)
}

func (s *ReplayRunSuite) TestAnUnrecordedRunOpensNoLog() {
	s.Require().NoError(s.runner.RunSource(s.T().Context(), s.T().TempDir(), ScriptOptions{}, "probe.buzz", pingScript, nil))
	s.Zero(s.opened)
	s.Empty(s.seen.names)
	s.Empty(s.recorded)
}

func (s *ReplayRunSuite) TestATestRunKeepsNoCalls() {
	const testScript = `
import "probe";
test "pings" { probe\ping("one"); }
`
	s.Require().NoError(s.runner.RunSource(s.T().Context(), s.T().TempDir(), ScriptOptions{Test: true}, "probe.buzz", testScript, nil))
	s.Zero(s.opened)
	s.Len(s.recorded, 1, "a test run still records its output")
}

func (s *ReplayRunSuite) TestARunThatEndsOnItsOwnClosesTheLogEnded() {
	s.Require().NoError(s.runner.RunSource(s.T().Context(), s.T().TempDir(), ScriptOptions{Record: true, Restart: true}, "probe.buzz", pingScript, nil))
	s.True(s.ended)
	s.True(s.restarted, "the option reaches the opener")
}

type testRefusal struct{}

func (testRefusal) Error() string { return "diverged" }
func (testRefusal) Refused()      {}

func (s *ReplayRunSuite) TestARefusalFromCloseReplacesTheRunsError() {
	s.closeErr = fmt.Errorf("replay: %w", testRefusal{})
	const failing = `
import "probe";
fun main(args: [str]) > void !> any { probe\fail(); }
`
	err := s.runner.RunSource(s.T().Context(), s.T().TempDir(), ScriptOptions{Record: true}, "probe.buzz", failing, nil)
	s.ErrorIs(err, testRefusal{})
	s.NotContains(err.Error(), "native refused", "the refusal is the cause and the script's error only its echo")
}

func (s *ReplayRunSuite) TestARestartWithoutRecordIsRefused() {
	err := s.runner.RunSource(s.T().Context(), s.T().TempDir(), ScriptOptions{Restart: true}, "probe.buzz", pingScript, nil)
	s.ErrorContains(err, "--restart discards a replay log")
	s.ErrorContains(err, "add --record")
	s.Zero(s.opened)
}

func (s *ReplayRunSuite) TestARecordedRunWithNoStoreIsRefused() {
	s.runner.Replay = nil
	err := s.runner.RunSource(s.T().Context(), s.T().TempDir(), ScriptOptions{Record: true}, "probe.buzz", pingScript, nil)
	s.ErrorContains(err, "no replay store is wired here")
}

// opens is an opener that finds no workspace and counts how often it was asked.
type opens struct {
	mu  sync.Mutex
	n   int
	err error
}

func (o *opens) open(context.Context, string) (*ScriptWorkspace, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.n++
	return nil, o.err
}

func (o *opens) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n
}

func runnerOver(o *opens) *ScriptRunner {
	return &ScriptRunner{Open: o.open, Adopted: func(context.Context) bool { return false }}
}

func TestScriptRunner_ContextRefusesAFailedLoadUnderTheSandbox(t *testing.T) {
	o := &opens{err: errors.New("magusfile.buzz:1: broken on purpose")}
	r := runnerOver(o)

	r.Sandbox = true
	_, err := r.Context(t.Context(), t.TempDir())
	require.ErrorIs(t, err, types.WorkspaceLoadFailed)
	assert.ErrorContains(t, err, "broken on purpose")
	assert.Equal(t, 1, o.count())

	// Adopted, so the open is attempted with the sandbox off too.
	r.Sandbox = false
	r.Adopted = func(context.Context) bool { return true }
	_, err = r.Context(t.Context(), t.TempDir())
	require.NoError(t, err, "with the sandbox off a failed load still only warns")
	assert.Equal(t, 2, o.count())
}

// A script run carries one evaluation holder, which std's graph members read.
// So two magus\graph().refs calls in one script build the symbol graph once.
func TestScriptRunner_RunContextCarriesOneEvaluation(t *testing.T) {
	ctx, err := runnerOver(&opens{}).RunContext(t.Context(), t.TempDir())
	require.NoError(t, err)
	once := types.EvalOnceFromContext(ctx)
	require.NotNil(t, once, "graph reads in a script would each rebuild the graph")

	builds := 0
	for range 2 {
		_, err := once.Do("graph+symbols", func() (any, error) {
			builds++
			return nil, nil
		})
		require.NoError(t, err)
	}
	assert.Equal(t, 1, builds)
}

func TestScriptRunner_ContextCarriesTheScriptsOwnInvocationAncestor(t *testing.T) {
	ctx, err := runnerOver(&opens{}).Context(t.Context(), t.TempDir())
	require.NoError(t, err)
	ancestors := types.InvocationAncestorsFromContext(ctx)
	require.NotEmpty(t, ancestors, "a magus the script spawns would otherwise be blind to its ancestry")
	assert.True(t, strings.HasPrefix(ancestors[len(ancestors)-1], strconv.Itoa(os.Getpid())+":"), "%v", ancestors)
}
