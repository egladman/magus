package forecast

import (
	"context"
	"github.com/egladman/magus/internal/hostmem"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
	"time"
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

func TestOptimalShardCount(t *testing.T) {
	t.Parallel()

	t.Run("trivial PR triggers circuit breaker", func(t *testing.T) {
		assert.Equal(t, 1, optimalShardCount(30_000, 30_000, 5_000, 8))
	})
	t.Run("zero work", func(t *testing.T) {
		assert.Equal(t, 1, optimalShardCount(0, 30_000, 5_000, 8))
	})
	t.Run("alpha=0 means fan out fully", func(t *testing.T) {
		assert.Equal(t, 8, optimalShardCount(1_000_000, 30_000, 0, 8))
	})
	t.Run("sqrt picks middle when both substantial", func(t *testing.T) {
		assert.Equal(t, 10, optimalShardCount(500_000, 30_000, 5_000, 16))
	})
	t.Run("big work clamps to maxN", func(t *testing.T) {
		assert.Equal(t, 8, optimalShardCount(100_000_000, 30_000, 1_000, 8))
	})
	t.Run("maxN floor of 1", func(t *testing.T) {
		assert.Equal(t, 1, optimalShardCount(60_000, 30_000, 5_000, 0))
	})
}

func testProjects(paths ...string) []*types.Project {
	out := make([]*types.Project, len(paths))
	for i, p := range paths {
		out[i] = &types.Project{Path: p}
	}
	return out
}

func TestLPT_balancesByDuration(t *testing.T) {
	t.Parallel()
	ps := testProjects("slow1", "slow2", "slow3", "fast1", "fast2", "fast3")
	durs := []int64{60_000, 60_000, 60_000, 1_000, 1_000, 1_000}

	shards := lpt(ps, durs, 3)
	require.Len(t, shards, 3)
	for i, s := range shards {
		slow := 0
		for _, p := range s {
			if p.Path == "slow1" || p.Path == "slow2" || p.Path == "slow3" {
				slow++
			}
		}
		assert.Equalf(t, 1, slow, "shard %d should have exactly 1 slow project", i)
	}
}

func TestLPT_emptyAndDegenerate(t *testing.T) {
	t.Parallel()

	assert.Nil(t, lpt(nil, nil, 4))

	ps := testProjects("a")
	got := lpt(ps, []int64{1_000}, 8)
	assert.Len(t, got, 1, "1 project, 8 shards → 1 shard (empty shards pruned)")
}

// seedPeak records one outcome carrying a peak, enough for PredictPeakRSS.
// consolidating returns constants under which optimalShardCount collapses to a
// single shard (work < 2*setup), which is the situation the memory constraint
// exists to override.
func consolidating() Constants { return Constants{SetupP50Ms: 600_000, AlphaMs: 5_000} }

func seedPeak(h *History, project, target string, bytes int64) {
	if h.Projects == nil {
		h.Projects = map[string]map[string]Stats{}
	}
	if h.Projects[project] == nil {
		h.Projects[project] = map[string]Stats{}
	}
	s := h.Projects[project][target]
	s.RecentOutcomes = append(s.RecentOutcomes, Outcome{Result: "pass", MaxRSSBytes: bytes})
	h.Projects[project][target] = s
}

func TestPlan_memoryBudgetSplitsAShardThatWouldNotFit(t *testing.T) {
	t.Parallel()
	// The shape that took the runner down: two heavy projects the duration model
	// is happy to consolidate, on a machine that cannot hold both at once.
	h := History{Constants: consolidating()}
	seedPeak(&h, "docs", "ci", 7<<30)
	seedPeak(&h, ".", "ci", 6<<30)
	ps := projects("docs", ".")

	// An effectively infinite budget, NOT a zero one: zero means "derive from this
	// host", so a bare Forecaster asserts something different on a 16GB CI runner
	// than on a workstation - which is precisely how this test passed locally and
	// failed in CI. Pin the budget whenever the assertion is about the time model.
	unbudgeted := Forecaster{History: h, Target: "ci", MemoryBudgetBytes: 1 << 62}.Plan(ps, 8)
	require.Len(t, unbudgeted, 1, "precondition: the time model consolidates these")

	// The budget is USABLE memory, not the runner's nameplate: a 16GB runner also
	// holds the agent, the Go build cache, node and the toolchains. 13GB of
	// projects does not fit in what is left.
	budgeted := Forecaster{History: h, Target: "ci", MemoryBudgetBytes: 12 << 30}.Plan(ps, 8)
	assert.Len(t, budgeted, 2, "13GB of projects exceeds 12GB of usable memory; split it")
}

func TestPlan_memoryBudgetLeavesAFittingPlanAlone(t *testing.T) {
	t.Parallel()
	// Two small projects fit together, so the budget must not buy runners nobody
	// needs - the constraint exists to prevent a death, not to fan out by default.
	h := History{Constants: consolidating()}
	seedPeak(&h, "a", "ci", 200<<20)
	seedPeak(&h, "b", "ci", 300<<20)
	ps := projects("a", "b")

	got := Forecaster{History: h, Target: "ci", MemoryBudgetBytes: 16 << 30}.Plan(ps, 8)
	assert.Len(t, got, 1, "500MB fits comfortably; do not split")
}

func TestPlan_unmeasuredProjectsPlanExactlyAsBefore(t *testing.T) {
	t.Parallel()
	// The compatibility guarantee. A workspace that has recorded no peaks - every
	// workspace, the first time this ships - must get byte-identical planning,
	// because unknown is not zero and must not be read as either free or vast.
	h := History{}
	ps := projects("a", "b", "c", "d")

	// Pinned, not derived: a bare Forecaster reads this machine's memory, which
	// would make the comparison mean different things on different hosts.
	before := Forecaster{History: h, Target: "ci", MemoryBudgetBytes: 1 << 62}.Plan(ps, 8)
	after := Forecaster{History: h, Target: "ci", MemoryBudgetBytes: 1 << 30}.Plan(ps, 8)
	assert.Equal(t, len(before), len(after), "no recorded peaks must mean no change in plan")
}

func TestPlan_neverPlansFewerShardsThanTheTimeModelAsked(t *testing.T) {
	t.Parallel()
	// The constraint may only add runners. A memory prediction that is wrong
	// should cost money, never correctness.
	h := History{Constants: consolidating()}
	seedPeak(&h, "a", "ci", 1<<20)
	seedPeak(&h, "b", "ci", 1<<20)
	ps := projects("a", "b")

	base := Forecaster{History: h, Target: "ci", MemoryBudgetBytes: 1 << 62}.Plan(ps, 8)
	withBudget := Forecaster{History: h, Target: "ci", MemoryBudgetBytes: 64 << 30}.Plan(ps, 8)
	assert.GreaterOrEqual(t, len(withBudget), len(base))
}

func TestPlan_oneProjectOverBudgetDoesNotSpin(t *testing.T) {
	t.Parallel()
	// A single project bigger than the whole budget cannot be helped by splitting.
	// It must terminate at the limit rather than loop, and still return every
	// project exactly once.
	h := History{Constants: consolidating()}
	seedPeak(&h, "huge", "ci", 100<<30)
	ps := projects("huge", "small")
	seedPeak(&h, "small", "ci", 1<<20)

	got := Forecaster{History: h, Target: "ci", MemoryBudgetBytes: 1 << 30}.Plan(ps, 8)
	seen := map[string]int{}
	for _, s := range got {
		for _, p := range s {
			seen[p.Path]++
		}
	}
	assert.Equal(t, map[string]int{"huge": 1, "small": 1}, seen, "never drop or duplicate work")
}

func TestMemoryBudget_derivedFromTheHostWithNoKnob(t *testing.T) {
	t.Parallel()
	// The default path: no caller sets anything, and a budget still appears on any
	// host magus can ask. This is the whole point of not exposing a config key -
	// the protection has to be on for people who never heard of it.
	f := Forecaster{Target: "ci"}
	got := f.memoryBudget()
	if hostmem.Total() == 0 {
		assert.Zero(t, got, "an unmeasurable host must plan exactly as before, never on a guess")
		return
	}
	assert.Positive(t, got, "a measurable host must get a budget without being configured")
	assert.Less(t, got, hostmem.Total(), "the budget must leave headroom for the agent, caches and toolchains")
}

func TestMemoryBudget_explicitSettingWins(t *testing.T) {
	t.Parallel()
	// Settable only so a test can pin behavior independently of the machine
	// running it; no config key reaches this.
	f := Forecaster{Target: "ci", MemoryBudgetBytes: 7 << 30}
	assert.Equal(t, int64(7<<30), f.memoryBudget())
}
