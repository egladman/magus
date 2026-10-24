package guard

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

// claudeLimits are the foreground limits one host's wiring declares: two minutes for a call
// naming no timeout, ten at most.
var claudeLimits = shellCall{foregroundDefault: 2 * time.Minute, foregroundMax: 10 * time.Minute}

// workerDenial is denyWorkerBackgroundRun for command as lease's call, through the read
// Judge makes of it.
func workerDenial(ctx context.Context, deps Dependencies, lease, command string, call shellCall) denial {
	run, _ := workerRuns(ctx, deps, lease, command)
	return denyWorkerBackgroundRun(run, call)
}

// A bound worker's backgrounded magus run is refused, and nothing else is: a foreground
// run, a run that only reports, another verb, or a call outside magus.
func TestDenyWorkerBackgroundRunRefusesOnlyABackgroundedCheck(t *testing.T) {
	row := figuresLease()
	ctx, _ := fleetFixture(t, row)
	deps := figuresWorkspace(t)
	background := shellCall{background: true}

	for _, tt := range []struct {
		name, command string
		call          shellCall
		deny          bool
	}{
		{"the check in the background", "./magus run diagrams_generate docs", background, true},
		{"affected in the background", "./magus affected diagrams-generate", background, true},
		{"the check in the foreground", "./magus run diagrams_generate docs", claudeLimits, false},
		{"a run that only reports", "./magus run diagrams_generate docs --plan", background, false},
		{"another verb", "./magus describe job figures/flow", background, false},
		{"not magus", "sleep 30", background, false},
	} {
		d := workerDenial(ctx, deps, row.ID, tt.command, tt.call)
		if !tt.deny {
			assert.Empty(t, d, tt.name)
			continue
		}
		require.True(t, d.refused(), tt.name)
		assert.NotContains(t, d.Say, "\n", "%s: the verdict is one line", tt.name)
		assert.Contains(t, d.Say, "run it in the foreground with a timeout that covers it", tt.name)
		assert.Contains(t, d.Why, "parks until the run ends", tt.name)
	}

	withLimits := claudeLimits
	withLimits.background = true
	assert.Contains(t, workerDenial(ctx, deps, row.ID, "./magus run diagrams_generate docs", withLimits).Why, "up to 10m0s",
		"a wiring that declares the maximum has it named")

	t.Run("silent for anyone but a bound worker", func(t *testing.T) {
		assert.Empty(t, workerDenial(ctx, deps, "", "./magus run lint docs", background), "no lease")
		integrated := figuresLease()
		integratorCtx, integrator := integratorFixture(t, integrated, "root/integrate "+integrated.ID)
		assert.Empty(t, workerDenial(integratorCtx, integrator, integrated.ID, "./magus affected ci", background), "the checkout's integrator")
		done := figuresLease()
		done.State = types.StatePass
		doneCtx, _ := fleetFixture(t, done)
		assert.Empty(t, workerDenial(doneCtx, deps, done.ID, "./magus run diagrams_generate docs", background), "a finished row")
	})
}

// TestJudgeRefusesABackgroundedWorkerRun reads the background request both ways a wiring
// hands it over: as the flag beside a bare command, and inside the whole envelope.
func TestJudgeRefusesABackgroundedWorkerRun(t *testing.T) {
	row := figuresLease()
	ctx, _ := fleetFixture(t, row)
	const command = "./magus run diagrams_generate docs"

	flagged := Judge(ctx, Dependencies{}, Request{Input: command, Lease: row.ID, CallBackground: true})
	assert.Equal(t, verdictWithRule("deny", string(denyRuleWorkerBackgroundRun)), unworded(flagged))

	envelope, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Shell",
		"tool_input":      map[string]any{"command": command, "run_in_background": true, "timeout": 600000},
	})
	require.NoError(t, err)
	enveloped := Judge(ctx, Dependencies{}, Request{Input: string(envelope), Lease: row.ID})
	assert.Equal(t, verdictWithRule("deny", string(denyRuleWorkerBackgroundRun)), unworded(enveloped))

	foreground := Judge(ctx, Dependencies{}, Request{Input: command, Lease: row.ID})
	assert.NotEqual(t, "deny", foreground.Decision, "a foreground run of the check passes")
}

func TestDecodeHookEnvelopeReadsHowTheHostRunsACommand(t *testing.T) {
	req, ok := decodeTestEnvelope(t, `{"hook_event_name":"PreToolUse","tool_input":{"command":"ls","run_in_background":true,"timeout":300000}}`)
	require.True(t, ok)
	assert.True(t, req.CallBackground)
	assert.Equal(t, 5*time.Minute, req.CallTimeout)

	req, ok = decodeTestEnvelope(t, `{"hook_event_name":"PreToolUse","tool_input":{"command":"ls","run_in_background":"yes","timeout":"soon"}}`)
	require.True(t, ok)
	assert.False(t, req.CallBackground, "only an explicit true")
	assert.Zero(t, req.CallTimeout, "a timeout that is not a number is none")
}

// recordedRuns numbers the invocations recordRun files, so two never share an id.
var recordedRuns atomic.Int64

// recordRun files one real run of target on project in the workspace's session store.
func recordRun(t *testing.T, root, project, target string, took time.Duration) {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	w, err := sessions.Open(dir, fmt.Sprintf("invmv%d1", recordedRuns.Add(1)), sessions.InvocationStart{Command: "run"})
	require.NoError(t, err)
	require.NoError(t, w.Append(sessions.KindTargetResult, sessions.TargetResult{
		Target: target, Project: project, Outcome: sessions.OutcomePass, DurationMs: took.Milliseconds(),
	}))
}

// checkTimeoutAdvice is adviseCheckTimeout for command as lease's call, through the read
// Judge makes of it.
func checkTimeoutAdvice(ctx context.Context, lease, command string, call shellCall) advice {
	run, _ := workerRuns(ctx, Dependencies{}, lease, command)
	return adviseCheckTimeout(hookLocation(ctx, Dependencies{}), run, call)
}

// A worker's magus run whose timeout falls short of the target's last real run is told the
// timeout that covers it, capped at the host's maximum, or told to ask for a narrower check
// when the maximum cannot cover it.
func TestAdviseCheckTimeoutCoversTheLastRealRun(t *testing.T) {
	maxed := shellCall{timeout: 10 * time.Minute, foregroundDefault: 2 * time.Minute, foregroundMax: 10 * time.Minute}
	const command = "./magus run diagrams_generate docs"
	for _, tt := range []struct {
		name string
		took time.Duration
		call shellCall
		say  string
	}{
		{"the default would cut it off", 5 * time.Minute, claudeLimits, "give it a timeout of 8m0s (480000 ms)"},
		{"a named timeout too short", 5 * time.Minute, shellCall{timeout: 3 * time.Minute, foregroundDefault: 2 * time.Minute, foregroundMax: 10 * time.Minute}, "this call waits 3m0s"},
		{"capped at the maximum", 9 * time.Minute, claudeLimits, "give it a timeout of 10m0s (600000 ms)"},
		{"past the maximum", 12 * time.Minute, claudeLimits, "a foreground call may wait; run it bounded in the background: `./magus job exec --timeout 18m0s -- ./magus run diagrams_generate docs`."},
		{"already waiting the maximum", 9 * time.Minute, maxed, "needs 13m30s with room, past the 10m0s a foreground call may wait; run it bounded in the background: `./magus job exec --timeout 14m0s -- "},
		{"a timeout that covers it", 5 * time.Minute, shellCall{timeout: 8 * time.Minute, foregroundDefault: 2 * time.Minute, foregroundMax: 10 * time.Minute}, ""},
		{"the default covers it", time.Minute, claudeLimits, ""},
		{"no foreground limits declared", 5 * time.Minute, shellCall{timeout: time.Minute}, ""},
		{"a backgrounded call, which the deny answers", 5 * time.Minute, shellCall{background: true, foregroundDefault: 2 * time.Minute, foregroundMax: 10 * time.Minute}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			row := figuresLease()
			ctx, root := fleetFixture(t, row)
			recordRun(t, root, "docs", "diagrams-generate:rw", tt.took)
			a := checkTimeoutAdvice(ctx, row.ID, command, tt.call)
			if tt.say == "" {
				assert.Empty(t, a.Say)
				return
			}
			assert.Contains(t, a.Say, tt.say)
			assert.NotContains(t, a.Say, "\n", "the advice is one line")
			assert.NotContains(t, a.Say, "give it a timeout of "+tt.call.timeout.String(), "never the timeout the call already has")
			assert.Contains(t, string(a.Key), string(advisoryCheckTimeout)+"-", "held per target")
		})
	}

	t.Run("silent for a target with no recorded run, and for a caller with no lease", func(t *testing.T) {
		row := figuresLease()
		ctx, root := fleetFixture(t, row)
		assert.Empty(t, checkTimeoutAdvice(ctx, row.ID, command, claudeLimits).Say, "nothing recorded")
		recordRun(t, root, "docs", "diagrams-generate", 5*time.Minute)
		a := checkTimeoutAdvice(ctx, "", command, claudeLimits)
		assert.Empty(t, a.Say, "no lease")
	})

	t.Run("once per target per session through Judge", func(t *testing.T) {
		row := figuresLease()
		ctx, root := fleetFixture(t, row)
		recordRun(t, root, "docs", "diagrams-generate", 5*time.Minute)
		// A subagent, which holds a worker's lease whatever its row says.
		req := Request{Input: command, Lease: row.ID, Host: "h", Session: "s1", Agent: "a1b2c3", HostForegroundDefault: 2 * time.Minute, HostForegroundMax: 10 * time.Minute}
		denied := Judge(ctx, Dependencies{}, req)
		assert.Equal(t, verdictWithRule("deny", string(advisoryCheckTimeout)), unworded(denied), "refused by default")
		assert.Contains(t, denied.Reason, "480000 ms")

		lowered := advising(Dependencies{}, string(advisoryCheckTimeout))
		first := Judge(ctx, lowered, req)
		assert.Equal(t, verdictWithRule("advise", string(advisoryCheckTimeout)), unworded(first))
		assert.Contains(t, first.Context, "480000 ms")
		again := Judge(ctx, lowered, req)
		assert.NotContains(t, again.Context, "480000 ms", "held to one firing per target per session")
	})
}

// A run narrowed with `-- -run` is priced as the whole target, since no per-test durations
// are recorded, so past the foreground maximum it is told the bounded form with its filter
// kept. That line carries its own deadline and passes, in the foreground or the background.
func TestCheckTimeoutSendsANarrowedRunToTheBoundedForm(t *testing.T) {
	row := figuresLease()
	ctx, root := fleetFixture(t, row)
	recordRun(t, root, "docs", "diagrams-generate", 13*time.Minute)
	const bounded = "./magus job exec --timeout 20m0s -- ./magus run diagrams_generate docs -- -run 'TestA|TestB'"

	a := checkTimeoutAdvice(ctx, row.ID, "./magus run diagrams_generate docs -- -run 'TestA|TestB'", claudeLimits)
	assert.Contains(t, a.Say, "`"+bounded+"`")

	assert.Empty(t, checkTimeoutAdvice(ctx, row.ID, bounded, claudeLimits).Say)
	for _, background := range []bool{false, true} {
		req := Request{Input: bounded, Lease: row.ID, Host: "h", Session: "s1", Agent: "a1b2c3", CallBackground: background,
			HostForegroundDefault: 2 * time.Minute, HostForegroundMax: 10 * time.Minute}
		v := Judge(ctx, Dependencies{}, req)
		assert.NotEqual(t, "deny", v.Decision, "background %v: %s", background, v.Reason)
	}
}

// bgCall is bgSearchCall and its result with command in place of the search.
func bgCall(command string) []string {
	const search = `cd ~/.claude/projects; grep -rhoE 'denied' --include='*.jsonl' . | sort | uniq -c`
	return []string{strings.Replace(bgSearchCall, search, command, 1), bgSearchResult}
}

// A subagent's stop is not refused over the bounded run check-timeout sent it to background,
// nor over its own commit the host moved to the background: each ends by itself, and its
// notice is how the agent hears the result. Any other live task still holds the stop.
func TestJudgeStopLetsWorkThatEndsOnItsOwnRun(t *testing.T) {
	started := bgTime(t, "2026-10-13T11:37:41.120Z")
	stop := func(lines ...string) bool {
		_, refused := judgeStop(Dependencies{}, hint.Gate{}, subagentStop(t, "a05d44b006f8f68af", writeTranscript(t, lines...)), started.Add(time.Minute))
		return refused
	}
	for _, command := range []string{
		"cd /repo && ./magus job exec --timeout 11m -- ./magus run go::go-test . -- -run TestLedger",
		"git commit -q -m 'add the ledger'",
		"git -C /repo merge --no-edit feature",
	} {
		assert.False(t, stop(bgCall(command)...), command)
	}
	for _, command := range []string{
		"./magus job exec -- ./magus run go::go-test .",
		"./magus run go::go-test .",
		"git commit -m x; sleep 600",
		"while true; do git commit -m x; done",
	} {
		assert.True(t, stop(bgCall(command)...), command)
	}
}

// check-timeout refuses once per target per session, as it advises: the first run of the
// target is refused naming the timeout, and the rerun narrowed to one test goes through.
func TestCheckTimeoutRefusesOncePerTarget(t *testing.T) {
	row := figuresLease()
	ctx, root := fleetFixture(t, row)
	recordRun(t, root, "docs", "diagrams-generate", 5*time.Minute)
	req := Request{Input: "./magus run diagrams_generate docs", Lease: row.ID, Host: "h", Session: "s1", Agent: "a1b2c3",
		HostForegroundDefault: 2 * time.Minute, HostForegroundMax: 10 * time.Minute}

	first := Judge(ctx, Dependencies{}, req)
	require.Equal(t, verdictWithRule("deny", string(advisoryCheckTimeout)), unworded(first))

	req.Input = "./magus run diagrams_generate docs -- -run TestFlow"
	narrowed := Judge(ctx, Dependencies{}, req)
	assert.NotEqual(t, string(advisoryCheckTimeout), narrowed.Rule, narrowed.Reason)

	other := req
	other.Session = "s2"
	assert.Equal(t, string(advisoryCheckTimeout), Judge(ctx, Dependencies{}, other).Rule, "another session is told again")
}

// A run naming no project and one naming the root are one target to check-timeout.
func TestCheckTimeoutKeyReadsNoProjectAsTheRoot(t *testing.T) {
	assert.Equal(t, checkTimeoutKey(targetRun{verb: "run", target: "test", projects: []string{"."}}), checkTimeoutKey(targetRun{verb: "run", target: "test"}))
}
