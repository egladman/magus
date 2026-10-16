package guard

import (
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			assert.Contains(t, v.Deny, hint.Status.With("--watch=15s"), tt.line)
		} else {
			assert.NotEqual(t, denyRuleSleepPoll, v.Rule.Name, tt.line)
		}
	}
	// It advises by default: a workspace raises it.
	v := Evaluate(Dependencies{}, "sleep 30; ./magus status")
	assert.Empty(t, v.Deny)
	assert.Equal(t, hint.MarkerKind(denyRuleSleepPoll), v.Kind)
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
// refused as a timer when the workspace raises sleep-poll, and each counts toward the pile.
func TestBackgroundedSleepIsATimerAndCountsTowardThePile(t *testing.T) {
	ctx, _ := spawnFixture(t)
	judge := func(deps Dependencies, line string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: line, Session: "timers", Host: "claude-code", CallBackground: true})
	}

	raised := withSetting(string(denyRuleSleepPoll), builtin.Deny)
	v := judge(raised, "sleep 540")
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleSleepPoll)}, judgedAs(v))

	// By default it advises, and the timers it lets through are counted.
	for i := range monitorPileAdvise - 1 {
		v = judge(Dependencies{}, "sleep "+strings.Repeat("9", i+2)+" && echo tick")
		assert.NotEqual(t, "deny", v.Decision)
	}
	v = judge(withSetting(string(denyRuleMonitorPile), builtin.Deny), "sleep 300")
	assert.Equal(t, types.GuardJudgment{Decision: "advise", Rule: string(denyRuleMonitorPile)}, judgedAs(v),
		"the third live waiter is advised against")
	for range monitorPileDeny - monitorPileAdvise - 1 {
		judge(Dependencies{}, "sleep 301")
	}
	v = judge(withSetting(string(denyRuleMonitorPile), builtin.Deny), "sleep 302")
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
		name  string
		row   types.Job
		first bool
		want  bool
	}{
		// A row stays running after its worker dies, so running alone says nothing.
		{"a running row no spawn here took", types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"a/**"}}, false, false},
		{"a running row an earlier spawn took", types.Job{ID: "guard-facts", State: types.StateRunning, WritePaths: []string{"a/**"}}, true, true},
		{"a declared row nobody took", types.Job{ID: "guard-facts", State: types.StateDeclared, WritePaths: []string{"a/**"}}, false, false},
		{"a declared row an earlier spawn took", types.Job{ID: "guard-facts", State: types.StateDeclared, WritePaths: []string{"a/**"}}, true, true},
		{"a finished row", types.Job{ID: "guard-facts", State: types.StatePass, WritePaths: []string{"a/**"}}, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := fleetFixture(t, tt.row)
			if tt.first {
				Judge(ctx, Dependencies{}, Request{Input: finishedSpawn(t, title, "brisk-heron", "sonnet", "", "a1b2c3"), Host: "claude-code"})
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
		return Judge(ctx, withSetting(string(denyRuleSpawnWaiter), builtin.Deny), Request{Input: hookJSON(t, env), Host: "claude-code"})
	}
	v := spawn("", "wait for the workers to finish")
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleSpawnWaiter)}, judgedAs(v))

	v = spawn("a1b2c3", "add the watch flag")
	assert.Equal(t, types.GuardJudgment{Decision: "advise", Rule: string(advisorySpawnNestedWaiter)}, judgedAs(v),
		"a worker's helper speaking of watching is advised against")
	assert.Equal(t, "pass", spawn("a1b2c3", "add the watch flag").Decision, "once a session")
	assert.Equal(t, "pass", spawn("", "add the watch flag").Decision, "the root is not nested")
}
