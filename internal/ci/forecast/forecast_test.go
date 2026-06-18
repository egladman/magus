package forecast

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func projects(paths ...string) []*types.Project {
	out := make([]*types.Project, len(paths))
	for i, p := range paths {
		out[i] = &types.Project{Path: p}
	}
	return out
}

func TestForecaster_Plan_emptyHistory(t *testing.T) {
	t.Parallel()
	// Empty history: predictor falls back to DefaultDurationMs per
	// project, default constants. With 5 projects and defaults, the
	// circuit breaker should NOT fire (W = 5×60s = 300s > 60s = 2×30s).
	f := Forecaster{
		History: History{},
		Target:  "ci",
	}
	ps := projects("a", "b", "c", "d", "e")
	shards := f.Plan(ps, 8)
	require.NotEmpty(t, shards, "want at least 1 shard")
	// Sanity: every project assigned exactly once.
	seen := map[string]int{}
	for _, s := range shards {
		for _, p := range s {
			seen[p.Path]++
		}
	}
	for _, p := range ps {
		assert.Equalf(t, 1, seen[p.Path], "project %q assignment count", p.Path)
	}
}

func TestForecaster_Plan_circuitBreakerOnTrivialPR(t *testing.T) {
	t.Parallel()
	// Single fast project: W = DefaultDurationMs (60s) < 2×SetupP50Ms (60s)?
	// 60_000 < 60_000 is false. So we craft a small project explicitly
	// in history to force the circuit breaker.
	now := time.Now()
	h := History{}
	// Seed enough samples that PredictDuration uses the project value.
	samples := make([]Sample, 0, 5)
	for i := 0; i < 5; i++ {
		samples = append(samples, Sample{
			Project: "tiny", Target: "ci", DurationMs: 5_000, // 5s
		})
	}
	h.Update(now, samples, nil)

	f := Forecaster{History: h, Target: "ci"}
	shards := f.Plan(projects("tiny"), 8)
	assert.Len(t, shards, 1, "circuit breaker: want 1 shard for trivial PR")
}

func TestHistory_PredictDuration(t *testing.T) {
	t.Parallel()
	now := time.Now()
	h := History{}
	for i := 0; i < 5; i++ {
		h.Update(now, []Sample{
			{Project: "a", Target: "ci", DurationMs: 10_000},
		}, nil)
	}

	assert.Equal(t, 10*time.Second, h.PredictDuration("a", "ci", nil), "project with 5 samples of 10s")

	// Project with no entry falls back to workspace fallback.
	assert.Equal(t, 10*time.Second, h.PredictDuration("never-seen", "ci", nil), "unknown project, workspace fallback")

	// Brand-new history: hard default.
	empty := History{}
	want := time.Duration(DefaultDurationMs) * time.Millisecond
	assert.Equal(t, want, empty.PredictDuration("x", "ci", nil), "empty history")
}

func TestHistory_PredictDuration_lowSampleFloor(t *testing.T) {
	t.Parallel()
	// Fewer than 3 samples → fall through to workspace fallback.
	now := time.Now()
	h := History{}
	h.Update(now, []Sample{
		{Project: "alpha", Target: "ci", DurationMs: 100_000}, // outlier
		{Project: "other", Target: "ci", DurationMs: 1_000},
		{Project: "other", Target: "ci", DurationMs: 1_000},
		{Project: "other", Target: "ci", DurationMs: 1_000},
	}, nil)

	// alpha has 1 sample (<3) → workspace fallback (p75 of all four
	// samples, which is dominated by the 100s outlier).
	got := h.PredictDuration("alpha", "ci", nil)
	assert.NotEqual(t, 100*time.Second, got, "with only 1 sample, alpha should not return its own 100s value")
}

func TestHistory_SaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	h := History{}
	h.Update(now, []Sample{
		{Project: "api", Target: "ci", DurationMs: 42_000},
		{Project: "api", Target: "ci", DurationMs: 45_000},
		{Project: "api", Target: "ci", DurationMs: 40_000},
	}, []ShardSample{
		{SetupMs: 25_000, TotalMs: 100_000, WorkMs: 200_000, NShards: 4},
	})

	require.NoError(t, h.Save(context.Background(), path), "Save")

	var got History
	require.NoError(t, got.Load(context.Background(), path), "Load")

	assert.Equal(t, HistoryVersion, got.Version)
	assert.Equal(t, 3, got.Projects["api"]["ci"].Samples, "api/ci samples")
	assert.Equal(t, Millis(25_000), got.Constants.SetupP50Ms)
}

func TestLoad_missingFile(t *testing.T) {
	t.Parallel()
	var got History
	require.NoError(t, got.Load(context.Background(), filepath.Join(t.TempDir(), "does-not-exist.json")), "missing file should not error")
	assert.Equal(t, HistoryVersion, got.Version, "missing file")
}

func TestHistory_Update_capsAtSampleWindow(t *testing.T) {
	t.Parallel()
	now := time.Now()
	h := History{}
	for i := 0; i < SampleWindow*2; i++ {
		h.Update(now, []Sample{
			{Project: "p", Target: "ci", DurationMs: int64(1_000 + i)},
		}, nil)
	}
	st := h.Projects["p"]["ci"]
	assert.Len(t, st.Recent, SampleWindow, "Recent length")
	assert.Equal(t, SampleWindow*2, st.Samples, "Samples counter")
}

// TestForecaster_Plan_hitRateReducesShards verifies that a workspace where
// most projects have a high cache-hit rate collapses to fewer shards than the
// same workspace without any hit history. Six projects, five with hit_rate≈0.95
// and one always-miss, all with miss p75=60s. The hit-aware plan should require
// fewer shards than the miss-only baseline.
func TestForecaster_Plan_hitRateReducesShards(t *testing.T) {
	t.Parallel()
	now := time.Now()
	h := History{}

	// Seed 3 miss samples per project so p75 = 60_000 and Samples ≥ 3.
	allProjects := []string{"a", "b", "c", "d", "e", "f"}
	for _, proj := range allProjects {
		for i := 0; i < 3; i++ {
			h.Update(now, []Sample{
				{Project: proj, Target: "ci", DurationMs: 60_000},
			}, nil)
		}
	}

	// Seed the scheduler constants so OptimalShardCount is deterministic.
	h.Update(now, nil, []ShardSample{
		{SetupMs: 30_000, TotalMs: 100_000, WorkMs: 200_000, NShards: 4},
	})

	// Build a baseline forecaster (no hit history yet — cold start for all).
	baseline := Forecaster{History: h, Target: "ci"}
	ps := projects(allProjects...)
	baselineShards := baseline.Plan(ps, 8)

	// Add 9 consecutive hits to five of the six projects. Starting from
	// MissCount=3 (duration seeds), hits 8 and 9 evict misses one by one
	// so the window settles at HitCount=9, MissCount=1 → hit_rate=0.9.
	highHitProjects := []string{"a", "b", "c", "d", "e"}
	for _, proj := range highHitProjects {
		for i := 0; i < 9; i++ {
			h.Update(now, []Sample{
				{Project: proj, Target: "ci", Hit: true},
			}, nil)
		}
	}

	hitAware := Forecaster{History: h, Target: "ci"}
	hitAwareShards := hitAware.Plan(ps, 8)

	assert.Lessf(t, len(hitAwareShards), len(baselineShards),
		"expected fewer shards with high hit rate (hit-aware=%d baseline=%d)", len(hitAwareShards), len(baselineShards))

	// Every project must appear exactly once in both plans.
	for _, plan := range [][][]*types.Project{baselineShards, hitAwareShards} {
		seen := map[string]int{}
		for _, shard := range plan {
			for _, p := range shard {
				seen[p.Path]++
			}
		}
		for _, proj := range allProjects {
			assert.Equalf(t, 1, seen[proj], "project %q assignment count", proj)
		}
	}
}
