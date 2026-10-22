package guard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

const (
	warmAgent      = "a1b2c3"
	warmEndedTitle = "orchestrator/feat gate-fits"
	warmSpawnTitle = "orchestrator/fix regress-fix"
)

func (f reworkFixture) putRow(t *testing.T, row types.Job) {
	t.Helper()
	_, err := f.store.Update(f.ctx, row.ID, func(cur *types.Job) { *cur = row; cur.Validated = 1 })
	require.NoError(t, err)
}

// warmFixture holds an exited job gate-fits whose worker this session spawned, and a declared
// job regress-fix built from follow.
func warmFixture(t *testing.T, follow func(*types.Job)) reworkFixture {
	t.Helper()
	f := newReworkFixture(t)
	f.putRow(t, types.Job{ID: "gate-fits", State: types.StateExited, WritePaths: []string{"internal/cache/machine.go"}})
	next := types.Job{ID: "regress-fix", State: types.StateDeclared, WritePaths: []string{"internal/cache/order.go"}}
	follow(&next)
	f.putRow(t, next)
	writeSpawnedAgent(f.facts, warmAgent, spawnedAgent{ID: warmAgent, Description: warmEndedTitle, Name: "brisk-heron"})
	return f
}

func (f reworkFixture) spawn(t *testing.T) types.GuardJudgment {
	t.Helper()
	return Judge(f.ctx, Dependencies{}, Request{Input: hookJSON(t, map[string]any{
		"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Agent",
		"tool_input": map[string]any{"description": warmSpawnTitle, "prompt": "Carry the job to its done-when."},
	}), Host: "claude-code"})
}

func TestSpawnWarmWorkerRefusesAFreshSpawnWhileTheWriterIsWarm(t *testing.T) {
	tests := []struct {
		name   string
		follow func(*types.Job)
		link   string
	}{
		{"a job naming its predecessor", func(j *types.Job) { j.Follows = "gate-fits" }, "follows gate-fits"},
		{"a job sharing a write path", func(j *types.Job) { j.WritePaths = append(j.WritePaths, "internal/cache/*.go") }, "shares internal/cache/*.go with gate-fits"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := warmFixture(t, tc.follow).spawn(t)

			require.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleSpawnWarmWorker)}, judgedAs(v), v.Context)
			assert.Contains(t, v.Reason, "job regress-fix "+tc.link)
			assert.Contains(t, v.Reason, "continue agent a1b2c3 with a message (SendMessage)")
			assert.Contains(t, v.Reason, "magus job apply -f <record>")
			assert.Contains(t, v.Reason, "no harness model row carries a prompt-cache TTL", "the TTL's source is said")
		})
	}
}

func TestSpawnWarmWorkerPassesAFreshReason(t *testing.T) {
	v := warmFixture(t, func(j *types.Job) {
		j.Follows = "gate-fits"
		j.FreshReason = "gate-fits read the old limiter; this job replaces it"
	}).spawn(t)

	assert.NotEqual(t, string(denyRuleSpawnWarmWorker), v.Rule, v.Reason)
}

func TestSpawnWarmWorkerLeavesAnUnrelatedJobAlone(t *testing.T) {
	v := warmFixture(t, func(*types.Job) {}).spawn(t)

	assert.NotEqual(t, string(denyRuleSpawnWarmWorker), v.Rule, v.Reason)
}

func TestSpawnWarmWorkerRemedyPassesTheGuard(t *testing.T) {
	f := warmFixture(t, func(j *types.Job) { j.Follows = "gate-fits" })
	require.Equal(t, string(denyRuleSpawnWarmWorker), f.spawn(t).Rule)

	v := Judge(f.ctx, Dependencies{}, Request{Input: hookJSON(t, map[string]any{
		"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "SendMessage",
		"tool_input": map[string]any{"to": warmAgent, "message": "The order test regressed; fix it."},
	}), Host: "claude-code"})

	assert.NotEqual(t, "deny", v.Decision, v.Reason)
}

func TestFindWarmWorker(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ttl := 5 * time.Minute
	ended := types.Job{ID: "gate-fits", State: types.StatePass, WritePaths: []string{"a.go"}, Updated: now.Add(-2 * time.Minute).Unix()}
	next := types.Job{ID: "regress-fix", State: types.StateDeclared, Follows: "gate-fits"}
	haiku := []types.ModelPricing{{ID: "claude-haiku-9", Alias: "haiku", LongContext: &types.PriceStep{Above: 100_000, Multiplier: 2}}}
	tokens := func(n int64) *int64 { return &n }

	tests := []struct {
		name   string
		rec    spawnedAgent
		now    time.Time
		models []types.ModelPricing
		want   bool
	}{
		{"within the TTL", spawnedAgent{ID: warmAgent, Description: warmEndedTitle}, now, nil, true},
		{"after the TTL", spawnedAgent{ID: warmAgent, Description: warmEndedTitle}, now.Add(ttl), nil, false},
		{"economy near its price step", spawnedAgent{ID: warmAgent, Description: warmEndedTitle, Model: "haiku", ContextTokens: tokens(95_000)}, now, haiku, false},
		{"economy well below its price step", spawnedAgent{ID: warmAgent, Description: warmEndedTitle, Model: "haiku", ContextTokens: tokens(40_000)}, now, haiku, true},
		{"a model with no price step at any size", spawnedAgent{ID: warmAgent, Description: warmEndedTitle, Model: "opus", ContextTokens: tokens(359_000)}, now, haiku, true},
		{"a title its spawner could not hand out", spawnedAgent{ID: warmAgent, Description: warmEndedTitle, UntrustedJob: "gate-fits"}, now, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newReworkFixture(t)
			writeSpawnedAgent(f.facts, warmAgent, tc.rec)

			w, warm := findWarmWorker(f.facts, []types.Job{ended, next}, next, tc.models, ttl, tc.now)

			require.Equal(t, tc.want, warm)
			if warm {
				assert.Equal(t, warmWorker{agent: warmAgent, job: "gate-fits", link: "follows gate-fits", idle: 2 * time.Minute, left: 3 * time.Minute}, w)
			}
		})
	}
}

func TestResumeTTLNamesItsSource(t *testing.T) {
	got := resumeTTL()

	assert.Equal(t, 5*time.Minute, got.ttl)
	assert.Contains(t, got.source, "https://")
}
