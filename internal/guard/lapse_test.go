package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// lapseNow judges req by a judgment that runs judge's setup and then never answers, past a
// short deadline.
func lapseNow(ctx context.Context, t *testing.T, base Dependencies, req Request, setup func(context.Context)) types.GuardJudgment {
	t.Helper()
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return JudgeWithin(short, base, req, func(ctx context.Context) types.GuardJudgment {
		setup(ctx)
		<-release
		return types.GuardJudgment{Decision: decisionPass}
	})
}

// A worker's loop while every judgment lapses: it sleeps until a background task's output
// file is non-empty, then prints it. The lines only read, so the lapse ladder would pass
// them unjudged; busy-wait refuses them on every lapse, past the degrade too, graded by the
// settings the judgment loaded before it stalled.
func TestLapsedJudgmentStillRefusesAPollingLoop(t *testing.T) {
	ctx, _ := spawnFixture(t)
	capture := filepath.Join(t.TempDir(), "tasks", "bglno2daz.output")
	loops := []string{
		fmt.Sprintf("until [ -s %s ]; do sleep 5; done; cat %s", capture, capture),
		fmt.Sprintf("until grep -q '^summary:' %s; do sleep 5; done", capture),
		fmt.Sprintf("while ! test -s %s; do echo waiting; sleep 5; done", capture),
	}
	settle := func(ctx context.Context) { judgmentPhases(ctx).settle(strict(Dependencies{})) }
	// One session, so the lapses count up past the degrade.
	for i, line := range append(loops, loops[0]) {
		v := lapseNow(ctx, t, Dependencies{}, Request{Input: line, Host: "claude-code", Session: "s1"}, settle)
		assert.Equal(t, verdictWithRule(decisionDeny, string(denyRuleBusyWait)), unworded(v), "lapse %d: %s", i+1, line)
	}
}

// A judgment that lapses before it loads anything grades by the settings the last judgment
// in the cache dir applied, so a workspace that lowered busy-wait to advise keeps it lowered.
func TestLapseBeforeTheLoadGradesByTheLastAppliedSettings(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	line := "until [ -s out.txt ]; do sleep 5; done"
	req := Request{Input: line, Host: "claude-code", Session: "s1"}
	nothing := func(context.Context) {}

	assert.Equal(t, verdictWithRule(decisionDeny, string(denyRuleBusyWait)), unworded(lapseNow(ctx, t, Dependencies{}, req, nothing)),
		"with no settings on record busy-wait denies, as it does by default")

	lowered := strict(Dependencies{}).Builtins
	lowered[string(denyRuleBusyWait)] = builtin.Setting{Decision: builtin.Advise}
	rememberSettings(cacheDir, lowered)
	assert.Equal(t, string(advisoryUnfinished), lapseNow(ctx, t, Dependencies{}, req, nothing).Rule)
}

// A destructive VCS verb is refused by its own rule on a lapse, and a push is never
// degraded into running unjudged: its gate is in the run log only a finished judgment reads.
func TestLapseNeverDegradesADestructiveVerbOrAPush(t *testing.T) {
	ctx, _ := spawnFixture(t)
	nothing := func(context.Context) {}
	reset := "git reset --hard origin/main"
	want := Evaluate(Dependencies{}, reset)
	require.NotEmpty(t, want.Deny, "the destructive verb denies by default")
	for range lapsesBeforeDegrade + 2 {
		v := lapseNow(ctx, t, Dependencies{}, Request{Input: reset, Host: "claude-code", Session: "s1"}, nothing)
		assert.Equal(t, verdictWithRule(decisionDeny, want.RuleName()), unworded(v))
	}
	for i := range lapsesBeforeDegrade + 2 {
		v := lapseNow(ctx, t, Dependencies{}, Request{Input: "git push origin topic", Host: "claude-code", Session: "s2"}, nothing)
		assert.Equal(t, verdictWithRule(decisionDeny, string(advisoryUnfinished)), unworded(v), "lapse %d", i+1)
	}
}

// A judgment that answers in time records the settings it applied, and a lapse reads them.
func TestRememberSettingsRoundTrips(t *testing.T) {
	dir := t.TempDir()
	assert.Nil(t, recalled(t, dir))
	settings := strict(Dependencies{}).Builtins
	rememberSettings(dir, settings)
	assert.Equal(t, settings, recalled(t, dir))
	rememberSettings(dir, nil)
	assert.Nil(t, recalled(t, dir), "the defaults are recorded as none")
}

// recalled is recallSettings, failing the test when the record cannot be read.
func recalled(t *testing.T, dir string) map[string]builtin.Setting {
	t.Helper()
	settings, err := recallSettings(dir)
	require.NoError(t, err)
	return settings
}

// The settings are appended as a line per rule that changed, so a judgment that applies the
// same settings again writes nothing, and a rule no longer set ends with a removal.
func TestRememberedSettingsAreAppendedPerRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, settingsFile)
	busy := builtin.Setting{Decision: builtin.Advise}
	deny := builtin.Setting{Decision: builtin.Deny, Lines: 200, Markers: []string{"TODO", "a\tb"}, Profile: types.GuardProfileStrict}
	rememberSettings(dir, map[string]builtin.Setting{"read-navigation": deny, "busy-wait": busy})
	rememberSettings(dir, map[string]builtin.Setting{"read-navigation": deny, "busy-wait": busy})

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(b), "\n"), "an unchanged set is not written again")
	got := recalled(t, dir)
	assert.Equal(t, deny, got["read-navigation"])
	assert.Equal(t, busy, got["busy-wait"])

	rememberSettings(dir, map[string]builtin.Setting{"busy-wait": busy})
	b, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(b), `{"rule":"read-navigation","removed":true}`)
	assert.Equal(t, map[string]builtin.Setting{"busy-wait": busy}, recalled(t, dir))
}

// A JSON memo an older magus left is not read as a second format: recalling the settings fails
// naming the file to preserve, and a lapse grades by the defaults.
func TestOldSettingsJSONIsNotRead(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, oldSettingsFile)
	require.NoError(t, os.WriteFile(old, []byte(`{"busy-wait":{"decision":"deny"}}`), 0o644))

	settings, err := recallSettings(dir)
	require.ErrorIs(t, err, file.ErrLegacyStore)
	require.ErrorContains(t, err, "preserve it")
	assert.Nil(t, settings)
	assert.Nil(t, recalledSettings(dir))
}

// LapseDegradeSuite pins decision 127: past lapsesBeforeDegrade a subagent's
// write or command in a live job's checkout stays denied, and others degrade.
type LapseDegradeSuite struct {
	suite.Suite
	ctx     context.Context
	session string
	own     string
}

func TestLapseDegradeSuite(t *testing.T) {
	suite.Run(t, new(LapseDegradeSuite))
}

func (s *LapseDegradeSuite) SetupTest() {
	s.ctx, s.session = fleetFixture(s.T())
	s.own = s.T().TempDir()
	row := types.Job{ID: "w", WritePaths: []string{"std/*.go"}, State: types.StateRunning, Registered: 1,
		CheckoutRoot: s.own, Worktree: &types.JobWorktree{Root: s.own, Source: s.session}}
	s.ctx = job.WithSnapshot(s.ctx, job.Snapshot{Rows: []types.Job{row}})
}

// lapsedWrite lapses an editor Write of path by agent, or by the root
// session when agent is "".
func (s *LapseDegradeSuite) lapsedWrite(agent, path string) types.GuardJudgment {
	env := map[string]any{"session_id": "s1", "cwd": s.session, "hook_event_name": "PreToolUse",
		"tool_name": "Write", "tool_input": map[string]any{"file_path": path, "content": "package std\n"}}
	if agent != "" {
		env["agent_id"], env["agent_type"] = agent, "general-purpose"
	}
	req := Request{Input: hookJSON(s.T(), env), Host: "claude-code", Session: "s1"}
	return lapseNow(s.ctx, s.T(), Dependencies{}, req, func(context.Context) {})
}

// lapsedCommand lapses a Bash call running line from cwd, by agent or by the
// root session when agent is "".
func (s *LapseDegradeSuite) lapsedCommand(agent, cwd, line string) types.GuardJudgment {
	env := map[string]any{"session_id": "s1", "cwd": cwd, "hook_event_name": "PreToolUse",
		"tool_name": "Bash", "tool_input": map[string]any{"command": line}}
	if agent != "" {
		env["agent_id"], env["agent_type"] = agent, "general-purpose"
	}
	req := Request{Input: hookJSON(s.T(), env), Host: "claude-code", Session: "s1"}
	return lapseNow(s.ctx, s.T(), Dependencies{}, req, func(context.Context) {})
}

func (s *LapseDegradeSuite) TestDegradedLapseStillDeniesACommandInALeasedCheckout() {
	for i := range lapsesBeforeDegrade + 2 {
		v := s.lapsedCommand("a1b2c3", s.own, "make build")
		s.Equal(verdictWithRule(decisionDeny, string(advisoryUnfinished)), unworded(v), "lapse %d", i+1)
	}
}

// A subagent's host cwd stays the session's checkout; its line moves it.
func (s *LapseDegradeSuite) TestDegradedLapseStillDeniesALineThatCdsIntoALeasedCheckout() {
	line := fmt.Sprintf("cd %s && make build", s.own)
	for i := range lapsesBeforeDegrade + 2 {
		v := s.lapsedCommand("a1b2c3", s.session, line)
		s.Equal(verdictWithRule(decisionDeny, string(advisoryUnfinished)), unworded(v), "lapse %d", i+1)
	}
}

func (s *LapseDegradeSuite) TestDegradedLapseStillDeniesAnAbsoluteWriteIntoALeasedCheckout() {
	line := fmt.Sprintf("touch %s", filepath.Join(s.own, "std", "module.go"))
	for i := range lapsesBeforeDegrade + 2 {
		v := s.lapsedCommand("a1b2c3", s.session, line)
		s.Equal(decisionDeny, v.Decision, "lapse %d", i+1)
	}
}

func (s *LapseDegradeSuite) TestDegradedLapseStillDeniesABoundWorkerFromTheSessionCheckout() {
	env := map[string]any{"session_id": "s1", "cwd": s.session, "hook_event_name": "PreToolUse",
		"agent_id": "a1b2c3", "agent_type": "general-purpose",
		"tool_name": "Bash", "tool_input": map[string]any{"command": "make build"}}
	req := Request{Input: hookJSON(s.T(), env), Host: "claude-code", Session: "s1", Lease: "w"}
	for i := range lapsesBeforeDegrade + 2 {
		v := lapseNow(s.ctx, s.T(), Dependencies{}, req, func(context.Context) {})
		s.Equal(decisionDeny, v.Decision, "lapse %d", i+1)
	}
}

// A checkout `job exec` took is the job's as much as one its fork made.
func (s *LapseDegradeSuite) TestDegradedLapseStillDeniesAWriteInAnAdoptedCheckout() {
	adopted := s.T().TempDir()
	row := types.Job{ID: "a", WritePaths: []string{"std/*.go"}, State: types.StateRunning, Registered: 1, CheckoutRoot: adopted}
	s.ctx = job.WithSnapshot(s.ctx, job.Snapshot{Rows: []types.Job{row}})
	for i := range lapsesBeforeDegrade + 2 {
		v := s.lapsedWrite("a1b2c3", filepath.Join(adopted, "std", "module.go"))
		s.Equal(decisionDeny, v.Decision, "lapse %d", i+1)
	}
}

func (s *LapseDegradeSuite) TestDegradedLapseStillDeniesACommandInAnAdoptedCheckout() {
	adopted := s.T().TempDir()
	row := types.Job{ID: "a", WritePaths: []string{"std/*.go"}, State: types.StateRunning, Registered: 1, CheckoutRoot: adopted}
	s.ctx = job.WithSnapshot(s.ctx, job.Snapshot{Rows: []types.Job{row}})
	for i := range lapsesBeforeDegrade + 2 {
		v := s.lapsedCommand("a1b2c3", adopted, "make build")
		s.Equal(decisionDeny, v.Decision, "lapse %d", i+1)
	}
}

func (s *LapseDegradeSuite) TestDegradedLapsePassesACheckoutWhoseJobEnded() {
	row := types.Job{ID: "a", WritePaths: []string{"std/*.go"}, State: types.StatePass, Registered: 1, CheckoutRoot: s.own}
	s.ctx = job.WithSnapshot(s.ctx, job.Snapshot{Rows: []types.Job{row}})
	for range lapsesBeforeDegrade {
		s.Equal(decisionDeny, s.lapsedCommand("a1b2c3", s.own, "make build").Decision)
	}
	s.Equal(decisionAdvise, s.lapsedCommand("a1b2c3", s.own, "make build").Decision)
}

func (s *LapseDegradeSuite) TestDegradedLapsePassesACommandOutsideEveryCheckout() {
	scratch := s.T().TempDir()
	for range lapsesBeforeDegrade {
		s.Equal(decisionDeny, s.lapsedCommand("a1b2c3", scratch, "make build").Decision)
	}
	s.Equal(decisionAdvise, s.lapsedCommand("a1b2c3", scratch, "make build").Decision)
}

func (s *LapseDegradeSuite) TestDegradedLapsePassesTheRootSessionsCommandInALeasedCheckout() {
	for range lapsesBeforeDegrade {
		s.Equal(decisionDeny, s.lapsedCommand("", s.own, "make build").Decision)
	}
	s.Equal(decisionAdvise, s.lapsedCommand("", s.own, "make build").Decision)
}

func (s *LapseDegradeSuite) TestDegradedLapseStillDeniesAWriteInALeasedCheckout() {
	for i := range lapsesBeforeDegrade + 2 {
		v := s.lapsedWrite("a1b2c3", filepath.Join(s.own, "std", "module.go"))
		s.Equal(verdictWithRule(decisionDeny, string(advisoryUnfinished)), unworded(v), "lapse %d", i+1)
	}
}

func (s *LapseDegradeSuite) TestDegradedLapsePassesAWriteOutsideEveryCheckout() {
	scratch := s.T().TempDir()
	for range lapsesBeforeDegrade {
		s.Equal(decisionDeny, s.lapsedWrite("a1b2c3", filepath.Join(scratch, "notes.txt")).Decision)
	}
	s.Equal(decisionAdvise, s.lapsedWrite("a1b2c3", filepath.Join(scratch, "notes.txt")).Decision)
}

func (s *LapseDegradeSuite) TestDegradedLapsePassesTheRootSessionsWriteInALeasedCheckout() {
	for range lapsesBeforeDegrade {
		s.Equal(decisionDeny, s.lapsedWrite("", filepath.Join(s.own, "std", "module.go")).Decision)
	}
	s.Equal(decisionAdvise, s.lapsedWrite("", filepath.Join(s.own, "std", "module.go")).Decision)
}
