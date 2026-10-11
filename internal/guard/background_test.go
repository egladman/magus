package guard

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

// claudeLimits are the foreground limits one host's wiring declares: two minutes for a call
// naming no timeout, ten at most.
var claudeLimits = hostCall{foregroundDefault: 2 * time.Minute, foregroundMax: 10 * time.Minute}

func TestWorkerBackgroundRun(t *testing.T) {
	row := figuresLease()
	ctx, _ := fleetFixture(t, row)
	deps := figuresWorkspace(t)
	background := hostCall{background: true}

	for _, tt := range []struct {
		name, command string
		call          hostCall
		deny          bool
	}{
		{"the check in the background", "./magus run diagrams_generate docs", background, true},
		{"affected in the background", "./magus affected diagrams-generate", background, true},
		{"the check in the foreground", "./magus run diagrams_generate docs", claudeLimits, false},
		{"a run that only reports", "./magus run diagrams_generate docs --plan", background, false},
		{"another verb", "./magus describe job figures/flow", background, false},
		{"not magus", "sleep 30", background, false},
	} {
		d := denyWorkerBackgroundRun(ctx, deps, row.ID, tt.command, tt.call)
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
	assert.Contains(t, denyWorkerBackgroundRun(ctx, deps, row.ID, "./magus run diagrams_generate docs", withLimits).Why, "up to 10m0s",
		"a wiring that declares the maximum has it named")

	t.Run("silent for anyone but a bound worker", func(t *testing.T) {
		assert.Empty(t, denyWorkerBackgroundRun(ctx, deps, "", "./magus run lint docs", background), "no lease")
		owns := figuresLease()
		owns.Check = &types.LeaseCheck{Target: "ci", Project: "."}
		ownsCtx, _ := fleetFixture(t, owns)
		assert.Empty(t, denyWorkerBackgroundRun(ownsCtx, deps, owns.ID, "./magus affected ci", background), "the gate's owner")
		done := figuresLease()
		done.State = types.StatePass
		doneCtx, _ := fleetFixture(t, done)
		assert.Empty(t, denyWorkerBackgroundRun(doneCtx, deps, done.ID, "./magus run diagrams_generate docs", background), "a finished row")
	})
}

// TestJudgeRefusesABackgroundedWorkerRun reads the background request both ways a wiring
// hands it over: as the flag beside a bare command, and inside the whole envelope.
func TestJudgeRefusesABackgroundedWorkerRun(t *testing.T) {
	t.Setenv("BAGGAGE", "")
	row := figuresLease()
	ctx, _ := fleetFixture(t, row)
	const command = "./magus run diagrams_generate docs"

	flagged := Judge(ctx, Dependencies{}, Request{Input: command, Lease: row.ID, Background: true})
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
	req, ok := decodeHookEnvelope(`{"hook_event_name":"PreToolUse","tool_input":{"command":"ls","run_in_background":true,"timeout":300000}}`)
	require.True(t, ok)
	assert.True(t, req.Background)
	assert.Equal(t, 5*time.Minute, req.Timeout)

	req, ok = decodeHookEnvelope(`{"hook_event_name":"PreToolUse","tool_input":{"command":"ls","run_in_background":"yes","timeout":"soon"}}`)
	require.True(t, ok)
	assert.False(t, req.Background, "only an explicit true")
	assert.Zero(t, req.Timeout, "a timeout that is not a number is none")
}

// recordRun files one real run of target on project in the workspace's session store.
func recordRun(t *testing.T, root, project, target string, took time.Duration) {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	w, err := sessions.Open(dir, fmt.Sprintf("invmv%d1", time.Now().UnixNano()), sessions.InvocationStart{Command: "run"})
	require.NoError(t, err)
	require.NoError(t, w.Append(sessions.KindTargetResult, sessions.TargetResult{
		Target: target, Project: project, Outcome: sessions.OutcomePass, DurationMs: took.Milliseconds(),
	}))
}

func TestCheckTimeoutAdvice(t *testing.T) {
	const command = "./magus run diagrams_generate docs"
	for _, tt := range []struct {
		name string
		took time.Duration
		call hostCall
		say  string
	}{
		{"the default would cut it off", 5 * time.Minute, claudeLimits, "give it a timeout of 8m0s (480000 ms)"},
		{"a named timeout too short", 5 * time.Minute, hostCall{timeout: 3 * time.Minute, foregroundDefault: 2 * time.Minute, foregroundMax: 10 * time.Minute}, "this call waits 3m0s"},
		{"capped at the maximum", 9 * time.Minute, claudeLimits, "give it a timeout of 10m0s (600000 ms)"},
		{"past the maximum", 12 * time.Minute, claudeLimits, "a foreground call may wait; ask the orchestrator for a narrower check"},
		{"a timeout that covers it", 5 * time.Minute, hostCall{timeout: 8 * time.Minute, foregroundDefault: 2 * time.Minute, foregroundMax: 10 * time.Minute}, ""},
		{"the default covers it", time.Minute, claudeLimits, ""},
		{"no foreground limits declared", 5 * time.Minute, hostCall{timeout: time.Minute}, ""},
		{"a backgrounded call, which the deny answers", 5 * time.Minute, hostCall{background: true, foregroundDefault: 2 * time.Minute, foregroundMax: 10 * time.Minute}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			row := figuresLease()
			ctx, root := fleetFixture(t, row)
			recordRun(t, root, "docs", "diagrams-generate:rw", tt.took)
			a, key := adviseCheckTimeout(ctx, Dependencies{}, hookLocation(ctx, Dependencies{}), row.ID, command, tt.call)
			if tt.say == "" {
				assert.Empty(t, a.Say)
				return
			}
			assert.Contains(t, a.Say, tt.say)
			assert.NotContains(t, a.Say, "\n", "the advice is one line")
			assert.Contains(t, string(key), string(advisoryCheckTimeout)+"-", "held per target")
		})
	}

	t.Run("silent for a target with no recorded run, and for a caller with no lease", func(t *testing.T) {
		row := figuresLease()
		ctx, root := fleetFixture(t, row)
		at := hookLocation(ctx, Dependencies{})
		a, _ := adviseCheckTimeout(ctx, Dependencies{}, at, row.ID, command, claudeLimits)
		assert.Empty(t, a.Say, "nothing recorded")
		recordRun(t, root, "docs", "diagrams-generate", 5*time.Minute)
		a, _ = adviseCheckTimeout(ctx, Dependencies{}, at, "", command, claudeLimits)
		assert.Empty(t, a.Say, "no lease")
	})

	t.Run("once per target per session through Judge", func(t *testing.T) {
		t.Setenv("BAGGAGE", "")
		row := figuresLease()
		ctx, root := fleetFixture(t, row)
		recordRun(t, root, "docs", "diagrams-generate", 5*time.Minute)
		// A subagent, which holds a worker's lease whatever its row says.
		req := Request{Input: command, Lease: row.ID, Host: "h", Session: "s1", Agent: "a1b2c3", ForegroundDefault: 2 * time.Minute, ForegroundMax: 10 * time.Minute}
		first := Judge(ctx, Dependencies{}, req)
		assert.Equal(t, verdictWithRule("advise", string(advisoryCheckTimeout)), unworded(first))
		assert.Contains(t, first.Context, "480000 ms")
		again := Judge(ctx, Dependencies{}, req)
		assert.NotContains(t, again.Context, "480000 ms", "held to one firing per target per session")
	})
}
