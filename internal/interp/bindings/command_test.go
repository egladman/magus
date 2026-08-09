package bindings

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordRunner is a service.Runner that records starts without forking a process.
type recordRunner struct{ started int }

func (r *recordRunner) Start(context.Context, spells.Service) (service.Handle, error) {
	r.started++
	return struct{}{}, nil
}
func (r *recordRunner) Stop(service.Handle) {}

func serviceOp() spells.Op {
	// bin "true" exits 0, so the non-supervised fall-through fork is harmless.
	return spells.Op{
		Kind:    spells.OpKindService,
		Command: spells.Command{Bin: "true"},
		Service: &spells.Service{Command: spells.Command{Bin: "true"}},
	}
}

// TestRunCommandResolvesCwdAgainstContext proves a command op with no explicit cwd
// runs in the context working directory (the project dir the magusfile runner sets via
// std.WithCwd), not the process cwd. This is what lets a spell op invoked from a
// subproject target - go["go-run"] from docs/ - resolve its relative paths correctly.
func TestRunCommandResolvesCwdAgainstContext(t *testing.T) {
	dir := t.TempDir()
	ctx := std.WithCwd(context.Background(), dir)

	op := spells.Op{Command: spells.Command{Bin: "sh", Args: []string{"-c", "echo hi > marker.txt"}}}
	_, err := runCommand(ctx, op, commandOpts{})
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, "marker.txt"),
		"an op with no explicit cwd must run in the context cwd, not the process cwd")
}

// TestRunCommandSupervisesServiceDependency proves runCommand routes a service op to
// the supervisor (not a foreground fork) when supervision is active - the case of a
// service reached via magus.needs.
func TestRunCommandSupervisesServiceDependency(t *testing.T) {
	rr := &recordRunner{}
	sess := service.NewSession(service.New(rr, 0), nil, nil)
	ctx := service.WithSupervision(service.WithSession(context.Background(), sess))

	_, err := runCommand(ctx, serviceOp(), commandOpts{})
	require.NoError(t, err)
	assert.Equal(t, 1, rr.started, "service dependency should be supervised, not forked")
}

// TestRunCommandForegroundsDirectService proves a service op with no active
// supervision falls through to a real fork (the directly-run case), and is not
// handed to the supervisor.
func TestRunCommandForegroundsDirectService(t *testing.T) {
	rr := &recordRunner{}
	sess := service.NewSession(service.New(rr, 0), nil, nil)
	// Session present but supervision NOT active: this is a directly-run service.
	ctx := service.WithSession(context.Background(), sess)

	_, err := runCommand(ctx, serviceOp(), commandOpts{})
	require.NoError(t, err) // `true` exits 0
	assert.Equal(t, 0, rr.started, "directly-run service must foreground, not be supervised")
}

// TestExecCommandReportsCancellationNotExitCode proves a child killed because the
// RUN was cancelled reports the cancellation, not a verdict on the tool. A killed
// process has no exit code of its own - ExitCode() is -1 - so synthesizing "exited
// 1" for it is indistinguishable from the tool genuinely failing, and is printed
// with a `reproduce:` line that does not reproduce. That is the shape that made a
// sibling project's failure surface as `docs content-generate: go exited 1` with no
// stderr, in a batch where docs alone passes.
func TestExecCommandReportsCancellationNotExitCode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)

	_, err := execCommand(ctx, t.TempDir(), "sh", []string{"-c", "sleep 30"}, nil, "", true)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled,
		"a child killed by the run's cancellation must report the cancellation")
	assert.NotContains(t, err.Error(), "exited",
		"a killed child has no exit code of its own to report")
}

// TestExecCommandReportsRealExitCode is the other half: a tool that fails on its own
// still reports its own exit code, so the cancellation check above cannot swallow a
// genuine failure.
func TestExecCommandReportsRealExitCode(t *testing.T) {
	_, err := execCommand(context.Background(), t.TempDir(), "sh", []string{"-c", "exit 3"}, nil, "", true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sh exited 3")
}

// TestAdviceFor pins the failure-classification rule a command op declares. Declaration
// order is the precedence, so a spell author reads the outcome off the file.
func TestAdviceFor(t *testing.T) {
	hints := []spells.Hint{
		{Contains: "denied: requested access", Advise: "specific"},
		{Contains: "denied", Advise: "general"},
	}
	t.Run("first declared match wins", func(t *testing.T) {
		assert.Equal(t, "specific", adviceFor(hints, "error: denied: requested access to the resource"),
			"declaration order is the precedence, not match length")
	})
	t.Run("falls through to a later rule", func(t *testing.T) {
		assert.Equal(t, "general", adviceFor(hints, "push denied by policy"))
	})
	t.Run("no match is silent", func(t *testing.T) {
		assert.Empty(t, adviceFor(hints, "compilation failed"), "an unrecognized failure must not invent advice")
	})
	t.Run("an empty Contains never fires", func(t *testing.T) {
		// strings.Contains(x, "") is TRUE, so an unguarded empty rule would advise on
		// every failure of the command. decode rejects it; this covers a value built in Go.
		assert.Empty(t, adviceFor([]spells.Hint{{Advise: "no contains set"}, {Contains: "x"}}, "anything x"))
	})
	// The bug this signature exists to prevent: stdout ending mid-word and stderr
	// beginning mid-word must not be joined into a match that appeared in neither.
	t.Run("a match cannot straddle two streams", func(t *testing.T) {
		h := []spells.Hint{{Contains: "unauthorized", Advise: "should not fire"}}
		assert.Empty(t, adviceFor(h, "...ends with unauth", "orized: denied..."),
			"sources are matched independently, never concatenated")
	})
	t.Run("either stream alone can match", func(t *testing.T) {
		h := []spells.Hint{{Contains: "unauthorized", Advise: "fires"}}
		assert.Equal(t, "fires", adviceFor(h, "unauthorized", ""), "stdout")
		assert.Equal(t, "fires", adviceFor(h, "", "unauthorized"), "stderr")
	})
}

// TestOutputTailKeepsTheEnd covers the bounded buffer advice is matched against. The tail is
// what matters: a tool's explanation of why it failed is the last thing it prints.
func TestOutputTailKeepsTheEnd(t *testing.T) {
	tail := newOutputTail(8)
	n, err := tail.Write([]byte("0123456789"))
	require.NoError(t, err)
	assert.Equal(t, 10, n, "must report the full write so a MultiWriter does not short-circuit the real stream")
	assert.Equal(t, "23456789", tail.String(), "keeps the LAST limit bytes")

	tail.Write([]byte("abc"))
	assert.Equal(t, "56789abc", tail.String(), "still bounded after a second write")

	small := newOutputTail(64)
	small.Write([]byte("short"))
	assert.Equal(t, "short", small.String(), "under the limit is kept whole")
}

// TestNewOutputTailFloorsLimit: a zero limit would make Write discard everything, so advice
// would silently never fire.
func TestNewOutputTailFloorsLimit(t *testing.T) {
	tail := newOutputTail(0)
	tail.Write([]byte("kept"))
	assert.Equal(t, "kept", tail.String())
}

// hintOp is a command that always fails with text a declared hint matches.
func hintOp() spells.Op {
	return spells.Op{Command: spells.Command{
		Bin:   "sh",
		Args:  []string{"-c", "echo 'denied: authentication required' >&2; exit 1"},
		Hints: []spells.Hint{{Contains: "authentication required", Advise: "run docker login"}},
	}}
}

// TestRunCommandAdvisesOnRealFailure covers the WIRING - the tee, the fire condition, and
// the sink - rather than adviceFor in isolation. Every defect the review found lived in
// these lines and none of them were reachable from a unit test of the matcher.
func TestRunCommandAdvisesOnRealFailure(t *testing.T) {
	run6 := func(ctx context.Context, op spells.Op) string {
		var stderr bytes.Buffer
		ctx = std.WithCwd(ctx, t.TempDir())
		// The advice must ride the run's OWN writers: that is what puts it in the
		// persisted log and the output ref rather than only on a terminal.
		ctx = run.WithOutputWriters(ctx, io.Discard, &stderr)
		_, _ = runCommand(ctx, op, commandOpts{})
		return stderr.String()
	}

	t.Run("a real non-zero exit advises through the run writers", func(t *testing.T) {
		got := run6(context.Background(), hintOp())
		assert.Contains(t, got, "run docker login", "advice must reach the run's stderr writer")
		assert.Contains(t, got, "authentication required", "the tool's own error still streams")
	})

	t.Run("a cancelled run advises nothing", func(t *testing.T) {
		// The critical case: run.Exec joins ctx.Err() into its error, so gating on err
		// alone told a user who pressed Ctrl-C to go and authenticate.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assert.NotContains(t, run6(ctx, hintOp()), "run docker login",
			"a cancelled run must not be diagnosed as an authentication failure")
	})

	t.Run("a success advises nothing", func(t *testing.T) {
		ok := spells.Op{Command: spells.Command{
			Bin:   "sh",
			Args:  []string{"-c", "echo 'authentication required'"},
			Hints: hintOp().Hints,
		}}
		assert.NotContains(t, run6(context.Background(), ok), "run docker login",
			"matching text on a PASSING command must not advise")
	})

	t.Run("an op declaring no hints installs no tee", func(t *testing.T) {
		bare := spells.Op{Command: spells.Command{Bin: "sh", Args: []string{"-c", "echo hi >&2; exit 1"}}}
		assert.NotContains(t, run6(context.Background(), bare), "hint:")
	})
}
