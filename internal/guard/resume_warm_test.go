package guard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

const (
	warmAgent      = "a1b2c3"
	warmEndedTitle = "orchestrator/feat gate-fits"
	warmSpawnTitle = "orchestrator/feat regress-fix"
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
			assert.Contains(t, v.Reason, "TTL 5m0s: the provider default", "the TTL's source is said")
		})
	}
}

func TestSpawnWarmWorkerReadsTheWorkspaceTTL(t *testing.T) {
	f := warmFixture(t, func(j *types.Job) { j.Follows = "gate-fits" })
	require.NoError(t, os.WriteFile(filepath.Join(f.at.workspace, "magus.yaml"), []byte("agents:\n  prompt_cache_ttl: 1h\n"), 0o644))

	v := f.spawn(t)

	require.Equal(t, string(denyRuleSpawnWarmWorker), v.Rule, v.Reason)
	assert.Contains(t, v.Reason, "TTL 1h0m0s: agents.prompt_cache_ttl in magus.yaml")
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

			w, warm := findWarmWorker(f.facts, []types.Job{ended, next}, next, warmSpawnTitle, tc.models, ttl, tc.now)

			require.Equal(t, tc.want, warm)
			if warm {
				assert.Equal(t, warmWorker{agent: warmAgent, job: "gate-fits", link: "follows gate-fits", idle: 2 * time.Minute, left: 3 * time.Minute}, w)
			}
		})
	}
}

func TestFindWarmWorkerSkipsAWorkerOfAnotherRole(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ended := types.Job{ID: "gate-fits", State: types.StatePass, WritePaths: []string{"a.go"}, Updated: now.Add(-time.Minute).Unix()}
	next := types.Job{ID: "regress-fix", State: types.StateDeclared, Follows: "gate-fits"}
	f := newReworkFixture(t)
	writeSpawnedAgent(f.facts, warmAgent, spawnedAgent{ID: warmAgent, Description: warmEndedTitle})

	_, warm := findWarmWorker(f.facts, []types.Job{ended, next}, next, "orchestrator/review regress-fix", nil, 5*time.Minute, now)
	assert.False(t, warm, "a reviewer is not the writer's continuation")

	_, warm = findWarmWorker(f.facts, []types.Job{ended, next}, next, "other/feat regress-fix", nil, 5*time.Minute, now)
	assert.True(t, warm, "the same role under another parent still continues")
}

// A resumed worker stays bound to its own checkout, so it is served only for a job taken there.
func TestFindWarmWorkerServesOnlyAWorkerInTheJobsCheckout(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	root := t.TempDir()
	ended := types.Job{ID: "gate-fits", State: types.StatePass, WritePaths: []string{"a.go"}, CheckoutRoot: root, Updated: now.Add(-time.Minute).Unix()}
	f := newReworkFixture(t)
	writeSpawnedAgent(f.facts, warmAgent, spawnedAgent{ID: warmAgent, Description: warmEndedTitle})
	warmFor := func(next types.Job) bool {
		_, warm := findWarmWorker(f.facts, []types.Job{ended, next}, next, warmSpawnTitle, nil, 5*time.Minute, now)
		return warm
	}

	here := types.Job{ID: "regress-fix", State: types.StateDeclared, Follows: "gate-fits"}
	assert.True(t, warmFor(here), "a job taken in the worker's checkout continues it")
	elsewhere := here
	elsewhere.Worktree = &types.JobWorktree{Root: filepath.Join(t.TempDir(), "gate2")}
	assert.False(t, warmFor(elsewhere), "a job forked into another checkout does not")
}

func TestResumeTTLNamesItsSource(t *testing.T) {
	got := resumeTTL(account{})
	assert.Equal(t, 5*time.Minute, got.ttl)
	assert.Contains(t, got.source, "the provider default")
	assert.Contains(t, got.source, "https://")

	got = resumeTTL(account{cacheTTL: time.Hour})
	assert.Equal(t, promptCacheTTL{ttl: time.Hour, source: "agents.prompt_cache_ttl in magus.yaml"}, got)

	got = resumeTTL(account{loadErr: errors.New("agents.prompt_cache_ttl: bad duration")})
	assert.Equal(t, 5*time.Minute, got.ttl)
	assert.Contains(t, got.source, "magus.yaml did not load (agents.prompt_cache_ttl: bad duration), so the provider default")
}
