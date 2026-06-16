package forecast

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/types"
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
	if len(shards) < 1 {
		t.Fatalf("want at least 1 shard, got 0")
	}
	// Sanity: every project assigned exactly once.
	seen := map[string]int{}
	for _, s := range shards {
		for _, p := range s {
			seen[p.Path]++
		}
	}
	for _, p := range ps {
		if seen[p.Path] != 1 {
			t.Errorf("project %q assigned %d times, want 1", p.Path, seen[p.Path])
		}
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
	if len(shards) != 1 {
		t.Fatalf("circuit breaker: want 1 shard for trivial PR, got %d", len(shards))
	}
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

	if got := h.PredictDuration("a", "ci", nil); got != 10*time.Second {
		t.Errorf("project with 5 samples of 10s: got %v, want 10s", got)
	}

	// Project with no entry falls back to workspace fallback.
	if got := h.PredictDuration("never-seen", "ci", nil); got != 10*time.Second {
		t.Errorf("unknown project, workspace fallback should be 10s, got %v", got)
	}

	// Brand-new history: hard default.
	empty := History{}
	want := time.Duration(DefaultDurationMs) * time.Millisecond
	if got := empty.PredictDuration("x", "ci", nil); got != want {
		t.Errorf("empty history: got %v, want %v", got, want)
	}
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
	if got == 100*time.Second {
		t.Errorf("with only 1 sample, alpha should not return its own 100s value, got %v", got)
	}
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

	if err := h.Save(context.Background(), path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var got History
	if err := got.Load(context.Background(), path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Version != HistoryVersion {
		t.Errorf("Version: got %d, want %d", got.Version, HistoryVersion)
	}
	if got.Projects["api"]["ci"].Samples != 3 {
		t.Errorf("api/ci samples: got %d, want 3", got.Projects["api"]["ci"].Samples)
	}
	if got.Constants.SetupP50Ms != 25_000 {
		t.Errorf("SetupP50Ms: got %d, want 25000", got.Constants.SetupP50Ms)
	}
}

func TestLoad_missingFile(t *testing.T) {
	t.Parallel()
	var got History
	if err := got.Load(context.Background(), filepath.Join(t.TempDir(), "does-not-exist.json")); err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if got.Version != HistoryVersion {
		t.Errorf("missing file: got version %d, want %d", got.Version, HistoryVersion)
	}
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
	if len(st.Recent) != SampleWindow {
		t.Errorf("Recent length: got %d, want %d", len(st.Recent), SampleWindow)
	}
	if st.Samples != SampleWindow*2 {
		t.Errorf("Samples counter: got %d, want %d", st.Samples, SampleWindow*2)
	}
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

	if len(hitAwareShards) >= len(baselineShards) {
		t.Errorf("hit-aware plan has %d shards, baseline has %d; expected fewer shards with high hit rate",
			len(hitAwareShards), len(baselineShards))
	}

	// Every project must appear exactly once in both plans.
	for _, plan := range [][][]*types.Project{baselineShards, hitAwareShards} {
		seen := map[string]int{}
		for _, shard := range plan {
			for _, p := range shard {
				seen[p.Path]++
			}
		}
		for _, proj := range allProjects {
			if seen[proj] != 1 {
				t.Errorf("project %q assigned %d times, want 1", proj, seen[proj])
			}
		}
	}
}
