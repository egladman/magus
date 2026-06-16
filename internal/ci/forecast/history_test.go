package forecast

import (
	"encoding/json"
	"testing"
	"time"
)

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
	if err != nil {
		t.Fatalf("marshal History: %v", err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

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
		if !allowed[k] {
			t.Errorf("unexpected History JSON key %q — add to allowed map with a safety justification, or remove the field", k)
		}
	}

	// Also lock the Stats sub-schema.
	var projects map[string]map[string]map[string]json.RawMessage
	if err := json.Unmarshal(top["projects"], &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
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
				if !allowedStats[k] {
					t.Errorf("unexpected Stats JSON key %q (project=%q target=%q) — add to allowedStats with a safety justification", k, path, target)
				}
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
			if err := json.Unmarshal(rawOutcomes, &outcomes); err != nil {
				t.Fatalf("unmarshal recent_outcomes (project=%q target=%q): %v", path, target, err)
			}
			for _, o := range outcomes {
				for k := range o {
					if !allowedOutcome[k] {
						t.Errorf("unexpected Outcome JSON key %q (project=%q target=%q) — add to allowedOutcome with a safety justification", k, path, target)
					}
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
	if err := json.Unmarshal([]byte(v1JSON), &h); err != nil {
		t.Fatalf("unmarshal v1 history: %v", err)
	}

	// Should fall through to project-wide p75 (no buckets, no hit data → cold start).
	got := h.PredictDuration("services/api", "ci", []string{"direct", "direct.src"})
	if got != 12*time.Second {
		t.Errorf("PredictDuration = %v, want 12s (project p75)", got)
	}

	// Transitive tag also falls through cleanly.
	got = h.PredictDuration("services/api", "ci", []string{"transitive"})
	if got != 12*time.Second {
		t.Errorf("PredictDuration (transitive, no buckets) = %v, want 12s", got)
	}
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
	if err := json.Unmarshal([]byte(v2JSON), &h); err != nil {
		t.Fatalf("unmarshal v2 history: %v", err)
	}

	// hit_count and miss_count default to zero → cold start → raw p75 returned.
	got := h.PredictDuration("apps/web", "ci", nil)
	if got != 20*time.Second {
		t.Errorf("v2→v3: PredictDuration = %v, want 20s (raw p75, cold start)", got)
	}
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
	if st.HitCount+st.MissCount < 5 {
		t.Fatalf("need ≥5 combined observations for discount, got %d", st.HitCount+st.MissCount)
	}
	if st.HitRate <= 0 {
		t.Fatalf("expected positive hit rate, got %v", st.HitRate)
	}

	got := h.PredictDuration("svc", "ci", nil)
	// Expected: p75 * (1 - hit_rate). p75 = 60s, hit_rate ≈ 0.9 → ~6s.
	// Allow ±20% tolerance for integer arithmetic in the rolling window.
	if got >= 60*time.Second {
		t.Errorf("hit-rate discount not applied: PredictDuration = %v, want < 60s", got)
	}
	if got < time.Millisecond {
		t.Errorf("stability floor violated: PredictDuration = %v, want ≥ 1ms", got)
	}
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
	if total >= 5 {
		t.Fatalf("test setup: need total < 5, got %d", total)
	}

	got := h.PredictDuration("svc", "ci", nil)
	if got != 60*time.Second {
		t.Errorf("cold start: PredictDuration = %v, want 60s (raw p75)", got)
	}
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
	if err := json.Unmarshal([]byte(v3JSON), &h); err != nil {
		t.Fatalf("unmarshal v3 history: %v", err)
	}

	st := h.Projects["services/api"]["ci"]
	if st.PassCount != 0 || st.FailCount != 0 || st.FlakeCount != 0 {
		t.Errorf("v3→v4: expected zero flake counts, got pass=%d fail=%d flake=%d",
			st.PassCount, st.FailCount, st.FlakeCount)
	}
	if len(st.RecentOutcomes) != 0 {
		t.Errorf("v3→v4: expected empty RecentOutcomes, got %d entries", len(st.RecentOutcomes))
	}
	// Duration prediction should still work correctly.
	got := h.PredictDuration("services/api", "ci", nil)
	if got <= 0 {
		t.Errorf("v3→v4: PredictDuration = %v, want > 0", got)
	}
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
		if st.HitRate != wantRate {
			t.Errorf("after observation: HitRate = %v, want %v (HitCount=%d MissCount=%d)",
				st.HitRate, wantRate, st.HitCount, st.MissCount)
		}
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

	cases := map[string]int64{"a": 150, "b": 200, "c": 300} // a: fresher shard1 wins
	for project, want := range cases {
		got := merged.Projects[project]["test"].P75Ms
		if got != want {
			t.Errorf("project %q: P75Ms = %d, want %d", project, got, want)
		}
	}
	if len(merged.Projects) != 3 {
		t.Errorf("merged projects = %d, want 3 (a,b,c)", len(merged.Projects))
	}
}
