package guard

import (
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Transcript lines shaped like Claude Code 2.1.295's, cut from a subagent log and a root
// log of 2026-10-13: the calls that start a background task, the results that name its
// id, and the records that end it.
const (
	bgSearchCall    = `{"parentUuid":"fc15","isSidechain":true,"agentId":"a05d44b006f8f68af","message":{"model":"claude-opus-5-5","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_01LEU9","name":"Bash","input":{"command":"cd ~/.claude/projects; grep -rhoE 'denied' --include='*.jsonl' . | sort | uniq -c","run_in_background":true},"caller":{"type":"direct"}}]},"type":"assistant","uuid":"327c","timestamp":"2026-10-13T11:37:41.120Z"}`
	bgSearchResult  = `{"parentUuid":"327c","isSidechain":true,"agentId":"a05d44b006f8f68af","type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01LEU9","type":"tool_result","content":"Command running in background with ID: bc0m5o2bg. Output is being written to: /private/tmp/claude-501/p/s/tasks/bc0m5o2bg.output. You will be notified when it completes. To check interim output, use Read on that file path.","is_error":false}]},"uuid":"2aef","timestamp":"2026-10-13T11:37:42.674Z"}`
	bgSearchDenied  = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01LEU9","type":"tool_result","content":"denied [symbol-search]: a text search of the tree","is_error":true}]},"uuid":"2af0","timestamp":"2026-10-13T11:37:42.700Z"}`
	bgSearchErrored = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01LEU9","type":"tool_result","content":"Command running in background with ID: bc0m5o2bg. failed to start","is_error":true}]},"uuid":"2af1","timestamp":"2026-10-13T11:37:42.800Z"}`
	bgSearchDone    = `{"parentUuid":"be04","isSidechain":true,"agentId":"a05d44b006f8f68af","attachment":{"type":"queued_command","prompt":"<task-notification>\n<task-id>bc0m5o2bg</task-id>\n<tool-use-id>toolu_01LEU9</tool-use-id>\n<output-file>/private/tmp/claude-501/p/s/tasks/bc0m5o2bg.output</output-file>\n<status>completed</status>\n<summary>Background command \"grep\" completed (exit code 0)</summary>\n</task-notification>","commandMode":"task-notification","origin":{"kind":"task-notification","producer":"session-task"}},"type":"attachment","uuid":"f850","timestamp":"2026-10-13T11:38:57.259Z"}`

	movedCall   = `{"parentUuid":"0c59","isSidechain":true,"agentId":"a05d44b006f8f68af","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_012PTz","name":"Bash","input":{"command":"grep -rhoE 'git (rm|mv)' --include='*.jsonl' ."}}]},"type":"assistant","uuid":"0c5a","timestamp":"2026-10-13T11:42:00.000Z"}`
	movedResult = `{"parentUuid":"0c5a","isSidechain":true,"agentId":"a05d44b006f8f68af","type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_012PTz","type":"tool_result","content":"Command did not complete within its 120s timeout and was moved to the background (ID: bwu4rorj7). Output is being written to: /private/tmp/claude-501/p/s/tasks/bwu4rorj7.output. You will be notified when it completes. To check interim output, use Read on that file path.\nSession cwd remains /Users/dev/repo; directory changes made by the backgrounded command do not apply to subsequent commands.","is_error":false}]},"uuid":"3eb6","timestamp":"2026-10-13T11:44:00.000Z"}`
	movedStop   = `{"parentUuid":"958e","isSidechain":true,"agentId":"a05d44b006f8f68af","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01DSoU","name":"TaskStop","input":{"task_id":"bwu4rorj7"},"caller":{"type":"direct"}}]},"type":"assistant","uuid":"facc","timestamp":"2026-10-13T11:47:06.000Z"}`
	// A read of the task's output that waits: it names the task and stops nothing.
	movedRead = `{"isSidechain":true,"message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01Rd","name":"TaskOutput","input":{"task_id":"bwu4rorj7","block":true,"timeout":30000}}]},"type":"assistant","uuid":"r1","timestamp":"2026-10-13T11:46:00.000Z"}`

	bgWatchCall   = `{"isSidechain":true,"agentId":"a248ebb0781a027a9","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_019mBZ","name":"Monitor","input":{"command":"until grep -q . /tmp/t.output; do sleep 3; done; echo done","description":"wait for push test run","timeout_ms":600000,"persistent":"false"}}]},"type":"assistant","uuid":"m1","timestamp":"2026-10-13T12:00:00.000Z"}`
	bgWatchResult = `{"isSidechain":true,"agentId":"a248ebb0781a027a9","type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_019mBZ","type":"tool_result","content":"Monitor started (task bpbnai1yv, expires in 10m unless the source ends first; you get one notice at expiry - re-arm if you still need the watch). You will be notified on each event. Keep working - do not poll or sleep."}]},"uuid":"m2","timestamp":"2026-10-13T12:00:01.000Z"}`
	bgWatchEvent  = `{"isSidechain":true,"agentId":"a248ebb0781a027a9","attachment":{"type":"queued_command","prompt":"<task-notification>\n<task-id>bpbnai1yv</task-id>\n<summary>Monitor event: \"wait for push test run\"</summary>\n<event>[pass] magus (cached, 10ms)</event>\n</task-notification>","commandMode":"task-notification"},"type":"attachment","uuid":"m3","timestamp":"2026-10-13T12:01:00.000Z"}`
	bgWatchEnded  = `{"isSidechain":true,"agentId":"a248ebb0781a027a9","attachment":{"type":"queued_command","prompt":"<task-notification>\n<task-id>bpbnai1yv</task-id>\n<tool-use-id>toolu_019mBZ</tool-use-id>\n<status>completed</status>\n<summary>Monitor \"wait for push test run\" stream ended</summary>\n<event>done</event>\n</task-notification>","commandMode":"task-notification"},"type":"attachment","uuid":"m4","timestamp":"2026-10-13T12:02:00.000Z"}`

	// A root session records a notice twice: queued, then delivered as the user's turn.
	rootNoticeQueued    = `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-10-13T11:38:57.000Z","sessionId":"6696e31d","content":"<task-notification>\n<task-id>bc0m5o2bg</task-id>\n<tool-use-id>toolu_01LEU9</tool-use-id>\n<status>failed</status>\n<summary>Background command failed</summary>\n</task-notification>"}`
	rootNoticeDelivered = `{"parentUuid":"x","isSidechain":false,"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>bwu4rorj7</task-id>\n<tool-use-id>toolu_012PTz</tool-use-id>\n<status>stopped</status>\n</task-notification>"},"origin":{"kind":"task-notification","producer":"session-task"},"timestamp":"2026-10-13T11:50:00.000Z"}`

	// An agent quoting a notice in its reply, and a search printing one: neither ends a task.
	quotedNotice  = `{"isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"The notice read <task-notification><task-id>bc0m5o2bg</task-id><status>completed</status></task-notification>."}]},"type":"assistant","uuid":"q1","timestamp":"2026-10-13T11:39:00.000Z"}`
	searchPrinted = `{"isSidechain":true,"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01LEU9","type":"tool_result","content":"12 <task-notification><task-id>bc0m5o2bg</task-id><status>completed</status></task-notification>"}]},"uuid":"q2","timestamp":"2026-10-13T11:39:30.000Z"}`
)

func bgTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, s)
	require.NoError(t, err)
	return at
}

func TestLiveBackgroundTasks(t *testing.T) {
	searchStarted := bgTime(t, "2026-10-13T11:37:41.120Z")
	movedStarted := bgTime(t, "2026-10-13T11:42:00Z")
	watchStarted := bgTime(t, "2026-10-13T12:00:00Z")
	search := backgroundTask{ID: "bc0m5o2bg", Command: "cd ~/.claude/projects; grep -rhoE 'denied' --include='*.jsonl' . | sort | uniq -c", Started: searchStarted}
	moved := backgroundTask{ID: "bwu4rorj7", Command: "grep -rhoE 'git (rm|mv)' --include='*.jsonl' .", Started: movedStarted}
	watch := backgroundTask{ID: "bpbnai1yv", Command: "until grep -q . /tmp/t.output; do sleep 3; done; echo done", Started: watchStarted, Expires: watchStarted.Add(10 * time.Minute)}
	persistent := strings.Replace(bgWatchCall, `"persistent":"false"`, `"persistent":true`, 1)
	noWatchExpiry := watch
	noWatchExpiry.Expires = time.Time{}

	cases := []struct {
		name  string
		lines []string
		now   time.Time
		want  []backgroundTask
	}{
		{"a backgrounded call with no notice is live", []string{bgSearchCall, bgSearchResult}, searchStarted.Add(4 * time.Hour), []backgroundTask{search}},
		{"its completion notice ends it", []string{bgSearchCall, bgSearchResult, bgSearchDone}, searchStarted.Add(4 * time.Hour), nil},
		{"a backgrounded call with no result is not yet a task", []string{bgSearchCall}, searchStarted.Add(time.Hour), nil},
		{"a denied backgrounded call leaves no task", []string{bgSearchCall, bgSearchDenied}, searchStarted.Add(time.Hour), nil},
		{"an errored result that quotes an id starts no task", []string{bgSearchCall, bgSearchErrored}, searchStarted.Add(time.Hour), nil},
		{"a call moved to the background is live", []string{movedCall, movedResult}, movedStarted.Add(time.Hour), []backgroundTask{moved}},
		{"a stop call ends it", []string{movedCall, movedResult, movedStop}, movedStarted.Add(time.Hour), nil},
		{"a read that waits stops nothing", []string{movedCall, movedResult, movedRead}, movedStarted.Add(time.Hour), []backgroundTask{moved}},
		{"a foreground call that finished is not a task", []string{movedCall}, movedStarted.Add(time.Hour), nil},
		{"both live, in start order", []string{bgSearchCall, bgSearchResult, movedCall, movedResult}, movedStarted.Add(time.Hour), []backgroundTask{search, moved}},
		{"a root's queued and delivered notices end them", []string{bgSearchCall, bgSearchResult, movedCall, movedResult, rootNoticeQueued, rootNoticeDelivered}, movedStarted.Add(time.Hour), nil},
		{"a quoted or printed notice ends nothing", []string{bgSearchCall, bgSearchResult, quotedNotice, searchPrinted}, searchStarted.Add(time.Hour), []backgroundTask{search}},
		{"a watcher is live until it expires", []string{bgWatchCall, bgWatchResult, bgWatchEvent}, watchStarted.Add(5 * time.Minute), []backgroundTask{watch}},
		{"a watcher past its expiry is not", []string{bgWatchCall, bgWatchResult}, watchStarted.Add(10 * time.Minute), nil},
		{"a watcher's stream ending ends it", []string{bgWatchCall, bgWatchResult, bgWatchEnded}, watchStarted.Add(5 * time.Minute), nil},
		{"a persistent watcher never expires", []string{persistent, bgWatchResult}, watchStarted.Add(24 * time.Hour), []backgroundTask{noWatchExpiry}},
		{"a malformed line is skipped", []string{bgSearchCall, `{"type":"user","message":`, bgSearchResult}, searchStarted.Add(time.Hour), []backgroundTask{search}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := liveBackgroundTasks(writeTranscript(t, tc.lines...), tc.now)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	_, err := liveBackgroundTasks("agent-a1b2c3.jsonl", time.Now())
	assert.Error(t, err, "a relative path is never read")
}

// rootStop is Claude Code's Stop for the root session: no agent, and the session's own
// transcript.
func rootStop(t *testing.T, transcript string) string {
	t.Helper()
	return hookJSON(t, map[string]any{"session_id": "8f2c6a1e", "transcript_path": transcript, "cwd": "/Users/dev/repo", "permission_mode": "default", "hook_event_name": "Stop", "stop_hook_active": false})
}

func TestJudgeStopNamesLiveBackgroundTasks(t *testing.T) {
	started := bgTime(t, "2026-10-13T11:37:41.120Z")
	live := []string{bgSearchCall, bgSearchResult}
	advise := Dependencies{Builtins: map[string]builtin.Setting{string(denyRuleBackgroundLeft): {Decision: builtin.Advise}}}
	off := Dependencies{Builtins: map[string]builtin.Setting{string(denyRuleBackgroundLeft): {Decision: builtin.Off}}}

	t.Run("a subagent with a live task is refused, the task named", func(t *testing.T) {
		v, ok := judgeStop(Dependencies{}, hint.Gate{}, subagentStop(t, "a05d44b006f8f68af", writeTranscript(t, live...)), started.Add(4*time.Hour))
		require.True(t, ok)
		assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleBackgroundLeft)}, judgedAs(v))
		assert.Contains(t, v.Reason, "bc0m5o2bg  started 11:37:41Z, 4h0m0s ago: cd ~/.claude/projects; grep -rhoE")
	})
	t.Run("set to advise, a subagent is advised", func(t *testing.T) {
		v, ok := judgeStop(advise, hint.Gate{}, subagentStop(t, "a05d44b006f8f68af", writeTranscript(t, live...)), started.Add(time.Hour))
		require.True(t, ok)
		assert.Equal(t, decisionAdvise, v.Decision)
		assert.Contains(t, v.Context, "bc0m5o2bg")
	})
	t.Run("set off, nothing is said", func(t *testing.T) {
		_, ok := judgeStop(off, hint.Gate{}, subagentStop(t, "a05d44b006f8f68af", writeTranscript(t, live...)), started.Add(time.Hour))
		assert.False(t, ok)
	})
	t.Run("a subagent whose tasks ended stops", func(t *testing.T) {
		_, ok := judgeStop(Dependencies{}, hint.Gate{}, subagentStop(t, "a05d44b006f8f68af", writeTranscript(t, bgSearchCall, bgSearchResult, bgSearchDone)), started.Add(time.Hour))
		assert.False(t, ok)
	})
	t.Run("a root is advised of an old task, never refused", func(t *testing.T) {
		v, ok := judgeStop(Dependencies{}, hint.Gate{}, rootStop(t, writeTranscript(t, live...)), started.Add(rootBackgroundAge))
		require.True(t, ok)
		assert.Equal(t, decisionAdvise, v.Decision)
		assert.Contains(t, v.Context, "bc0m5o2bg")
	})
	t.Run("a root's young task is one it waits on", func(t *testing.T) {
		_, ok := judgeStop(Dependencies{}, hint.Gate{}, rootStop(t, writeTranscript(t, live...)), started.Add(rootBackgroundAge-time.Second))
		assert.False(t, ok)
	})
	t.Run("a tool call is not a stop", func(t *testing.T) {
		_, ok := judgeStop(Dependencies{}, hint.Gate{}, finishedSpawn(t, "scout", "", "", "", "a1b2c3"), started)
		assert.False(t, ok)
	})
	t.Run("a stop with no readable transcript says nothing", func(t *testing.T) {
		_, ok := judgeStop(Dependencies{}, hint.Gate{}, subagentStop(t, "a05d44b006f8f68af", "/nonexistent/agent.jsonl"), started)
		assert.False(t, ok)
	})
}

func TestJudgeStopAdvisesARootOncePerTask(t *testing.T) {
	searchStarted := bgTime(t, "2026-10-13T11:37:41.120Z")
	now := searchStarted.Add(4 * time.Hour)
	said := hint.NewGate(t.TempDir(), "claude-code/8f2c6a1e")
	stop := func(lines ...string) (types.GuardJudgment, bool) {
		return judgeStop(Dependencies{}, said, rootStop(t, writeTranscript(t, lines...)), now)
	}

	_, ok := judgeStop(Dependencies{}, said, rootStop(t, writeTranscript(t, bgSearchCall, bgSearchResult)), searchStarted.Add(rootBackgroundAge-time.Second))
	require.False(t, ok, "a young task is not advised")

	v, ok := stop(bgSearchCall, bgSearchResult)
	require.True(t, ok, "a young task's silence spent nothing")
	assert.Equal(t, types.GuardJudgment{Decision: decisionAdvise, Rule: string(denyRuleBackgroundLeft)}, judgedAs(v))
	assert.Equal(t, "magus workspace: a shell command this session ran in the background is still running; stop any nothing waits on:\n"+
		"  bc0m5o2bg  started 11:37:41Z, 4h0m0s ago: cd ~/.claude/projects; grep -rhoE 'denied' --include='*.jsonl' . | sort | uniq -c\n"+
		`    stop it: TaskStop {"task_id": "bc0m5o2bg"}`, v.Context)

	_, ok = stop(bgSearchCall, bgSearchResult)
	assert.False(t, ok, "the second stop says nothing of the same task")

	v, ok = stop(bgSearchCall, bgSearchResult, movedCall, movedResult)
	require.True(t, ok, "a task started after the first advice is named")
	assert.Equal(t, "magus workspace: a shell command this session ran in the background is still running; stop any nothing waits on:\n"+
		"  bwu4rorj7  started 11:42:00Z, 3h55m41s ago: grep -rhoE 'git (rm|mv)' --include='*.jsonl' .\n"+
		`    stop it: TaskStop {"task_id": "bwu4rorj7"}`, v.Context)
}

func TestDenyUnboundedBackground(t *testing.T) {
	cases := []struct {
		command string
		want    string // the program the refusal names; "" for none
	}{
		// The 10-13 runaway, and a scout's scan.
		{`exec </dev/null; cd ~/.claude/projects; grep -rhoE '(denied|refused)' --include='*.jsonl' -- -Users-dev* 2>/dev/null | sort | uniq -c | sort -rn | head -8`, "grep"},
		{`S=/tmp/scratch; python3 -I $S/scan.py $S/db/scan.json`, "python3"},
		{`sleep 540; echo tick`, "sleep"},
		{`while true; do sleep 5; done`, "while"},
		{`magus status --watch=15s`, "magus"},
		{`bash -c 'grep -r x .'`, "grep"},
		{`timeout 0 grep -r x .`, "timeout"},
		{`timeout grep -r x .`, "timeout"},
		{`$RUNNER test`, "a command named by a parameter"},
		{`magus job exec -- rg x`, "magus"},
		{`magus job exec --timeout 0s -- rg x`, "magus"},
		{`magus job exec --timeout 10m --`, "magus"},
		{`magus job wait w --timeout 10m -- rg x`, "magus"},

		{`timeout 600 grep -r x .`, ""},
		{`gtimeout --kill-after=10 300 rg x`, ""},
		{`timeout -k 5 1.5m python3 scan.py`, ""},
		{`cd /repo && timeout 60 make`, ""},
		{`timeout 300 grep -rh x . | sort | uniq -c`, ""},
		{`./magus run go-test .`, ""},
		{`BAGGAGE=magus.lease=x ./magus run test . -- -run TestX`, ""},
		{`bash -c 'timeout 5 grep -r x .'`, ""},
		{`magus job exec --timeout 10m -- rg x`, ""},
		{`./magus -C /repo job exec --timeout=90s -- python3 scan.py`, ""},
		{`BAGGAGE=magus.lease=w magus job exec --timeout 1h -- go test ./...`, ""},
		{`for f in a b; do timeout 5 grep x $f; done`, ""},
		{`export A=1`, ""},
		{`if (( 1 )`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			d := denyUnboundedBackground(tc.command, true, DialectBash)
			if tc.want == "" {
				assert.False(t, d.refused(), d.Say)
				return
			}
			require.True(t, d.refused())
			assert.Contains(t, d.Say, "`"+tc.want+"` runs in the background")
			assert.Contains(t, d.Say, "magus job exec --timeout <duration> -- <command>")
			assert.Contains(t, d.Why, "`timeout N` or `gtimeout N`", "the other remedy is behind the ref")
		})
	}
	assert.False(t, denyUnboundedBackground(`grep -r x .`, false, DialectBash).refused(), "a foreground call is the host's to time out")
}

func TestSessionEndedIsASubagentStopOrASessionEnd(t *testing.T) {
	assert.True(t, sessionEnded(subagentStop(t, "a05d44b006f8f68af", "/x/agent.jsonl")))
	assert.True(t, sessionEnded(hookJSON(t, map[string]any{"session_id": "8f2c6a1e", "hook_event_name": "SessionEnd", "reason": "prompt_input_exit"})))
	assert.False(t, sessionEnded(rootStop(t, "/x/root.jsonl")), "a root's Stop ends a turn")
	assert.False(t, sessionEnded(hookJSON(t, map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}})))
	assert.False(t, sessionEnded("ls"))
}

// A subagent whose stop the guard lets through ends the job it held; one it refuses is
// still running, so its job stays.
func TestEndStoppedSessionEndsTheJobOnlyWhenTheStopStands(t *testing.T) {
	root := t.TempDir()
	commitCheckout(t, root)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), Root: root, CacheDir: t.TempDir(), Actor: &job.Actor{}})
	_, err := store.Update(t.Context(), "w", func(row *types.Job) { *row = types.Job{ID: "w", State: types.StateDeclared} })
	require.NoError(t, err)
	_, err = store.Exec(t.Context(), "w")
	require.NoError(t, err)
	worker := job.Caller{Host: "claude-code", Session: "8f2c6a1e", Agent: "a05d44b006f8f68af"}
	require.NoError(t, store.Bind(worker, "w"))
	stop := subagentStop(t, worker.Agent, "/x/agent.jsonl")

	ended, err := endStoppedSession(t.Context(), store, worker, stop, types.GuardJudgment{Decision: decisionDeny})
	require.NoError(t, err)
	assert.False(t, ended)

	ended, err = endStoppedSession(t.Context(), store, worker, stop, types.GuardJudgment{})
	require.NoError(t, err)
	assert.True(t, ended)
	rows, err := store.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].EndedWithSession(), rows[0].EndReason)
}

// A backgrounded call nothing bounds is refused, unless it is a waiter: the waiter rules
// judge that one, though it is unbounded too.
func TestJudgeRefusesAnUnboundedBackgroundedCall(t *testing.T) {
	ctx, _ := spawnFixture(t)
	judge := func(line string) types.GuardJudgment {
		return Judge(ctx, Dependencies{}, Request{Input: line, Session: "bg", Host: "claude-code", CallBackground: true})
	}
	v := judge("rsync -a src/ dst/")
	assert.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleBackgroundUnbounded)}, judgedAs(v))
	assert.NotEqual(t, "deny", judge("timeout 600 rsync -a src/ dst/").Decision)

	timer := judge("sleep 540")
	assert.Equal(t, string(denyRuleSleepPoll), timer.Rule, timer.Reason)
}
