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

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestSleepPollRefusesASleepBeforeAStatusRead(t *testing.T) {
	t.Parallel()
	deps := withSetting(string(denyRuleSleepPoll), builtin.Deny)
	for _, tt := range []struct {
		line string
		want bool
	}{
		{"sleep 30; ./magus server status", true},
		{"sleep 60 && gh pr checks 12", true},
		{"sleep 45 && gh run view 99", true},
		// A settle, not a wait.
		{"sleep 5; ./magus server status", false},
		// The read came first.
		{"./magus server status; sleep 30", false},
		// A watch waits for the change itself.
		{"sleep 30; gh pr checks 12 --watch", false},
		{"sleep 30; ./magus server status --watch=15s", false},
		{"sleep 30 && echo done", false},
		{"echo 'sleep 30; magus server status'", false},
	} {
		v := Evaluate(deps, tt.line)
		if tt.want {
			assert.Equal(t, denyRuleSleepPoll, v.Rule.Name, tt.line)
			assert.Contains(t, v.Deny, "background the work as `magus job exec --timeout <d> -- <command>` and wait for its notice", tt.line)
		} else {
			assert.NotEqual(t, denyRuleSleepPoll, v.Rule.Name, tt.line)
		}
	}
	// It denies by default too.
	v := Evaluate(Dependencies{}, "sleep 30; ./magus server status")
	assert.Equal(t, denyRuleSleepPoll, v.Rule.Name)
	assert.NotEmpty(t, v.Deny)
}

func TestSleepTimerLine(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		line string
		want time.Duration
		ok   bool
	}{
		{"sleep 540", 540 * time.Second, true},
		{"sleep 300 && echo tick", 300 * time.Second, true},
		{"sleep 2m; echo tick; sleep 20", 2 * time.Minute, true},
		{"sleep 120 && tail -5 out.log", 120 * time.Second, true},
		{"for i in 1 2 3; do sleep 60; echo tick $i; done", time.Minute, true},
		{"sleep 5; tail -f out.log", 0, false},
		{"echo tick", 0, false},
		{"sleep 30 && ./magus run test .", 0, false},
		{"./magus run test .", 0, false},
	} {
		got, ok := sleepTimerLine(tt.line, DialectBash)
		assert.Equal(t, tt.ok, ok, tt.line)
		assert.Equal(t, tt.want, got, tt.line)
	}
}

func TestSpawnWaitsFires(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		title, brief string
		want         bool
	}{
		{"orchestrator/waiter", "Wait for the a2-rules worker to finish, then report.", true},
		{"babysit the gate", "", true},
		{"orchestrator/review", "Poll until the job a1-fixes passes.", true},
		{"orchestrator/review", "Review the diff. Then wait on the other workers before you report.", true},
		{"orchestrator/impl monitor-route", "Add a /healthz route to the server.", false},
		{"orchestrator/impl paging", "Implement read paging. Do not wait for review.", false},
		{"orchestrator/scout", "Find where the stopwatch type is declared.", false},
	} {
		assert.Equal(t, tt.want, spawnWaitsFires(tt.title, tt.brief), "%s | %s", tt.title, tt.brief)
	}
}

// The pile the waiters scout found: timers a worker backgrounds to wake itself. Each is
// refused as a timer by default, and counts toward the pile when a workspace lowers
// sleep-poll to advise.
func TestBackgroundedSleepIsATimerAndCountsTowardThePile(t *testing.T) {
	ctx, _ := spawnFixture(t)
	judge := func(deps Dependencies, line string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: line, Session: "timers", Host: "claude-code", CallBackground: true})
	}

	v := judge(Dependencies{}, "sleep 540")
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleSleepPoll)}, judgedAs(v))

	lowered := withSetting(string(denyRuleSleepPoll), builtin.Advise)
	piled := Dependencies{Builtins: map[string]builtin.Setting{
		string(denyRuleSleepPoll):   {Decision: builtin.Advise},
		string(denyRuleMonitorPile): {Decision: builtin.Deny},
	}}
	for i := range monitorPileAdvise - 1 {
		v = judge(lowered, fmt.Sprintf("sleep %s && echo tick", strings.Repeat("9", i+2)))
		assert.NotEqual(t, "deny", v.Decision)
	}
	v = judge(piled, "sleep 300")
	assert.Equal(t, types.GuardJudgment{Decision: "advise", Rule: string(denyRuleMonitorPile)}, judgedAs(v),
		"the third live waiter is advised against")
	for range monitorPileDeny - monitorPileAdvise - 1 {
		judge(lowered, "sleep 301")
	}
	v = judge(piled, "sleep 302")
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleMonitorPile)}, judgedAs(v), "the fifth is refused")

	// A settle in the foreground is not a waiter.
	v = Judge(ctx, Dependencies{}, Request{Input: "sleep 2", Session: "timers", Host: "claude-code"})
	assert.Equal(t, "pass", v.Decision)
}

func TestTimerIsRefusedAndWorkPasses(t *testing.T) {
	ctx, _ := spawnFixture(t)
	raised := withSetting(string(denyRuleSleepPoll), builtin.Deny)
	for _, tt := range []struct {
		line       string
		background bool
		deny       bool
	}{
		{"sleep 240; echo tick", true, true},
		{"sleep 2", true, true},
		{"sleep 120 && cat /tmp/out.log", true, true},
		{"for i in 1 2 3; do sleep 60; echo tick; done", true, true},
		{"sleep 240; echo tick", false, true},
		{"sleep 30", false, true},
		{"for i in 1 2 3; do sleep 60; echo tick; done", false, true},
		{"sleep 2; cat out.log", false, false},
		{"./magus run go-test .", true, false},
		{"sleep 30 && ./magus run test .", true, false},
		{"make build", false, false},
	} {
		name := tt.line
		if tt.background {
			name += " (background)"
		}
		v := Judge(ctx, raised, Request{Input: tt.line, Session: "timers-" + name, Host: "claude-code", CallBackground: tt.background})
		if tt.deny {
			assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleSleepPoll)}, judgedAs(v), name)
			assert.Contains(t, v.Reason, "wait for that notice or do other work", name)
		} else {
			assert.NotEqual(t, string(denyRuleSleepPoll), v.Rule, name)
		}
	}
}

func TestSpawnDuplicateRefusesASecondWorkerForAJob(t *testing.T) {
	const title = "orchestrator/impl guard-facts"
	for _, tt := range []struct {
		name      string
		row       types.Job
		first     bool
		stopped   bool
		continued bool
		want      bool
	}{
		// A row stays running after its worker dies, so running alone says nothing.
		{"a running row no spawn here took", types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"a/**"}}, false, false, false, false},
		{"a running row an earlier spawn took", types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"a/**"}}, true, false, false, true},
		{"a declared row nobody took", types.Job{ID: "guard-facts", State: types.StateDeclared, WritePaths: []string{"a/**"}}, false, false, false, false},
		{"a declared row an earlier spawn took", types.Job{ID: "guard-facts", State: types.StateDeclared, WritePaths: []string{"a/**"}}, true, false, false, true},
		{"a finished row", types.Job{ID: "guard-facts", State: types.StatePass, WritePaths: []string{"a/**"}}, true, false, false, false},
		// Gap 218: the host's stop releases the row, and a continue takes it back.
		{"a running row whose worker stopped", types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"a/**"}}, true, true, false, false},
		{"a running row whose stopped worker was continued", types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"a/**"}}, true, true, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := fleetFixture(t, tt.row)
			if tt.first {
				Judge(ctx, Dependencies{}, Request{Input: finishedSpawn(t, title, "brisk-heron", "sonnet", "", "a1b2c3"), Host: "claude-code"})
			}
			if tt.stopped {
				Judge(ctx, Dependencies{}, Request{Input: subagentStop(t, "a1b2c3", filepath.Join(t.TempDir(), "agent.jsonl")), Host: "claude-code"})
			}
			if tt.continued {
				time.Sleep(10 * time.Millisecond)
				Judge(ctx, Dependencies{}, Request{Input: hookJSON(t, map[string]any{
					"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "SendMessage",
					"tool_input": map[string]any{"to": "brisk-heron", "message": "Carry on."},
				}), Host: "claude-code"})
			}
			v := Judge(ctx, Dependencies{}, Request{Input: hookJSON(t, map[string]any{
				"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Agent",
				"tool_input": map[string]any{"description": title, "prompt": "Carry the job to its done-when."},
			}), Host: "claude-code"})
			if tt.want {
				require.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleSpawnDuplicate)}, judgedAs(v), v.Context)
				assert.Contains(t, v.Reason, "guard-facts")
			} else {
				assert.NotEqual(t, string(denyRuleSpawnDuplicate), v.Rule)
			}
		})
	}
}

// A worker's run waits for the build budget in its own call, so every other wait
// holds nothing, whichever tool would run it.
func TestSleepPollRefusesAWorkersWaitForTheBuildBudget(t *testing.T) {
	worker := figuresLease()
	worker.Parent = "figures"
	ctx, _ := fleetFixture(t, worker)
	waits := []string{
		"./magus server status --watch=15s | ./magus run diagrams_generate docs",
		"./magus server status --watch=15s",
		"./magus server status -W 15s",
		"sleep 60; ./magus run diagrams_generate docs",
		"sleep 60; ./magus server status",
	}
	for i, line := range waits {
		bash := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(bash), "Bash: %s", line)
		if i == 0 {
			assert.Contains(t, bash.Reason, "waits for the build budget itself", "a repeat is shortened to the rule's name")
		}
		watcher := Judge(ctx, Dependencies{}, Request{Input: monitorCall(t, "", line), Lease: worker.ID, Host: "claude-code"})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(watcher), "Monitor: %s", line)
	}
	for _, line := range []string{"./magus server status"} {
		watched := Judge(ctx, Dependencies{}, Request{Input: monitorCall(t, "", line), Lease: worker.ID, Host: "claude-code"})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(watched), "a watcher repeats the call: %s", line)
	}

	for _, line := range []string{
		"./magus server status",
		"./magus run diagrams_generate docs",
		"sleep 2; ./magus run diagrams_generate docs",
	} {
		v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.NotEqual(t, string(denyRuleSleepPoll), v.Rule, "a worker reads the pool once, or waits in its check's own call: %s", line)
	}
}

// TestWorkerRetryAfterTheBoundIsRefused: inside the minute after a refusal, which
// came after the run waited as long as budget_wait allows, a worker's rerun of its check is refused.
func TestWorkerRetryAfterTheBoundIsRefused(t *testing.T) {
	worker := figuresLease()
	worker.Parent = "figures"
	ctx, _ := fleetFixture(t, worker)
	runsDir := filepath.Join(ctx.Value(locationKey{}).(location).cacheDir, cache.RunsDir)
	require.NoError(t, os.MkdirAll(runsDir, 0o755))
	writeRefusalLog(t, runsDir, "invrefused", time.Now().Add(-10*time.Second), true, "run", "diagrams_generate", "docs")

	bare := Judge(ctx, Dependencies{}, Request{Input: "./magus run diagrams_generate docs", Lease: worker.ID})
	assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(bare))
	assert.Contains(t, bare.Reason, "within its budget_wait")
}

// The orchestrator is who waits for the build budget, so the same lines pass for it.
func TestSleepPollLeavesTheOrchestratorsBudgetWait(t *testing.T) {
	root := figuresLease()
	ctx, _ := fleetFixture(t, root)
	for _, line := range []string{
		"./magus run diagrams_generate docs",
		"./magus server status --watch=15s",
		"sleep 60; ./magus run diagrams_generate docs",
	} {
		bash := Judge(ctx, Dependencies{}, Request{Input: line, Lease: root.ID, Session: "root", Host: "claude-code"})
		assert.NotEqual(t, string(denyRuleSleepPoll), bash.Rule, "Bash: %s", line)
		unleased := Judge(ctx, Dependencies{}, Request{Input: line})
		assert.NotEqual(t, string(denyRuleSleepPoll), unleased.Rule, "no lease: %s", line)
		watcher := Judge(ctx, Dependencies{}, Request{Input: monitorCall(t, "", line), Lease: root.ID, Host: "claude-code"})
		assert.NotEqual(t, string(denyRuleSleepPoll), watcher.Rule, "Monitor: %s", line)
	}
}

func TestBudgetFullSaysHowFullAndWhatHoldsIt(t *testing.T) {
	t.Parallel()
	now := time.Now()
	assert.Empty(t, BudgetFull(types.MachineSnapshot{}, now), "an unmeasured budget")
	assert.Empty(t, BudgetFull(types.MachineSnapshot{BudgetSlots: 4, HeldSlots: 3, BudgetMB: 8192, HeldMB: 1024}, now))

	full := BudgetFull(types.MachineSnapshot{BudgetSlots: 4, HeldSlots: 2, BudgetMB: 8192, HeldMB: 8192, Holders: []types.MachineClaimant{
		{Target: "go-test", Dir: "/w/a", Since: now.Add(-90 * time.Second)},
	}}, now)
	assert.Contains(t, full, "the build budget is full (mem ")
	assert.NotContains(t, full, "slots", "only the half that is spent")
	assert.Contains(t, full, "held by 1 run(s); the oldest, go-test . in /w/a, has held its seat 1m30s")
	assert.Contains(t, full, "hold this worker until the completion notice of work you already started arrives")
	assert.NotContains(t, full, "--watch", "a worker is refused a watch")
}

func TestSpawnWaiterAndNestedWaiter(t *testing.T) {
	ctx, _ := spawnFixture(t)
	spawn := func(agent, description string) types.GuardJudgment {
		env := map[string]any{
			"session_id": "nested", "hook_event_name": "PreToolUse", "tool_name": "Agent",
			"tool_input": map[string]any{"description": description, "prompt": "Carry it out."},
		}
		if agent != "" {
			env["agent_id"] = agent
		}
		return Judge(ctx, withSetting(string(advisorySpawnNestedWaiter), builtin.Advise), Request{Input: hookJSON(t, env), Host: "claude-code"})
	}
	v := spawn("", "wait for the workers to finish")
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleSpawnWaiter)}, judgedAs(v))

	nested := hookJSON(t, map[string]any{
		"session_id": "nested-default", "hook_event_name": "PreToolUse", "tool_name": "Agent", "agent_id": "a1b2c3",
		"tool_input": map[string]any{"description": "add the watch flag", "prompt": "Carry it out."},
	})
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(advisorySpawnNestedWaiter)}, judgedAs(Judge(ctx, Dependencies{}, Request{Input: nested, Host: "claude-code"})),
		"refused by default")

	v = spawn("a1b2c3", "add the watch flag")
	assert.Equal(t, types.GuardJudgment{Decision: "advise", Rule: string(advisorySpawnNestedWaiter)}, judgedAs(v),
		"set to advise, a worker's helper speaking of watching is advised against")
	assert.Equal(t, "pass", spawn("a1b2c3", "add the watch flag").Decision, "once a session")
	assert.Equal(t, "pass", spawn("", "add the watch flag").Decision, "the root is not nested")
}

// writeRefusalLog writes a run log for args started at at, its target refused with MGS3009
// when refused and passed otherwise, last written at at.
func writeRefusalLog(t *testing.T, runsDir, inv string, at time.Time, refused bool, args ...string) {
	t.Helper()
	var code types.DiagnosticCode
	if refused {
		code = types.MachineBudgetExhausted
	}
	writeCodedLog(t, runsDir, inv, at, code, args...)
}

// writeCodedLog is writeRefusalLog naming the code; "" passes the target.
func writeCodedLog(t *testing.T, runsDir, inv string, at time.Time, code types.DiagnosticCode, args ...string) {
	t.Helper()
	result := journal.Event{Ts: at.UnixMilli(), Inv: inv, Project: ".", Target: "go-test:rw", Kind: journal.KindResult, Status: journal.StatusPass}
	if code != "" {
		result.Status, result.DiagnosticCode = journal.StatusFail, string(code)
	}
	var body []byte
	for _, ev := range []journal.Event{
		{Ts: at.UnixMilli(), Inv: inv, Kind: journal.KindStarted, Command: &journal.Command{Arguments: args}},
		{Ts: at.UnixMilli(), Inv: inv, Kind: journal.KindOutput, Text: strings.Repeat("x", 128<<10)},
		result,
		{Ts: at.UnixMilli(), Inv: inv, Kind: journal.KindFinished, Status: result.Status},
	} {
		line, err := json.Marshal(ev)
		require.NoError(t, err)
		body = append(append(body, line...), '\n')
	}
	path := filepath.Join(runsDir, inv+".jsonl")
	require.NoError(t, os.WriteFile(path, body, 0o644))
	require.NoError(t, os.Chtimes(path, at, at))
}

type PressureRetrySuite struct {
	suite.Suite
}

func TestPressureRetrySuite(t *testing.T) {
	suite.Run(t, new(PressureRetrySuite))
}

const pressureRetry = "./magus run lint ."

func (s *PressureRetrySuite) refusedAgo(ago time.Duration) context.Context {
	cacheDir := s.T().TempDir()
	runsDir := filepath.Join(cacheDir, "runs")
	s.Require().NoError(os.MkdirAll(runsDir, 0o755))
	writeCodedLog(s.T(), runsDir, "invrefused", time.Now().Add(-ago), types.MemoryPressureHigh, "run", "lint", ".")
	return context.WithValue(s.T().Context(), locationKey{}, location{cacheDir: cacheDir})
}

func (s *PressureRetrySuite) TestALeasedRetryIsToldTheOwedFilingAndNotTheWait() {
	d := denyRefusedRetry(s.refusedAgo(10*time.Second), Dependencies{lease: "figures"}, "", pressureRetry)
	s.Require().True(d.refused())
	s.Contains(d.Say, "refused with MGS3053 10s ago")
	s.Contains(d.Say, cache.LeasedPressureNext("figures"), "the guard and the refusal say one thing")
	s.NotContains(d.Say, "within its budget_wait", "no seat clears this")
}

func (s *PressureRetrySuite) TestEachListedCodeIsReadBack() {
	for _, code := range refusalCodes {
		runsDir := s.T().TempDir()
		writeCodedLog(s.T(), runsDir, "inv", time.Now(), code, "run", "lint", ".")
		got, ok := recentRefusal(runsDir, [][]string{{"run", "lint", "."}}, time.Now())
		s.Require().True(ok, code)
		s.Equal(code, got.code)
	}
}

func (s *PressureRetrySuite) TestTheRuleTextNamesBothRefusals() {
	s.Contains(ruleDocSleepPoll.Why, "refused with MGS3009 or MGS3053")
	s.Contains(ruleDocSleepPoll.Why, "After MGS3053 no seat clears it")
}

func (s *PressureRetrySuite) TestAnUnboundRetryIsToldToFreeMemoryAndNotToFork() {
	d := denyRefusedRetry(s.refusedAgo(10*time.Second), Dependencies{}, "", pressureRetry)
	s.Require().True(d.refused())
	s.Contains(d.Say, "Free memory first")
	s.NotContains(d.Say, "fork a job")
}

func (s *PressureRetrySuite) TestARetryAfterTheMinutePasses() {
	s.False(denyRefusedRetry(s.refusedAgo(90*time.Second), Dependencies{}, "", pressureRetry).refused())
}

// TestDenyRefusedRetry: a run this checkout saw refused with MGS3009 inside the minute,
// after it waited its bound, is refused again; an older refusal, another command and a
// run that passed pass.
func TestDenyRefusedRetry(t *testing.T) {
	t.Parallel()
	const retry = "./magus run go-test . -s -- -run 'TestHookCmd|TestJudge'"
	refusedArgv := []string{"run", "go-test", ".", "-s", "--", "-run", "TestHookCmd|TestJudge"}
	at := func(t *testing.T, refusedAgo time.Duration, refused bool) context.Context {
		cacheDir := t.TempDir()
		runsDir := filepath.Join(cacheDir, "runs")
		require.NoError(t, os.MkdirAll(runsDir, 0o755))
		writeRefusalLog(t, runsDir, "invrefused", time.Now().Add(-refusedAgo), refused, refusedArgv...)
		writeRefusalLog(t, runsDir, "invother", time.Now().Add(-5*time.Second), false, "run", "lint", ".")
		return context.WithValue(t.Context(), locationKey{}, location{cacheDir: cacheDir})
	}

	t.Run("a repeat inside the minute", func(t *testing.T) {
		t.Parallel()
		d := denyRefusedRetry(at(t, 10*time.Second, true), Dependencies{lease: "figures"}, "", retry)
		require.True(t, d.refused())
		assert.Contains(t, d.Say, "refused with MGS3009 10s ago")
		assert.Contains(t, d.Say, "within its budget_wait", "the run already waited, so the next line raises the bound or files it owed")
		assert.Contains(t, d.Say, " run go-test . -s -- -run 'TestHookCmd|TestJudge'`", "quoted so the pipe in the pattern stays an argument")
	})
	t.Run("a repeat after the minute", func(t *testing.T) {
		t.Parallel()
		assert.False(t, denyRefusedRetry(at(t, 90*time.Second, true), Dependencies{}, "", retry).refused())
	})
	t.Run("a different command", func(t *testing.T) {
		t.Parallel()
		ctx := at(t, 10*time.Second, true)
		for _, line := range []string{"./magus run lint .", "./magus run go-test . -- -run 'TestHookCmd|TestJudge'", "./magus server status", "git status"} {
			assert.False(t, denyRefusedRetry(ctx, Dependencies{}, "", line).refused(), line)
		}
	})
	t.Run("the same command after a run that passed", func(t *testing.T) {
		t.Parallel()
		assert.False(t, denyRefusedRetry(at(t, 10*time.Second, false), Dependencies{}, "", retry).refused())
	})
	t.Run("the same command after a later run that got its seat", func(t *testing.T) {
		t.Parallel()
		ctx := at(t, 50*time.Second, true)
		runsDir := workspaceRunsDir(ctx.Value(locationKey{}).(location).cacheDir)
		writeRefusalLog(t, runsDir, "invpassed", time.Now().Add(-20*time.Second), false, refusedArgv...)
		assert.False(t, denyRefusedRetry(ctx, Dependencies{}, "", retry).refused(), "the newest run of the command passed")
	})
	t.Run("no run log", func(t *testing.T) {
		t.Parallel()
		ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir()})
		assert.False(t, denyRefusedRetry(ctx, Dependencies{}, "", retry).refused())
	})
}

// TestJudgeRefusesARetryAfterARefusal: the rule fires through Judge, which runs only the
// role-scoped rules roleScopedCommandRules lists.
func TestJudgeRefusesARetryAfterARefusal(t *testing.T) {
	testkit.Isolate(t)
	cacheDir := t.TempDir()
	runsDir := filepath.Join(cacheDir, "runs")
	require.NoError(t, os.MkdirAll(runsDir, 0o755))
	writeRefusalLog(t, runsDir, "invrefused", time.Now().Add(-10*time.Second), true, "run", "lint", ".")
	ctx := WithLocation(t.Context(), cacheDir, "/ws", "/ws")

	v := Judge(ctx, Dependencies{}, Request{Input: "./magus run lint ."})
	assert.Contains(t, v.Reason, "refused with MGS3009")
	assert.Equal(t, types.GuardJudgment{
		SchemaVersion: agent.GuardSchemaVersion, Decision: decisionDeny, Reason: v.Reason, Rule: string(denyRuleSleepPoll),
	}, v, v.Reason)
}

// The bounded background run check-timeout serves passes the guard for every
// role it is served to.
func TestEveryServedBackgroundCheckPassesTheGuardForEveryRole(t *testing.T) {
	ctx, roles := servedRoles(t)
	deps := strict(loadingWorkspace(t))
	deps.Spells = testDependencies().Spells
	for _, role := range roles {
		for id, run := range map[string]string{
			"bounded background check": "./magus job exec --timeout 18m0s -- ./magus run go::go-test . -- -run TestLedger",
		} {
			t.Run(fmt.Sprintf("%s/%s", role.name, id), func(t *testing.T) {
				call := servedAs(Request{}, role)
				call.CallBackground = id == "bounded background check"
				gradeServed(t, ctx, deps, call, role.name, id, run)
			})
		}
	}
}

func TestProcessPollOnALineWithARunServesThatRunInTheBackground(t *testing.T) {
	v := Evaluate(strict(testDependencies()), "kill -0 4242 && ./magus run go-test .")
	assert.Equal(t, denyRuleProcessPoll, v.Rule.Name)
	require.Len(t, v.Next, 1)
	assert.Contains(t, v.Next[0].Run, "job exec --timeout")
	assert.Contains(t, v.Next[0].Run, "-- ./magus run go-test .")
	assert.Contains(t, v.Why, "never waits")
}

func TestProcessPollWithNoRunOnTheLineReadsStatus(t *testing.T) {
	v := Evaluate(strict(testDependencies()), "pgrep -fl magus")
	assert.Equal(t, denyRuleProcessPoll, v.Rule.Name)
	require.Len(t, v.Next, 1)
	assert.Equal(t, hint.ServerStatus.String(), v.Next[0].Run)
}

// A wait on the pid of a run holding a seat spends the budget wait outside the call that
// runs; a pid no run holds is some other process.
func TestSleepPollRefusesAProcessWaitOnARunHoldingASeat(t *testing.T) {
	worker := figuresLease()
	worker.Parent = "figures"
	ctx, _ := fleetFixture(t, worker)
	standBudget(t, types.MachineSnapshot{Holders: []types.MachineClaimant{{PID: 4242, Target: "go-test", Project: "."}}})
	for _, line := range []string{
		"caffeinate -w 4242",
		"caffeinate -i -w 4242",
		"wait 4242",
		"lsof -p 4242",
	} {
		v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(v), line)
	}
	probe := Judge(ctx, Dependencies{}, Request{Input: "kill -0 4242 && ./magus run go-test .", Lease: worker.ID})
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleProcessPoll)}, judgedAs(probe))
	for _, line := range []string{
		"caffeinate -w 9999",
		"wait 9999",
		"lsof -p 9999",
		"caffeinate -i ./magus run go-test .",
	} {
		v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.NotEqual(t, string(denyRuleSleepPoll), v.Rule, "%s: %s", line, v.Reason)
	}
}

// The unbound root is refused a run, so its retry remedy forks a job.
func TestRefusedRetryServesTheRootAForkedJob(t *testing.T) {
	cacheDir := t.TempDir()
	runsDir := filepath.Join(cacheDir, "runs")
	require.NoError(t, os.MkdirAll(runsDir, 0o755))
	writeRefusalLog(t, runsDir, "invrefused", time.Now().Add(-10*time.Second), true, "run", "lint", ".")
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: cacheDir})

	root := denyRefusedRetry(ctx, Dependencies{}, "", "./magus run lint .")
	require.True(t, root.refused())
	assert.Contains(t, root.Say, hint.JobFork.String())

	leased := denyRefusedRetry(ctx, Dependencies{lease: "figures"}, "", "./magus run lint .")
	require.True(t, leased.refused())
	assert.Contains(t, leased.Say, "within its budget_wait")
	assert.NotContains(t, leased.Say, hint.JobFork.String())
}

// SleepFormSuite pins how long each spelling of a sleep that is not `sleep`
// waits, and the near misses that are not sleeps at all.
type SleepFormSuite struct {
	suite.Suite
}

func TestSleepFormSuite(t *testing.T) {
	suite.Run(t, new(SleepFormSuite))
}

func (s *SleepFormSuite) waits(line string, want time.Duration) {
	cmds, parsed := ParseCommands(line)
	s.Require().True(parsed, line)
	got, ok := sleepDuration(cmds[0])
	s.True(ok, line)
	s.Equal(want, got, line)
}

func (s *SleepFormSuite) isNoSleep(line string) {
	cmds, parsed := ParseCommands(line)
	s.Require().True(parsed, line)
	_, ok := sleepDuration(cmds[0])
	s.False(ok, line)
}

func (s *SleepFormSuite) TestSleep() {
	s.waits("sleep 30", 30*time.Second)
}

func (s *SleepFormSuite) TestCaffeinateForSeconds() {
	s.waits("caffeinate -t 90", 90*time.Second)
}

func (s *SleepFormSuite) TestPerlSelect() {
	s.waits("perl -e 'select(undef,undef,undef,45)'", 45*time.Second)
}

func (s *SleepFormSuite) TestPerlSelectForAFraction() {
	s.waits("perl -e 'select(undef, undef, undef, 0.25)'", 250*time.Millisecond)
}

func (s *SleepFormSuite) TestPerlSleep() {
	s.waits("perl -e 'sleep 60'", time.Minute)
}

func (s *SleepFormSuite) TestPythonTimeSleep() {
	s.waits("python3 -c 'import time; time.sleep(120)'", 2*time.Minute)
}

func (s *SleepFormSuite) TestRubySleep() {
	s.waits("ruby -e 'sleep 15'", 15*time.Second)
}

func (s *SleepFormSuite) TestNodeSetTimeout() {
	s.waits("node -e 'setTimeout(()=>{}, 20000)'", 20*time.Second)
}

func (s *SleepFormSuite) TestReadWithATimeout() {
	s.waits("read -t 30 line", 30*time.Second)
}

func (s *SleepFormSuite) TestPerlPrintIsNoSleep() {
	s.isNoSleep("perl -e 'print 1'")
}

func (s *SleepFormSuite) TestPythonPrintIsNoSleep() {
	s.isNoSleep("python3 -c 'print(1)'")
}

func (s *SleepFormSuite) TestCaffeinateAroundACommandIsNoSleep() {
	s.isNoSleep("caffeinate -i make")
}

func (s *SleepFormSuite) TestTailFollowIsNoSleep() {
	s.isNoSleep("tail -f out.log")
}

func (s *SleepFormSuite) TestReadWithoutATimeoutIsNoSleep() {
	s.isNoSleep("read line")
}

func (s *SleepFormSuite) TestCaffeinateOnAPidIsNoSleep() {
	s.isNoSleep("caffeinate -w 4242 -t 90")
}

func (s *SleepFormSuite) TestWaitOnAPidIsNoSleep() {
	s.isNoSleep("wait 4242")
}

func (s *SleepFormSuite) TestTailOnAPidIsNoSleep() {
	s.isNoSleep("tail --pid=4242 -f out.log")
}

// SleepFormPollSuite pins that each spelling of a sleep before a status read
// is refused as the poll a plain `sleep` would be.
type SleepFormPollSuite struct {
	suite.Suite
}

func TestSleepFormPollSuite(t *testing.T) {
	suite.Run(t, new(SleepFormPollSuite))
}

func (s *SleepFormPollSuite) refusesBeforeAStatusRead(sleeper string) {
	v := Evaluate(Dependencies{}, sleeper+"; ./magus server status")
	s.Equal(denyRuleSleepPoll, v.Rule.Name, sleeper)
}

func (s *SleepFormPollSuite) TestCaffeinate() {
	s.refusesBeforeAStatusRead("caffeinate -t 30")
}

func (s *SleepFormPollSuite) TestPerlSelect() {
	s.refusesBeforeAStatusRead("perl -e 'select(undef,undef,undef,30)'")
}

func (s *SleepFormPollSuite) TestPython() {
	s.refusesBeforeAStatusRead("python3 -c 'import time; time.sleep(30)'")
}

func (s *SleepFormPollSuite) TestRuby() {
	s.refusesBeforeAStatusRead("ruby -e 'sleep 30'")
}

func (s *SleepFormPollSuite) TestNode() {
	s.refusesBeforeAStatusRead("node -e 'setTimeout(()=>{}, 30000)'")
}

func (s *SleepFormPollSuite) TestTimeoutAroundASleep() {
	s.refusesBeforeAStatusRead("timeout 60 sleep 30")
}

func (s *SleepFormPollSuite) TestRead() {
	s.refusesBeforeAStatusRead("read -t 30 line")
}

// WaitRemedySuite pins the fix a waiting line is served: its own target run,
// backgrounded under its measured bound. Unmeasured, the ceiling is named.
type WaitRemedySuite struct {
	suite.Suite
	root string
}

func TestWaitRemedySuite(t *testing.T) {
	suite.Run(t, new(WaitRemedySuite))
}

func (s *WaitRemedySuite) SetupTest() {
	s.root = s.T().TempDir()
}

func (s *WaitRemedySuite) evaluate(line string) ShellVerdict {
	return Evaluate(Dependencies{scope: workspaceScope{root: s.root}}, line)
}

func (s *WaitRemedySuite) TestBoundIsTheMeasuredRunWithHalfAgain() {
	bound, note := waitBound("test", 80*time.Second, true, 30*time.Minute)
	s.Equal(2*time.Minute, bound, "80s and half again is 2m, already on the minute")
	s.Contains(note, "`test` last ran 1m20s here, and 2m leaves half again")
}

func (s *WaitRemedySuite) TestBoundRoundsUpToTheMinute() {
	bound, _ := waitBound("test", 5*time.Minute, true, 30*time.Minute)
	s.Equal(8*time.Minute, bound, "7m30s rounds up")
}

func (s *WaitRemedySuite) TestUnmeasuredBoundIsTheCeilingAndSaysSo() {
	bound, note := waitBound("test", 0, false, 30*time.Minute)
	s.Equal(30*time.Minute, bound)
	s.Contains(note, "nothing recorded here measures `test`")
	s.Contains(note, "background-unbounded's 30m ceiling, not a measurement")
}

func (s *WaitRemedySuite) TestOneUnmeasuredProjectLeavesTheLineUnmeasured() {
	recordRun(s.T(), s.root, "docs", "test", 5*time.Minute)
	v := s.evaluate("until ./magus run test docs console; do sleep 30; done")
	s.Require().Len(v.Next, 1)
	s.Contains(v.Next[0].Run, "--timeout 30m")
	s.Contains(v.Lead, "not a measurement")
}

func (s *WaitRemedySuite) TestAffectedIsNeverPricedByTheRootsRun() {
	recordRun(s.T(), s.root, ".", "ci", 5*time.Minute)
	v := s.evaluate("until ./magus affected ci; do sleep 30; done")
	s.Require().Len(v.Next, 1)
	s.Equal("./magus job exec --timeout 30m -- ./magus affected ci", v.Next[0].Run)
	s.Contains(v.Lead, "not a measurement")
}

func (s *WaitRemedySuite) TestComputedWordServesNothing() {
	v := s.evaluate(`until ./magus run test "$project"; do sleep 30; done`)
	s.Equal(denyRuleBusyWait, v.Rule.Name)
	s.Empty(v.Next, "the served line would drop the project")
}

func (s *WaitRemedySuite) TestAssignedEnvironmentServesNothing() {
	v := s.evaluate("until GOFLAGS=-race ./magus run test .; do sleep 30; done")
	s.Equal(denyRuleBusyWait, v.Rule.Name)
	s.Empty(v.Next, "job exec would run it without GOFLAGS")
}

func (s *WaitRemedySuite) TestFilterAfterTheSeparatorStaysQuoted() {
	v := s.evaluate("until ./magus run test . -- -run 'TestA|TestB'; do sleep 30; done")
	s.Require().Len(v.Next, 1)
	s.Equal(`./magus job exec --timeout 30m -- ./magus run test . -- -run "TestA|TestB"`, v.Next[0].Run)
}

func (s *WaitRemedySuite) TestServedLineIsNotRefused() {
	v := s.evaluate("until ./magus run test .; do sleep 30; done")
	s.Require().Len(v.Next, 1)
	s.Empty(s.evaluate(v.Next[0].Run).Deny)
}

func (s *WaitRemedySuite) TestBusyWaitOnAMeasuredRunServesItBounded() {
	recordRun(s.T(), s.root, ".", "test", 5*time.Minute)
	v := s.evaluate("until ./magus run test .; do sleep 30; done")
	s.Equal(denyRuleBusyWait, v.Rule.Name)
	s.Require().Len(v.Next, 1)
	s.Equal(hint.NextForDenyRemedy(string(denyRuleBusyWait),
		[]string{"./magus", "job", "exec", "--timeout", "8m", "--", "./magus", "run", "test", "."},
		"run it in the background: the host announces it when it ends, and job exec stops it at the bound."), v.Next[0])
	s.Contains(v.Lead, "`test` last ran 5m0s here, and 8m leaves half again")
	s.Contains(v.Deny, "<command>", "the deny stays whole for a reader the remedy is dropped for")
}

func (s *WaitRemedySuite) TestBusyWaitOnAnUnmeasuredRunNamesTheCeiling() {
	v := s.evaluate("while ! ./magus run lint libs/xdg -- --fix; do sleep 10; done")
	s.Equal(denyRuleBusyWait, v.Rule.Name)
	s.Require().Len(v.Next, 1)
	s.Equal("./magus job exec --timeout 30m -- ./magus run lint libs/xdg -- --fix", v.Next[0].Run)
	s.Contains(v.Lead, "nothing recorded here measures `lint` on these projects, so the bound is background-unbounded's 30m ceiling, not a measurement")
}

func (s *WaitRemedySuite) TestMeasuredOnAnotherProjectIsUnmeasuredHere() {
	recordRun(s.T(), s.root, "docs", "test", 5*time.Minute)
	v := s.evaluate("until ./magus run test .; do sleep 30; done")
	s.Require().Len(v.Next, 1)
	s.Contains(v.Next[0].Run, "--timeout 30m")
}

func (s *WaitRemedySuite) TestBusyWaitWithNoTargetRunServesNothing() {
	v := s.evaluate("until test -f out.log; do sleep 5; done")
	s.Equal(denyRuleBusyWait, v.Rule.Name)
	s.Empty(v.Next)
	s.Empty(v.Lead)
}

func (s *WaitRemedySuite) TestBusyWaitOnTwoRunsServesNothing() {
	v := s.evaluate("until ./magus run test . && ./magus run lint .; do sleep 30; done")
	s.Equal(denyRuleBusyWait, v.Rule.Name)
	s.Empty(v.Next)
}

func (s *WaitRemedySuite) TestSleepPollOnAStatusReadServesNothing() {
	v := s.evaluate("sleep 30; ./magus server status")
	s.Equal(denyRuleSleepPoll, v.Rule.Name)
	s.Empty(v.Next, "the work it polls is not on the line")
}

func (s *WaitRemedySuite) TestSleepPollBesideARunServesThatRun() {
	recordRun(s.T(), s.root, ".", "test", 80*time.Second)
	v := s.evaluate("sleep 30; ./magus server status; ./magus run test .")
	s.Equal(denyRuleSleepPoll, v.Rule.Name)
	s.Require().Len(v.Next, 1)
	s.Equal("./magus job exec --timeout 2m -- ./magus run test .", v.Next[0].Run)
}
