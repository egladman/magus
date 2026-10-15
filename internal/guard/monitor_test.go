package guard

import (
	"strconv"
	"testing"
	"time"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// monitorCall is a watcher call as Claude Code's Monitor tool sends it.
func monitorCall(t *testing.T, agent, command string) string {
	t.Helper()
	env := map[string]any{
		"session_id": "watchers", "hook_event_name": "PreToolUse", "tool_name": "Monitor",
		"tool_input": map[string]any{"command": command, "description": "the test run", "timeout_ms": float64(600000)},
	}
	if agent != "" {
		env["agent_id"] = agent
	}
	return hookJSON(t, env)
}

func TestEnvelopeWatchReadsAWatcherByShape(t *testing.T) {
	t.Parallel()
	req, ok := decodeHookEnvelope(monitorCall(t, "", "tail -f run.log"))
	require.True(t, ok)
	wantReq := req
	wantReq.IsWatch = true
	wantReq.CallBackground = true // a watcher always runs in the background
	wantReq.WatchFor = 10 * time.Minute
	wantReq.Value = "tail -f run.log" // its command is judged as a shell line
	assert.Equal(t, wantReq, req)

	bash, ok := decodeHookEnvelope(hookJSON(t, map[string]any{"session_id": "s", "tool_name": "Bash",
		"tool_input": map[string]any{"command": "tail -f run.log", "description": "follow", "timeout": float64(600000)}}))
	require.True(t, ok)
	assert.False(t, bash.IsWatch, "a shell call with a timeout is not a watcher")
}

func TestWatchSubjectsNormalizeWhatIsWaitedOn(t *testing.T) {
	t.Parallel()
	same := []string{
		`until ! pgrep -f "magus run test"; do sleep 15; done`,
		`sleep 240; until ! pgrep -f "magus run test"; do sleep 20; done`,
	}
	assert.Equal(t, watchSubjects(same[0], DialectBash), watchSubjects(same[1], DialectBash))
	assert.Equal(t, []string{"pid:4242"}, watchSubjects("while kill -0 4242; do sleep 20; done", DialectBash))
	assert.Equal(t, []string{"file:/tmp/x/tasks/b1.output"}, watchSubjects("until [ -s /tmp/x/tasks/b1.output ]; do sleep 1; done", DialectBash))
	assert.Equal(t, []string{"status"}, watchSubjects("./magus status --watch=15s", DialectBash))
	assert.Equal(t, []string{"job:a2-rules"}, watchSubjects("./magus job wait a2-rules", DialectBash))
}

// The pile the waiters scout found: one worker arming watcher after watcher on one run.
func TestMonitorDuplicateAndPile(t *testing.T) {
	ctx, _ := spawnFixture(t)
	raised := Dependencies{Builtins: map[string]builtin.Setting{
		string(denyRuleMonitorDuplicate): {Decision: builtin.Deny},
		string(denyRuleMonitorPile):      {Decision: builtin.Deny},
	}}
	watch := func(agent, command string) types.GuardJudgment {
		return Judge(ctx, raised, Request{Input: monitorCall(t, agent, command), Host: "claude-code"})
	}

	first := watch("w1", `tail -f build.log | grep --line-buffered -E "FAIL|ok"`)
	assert.NotEqual(t, "deny", first.Decision, first.Reason)
	again := watch("w1", `tail -f build.log | grep --line-buffered -E "FAIL|ok"`)
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleMonitorDuplicate)}, judgedAs(again))
	assert.NotEqual(t, "deny", watch("w2", `tail -f build.log | grep --line-buffered -E "FAIL|ok"`).Decision,
		"another agent's watcher is its own")

	own := watch("w1", "until [ -s /tmp/claude/tasks/bpr89hsbc.output ]; do sleep 1; done")
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleMonitorDuplicate)}, judgedAs(own),
		"a task capture is the agent's own backgrounded work")

	assert.NotEqual(t, "deny", watch("w1", "tail -f deploy.log").Decision)
	third := watch("w1", "tail -f other.log")
	require.Equal(t, types.GuardJudgment{Decision: "advise", Rule: string(denyRuleMonitorPile)}, judgedAs(third))
	assert.NotEqual(t, "deny", watch("w1", "tail -f more.log").Decision)
	fifth := watch("w1", "tail -f last.log")
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleMonitorPile)}, judgedAs(fifth))
	assert.Contains(t, fifth.Reason, hint.JobWait.With("<job>"))
}

func TestMonitorOnABackgroundedCommandsProcessIsADuplicate(t *testing.T) {
	ctx, _ := spawnFixture(t)
	raised := withSetting(string(denyRuleMonitorDuplicate), builtin.Deny)
	run := Judge(ctx, raised, Request{Input: "timeout 600 go version", Session: "watchers", Host: "claude-code", Agent: "w1", CallBackground: true})
	require.NotEqual(t, "deny", run.Decision, run.Reason)
	v := Judge(ctx, raised, Request{Input: monitorCall(t, "w1", `until ! pgrep -f "go version"; do sleep 15; done`), Host: "claude-code"})
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleMonitorDuplicate)}, judgedAs(v))
}

func TestAnExpiredWatcherIsNotLive(t *testing.T) {
	t.Parallel()
	facts := hint.NewGate(t.TempDir(), "s")
	now := time.Now()
	w := watchCall{subject: "file:a.log", expires: now.Add(time.Minute)}
	recordWatch(facts, "w1", w, now)
	assert.Equal(t, denyRuleMonitorDuplicate, gradeWatch(facts, "w1", false, w, now).Rule.Name)
	assert.Empty(t, gradeWatch(facts, "w1", false, w, now.Add(2*time.Minute)).Rule.Name, "re-arming after expiry passes")
}

func TestEndlessBackgroundWatch(t *testing.T) {
	t.Parallel()
	assert.True(t, endlessWatch("./magus status --watch=15s", DialectBash))
	assert.True(t, endlessWatch("tail -F run.log", DialectBash))
	assert.False(t, endlessWatch("./magus status", DialectBash))
	assert.False(t, endlessWatch("tail -n 20 run.log", DialectBash))

	facts := hint.NewGate(t.TempDir(), "s")
	now := time.Now()
	w := watchCall{subject: "status", expires: now.Add(watchExpiryCap), endless: true}
	assert.Equal(t, denyRuleMonitorDuplicate, gradeWatch(facts, "", true, w, now).Rule.Name, "a worker waits on its own runs")
	assert.Empty(t, gradeWatch(facts, "", false, w, now).Rule.Name)
	recordWatch(facts, "", w, now)
	assert.Equal(t, denyRuleMonitorDuplicate, gradeWatch(facts, "", false, w, now).Rule.Name, "a second one in the session")
}

// The guard judges a tool by the shape of its input, so a command run through any host
// tool reaches the rules a shell call does. A wait loop one denies must be denied by the
// other, whichever of the watcher's two shapes (a timeout, or no end) it arrives in.
func TestAWaitLoopIsDeniedWhicheverToolRunsIt(t *testing.T) {
	ctx, _ := spawnFixture(t)
	deps := strict(Dependencies{})
	loops := []string{
		"until [ -s out.txt ]; do sleep 5; done; cat out.txt",
		"while ! test -s out.txt; do echo waiting; sleep 5; done",
		`until ! pgrep -f "magus run test"; do sleep 15; done`,
	}
	envelopes := map[string]map[string]any{
		"shell":      {"tool_name": "Bash", "tool_input": map[string]any{"description": "wait"}},
		"watcher":    {"tool_name": "Monitor", "tool_input": map[string]any{"description": "wait", "timeout_ms": float64(600000)}},
		"persistent": {"tool_name": "Monitor", "tool_input": map[string]any{"description": "wait", "persistent": true}},
	}
	for i, loop := range loops {
		for name, env := range envelopes {
			env["session_id"] = "wait-" + name + "-" + strconv.Itoa(i)
			env["hook_event_name"] = "PreToolUse"
			env["tool_input"].(map[string]any)["command"] = loop
			v := Judge(ctx, deps, Request{Input: hookJSON(t, env), Host: "claude-code"})
			assert.Equal(t, "deny", v.Decision, "%s: %s", name, loop)
			assert.Equal(t, string(denyRuleBusyWait), v.Rule, "%s: %s", name, loop)
		}
	}
}
