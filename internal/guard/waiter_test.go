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
		{"sleep 30; ./magus status", true},
		{"sleep 60 && gh pr checks 12", true},
		{"sleep 1m; kill -0 4242", true},
		{"sleep 45 && gh run view 99", true},
		// A settle, not a wait.
		{"sleep 5; ./magus status", false},
		// The read came first.
		{"./magus status; sleep 30", false},
		// A watch waits for the change itself.
		{"sleep 30; gh pr checks 12 --watch", false},
		{"sleep 30; ./magus status --watch=15s", false},
		{"sleep 30 && echo done", false},
		{"echo 'sleep 30; magus status'", false},
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
	v := Evaluate(Dependencies{}, "sleep 30; ./magus status")
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

// A worker waits for the build budget only in the call that spends the seat: a wait piped
// straight into its run reserves the seat for it, and every other wait holds nothing,
// whichever tool would run it.
func TestSleepPollRefusesAWorkersWaitForTheBuildBudget(t *testing.T) {
	worker := figuresLease()
	worker.Parent = "figures"
	ctx, _ := fleetFixture(t, worker)
	waits := []string{
		"./magus status -wait",
		"./magus status --wait; ./magus run diagrams_generate docs",
		"./magus status --watch=15s | ./magus run diagrams_generate docs",
		"./magus status --watch=15s",
		"./magus status -W 15s",
		"sleep 60; ./magus run diagrams_generate docs",
		"sleep 60; ./magus status --wait | ./magus run diagrams_generate docs",
	}
	for i, line := range waits {
		bash := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(bash), "Bash: %s", line)
		if i == 0 {
			assert.Contains(t, bash.Reason, "only in the call that spends the seat", "a repeat is shortened to the rule's name")
		}
		watcher := Judge(ctx, Dependencies{}, Request{Input: monitorCall(t, "", line), Lease: worker.ID, Host: "claude-code"})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(watcher), "Monitor: %s", line)
	}
	for _, line := range []string{"./magus status", "./magus status --wait | ./magus run diagrams_generate docs"} {
		watched := Judge(ctx, Dependencies{}, Request{Input: monitorCall(t, "", line), Lease: worker.ID, Host: "claude-code"})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(watched), "a watcher repeats the call: %s", line)
	}

	for _, line := range []string{
		"./magus status",
		"./magus run diagrams_generate docs",
		"sleep 2; ./magus run diagrams_generate docs",
		"./magus status --wait | ./magus run diagrams_generate docs",
	} {
		v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.NotEqual(t, string(denyRuleSleepPoll), v.Rule, "a worker reads the pool once, or waits in its check's own call: %s", line)
	}
}

// TestWorkerRetryIsSentToThePipedWait: inside the minute after a refusal a worker's bare
// rerun of its check is refused and named the piped wait, which it may then run.
func TestWorkerRetryIsSentToThePipedWait(t *testing.T) {
	worker := figuresLease()
	worker.Parent = "figures"
	ctx, _ := fleetFixture(t, worker)
	runsDir := filepath.Join(ctx.Value(locationKey{}).(location).cacheDir, cache.RunsDir)
	require.NoError(t, os.MkdirAll(runsDir, 0o755))
	writeRefusalLog(t, runsDir, "invrefused", time.Now().Add(-10*time.Second), true, "run", "diagrams_generate", "docs")

	bare := Judge(ctx, Dependencies{}, Request{Input: "./magus run diagrams_generate docs", Lease: worker.ID})
	assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(bare))
	assert.Contains(t, bare.Reason, "status --wait | ")

	piped := Judge(ctx, Dependencies{}, Request{Input: "./magus status --wait | ./magus run diagrams_generate docs", Lease: worker.ID})
	assert.NotEqual(t, string(denyRuleSleepPoll), piped.Rule, piped.Reason)
}

// The orchestrator is who waits for the build budget, so the same lines pass for it.
func TestSleepPollLeavesTheOrchestratorsBudgetWait(t *testing.T) {
	root := figuresLease()
	ctx, _ := fleetFixture(t, root)
	for _, line := range []string{
		"./magus status --wait | ./magus run diagrams_generate docs",
		"./magus status --watch=15s",
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
	assert.Contains(t, full, hint.Status.With("--watch=15s"))
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
	result := journal.Event{Ts: at.UnixMilli(), Inv: inv, Project: ".", Target: "go-test:rw", Kind: journal.KindResult, Status: journal.StatusPass}
	if refused {
		result.Status, result.DiagnosticCode = journal.StatusFail, string(types.MachineBudgetExhausted)
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

// TestDenyRefusedRetry: a run this checkout saw refused with MGS3009 inside the minute is
// refused again with the wait piped into it; an older refusal, another command, a run that
// passed and the piped wait itself pass.
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
		assert.Contains(t, d.Say, "status --wait | ", "the next line pipes the wait into the run")
		assert.Contains(t, d.Say, " run go-test . -s -- -run 'TestHookCmd|TestJudge'`", "quoted so the pipe in the pattern stays an argument")
	})
	t.Run("a repeat after the minute", func(t *testing.T) {
		t.Parallel()
		assert.False(t, denyRefusedRetry(at(t, 90*time.Second, true), Dependencies{}, "", retry).refused())
	})
	t.Run("a different command", func(t *testing.T) {
		t.Parallel()
		ctx := at(t, 10*time.Second, true)
		for _, line := range []string{"./magus run lint .", "./magus run go-test . -- -run 'TestHookCmd|TestJudge'", "./magus status", "git status"} {
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
	t.Run("the piped wait", func(t *testing.T) {
		t.Parallel()
		ctx := at(t, 10*time.Second, true)
		assert.False(t, denyRefusedRetry(ctx, Dependencies{}, "", "./magus status --wait | "+retry).refused())
		assert.False(t, denyRefusedRetry(ctx, Dependencies{}, "", "./magus status --wait 2>/dev/null | timeout 600 "+retry).refused())
	})
	t.Run("a wait that feeds no run", func(t *testing.T) {
		t.Parallel()
		ctx := at(t, 10*time.Second, true)
		for _, line := range []string{
			retry + "; ./magus status --wait",
			"./magus status --wait; " + retry,
			"./magus status -W 15s | " + retry,
			"./magus status --wait | ./magus run lint . && " + retry,
		} {
			assert.True(t, denyRefusedRetry(ctx, Dependencies{}, "", line).refused(), line)
		}
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

// Every way MGS3009's hint and the budget denies teach a wait passes a worker: the wait piped
// into the refused call, a run or a `graph build`, or run before it with `&&`. A wait that
// gates no magus call still waits for nothing.
func TestSleepPollPassesEveryWaitTheBudgetHintTeaches(t *testing.T) {
	worker := figuresLease()
	worker.Parent = "figures"
	ctx, _ := fleetFixture(t, worker)
	for _, line := range []string{
		"./magus status --wait | ./magus run diagrams_generate docs",
		"./magus status --wait | ./magus graph build --silent",
		"./magus status --wait && ./magus graph build --silent",
		"./magus status --wait && ./magus run diagrams_generate docs",
	} {
		v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.NotEqual(t, string(denyRuleSleepPoll), v.Rule, "%s: %s", line, v.Reason)
	}
	for _, line := range []string{
		"./magus status --wait; ./magus graph build --silent",
		"./magus status --wait | ./magus status",
		"./magus status --wait && echo ready",
		"./magus status --watch=15s && ./magus graph build --silent",
	} {
		v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(v), line)
	}
}

// A retry the wait gates with `&&` is not the poll denyRefusedRetry refuses; `;` still is.
func TestRefusedRetryPassesARunTheWaitGates(t *testing.T) {
	cacheDir := t.TempDir()
	runsDir := filepath.Join(cacheDir, "runs")
	require.NoError(t, os.MkdirAll(runsDir, 0o755))
	writeRefusalLog(t, runsDir, "invrefused", time.Now().Add(-10*time.Second), true, "run", "lint", ".")
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: cacheDir})

	assert.False(t, denyRefusedRetry(ctx, Dependencies{}, "", "./magus status --wait && ./magus run lint .").refused())
	assert.True(t, denyRefusedRetry(ctx, Dependencies{}, "", "./magus status --wait; ./magus run lint .").refused())
}

// The waits the budget denies and MGS3009 teach, and the bounded background run check-timeout
// serves, pass the guard for every role they are served to.
func TestEveryServedWaitPassesTheGuardForEveryRole(t *testing.T) {
	ctx, roles := servedRoles(t)
	deps := strict(loadingWorkspace(t))
	deps.Spells = testDependencies().Spells
	wait := hint.Status.With("--wait")
	for _, role := range roles {
		for id, run := range map[string]string{
			"wait piped into a run":    wait + " | magus run go::go-test . -- ./internal/ledger/",
			"wait piped into a build":  wait + " | magus graph build --silent",
			"wait before a build":      wait + " && magus graph build --silent",
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
		"kill -0 4242 && ./magus run go-test .",
	} {
		v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: worker.ID})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleSleepPoll)), unworded(v), line)
	}
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

// The unbound root is refused a run, so its retry remedy forks a job rather than piping
// into the run.
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
	assert.Contains(t, leased.Say, "status --wait | ")
	assert.NotContains(t, leased.Say, hint.JobFork.String())
}
