package forecast

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedNow is a deterministic ingest timestamp so no test depends on wall-clock.
var fixedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// TestHistorySchemaLock asserts that the JSON representation of a fully
// populated History contains only the approved set of top-level keys. Any
// field added to History, Stats, Constants, or their nested types must be
// added to the allowlist below WITH a comment explaining why it is safe to
// store in a shared CI cache (see the cache-safety notice at the top of
// history.go).
func TestHistorySchemaLock(t *testing.T) {
	h := History{
		Version:   HistoryVersion,
		UpdatedAt: time.Now(),
		Constants: Constants{
			SetupP50Ms: 30_000,
			AlphaMs:    5_000,
		},
		Projects: map[string]map[string]Stats{
			"services/api": {
				"ci": {
					P75Ms:       12_000,
					Samples:     10,
					LastUpdated: time.Now(),
					Recent:      []int64{11_000, 12_000, 13_000},
					HitCount:    8,
					MissCount:   2,
					HitRate:     0.8,
					PassCount:   7,
					FailCount:   1,
					FlakeCount:  2,
					RecentOutcomes: []Outcome{
						{Result: "pass", AffectedByDiff: true, DurationMs: 11_000, At: time.Now(), Attempts: 1},
						{Result: "flake", AffectedByDiff: false, DurationMs: 22_000, At: time.Now(), Attempts: 2},
					},
				},
			},
		},
		Setup:               []int64{29_000, 31_000},
		Alpha:               []int64{4_800, 5_200},
		WorkspaceFallbackMs: 9_000,
	}

	b, err := json.Marshal(h)
	require.NoError(t, err, "marshal History")

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &top), "unmarshal to map")

	// Approved top-level keys. Extend only with fields that contain
	// integer timings, project paths, or subdir tag names — never
	// source code, hashes, secrets, author identity, or error messages.
	allowed := map[string]bool{
		"version":               true, // schema version int
		"updated_at":            true, // ISO timestamp of last ingest
		"constants":             true, // {setup_p50_ms, alpha_ms} — integer ms
		"projects":              true, // map[path]map[target]Stats — paths + int timings
		"setup":                 true, // []int64 of per-shard setup observations
		"alpha":                 true, // []int64 of fitted α observations
		"workspace_fallback_ms": true, // int64 workspace-wide p75
	}
	for k := range top {
		assert.Truef(t, allowed[k], "unexpected History JSON key %q — add to allowed map with a safety justification, or remove the field", k)
	}

	// Also lock the Stats sub-schema.
	var projects map[string]map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["projects"], &projects), "unmarshal projects")
	allowedStats := map[string]bool{
		"p75_ms":          true, // int64 — rolling p75 of recent durations
		"samples":         true, // int   — total number of ingested miss samples
		"last_updated":    true, // ISO timestamp
		"recent":          true, // []int64 — raw recent duration samples
		"buckets":         true, // map[string]BucketStats — per-workload-tag sub-stores (tag names are top-level subdir names, safe to share)
		"hit_count":       true, // int — rolling count of cache hits; no source content, no keys, no payloads
		"miss_count":      true, // int — rolling count of cache misses; same safety profile as hit_count
		"hit_rate":        true, // float64 — hit_count/(hit_count+miss_count); derived aggregate, no new surface
		"pass_count":      true, // int — rolling count of passing runs; safe integer counter, no source content
		"fail_count":      true, // int — rolling count of failing runs; safe integer counter, no source content
		"flake_count":     true, // int — rolling count of flaky runs (fail→pass); safe integer counter
		"recent_outcomes": true, // []Outcome — per-run result enum + timing + bool; see Outcome allowlist below
	}
	for path, targets := range projects {
		for target, stats := range targets {
			for k := range stats {
				assert.Truef(t, allowedStats[k], "unexpected Stats JSON key %q (project=%q target=%q) — add to allowedStats with a safety justification", k, path, target)
			}
		}
	}

	// Lock the Outcome sub-schema (entries in recent_outcomes).
	allowedOutcome := map[string]bool{
		"result":      true, // string enum: "pass"|"fail"|"flake" — no source content
		"affected":    true, // bool — whether project was in the VCS diff's affected set; no source content
		"duration_ms": true, // int64 — wall-clock timing; same safety profile as p75_ms
		"at":          true, // ISO timestamp of when the run completed; same safety profile as last_updated
		"attempts":    true, // int — number of attempts (1 or 2); safe integer counter
	}
	type outcomeMap map[string]json.RawMessage
	for path, targets := range projects {
		for target, statsRaw := range targets {
			rawOutcomes, ok := statsRaw["recent_outcomes"]
			if !ok {
				continue
			}
			var outcomes []outcomeMap
			require.NoErrorf(t, json.Unmarshal(rawOutcomes, &outcomes), "unmarshal recent_outcomes (project=%q target=%q)", path, target)
			for _, o := range outcomes {
				for k := range o {
					assert.Truef(t, allowedOutcome[k], "unexpected Outcome JSON key %q (project=%q target=%q) — add to allowedOutcome with a safety justification", k, path, target)
				}
			}
		}
	}
}

// TestHistoryV1LoadsIntoV2 verifies that a v1-era history file (no Buckets
// field) loads cleanly into the v3 schema and predictions fall through to
// the project-wide p75 without panicking.
func TestHistoryV1LoadsIntoV2(t *testing.T) {
	const v1JSON = `{
		"version": 1,
		"updated_at": "2025-01-01T00:00:00Z",
		"constants": {"setup_p50_ms": 30000, "alpha_ms": 5000},
		"projects": {
			"services/api": {
				"ci": {"p75_ms": 12000, "samples": 5, "last_updated": "2025-01-01T00:00:00Z", "recent": [11000,12000,13000,11500,12500]}
			}
		},
		"setup": [29000],
		"alpha": [4800],
		"workspace_fallback_ms": 9000
	}`

	var h History
	require.NoError(t, json.Unmarshal([]byte(v1JSON), &h), "unmarshal v1 history")

	// Should fall through to project-wide p75 (no buckets, no hit data → cold start).
	got := h.PredictDuration("services/api", "ci", []string{"direct", "direct.src"})
	assert.Equal(t, 12*time.Second, got, "project p75")

	// Transitive tag also falls through cleanly.
	got = h.PredictDuration("services/api", "ci", []string{"transitive"})
	assert.Equal(t, 12*time.Second, got, "transitive, no buckets")
}

// TestHistoryV2LoadsIntoV3 verifies that a v2-era history file (no hit count
// fields) loads cleanly into the v3 schema and predictions use raw p75 (no
// discount) until hit/miss observations arrive.
func TestHistoryV2LoadsIntoV3(t *testing.T) {
	const v2JSON = `{
		"version": 2,
		"updated_at": "2025-06-01T00:00:00Z",
		"constants": {"setup_p50_ms": 30000, "alpha_ms": 5000},
		"projects": {
			"apps/web": {
				"ci": {
					"p75_ms": 20000, "samples": 5,
					"last_updated": "2025-06-01T00:00:00Z",
					"recent": [18000,20000,22000,19000,21000],
					"buckets": {
						"transitive": {"p75_ms": 18000, "samples": 3, "recent": [17000,18000,19000]}
					}
				}
			}
		},
		"setup": [28000],
		"alpha": [5100],
		"workspace_fallback_ms": 20000
	}`

	var h History
	require.NoError(t, json.Unmarshal([]byte(v2JSON), &h), "unmarshal v2 history")

	// hit_count and miss_count default to zero → cold start → raw p75 returned.
	got := h.PredictDuration("apps/web", "ci", nil)
	assert.Equal(t, 20*time.Second, got, "raw p75, cold start")
}

// TestHistory_PredictDuration_hitRateDiscount verifies that a Stats entry
// with sufficient hit observations applies the (1−hit_rate)·p75 formula.
func TestHistory_PredictDuration_hitRateDiscount(t *testing.T) {
	t.Parallel()
	now := time.Now()
	h := History{}

	// Seed 3 miss samples so the project-wide p75 = 60_000 and Samples ≥ 3.
	for i := 0; i < 3; i++ {
		h.Update(now, []Sample{
			{Project: "svc", Target: "ci", DurationMs: 60_000},
		}, nil)
	}
	// Add 9 consecutive hits. Starting from MissCount=3 (duration seeds),
	// hits 8 and 9 evict misses one by one so the window settles at
	// HitCount=9, MissCount=1 → hit_rate=0.9, total=10 ≥ hitColdStart=5.
	for i := 0; i < 9; i++ {
		h.Update(now, []Sample{
			{Project: "svc", Target: "ci", Hit: true},
		}, nil)
	}

	st := h.Projects["svc"]["ci"]
	require.GreaterOrEqual(t, st.HitCount+st.MissCount, 5, "need ≥5 combined observations for discount")
	require.Positive(t, st.HitRate, "expected positive hit rate")

	got := h.PredictDuration("svc", "ci", nil)
	// Expected: p75 * (1 - hit_rate). p75 = 60s, hit_rate ≈ 0.9 → ~6s.
	// Allow ±20% tolerance for integer arithmetic in the rolling window.
	assert.Less(t, got, 60*time.Second, "hit-rate discount should be applied")
	assert.GreaterOrEqual(t, got, time.Millisecond, "stability floor")
}

// TestHistory_PredictDuration_coldStart verifies that a Stats entry with
// fewer than hitColdStart combined observations returns the raw p75.
func TestHistory_PredictDuration_coldStart(t *testing.T) {
	t.Parallel()
	now := time.Now()
	h := History{}

	// 3 misses → p75 = 60_000, Samples = 3.
	for i := 0; i < 3; i++ {
		h.Update(now, []Sample{
			{Project: "svc", Target: "ci", DurationMs: 60_000},
		}, nil)
	}
	// 2 hits → total hit+miss = 2+3 = 5... wait, 3 misses already count.
	// We need HitCount+MissCount < 5. With 3 misses, MissCount = 3 (within window).
	// Add only 1 hit → total = 4 < 5.
	h.Update(now, []Sample{
		{Project: "svc", Target: "ci", Hit: true},
	}, nil)

	st := h.Projects["svc"]["ci"]
	total := st.HitCount + st.MissCount
	require.Less(t, total, 5, "test setup: need total < 5")

	got := h.PredictDuration("svc", "ci", nil)
	assert.Equal(t, 60*time.Second, got, "raw p75")
}

// TestHistoryV3LoadsIntoV4 verifies that a v3 history file (no flake fields)
// loads cleanly into the v4 schema and flake counts default to zero.
func TestHistoryV3LoadsIntoV4(t *testing.T) {
	const v3JSON = `{
		"version": 3,
		"updated_at": "2025-12-01T00:00:00Z",
		"constants": {"setup_p50_ms": 30000, "alpha_ms": 5000},
		"projects": {
			"services/api": {
				"ci": {
					"p75_ms": 15000, "samples": 5,
					"last_updated": "2025-12-01T00:00:00Z",
					"recent": [14000,15000,16000,14500,15500],
					"hit_count": 3, "miss_count": 2, "hit_rate": 0.6
				}
			}
		},
		"setup": [30000],
		"alpha": [5000],
		"workspace_fallback_ms": 15000
	}`

	var h History
	require.NoError(t, json.Unmarshal([]byte(v3JSON), &h), "unmarshal v3 history")

	st := h.Projects["services/api"]["ci"]
	assert.Zero(t, st.PassCount, "v3→v4: expected zero pass count")
	assert.Zero(t, st.FailCount, "v3→v4: expected zero fail count")
	assert.Zero(t, st.FlakeCount, "v3→v4: expected zero flake count")
	assert.Empty(t, st.RecentOutcomes, "v3→v4: expected empty RecentOutcomes")
	// Duration prediction should still work correctly.
	got := h.PredictDuration("services/api", "ci", nil)
	assert.Positive(t, got, "v3→v4: PredictDuration")
}

// TestHistory_Update_hitRateProperty verifies that after any sequence of
// hit/miss updates, HitRate == HitCount/(HitCount+MissCount) exactly.
func TestHistory_Update_hitRateProperty(t *testing.T) {
	t.Parallel()
	now := time.Now()
	h := History{}

	observations := []bool{
		true, false, true, true, false, true, true, true, false, true,
		false, true, false, false, true, true, true, false, true, false,
	}
	for _, hit := range observations {
		s := Sample{Project: "p", Target: "ci"}
		if hit {
			s.Hit = true
		} else {
			s.DurationMs = 10_000
		}
		h.Update(now, []Sample{s}, nil)

		st := h.Projects["p"]["ci"]
		total := st.HitCount + st.MissCount
		if total == 0 {
			continue
		}
		wantRate := float64(st.HitCount) / float64(total)
		assert.Equalf(t, wantRate, st.HitRate, "HitCount=%d MissCount=%d", st.HitCount, st.MissCount)
	}
}

// TestHistoryMerge verifies the per-(project,target) freshest-wins union used to
// combine per-shard CI histories: disjoint projects unite, and where shards share
// an entry the newer LastUpdated wins.
func TestHistoryMerge(t *testing.T) {
	older := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)

	// Base both shards share (project "a"), plus each shard's own project.
	shard0 := History{
		Version:   HistoryVersion,
		UpdatedAt: older,
		Projects: map[string]map[string]Stats{
			"a": {"test": {P75Ms: 100, LastUpdated: older}}, // stale base copy
			"b": {"test": {P75Ms: 200, LastUpdated: newer}}, // shard 0 ran b
		},
	}
	shard1 := History{
		Version:   HistoryVersion,
		UpdatedAt: newer,
		Projects: map[string]map[string]Stats{
			"a": {"test": {P75Ms: 150, LastUpdated: newer}}, // shard 1 ran a (fresher)
			"c": {"test": {P75Ms: 300, LastUpdated: newer}}, // shard 1 ran c
		},
	}

	var merged History
	merged.Merge(&shard0)
	merged.Merge(&shard1)

	assert.Equal(t, int64(150), merged.Projects["a"]["test"].P75Ms, "a: fresher shard1 wins")
	assert.Equal(t, int64(200), merged.Projects["b"]["test"].P75Ms)
	assert.Equal(t, int64(300), merged.Projects["c"]["test"].P75Ms)
	assert.Len(t, merged.Projects, 3, "merged projects (a,b,c)")
}

// TestPercentile exercises every branch of the type-7 interpolation: empty
// input, the p<=0 and p>=1 clamps, the lo==hi exact-rank hit, and the
// interpolated middle. Inputs are deliberately unsorted to prove the internal
// sort, and results are exact so a formula regression is caught.
func TestPercentile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values []int64
		p      float64
		want   int64
	}{
		{"empty returns zero", nil, 0.75, 0},
		{"p<=0 returns min", []int64{30, 10, 20}, 0, 10},
		{"p<=0 negative clamps to min", []int64{30, 10, 20}, -0.5, 10},
		{"p>=1 returns max", []int64{30, 10, 20}, 1, 30},
		{"p>1 clamps to max", []int64{30, 10, 20}, 1.5, 30},
		{"single element", []int64{42}, 0.75, 42},
		// len=5, rank = 0.75*4 = 3.0 -> lo==hi==3 -> sorted[3] exactly.
		{"exact rank no interpolation", []int64{10, 20, 30, 40, 50}, 0.75, 40},
		// len=3, rank = 0.5*2 = 1.0 -> exact middle.
		{"median odd length", []int64{10, 20, 30}, 0.5, 20},
		// len=4, rank = 0.5*3 = 1.5 -> between sorted[1]=20 and sorted[2]=30,
		// frac 0.5 -> 20 + round(0.5*10) = 25.
		{"interpolated median even length", []int64{40, 10, 30, 20}, 0.5, 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, percentile(tt.values, tt.p))
		})
	}
}

// TestHitRate covers the zero-total guard (returns 0) alongside a normal ratio.
func TestHitRate(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0.0, hitRate(0, 0), "zero total")
	assert.Equal(t, 0.75, hitRate(3, 1))
	assert.Equal(t, 1.0, hitRate(5, 0))
}

// TestAdvanceHitWindow drives all four eviction branches once the window is
// full: hit evicting a miss, miss evicting a hit, and the two cap-in-place
// paths where the window is already saturated with the same outcome.
func TestAdvanceHitWindow(t *testing.T) {
	t.Parallel()

	t.Run("under cap just increments", func(t *testing.T) {
		hc, mc := 1, 1
		advanceHitWindow(&hc, &mc, true)
		assert.Equal(t, 2, hc)
		assert.Equal(t, 1, mc)
	})

	t.Run("hit evicts a miss at cap", func(t *testing.T) {
		// Full window with a miss present: a new hit should trade the miss out
		// so the total stays at HitWindow.
		hc, mc := HitWindow-1, 1
		advanceHitWindow(&hc, &mc, true)
		assert.Equal(t, HitWindow, hc)
		assert.Equal(t, 0, mc)
		assert.Equal(t, HitWindow, hc+mc, "total capped")
	})

	t.Run("miss evicts a hit at cap", func(t *testing.T) {
		hc, mc := 1, HitWindow-1
		advanceHitWindow(&hc, &mc, false)
		assert.Equal(t, 0, hc)
		assert.Equal(t, HitWindow, mc)
		assert.Equal(t, HitWindow, hc+mc, "total capped")
	})

	t.Run("all-hits window caps in place on hit", func(t *testing.T) {
		// No misses to evict: the increment then self-decrement leaves the
		// window unchanged at all hits.
		hc, mc := HitWindow, 0
		advanceHitWindow(&hc, &mc, true)
		assert.Equal(t, HitWindow, hc)
		assert.Equal(t, 0, mc)
	})

	t.Run("all-misses window caps in place on miss", func(t *testing.T) {
		hc, mc := 0, HitWindow
		advanceHitWindow(&hc, &mc, false)
		assert.Equal(t, 0, hc)
		assert.Equal(t, HitWindow, mc)
	})
}

// TestResolvePrediction walks the resolution ladder tier by tier so each early
// return is exercised: subdir bucket, generic bucket, project p75, workspace
// fallback, and the hardcoded default.
func TestResolvePrediction(t *testing.T) {
	t.Parallel()

	t.Run("tier1 subdir bucket wins and sorts", func(t *testing.T) {
		h := History{Projects: map[string]map[string]Stats{
			"svc": {"ci": {
				P75Ms: 90_000, Samples: 5,
				Buckets: map[string]BucketStats{
					// Two subdir buckets; sorted order picks direct.a first.
					"direct.a": {P75Ms: 11_000, Samples: 4, HitCount: 3, MissCount: 1, HitRate: 0.75},
					"direct.b": {P75Ms: 22_000, Samples: 4},
				},
			}},
		}}
		p75, hc, mc, hr := h.resolvePrediction("svc", "ci", []string{"direct.b", "direct.a"})
		assert.Equal(t, int64(11_000), p75, "sorted subdir tag direct.a chosen")
		assert.Equal(t, 3, hc)
		assert.Equal(t, 1, mc)
		assert.Equal(t, 0.75, hr)
	})

	t.Run("tier1 skips buckets under sample floor", func(t *testing.T) {
		// The matching subdir bucket has only 2 samples (< 3) so it is skipped
		// and resolution falls to the generic bucket.
		h := History{Projects: map[string]map[string]Stats{
			"svc": {"ci": {
				P75Ms: 90_000, Samples: 5,
				Buckets: map[string]BucketStats{
					"direct.a": {P75Ms: 11_000, Samples: 2},
					"direct":   {P75Ms: 33_000, Samples: 5, HitCount: 1, MissCount: 1, HitRate: 0.5},
				},
			}},
		}}
		p75, hc, mc, hr := h.resolvePrediction("svc", "ci", []string{"direct.a", "direct"})
		assert.Equal(t, int64(33_000), p75, "generic bucket after subdir floor miss")
		assert.Equal(t, 1, hc)
		assert.Equal(t, 1, mc)
		assert.Equal(t, 0.5, hr)
	})

	t.Run("tier2 generic transitive bucket", func(t *testing.T) {
		h := History{Projects: map[string]map[string]Stats{
			"svc": {"ci": {
				P75Ms: 90_000, Samples: 5,
				Buckets: map[string]BucketStats{
					"transitive": {P75Ms: 44_000, Samples: 6},
				},
			}},
		}}
		p75, _, _, _ := h.resolvePrediction("svc", "ci", []string{"transitive"})
		assert.Equal(t, int64(44_000), p75)
	})

	t.Run("tier3 project p75 when no bucket matches", func(t *testing.T) {
		h := History{Projects: map[string]map[string]Stats{
			"svc": {"ci": {
				P75Ms: 55_000, Samples: 5,
				HitCount: 2, MissCount: 2, HitRate: 0.5,
				Buckets: map[string]BucketStats{
					// Bucket exists but tag doesn't match direct/transitive.
					"unrelated": {P75Ms: 1, Samples: 9},
				},
			}},
		}}
		p75, hc, mc, hr := h.resolvePrediction("svc", "ci", []string{"direct.zzz"})
		assert.Equal(t, int64(55_000), p75)
		assert.Equal(t, 2, hc)
		assert.Equal(t, 2, mc)
		assert.Equal(t, 0.5, hr)
	})

	t.Run("workspace fallback for unknown project", func(t *testing.T) {
		h := History{WorkspaceFallbackMs: 77_000}
		p75, hc, mc, hr := h.resolvePrediction("nope", "ci", nil)
		assert.Equal(t, int64(77_000), p75)
		assert.Zero(t, hc)
		assert.Zero(t, mc)
		assert.Zero(t, hr)
	})

	t.Run("default when nothing known", func(t *testing.T) {
		h := History{}
		p75, _, _, _ := h.resolvePrediction("nope", "ci", nil)
		assert.Equal(t, DefaultDurationMs, p75)
	})
}

// TestUpdate_skipsInvalidSamples asserts the two guard continues: an empty
// project/target pair and a non-hit sample with non-positive duration are both
// dropped, leaving the store empty.
func TestUpdate_skipsInvalidSamples(t *testing.T) {
	t.Parallel()

	h := History{}
	h.Update(fixedNow, []Sample{
		{Project: "", Target: "ci", DurationMs: 1_000},  // no project
		{Project: "svc", Target: "", DurationMs: 1_000}, // no target
		{Project: "svc", Target: "ci", DurationMs: 0},   // miss with no duration
		{Project: "svc", Target: "ci", DurationMs: -5},  // negative duration
	}, nil)

	assert.Empty(t, h.Projects["svc"], "all samples dropped by guards")
	assert.Equal(t, HistoryVersion, h.Version, "version stamped even with no folds")
	assert.Equal(t, fixedNow, h.UpdatedAt)
}

// TestUpdate_missWithTagsBuildsBuckets folds miss samples carrying subdir tags
// and asserts the full resulting Stats/BucketStats structs, so both the
// project-level and per-bucket miss paths (percentile, counters) are covered.
func TestUpdate_missWithTagsBuildsBuckets(t *testing.T) {
	t.Parallel()

	h := History{}
	// Three miss samples, same tag, so the bucket reaches Samples=3 with a
	// deterministic p75. Durations 10k,20k,30k -> project & bucket p75 = 25k.
	for _, d := range []int64{10_000, 20_000, 30_000} {
		h.Update(fixedNow, []Sample{
			{Project: "svc", Target: "ci", DurationMs: d, Tags: []string{"direct.src"}},
		}, nil)
	}

	want := Stats{
		P75Ms:       25_000,
		Samples:     3,
		LastUpdated: fixedNow,
		Recent:      []int64{10_000, 20_000, 30_000},
		MissCount:   3,
		HitRate:     0, // all misses -> 0/3
		Buckets: map[string]BucketStats{
			"direct.src": {
				P75Ms:     25_000,
				Samples:   3,
				Recent:    []int64{10_000, 20_000, 30_000},
				MissCount: 3,
				HitRate:   0,
			},
		},
	}
	assert.Equal(t, want, h.Projects["svc"]["ci"])
	assert.Equal(t, int64(25_000), h.WorkspaceFallbackMs, "workspace p75 from the single project's recent")
}

// TestUpdate_hitWithTagsUpdatesBucketHitRate folds a tagged cache-hit and
// asserts it moves only the hit counters (not the duration percentile) on both
// the project and bucket, covering the s.Hit tag branch.
func TestUpdate_hitWithTagsUpdatesBucketHitRate(t *testing.T) {
	t.Parallel()

	h := History{}
	h.Update(fixedNow, []Sample{
		{Project: "svc", Target: "ci", Hit: true, Tags: []string{"direct.src"}},
	}, nil)

	want := Stats{
		HitCount: 1,
		HitRate:  1,
		Buckets: map[string]BucketStats{
			"direct.src": {HitCount: 1, HitRate: 1},
		},
	}
	assert.Equal(t, want, h.Projects["svc"]["ci"], "hit updates only hit counters, no duration samples")
}

// TestUpdate_fitsSetupAndAlpha feeds shard samples so the setup and alpha
// windows fill and the fitted Constants are recomputed via percentile. Inputs
// are chosen so the arithmetic is exact.
func TestUpdate_fitsSetupAndAlpha(t *testing.T) {
	t.Parallel()

	h := History{}
	// SetupP50Ms starts unset, so residual uses DefaultSetupMs=30_000.
	// TotalMs=100_000, WorkMs=40_000, NShards=4:
	//   residual = 100_000 - 30_000 - (40_000/4) = 60_000
	//   alpha = 60_000 / 4 = 15_000
	// SetupMs=30_000 lands in the setup window.
	h.Update(fixedNow, nil, []ShardSample{
		{SetupMs: 30_000, TotalMs: 100_000, WorkMs: 40_000, NShards: 4},
	})

	assert.Equal(t, []int64{30_000}, h.Setup)
	assert.Equal(t, []int64{15_000}, h.Alpha)
	// Single-element windows -> percentile returns the lone value.
	assert.Equal(t, Millis(30_000), h.Constants.SetupP50Ms)
	assert.Equal(t, Millis(15_000), h.Constants.AlphaMs)
}

// TestUpdate_shardSampleNonPositiveResidual verifies that when the computed
// residual is not positive, no alpha observation is recorded (the residual<=0
// guard). Setup still records because SetupMs>0.
func TestUpdate_shardSampleNonPositiveResidual(t *testing.T) {
	t.Parallel()

	h := History{}
	// TotalMs barely above setup so residual = 30_100 - 30_000 - (200/2) = 0.
	h.Update(fixedNow, nil, []ShardSample{
		{SetupMs: 5_000, TotalMs: 30_100, WorkMs: 200, NShards: 2},
	})

	assert.Equal(t, []int64{5_000}, h.Setup)
	assert.Empty(t, h.Alpha, "non-positive residual records no alpha")
}

// TestMerge_nilOtherIsNoOp asserts the nil guard leaves the receiver untouched.
func TestMerge_nilOtherIsNoOp(t *testing.T) {
	t.Parallel()

	h := History{Version: 4, Projects: map[string]map[string]Stats{
		"a": {"ci": {P75Ms: 10}},
	}}
	h.Merge(nil)
	assert.Equal(t, int64(10), h.Projects["a"]["ci"].P75Ms)
	assert.Equal(t, 4, h.Version)
}

// TestMerge_adoptsVersionAndOlderUpdateSkipped covers two branches: a
// zero-version receiver adopts other's version, and an other with an OLDER
// UpdatedAt does not overwrite workspace-level fields.
func TestMerge_adoptsVersionAndOlderUpdateSkipped(t *testing.T) {
	t.Parallel()

	newer := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	older := newer.Add(-time.Hour)

	h := History{
		Version:             0, // unset -> adopts other's version
		UpdatedAt:           newer,
		Constants:           Constants{SetupP50Ms: 30_000, AlphaMs: 5_000},
		WorkspaceFallbackMs: 9_000,
		Setup:               []int64{1, 2, 3},
		Alpha:               []int64{1, 2, 3},
	}
	other := History{
		Version:             3,
		UpdatedAt:           older, // older -> workspace fields not adopted
		Constants:           Constants{SetupP50Ms: 1, AlphaMs: 1},
		WorkspaceFallbackMs: 1,
		Setup:               []int64{9}, // shorter, and older, so ignored
		Alpha:               []int64{9},
	}
	h.Merge(&other)

	assert.Equal(t, 3, h.Version, "zero version adopts other's")
	assert.Equal(t, Constants{SetupP50Ms: 30_000, AlphaMs: 5_000}, h.Constants, "older other does not overwrite constants")
	assert.Equal(t, int64(9_000), h.WorkspaceFallbackMs, "older other does not overwrite fallback")
	assert.Equal(t, []int64{1, 2, 3}, h.Setup, "setup window unchanged")
	assert.Equal(t, []int64{1, 2, 3}, h.Alpha, "alpha window unchanged")
}

// TestMerge_newerAdoptsWindows covers the UpdatedAt.After branch including the
// len-guarded Setup/Alpha adoption.
func TestMerge_newerAdoptsWindows(t *testing.T) {
	t.Parallel()

	older := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)

	h := History{
		Version:   4,
		UpdatedAt: older,
		Setup:     []int64{1},
		Alpha:     []int64{1},
	}
	other := History{
		Version:             4,
		UpdatedAt:           newer,
		Constants:           Constants{SetupP50Ms: 42, AlphaMs: 7},
		WorkspaceFallbackMs: 88,
		Setup:               []int64{10, 20}, // longer -> adopted
		Alpha:               []int64{10, 20},
	}
	h.Merge(&other)

	assert.Equal(t, Constants{SetupP50Ms: 42, AlphaMs: 7}, h.Constants)
	assert.Equal(t, int64(88), h.WorkspaceFallbackMs)
	assert.Equal(t, []int64{10, 20}, h.Setup, "longer newer setup adopted")
	assert.Equal(t, []int64{10, 20}, h.Alpha, "longer newer alpha adopted")
}

// TestLoad_missingFileIsZeroHistory asserts a nonexistent path is not an error
// and yields a version-stamped empty History.
func TestLoad_missingFileIsZeroHistory(t *testing.T) {
	t.Parallel()

	var h History
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	require.NoError(t, h.Load(context.Background(), path))
	assert.Equal(t, HistoryVersion, h.Version)
	assert.Nil(t, h.Projects, "missing file yields zero History with nil map")
}

// TestSaveThenLoad round-trips a populated history through the atomic writer
// and decoder, covering the happy paths of both Save and Load plus map init.
func TestSaveThenLoad(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.json")
	orig := History{
		Version:             HistoryVersion,
		UpdatedAt:           fixedNow,
		Constants:           Constants{SetupP50Ms: 30_000, AlphaMs: 5_000},
		WorkspaceFallbackMs: 9_000,
		Projects: map[string]map[string]Stats{
			"svc": {"ci": {P75Ms: 12_000, Samples: 5, LastUpdated: fixedNow, Recent: []int64{12_000}}},
		},
	}
	require.NoError(t, orig.Save(context.Background(), path))

	var got History
	require.NoError(t, got.Load(context.Background(), path))
	assert.Equal(t, orig.Version, got.Version)
	assert.Equal(t, orig.Constants, got.Constants)
	assert.Equal(t, orig.Projects, got.Projects)
	assert.True(t, orig.UpdatedAt.Equal(got.UpdatedAt), "updated_at round-trips")
}

// TestLoad_versionTooNew rejects a history whose schema version exceeds the
// build's HistoryVersion.
func TestLoad_versionTooNew(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "future.json")
	// Hand-craft a future-version file via the codec used by Save.
	future := History{Version: HistoryVersion + 1}
	require.NoError(t, future.Save(context.Background(), path))

	var h History
	err := h.Load(context.Background(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported history version")
}

// TestLoad_decodeError surfaces a malformed-file decode failure.
func TestLoad_decodeError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("{not valid json"), 0o644))

	var h History
	err := h.Load(context.Background(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode history")
}

// TestLoad_contextCancelled asserts the pre-read ctx guard returns the ctx
// error before touching the filesystem.
func TestLoad_contextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var h History
	err := h.Load(ctx, filepath.Join(t.TempDir(), "any.json"))
	require.ErrorIs(t, err, context.Canceled)
}

// TestSave_contextCancelled asserts the pre-encode ctx guard short-circuits.
func TestSave_contextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := History{Version: HistoryVersion}
	err := h.Save(ctx, filepath.Join(t.TempDir(), "any.json"))
	require.ErrorIs(t, err, context.Canceled)
}
