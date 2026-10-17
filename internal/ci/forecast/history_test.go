package forecast

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedNow is a deterministic ingest timestamp so no test depends on wall-clock.
var fixedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// TestHistorySchemaLockIsStructural walks the Go types by reflection instead of
// marshalling a fixture, because the fixture-based lock below can only see fields
// the fixture happens to set. A field added with `omitempty` and left unpopulated
// there emits no JSON key at all, so the allowlist assertion never runs against it
// and the whole lock passes while the new field ships unreviewed, which is exactly
// how `runs` first slipped in.
//
// This one is not fixture-dependent: every json tag on the structs is checked
// whether or not any test constructs a value carrying it.
func TestHistorySchemaLockIsStructural(t *testing.T) {
	// Same justification requirement as the fixture lock: an entry here means
	// someone decided the field is safe to put in a shared CI cache.
	allowed := map[reflect.Type]map[string]bool{
		reflect.TypeOf(History{}): {
			"version": true, "updated_at": true, "constants": true, "projects": true,
			"setup": true, "alpha": true, "workspace_fallback_ms": true, "runs": true,
			"jobs": true,
		},
		// The store's own framing: which entry a line holds, and the key of a
		// (project, target), the same path and name Projects is keyed by.
		reflect.TypeOf(historyLine{}): {"workspace": true, "stats": true, "run": true, "job": true, "removed": true},
		reflect.TypeOf(workspaceEntry{}): {
			"version": true, "updated_at": true, "constants": true, "setup": true,
			"alpha": true, "workspace_fallback_ms": true,
		},
		reflect.TypeOf(statsEntry{}): {"project": true, "target": true, "Stats": true},
		reflect.TypeOf(types.JobOutcome{}): {
			// Ids, enum cases, the sentence that chose a tier and the file:line of the
			// declaration that chose it: names the workspace declared and numbers about a
			// job, never its prose, file contents or output.
			"job": true, "model_tier": true, "kind": true, "skills": true, "model": true,
			"source": true, "reason": true, "site": true, "write_files": true, "state": true,
			// What a host reported the job used, absent when none did.
			"checks_run": true, "usage_tokens": true, "usage_cost_usd": true,
			"duration_ms": true, "ended": true, "rework_rounds": true, "widenings": true,
			// The attempt's number and the RetryChange case naming what must change first.
			"attempt": true, "retry": true,
			// The parent's job id and attempt number, a count, and workspace-relative
			// project paths, the same kind of path Projects keys on.
			"parent": true, "parent_attempt": true, "unattributed": true, "projects": true,
			"work": true,
		},
		reflect.TypeOf(types.JobWork{}): {
			// Counts of the holder's calls and the context sizes its host reported: numbers
			// about the calls, never a command, path or reply.
			"calls": true, "writes": true, "setup_calls": true, "setup_context": true,
			"setup_ms": true, "setup_tokens": true, "context_tokens": true, "peak_tokens": true,
			"denials": true,
		},
		reflect.TypeOf(Constants{}): {"setup_p50_ms": true, "alpha_ms": true},
		reflect.TypeOf(Run{}): {
			// Commit ids and branch names live here and ONLY here; the cache-safety
			// notice in history.go carries the reasoning for the exception.
			"commit": true, "ref": true, "target": true, "status": true, "at": true,
		},
		reflect.TypeOf(BucketStats{}): {
			"p75_ms": true, "samples": true, "recent": true,
			"hit_count": true, "miss_count": true, "hit_rate": true,
		},
		reflect.TypeOf(Stats{}): {
			"p75_ms": true, "samples": true, "last_updated": true, "recent": true,
			"buckets": true, "hit_count": true, "miss_count": true, "hit_rate": true,
			"pass_count": true, "fail_count": true, "volatile_count": true,
			"recent_outcomes": true,
		},
		reflect.TypeOf(Outcome{}): {
			"result": true, "affected": true, "duration_ms": true, "at": true,
			"attempts": true, "max_rss_bytes": true, "cpu_ms": true, "slots": true, "charms": true, "args_hash": true,
		},
	}
	for typ, keys := range allowed {
		for i := range typ.NumField() {
			f := typ.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" {
				name = f.Name
			}
			assert.Truef(t, keys[name], "unexpected %s field %q (json %q) - add it to this allowlist AND the fixture lock with a safety justification, or remove it", typ.Name(), f.Name, name)
		}
	}
}

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
					P75Ms:         12_000,
					Samples:       10,
					LastUpdated:   time.Now(),
					Recent:        []int64{11_000, 12_000, 13_000},
					HitCount:      8,
					MissCount:     2,
					HitRate:       0.8,
					PassCount:     7,
					FailCount:     1,
					VolatileCount: 2,
					RecentOutcomes: []Outcome{
						{Result: "pass", AffectedByDiff: true, DurationMs: 11_000, At: time.Now(), Attempts: 1},
						{Result: "volatile", AffectedByDiff: false, DurationMs: 22_000, At: time.Now(), Attempts: 2,
							MaxRSSBytes: 1 << 30, CPUMs: 41_000, Slots: 4, Charms: []string{"rw"}, ArgsHash: ArgsHash([]string{"-run", "X"})},
					},
				},
			},
		},
		Setup:               []int64{29_000, 31_000},
		Alpha:               []int64{4_800, 5_200},
		WorkspaceFallbackMs: 9_000,
		Runs:                []Run{{Commit: "39e02449", Ref: "main", Target: "ci", Status: RunPassed, At: time.Now()}},
		Jobs:                []types.JobOutcome{{Job: "docs", ModelTier: "economy", Kind: types.JobKindCheck, State: types.StatePass, Ended: 1}},
	}

	path := filepath.Join(t.TempDir(), "history.jsonl")
	seedHistory(t, path, h)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	// Approved line keys: which entry a line holds, and whether it ends one.
	allowed := map[string]bool{
		"workspace": true, // the workspace entry, locked below
		"stats":     true, // one (project, target)'s Stats, locked below
		"run":       true, // a Run: commit ids and ref names, the ONE exception; see the cache-safety notice in history.go
		"job":       true, // a types.JobOutcome: job ids and tier case names the workspace chose, enums and counters; locked field by field above
		"removed":   true, // bool: the line ends the entry its key names
	}
	// Approved workspace keys. Extend only with fields that contain
	// integer timings, project paths, or subdir tag names — never
	// source code, content hashes, secrets, author identity, or error messages.
	allowedWorkspace := map[string]bool{
		"version":               true, // schema version int
		"updated_at":            true, // ISO timestamp of last ingest
		"constants":             true, // {setup_p50_ms, alpha_ms} — integer ms
		"setup":                 true, // []int64 of per-shard setup observations
		"alpha":                 true, // []int64 of fitted α observations
		"workspace_fallback_ms": true, // int64 workspace-wide p75
	}
	type entry = map[string]json.RawMessage
	var workspaces []entry
	projects := map[string]map[string]entry{}
	for line := range strings.Lines(string(raw)) {
		var top entry
		require.NoError(t, json.Unmarshal([]byte(line), &top), "unmarshal a line to a map")
		for k := range top {
			assert.Truef(t, allowed[k], "unexpected history line key %q: add to allowed map with a safety justification, or remove the field", k)
		}
		if w, ok := top["workspace"]; ok {
			var keys entry
			require.NoError(t, json.Unmarshal(w, &keys))
			workspaces = append(workspaces, keys)
		}
		if s, ok := top["stats"]; ok {
			var keys entry
			require.NoError(t, json.Unmarshal(s, &keys))
			var at struct {
				Project string `json:"project"`
				Target  string `json:"target"`
			}
			require.NoError(t, json.Unmarshal(s, &at))
			delete(keys, "project")
			delete(keys, "target")
			if projects[at.Project] == nil {
				projects[at.Project] = map[string]entry{}
			}
			projects[at.Project][at.Target] = keys
		}
	}
	require.Len(t, workspaces, 1)
	for k := range workspaces[0] {
		assert.Truef(t, allowedWorkspace[k], "unexpected workspace JSON key %q: add to allowedWorkspace with a safety justification, or remove the field", k)
	}
	require.NotEmpty(t, projects, "the fixture's stats reached the file")

	// Also lock the Stats sub-schema; project and target are the line's key, a
	// workspace-relative path and a target name.
	allowedStats := map[string]bool{
		"p75_ms":          true, // int64 — rolling p75 of recent durations
		"samples":         true, // int   — total number of ingested miss samples
		"last_updated":    true, // ISO timestamp
		"recent":          true, // []int64 — raw recent duration samples
		"buckets":         true, // map[string]BucketStats — per-workload-tag sub-stores (tag names are top-level subdir names, safe to share)
		"hit_count":       true, // int — rolling count of cache hits; no source content, no keys, no payloads
		"miss_count":      true, // int — rolling count of cache misses; same safety profile as hit_count
		"hit_rate":        true, // float64 — hit_count/(hit_count+miss_count); derived aggregate, nothing new to leak
		"pass_count":      true, // int — rolling count of passing runs; safe integer counter, no source content
		"fail_count":      true, // int — rolling count of failing runs; safe integer counter, no source content
		"volatile_count":  true, // int — rolling count of volatile runs (fail→pass); safe integer counter
		"recent_outcomes": true, // []Outcome — per-run result enum + timing + bool; see Outcome allowlist below
	}
	for path, targets := range projects {
		for target, stats := range targets {
			for k := range stats {
				assert.Truef(t, allowedStats[k], "unexpected Stats JSON key %q (project=%q target=%q): add to allowedStats with a safety justification", k, path, target)
			}
		}
	}

	// Lock the Outcome sub-schema (entries in recent_outcomes).
	allowedOutcome := map[string]bool{
		"result":      true, // string enum: "pass"|"fail"|"volatile" — no source content
		"affected":    true, // bool — whether project was in the VCS diff's affected set; no source content
		"duration_ms": true, // int64 — wall-clock timing; same safety profile as p75_ms
		"at":          true, // ISO timestamp of when the run completed; same safety profile as last_updated
		"attempts":    true, // int — number of attempts (1 or 2); safe integer counter
		// int64: peak resident bytes over the target's processes. A magnitude
		// measured by the kernel, carrying no path, no argv and no payload: the
		// same safety profile as duration_ms, which is likewise a number about a
		// run rather than anything from inside it. Omitted when unknown, so it
		// adds no key at all to a history written where the platform cannot
		// report it.
		"max_rss_bytes": true,
		// int64: CPU milliseconds the kernel counted for the target's processes.
		// A magnitude about the run, the same safety profile as duration_ms.
		"cpu_ms": true,
		// int: the slots the run's claim held. A count magus sized, the same safety
		// profile as attempts.
		"slots": true,
		// []string: the run's active charms. Names a workspace declares, the same
		// safety profile as the target names this history is already keyed by.
		"charms": true,
		// string: 32 bits of a hash of the forwarded args, the one hash this history
		// carries. Truncated so an argument carrying a secret cannot be recovered
		// from it; see ArgsHash.
		"args_hash": true,
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
					assert.Truef(t, allowedOutcome[k], "unexpected Outcome JSON key %q (project=%q target=%q): add to allowedOutcome with a safety justification", k, path, target)
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
	// Expected: `p75 * (1 - hit_rate)`. p75 = 60s, hit_rate ≈ 0.9 → ~6s.
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

// TestHistoryV3LoadsIntoV4 verifies that a v3 history file (no volatility fields)
// loads cleanly into the v4 schema and volatility counts default to zero.
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
	assert.Zero(t, st.VolatileCount, "v3→v4: expected zero volatile count")
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

// TestHitWindowObserve drives all four eviction branches once the window is
// full: hit evicting a miss, miss evicting a hit, and the two cap-in-place
// paths where the window is already saturated with the same outcome.
func TestHitWindowObserve(t *testing.T) {
	t.Parallel()

	t.Run("under cap just increments", func(t *testing.T) {
		hc, mc := 1, 1
		(&hitWindow{&hc, &mc}).Observe(true)
		assert.Equal(t, 2, hc)
		assert.Equal(t, 1, mc)
	})

	t.Run("hit evicts a miss at cap", func(t *testing.T) {
		// Full window with a miss present: a new hit should trade the miss out
		// so the total stays at HitWindow.
		hc, mc := HitWindow-1, 1
		(&hitWindow{&hc, &mc}).Observe(true)
		assert.Equal(t, HitWindow, hc)
		assert.Equal(t, 0, mc)
		assert.Equal(t, HitWindow, hc+mc, "total capped")
	})

	t.Run("miss evicts a hit at cap", func(t *testing.T) {
		hc, mc := 1, HitWindow-1
		(&hitWindow{&hc, &mc}).Observe(false)
		assert.Equal(t, 0, hc)
		assert.Equal(t, HitWindow, mc)
		assert.Equal(t, HitWindow, hc+mc, "total capped")
	})

	t.Run("all-hits window caps in place on hit", func(t *testing.T) {
		// No misses to evict: the increment then self-decrement leaves the
		// window unchanged at all hits.
		hc, mc := HitWindow, 0
		(&hitWindow{&hc, &mc}).Observe(true)
		assert.Equal(t, HitWindow, hc)
		assert.Equal(t, 0, mc)
	})

	t.Run("all-misses window caps in place on miss", func(t *testing.T) {
		hc, mc := 0, HitWindow
		(&hitWindow{&hc, &mc}).Observe(false)
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

	// Every sample is dropped by a guard, yet the version and time are stamped.
	want := History{Version: HistoryVersion, UpdatedAt: fixedNow, Projects: map[string]map[string]Stats{}}
	assert.Equal(t, want, h)
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

	want := History{
		Version:   HistoryVersion,
		UpdatedAt: fixedNow,
		// Single-element windows -> percentile returns the lone value.
		Constants: Constants{SetupP50Ms: 30_000, AlphaMs: 15_000},
		Projects:  map[string]map[string]Stats{},
		Setup:     []int64{30_000},
		Alpha:     []int64{15_000},
	}
	assert.Equal(t, want, h)
}

// TestUpdate_shardSampleNonPositiveResidual verifies that when the computed
// residual is not positive, no alpha observation is recorded (the residual<=0
// guard). Setup still records because SetupMs>0.
func TestUpdate_shardSampleNonPositiveResidual(t *testing.T) {
	t.Parallel()

	h := History{}
	// TotalMs barely above setup so `residual = 30_100 - 30_000 - (200/2) = 0`.
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

	// The zero version adopts other's, while an older other leaves the constants, fallback
	// and Setup/Alpha windows alone.
	want := History{
		Version:             3,
		UpdatedAt:           newer,
		Constants:           Constants{SetupP50Ms: 30_000, AlphaMs: 5_000},
		Projects:            map[string]map[string]Stats{},
		Setup:               []int64{1, 2, 3},
		Alpha:               []int64{1, 2, 3},
		WorkspaceFallbackMs: 9_000,
	}
	assert.Equal(t, want, h)
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

	// The newer other's constants, fallback and longer Setup/Alpha windows are adopted.
	want := History{
		Version:             4,
		UpdatedAt:           newer,
		Constants:           Constants{SetupP50Ms: 42, AlphaMs: 7},
		Projects:            map[string]map[string]Stats{},
		Setup:               []int64{10, 20},
		Alpha:               []int64{10, 20},
		WorkspaceFallbackMs: 88,
	}
	assert.Equal(t, want, h)
}

// TestLoad_missingFileIsZeroHistory asserts a nonexistent path is not an error
// and yields a version-stamped empty History.
func TestLoad_missingFileIsZeroHistory(t *testing.T) {
	t.Parallel()

	var h History
	path := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	require.NoError(t, h.Load(context.Background(), path))
	assert.Equal(t, History{Version: HistoryVersion, Projects: map[string]map[string]Stats{}}, h)
}

// TestUpdateHistoryThenLoad round-trips a populated history through the writer
// and decoder, covering the happy paths of both UpdateHistory and Load plus map init.
func TestUpdateHistoryThenLoad(t *testing.T) {
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
	seedHistory(t, path, orig)

	var got History
	require.NoError(t, got.Load(context.Background(), path))
	assert.True(t, orig.UpdatedAt.Equal(got.UpdatedAt), "updated_at round-trips")
	// The instant round-tripped; its location need not, so compare the rest whole.
	got.UpdatedAt = orig.UpdatedAt
	assert.Equal(t, orig, got)
}

// An unmeasured outcome writes no cpu_ms or max_rss_bytes at all, so a reader
// cannot take a replay or a host that cannot measure for a run that spent nothing.
func TestOutcomeCostRoundTripsAndIsOmittedWhenUnmeasured(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.jsonl")
	orig := History{Version: HistoryVersion, Projects: map[string]map[string]Stats{
		"svc": {"go-test": {RecentOutcomes: []Outcome{
			{Result: OutcomePass, DurationMs: 1_000, CPUMs: 3_500},
			{Result: OutcomePass, DurationMs: 20},
		}}},
	}}
	seedHistory(t, path, orig)

	lines := readLines(t, path)
	require.Len(t, lines, 2, "the workspace line, then the one (project, target)")
	var keys struct {
		Stats struct {
			RecentOutcomes []map[string]json.RawMessage `json:"recent_outcomes"`
		} `json:"stats"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &keys))
	written := keys.Stats.RecentOutcomes
	require.Len(t, written, 2)
	assert.Contains(t, written[0], "cpu_ms")
	assert.NotContains(t, written[1], "cpu_ms", "an unmeasured outcome carries no key")
	assert.NotContains(t, written[1], "max_rss_bytes", "an unmeasured outcome carries no key")

	var got History
	require.NoError(t, got.Load(context.Background(), path))
	outcomes := got.Projects["svc"]["go-test"].RecentOutcomes
	require.Len(t, outcomes, 2)
	assert.Equal(t, int64(3_500), outcomes[0].CPUMs)
	assert.Zero(t, outcomes[1].CPUMs)
}

// TestLoad_versionTooNew rejects a history whose schema version exceeds the
// build's HistoryVersion.
func TestLoad_versionTooNew(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "future.jsonl")
	// Hand-crafted, since UpdateHistory stamps the build's own version.
	future, err := json.Marshal(historyLine{Workspace: &workspaceEntry{Version: HistoryVersion + 1}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(future, '\n'), 0o644))

	var h History
	err = h.Load(context.Background(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported history version")
}

// TestLoad_decodeError names the file and line of a complete line that does not decode,
// including one holding two entries, which no magus writes.
func TestLoad_decodeError(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"not json":    `{"workspace":{"version":8}}` + "\n{not valid json\n",
		"two entries": `{"workspace":{"version":8}}` + "\n" + `{"run":{"commit":"a"},"job":{"job":"j"}}` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "history.jsonl")
			require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

			var h History
			err := h.Load(context.Background(), path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), path+":2:")
		})
	}
}

// readLines is the store's lines, newline-terminated each.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(string(raw), "\n"), "every line ends in a newline")
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

func recordDuration(project, target string, ms int64) func(*History) error {
	return func(h *History) error {
		h.Update(fixedNow, []Sample{{Project: project, Target: target, DurationMs: ms}}, nil)
		return nil
	}
}

func seedHistory(tb testing.TB, path string, h History) {
	tb.Helper()
	require.NoError(tb, UpdateHistory(tb.Context(), path, func(dst *History) error {
		*dst = h
		return nil
	}))
}

// TestUpdateHistoryAppendsWhatChangedThenReloads pins the store as a log: an update
// appends a line for each entry it changed and leaves every other line alone, and a
// reload folds the lines back into the History that was written.
func TestUpdateHistoryAppendsWhatChangedThenReloads(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.jsonl")
	require.NoError(t, UpdateHistory(t.Context(), path, recordDuration("svc", "go/test", 1_000)))
	first := readLines(t, path)
	require.Len(t, first, 2, "the workspace line and the one (project, target)")

	require.NoError(t, UpdateHistory(t.Context(), path, recordDuration("web", "go/test", 2_000)))
	second := readLines(t, path)
	require.Len(t, second, 4, "the workspace fallback moved, and web is new; svc is unchanged")
	assert.Equal(t, first, second[:2], "nothing written is rewritten")
	assert.Contains(t, second[3], `"project":"web"`)

	require.NoError(t, UpdateHistory(t.Context(), path, func(h *History) error {
		h.RecordRun(Run{Commit: "aaa", Ref: "main", Target: "ci", Status: RunPassed}, fixedNow)
		return nil
	}))
	assert.Len(t, readLines(t, path), 5, "a run touches only its own line")

	var got History
	require.NoError(t, got.Load(t.Context(), path))
	assert.Equal(t, []int64{1_000}, got.Projects["svc"]["go/test"].Recent)
	assert.Equal(t, []int64{2_000}, got.Projects["web"]["go/test"].Recent)
	commit, ok := got.PassedCommit("main", "ci")
	assert.True(t, ok)
	assert.Equal(t, "aaa", commit)
}

// TestHistoryLastLinePerKeyWins pins the fold: a (project, target) written twice reads as
// its later line, and a removal ends the entry its key names.
func TestHistoryLastLinePerKeyWins(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.jsonl")
	lines := strings.Join([]string{
		`{"workspace":{"version":8,"workspace_fallback_ms":1}}`,
		`{"stats":{"project":"svc","target":"ci","p75_ms":100}}`,
		`{"run":{"commit":"aaa","ref":"main","target":"ci","status":"passed","at":"2026-01-02T03:04:05Z"}}`,
		`{"stats":{"project":"svc","target":"ci","p75_ms":200}}`,
		`{"workspace":{"version":8,"workspace_fallback_ms":2}}`,
		`{"removed":true,"run":{"commit":"aaa","ref":"main","target":"ci","status":"passed","at":"2026-01-02T03:04:05Z"}}`,
	}, "\n") + "\n"
	require.NoError(t, os.WriteFile(path, []byte(lines), 0o644))

	var got History
	require.NoError(t, got.Load(t.Context(), path))
	assert.Equal(t, int64(200), got.Projects["svc"]["ci"].P75Ms)
	assert.Equal(t, int64(2), got.WorkspaceFallbackMs)
	assert.Empty(t, got.Runs, "the removal ended the run")
}

// TestUpdateHistoryCompactsToTheLiveEntries pins the window and the compaction together:
// a run past RunWindow is ended by a removal line, and once the lines outgrow the entries
// the file is rewritten to the live ones alone.
func TestUpdateHistoryCompactsToTheLiveEntries(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.jsonl")
	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	const runs = 3 * RunWindow
	for i := range runs {
		require.NoError(t, UpdateHistory(t.Context(), path, func(h *History) error {
			h.RecordRun(Run{Commit: fmt.Sprint(i), Ref: "main", Target: "ci", Status: RunPassed}, t0.Add(time.Duration(i)*time.Minute))
			return nil
		}))
	}

	entries := RunWindow + 1 // the runs in the window and the workspace
	lines := readLines(t, path)
	assert.LessOrEqual(t, len(lines), 2*entries+file.CompactSlack, "compaction bounds the lines")

	var got History
	require.NoError(t, got.Load(t.Context(), path))
	require.Len(t, got.Runs, RunWindow)
	assert.Equal(t, fmt.Sprint(runs-RunWindow), got.Runs[0].Commit, "the oldest are the ones dropped")
	commit, _ := got.PassedCommit("main", "ci")
	assert.Equal(t, fmt.Sprint(runs-1), commit)
}

// TestHistoryTornLastLine pins what a line no writer finished costs: a read leaves it
// unread, since it may be an append in flight, and the next update rewrites the file
// without it and says so, rather than gluing its own line onto the fragment.
func TestHistoryTornLastLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	require.NoError(t, UpdateHistory(t.Context(), path, recordDuration("svc", "go/test", 1_000)))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	require.NoError(t, err)
	_, err = f.WriteString(`{"run":{"commit":"to`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	var read History
	require.NoError(t, read.Load(t.Context(), path))
	assert.Contains(t, read.Projects, "svc")
	assert.Empty(t, read.Runs)

	var logged strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	require.NoError(t, UpdateHistory(t.Context(), path, func(h *History) error {
		h.RecordRun(Run{Commit: "aaa", Ref: "main", Target: "ci", Status: RunPassed}, fixedNow)
		return nil
	}))
	assert.Contains(t, logged.String(), path+" ended in a line no writer finished")
	assert.Len(t, readLines(t, path), 3, "the workspace, svc and the run; the fragment is gone")

	var got History
	require.NoError(t, got.Load(t.Context(), path))
	assert.Contains(t, got.Projects, "svc")
	require.Len(t, got.Runs, 1)
	assert.Equal(t, "aaa", got.Runs[0].Commit)
}

// TestHistoryRefusesTheOldDocument pins that the JSON document an older magus kept beside
// the store is named and never read.
func TestHistoryRefusesTheOldDocument(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "v1.json"), []byte(`{"version":8}`), 0o644))
	path := filepath.Join(dir, "history.jsonl")

	var h History
	err := h.Load(t.Context(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mv "+filepath.Join(dir, "v1.json"))
	require.Error(t, UpdateHistory(t.Context(), path, recordDuration("svc", "go/test", 1)))
	assert.NoFileExists(t, path, "nothing is written while the old document stands")
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

// TestRunLog covers the v5 run log end to end: the last-passed lookup the affected
// base ref depends on, cross-target isolation, window trimming, and that Merge is a
// union rather than freshest-wins (two shard histories are different runs, not two
// readings of one).
func TestRunLog(t *testing.T) {
	var h History
	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	h.RecordRun(Run{Commit: "aaa", Ref: "main", Target: "ci", Status: RunPassed}, t0)
	h.RecordRun(Run{Commit: "bbb", Ref: "main", Target: "ci", Status: RunFailed}, t0.Add(time.Hour))
	h.RecordRun(Run{Commit: "ccc", Ref: "main", Target: "ci", Status: RunFailed}, t0.Add(2*time.Hour))

	if got, ok := h.PassedCommit("main", "ci"); !ok || got != "aaa" {
		t.Fatalf("PassedCommit = %q,%v; want aaa,true", got, ok)
	}
	if n := len(h.FailedSince("main", "ci")); n != 2 {
		t.Fatalf("FailedSince = %d; want 2", n)
	}
	// A different target must not shadow ci's marker.
	h.RecordRun(Run{Commit: "ddd", Ref: "main", Target: "nightly", Status: RunPassed}, t0.Add(3*time.Hour))
	if got, _ := h.PassedCommit("main", "ci"); got != "aaa" {
		t.Fatalf("cross-target leak: got %q; want aaa", got)
	}
	// Unknown branch is a clean miss, not a panic.
	if _, ok := h.PassedCommit("nope", "ci"); ok {
		t.Fatal("unknown branch reported a commit")
	}
	// Window trim.
	var w History
	for i := range RunWindow + 25 {
		w.RecordRun(Run{Commit: "x", Ref: "main", Target: "ci", Status: RunFailed}, t0.Add(time.Duration(i)*time.Minute))
	}
	if n := len(w.Runs); n != RunWindow {
		t.Fatalf("window = %d; want %d", n, RunWindow)
	}
	// Merge is a union, not freshest-wins.
	var a, b History
	a.RecordRun(Run{Commit: "aaa", Ref: "main", Target: "ci", Status: RunPassed}, t0)
	b.RecordRun(Run{Commit: "bbb", Ref: "main", Target: "ci", Status: RunFailed}, t0.Add(time.Hour))
	a.Merge(&b)
	if n := len(a.Runs); n != 2 {
		t.Fatalf("merge kept %d runs; want 2 (union)", n)
	}
	a.Merge(&b) // idempotent
	if n := len(a.Runs); n != 2 {
		t.Fatalf("re-merge duplicated: %d runs; want 2", n)
	}
}

// TestJobOutcomesAreCappedAndMergeAsAUnion pins the job log the Runs log's way: the newest
// JobWindow outcomes survive, merging two histories keeps every outcome once, oldest first,
// and the log survives a write and a read.
func TestJobOutcomesAreCappedAndMergeAsAUnion(t *testing.T) {
	t.Parallel()

	var w History
	for i := range JobWindow + 25 {
		w.RecordJob(types.JobOutcome{Job: "j", Ended: int64(i)})
	}
	require.Len(t, w.Jobs, JobWindow)
	assert.Equal(t, int64(25), w.Jobs[0].Ended, "the oldest are the ones dropped")

	var a, b History
	a.RecordJob(types.JobOutcome{Job: "x", Ended: 2, State: types.StatePass})
	b.RecordJob(types.JobOutcome{Job: "y", Ended: 1, State: types.StateFail})
	b.RecordJob(types.JobOutcome{Job: "x", Ended: 2, State: types.StatePass})
	a.Merge(&b)
	want := []types.JobOutcome{
		{Job: "y", Ended: 1, State: types.StateFail},
		{Job: "x", Ended: 2, State: types.StatePass},
	}
	assert.Equal(t, want, a.Jobs, "a shared outcome is kept once, in time order")

	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, UpdateHistory(t.Context(), path, func(h *History) error {
		h.RecordJob(want[0])
		return nil
	}))
	var got History
	require.NoError(t, got.Load(t.Context(), path))
	assert.Equal(t, want[:1], got.Jobs)
}

// TestJobOutcomeWithWorkSurvivesARoundTripAndMergesOnce pins the v8 fields: a work profile
// and projects read back as written, an absent profile reads back nil rather than an empty
// one, and an outcome read from disk merges with its in-memory copy once even though its
// pointers are new.
func TestJobOutcomeWithWorkSurvivesARoundTripAndMergesOnce(t *testing.T) {
	t.Parallel()

	tokens := int64(41_000)
	worked := types.JobOutcome{
		Job: "w", Ended: 5, Attempt: 1, State: types.StatePass, Parent: "root", ParentAttempt: 2,
		Projects: []string{"internal/job", "types"}, Unattributed: 1,
		Work: &types.JobWork{Calls: 9, Writes: 2, SetupCalls: 6, SetupTokens: &tokens, Denials: 1},
	}
	unknown := types.JobOutcome{Job: "u", Ended: 6, Attempt: 1, State: types.StateFail}

	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, UpdateHistory(t.Context(), path, func(h *History) error {
		h.RecordJob(worked)
		h.RecordJob(unknown)
		return nil
	}))
	var got History
	require.NoError(t, got.Load(t.Context(), path))
	assert.Equal(t, []types.JobOutcome{worked, unknown}, got.Jobs)
	assert.Nil(t, got.Jobs[1].Work, "no profile is unknown, not an idle holder")

	mem := History{Jobs: []types.JobOutcome{worked}}
	mem.Merge(&got)
	assert.Equal(t, []types.JobOutcome{worked, unknown}, mem.Jobs, "one attempt's outcome is kept once")
}

// TestFoldTargetHistoriesCombinesEverySpellServingATarget pins the fold both remaining
// bare-key readers depend on. A project bound to several spells has one history per
// spell, and a caller naming the bare target must see all of them: `magus affected
// --bisect` asks by the name a person types, and the knowledge graph keys its timing
// attributes by the same name the graph's own target nodes carry.
func TestFoldTargetHistoriesCombinesEverySpellServingATarget(t *testing.T) {
	t.Parallel()

	now := time.Now()
	h := History{Projects: map[string]map[string]Stats{
		"p": {
			"magusfile/ci": {P75Ms: 200, Samples: 10, PassCount: 4,
				RecentOutcomes: []Outcome{{Result: OutcomePass, At: now.Add(-time.Minute)}}},
			"go/ci": {P75Ms: 50, Samples: 3, FailCount: 1,
				RecentOutcomes: []Outcome{{Result: OutcomeFail, At: now}}},
		},
	}}

	got, ok := h.FoldTargetHistories("p", "ci")
	require.True(t, ok)
	// A project runs each spell's implementation, so durations add; a failure in any spell
	// failed the target; outcomes interleave by time, newest last.
	want := Stats{
		P75Ms:     250,
		Samples:   13,
		PassCount: 4,
		FailCount: 1,
		RecentOutcomes: []Outcome{
			{Result: OutcomePass, At: now.Add(-time.Minute)},
			{Result: OutcomeFail, At: now},
		},
	}
	assert.Equal(t, want, got)

	// An exact key still answers alone, so the callers that record and read
	// "<spell>/<target>" are unchanged by the fold.
	exact, ok := h.FoldTargetHistories("p", "go/ci")
	require.True(t, ok)
	assert.Equal(t, int64(50), exact.P75Ms)

	_, ok = h.FoldTargetHistories("p", "nope")
	assert.False(t, ok)
}

// TestNewShape pins what separates two shapes: charm order and spelling never do,
// forwarded-arg order and word boundaries always do, since the op's grammar is opaque.
func TestNewShape(t *testing.T) {
	t.Parallel()

	assert.Equal(t, NewShape([]string{"rw", "no-cache"}, nil), NewShape([]string{"noCache", "rw", "rw"}, nil))
	assert.Equal(t, NewShape(nil, nil), NewShape([]string{}, []string{}))
	assert.Nil(t, NewShape(nil, nil).Charms, "no charms records no key at all")
	assert.Len(t, NewShape(nil, nil).ArgsHash, 8, "never empty, which is what marks a pre-v6 outcome")

	runX := ArgsHash([]string{"-run", "X", "-count", "1"})
	assert.Equal(t, runX, ArgsHash([]string{"-run", "X", "-count", "1"}), "stable")
	assert.NotEqual(t, runX, ArgsHash([]string{"-count", "1", "-run", "X"}), "order matters")
	assert.NotEqual(t, ArgsHash([]string{"a b"}), ArgsHash([]string{"a", "b"}))
	assert.NotEqual(t, ArgsHash(nil), ArgsHash([]string{""}))
}

// TestMeasuredRunsPeak pins which runs a memory claim may be sized from: successful
// ones that measured a peak, split by shape, with every spell serving the target folded
// in.
func TestMeasuredRunsPeak(t *testing.T) {
	t.Parallel()

	const mb = int64(1) << 20
	full := NewShape(nil, nil)
	narrow := NewShape(nil, []string{"-run", "TestOne"})
	o := func(r OutcomeResult, peakMB int64, s Shape) Outcome {
		return Outcome{Result: r, MaxRSSBytes: peakMB * mb, Charms: s.Charms, ArgsHash: s.ArgsHash}
	}
	h := History{Projects: map[string]map[string]Stats{".": {
		"magusfile/test": {RecentOutcomes: []Outcome{
			o(OutcomePass, 4000, full),
			o(OutcomeVolatile, 4500, full),
			o(OutcomeFail, 9000, full), // an early death or a leak; not a peak to size from
			o(OutcomePass, 0, full),    // unmeasured
			o(OutcomePass, 300, narrow),
			{Result: OutcomePass, MaxRSSBytes: 5000 * mb}, // before v6: no shape
			{Result: OutcomePass, CPUMs: 900, DurationMs: 100, Charms: full.Charms, ArgsHash: full.ArgsHash}, // a width, no peak
		}},
		"go/test": {RecentOutcomes: []Outcome{o(OutcomePass, 2000, full), o(OutcomePass, 2100, narrow)}},
	}}}
	x := h.MeasuredRuns()

	same, all := x.Peak(".", "test", full)
	assert.Equal(t, Measurement{Value: 4500 * mb, Runs: 1}, same,
		"the larger spell's peak, over the least-observed spell's run count")
	assert.Equal(t, Measurement{Value: 5000 * mb, Runs: 2}, all, "every shape, the unshaped run included")

	same, _ = x.Peak(".", "test", narrow)
	assert.Equal(t, Measurement{Value: 2100 * mb, Runs: 1}, same)

	same, all = x.Peak(".", "magusfile/test", full)
	assert.Equal(t, Measurement{Value: 4500 * mb, Runs: 2}, same, "an exact key answers alone")
	assert.Equal(t, Measurement{Value: 5000 * mb, Runs: 4}, all)

	same, all = x.Peak(".", "test", Shape{})
	assert.Zero(t, same, "the zero Shape matches nothing, not the unshaped runs")
	assert.Equal(t, 2, all.Runs)

	same, all = x.Peak(".", "lint", full)
	assert.Zero(t, same)
	assert.Zero(t, all)

	// A snapshot: recording into the History afterwards does not reach it.
	st := h.Projects["."]["go/test"]
	st.RecentOutcomes = append(st.RecentOutcomes, o(OutcomePass, 8000, full))
	h.Projects["."]["go/test"] = st
	_, all = x.Peak(".", "test", full)
	assert.Equal(t, 5000*mb, all.Value)
}

// TestMeasuredRunsWidth pins how a slot claim is sized from unsaturated runs: the p75 of
// CPU time over wall time across successful runs that measured both and the claim they
// held, rounded up to whole slots and never below one, split by shape like a peak.
func TestMeasuredRunsWidth(t *testing.T) {
	t.Parallel()

	full := NewShape(nil, nil)
	narrow := NewShape(nil, []string{"-run", "TestOne"})
	// Every run held 16 slots, so none of them is saturated.
	o := func(r OutcomeResult, cpuMs, wallMs int64, s Shape) Outcome {
		return Outcome{Result: r, CPUMs: cpuMs, DurationMs: wallMs, Slots: 16, Charms: s.Charms, ArgsHash: s.ArgsHash}
	}
	h := History{Projects: map[string]map[string]Stats{".": {
		"magusfile/scip": {RecentOutcomes: []Outcome{
			o(OutcomePass, 2000, 1000, full),     // 2.0 cores
			o(OutcomePass, 3000, 1000, full),     // 3.0
			o(OutcomeVolatile, 3400, 1000, full), // 3.4
			o(OutcomePass, 9000, 1000, full),     // 9.0: a burst the percentile does not chase
			o(OutcomeFail, 20000, 1000, full),    // a failed run sizes nothing
			o(OutcomePass, 0, 1000, full),        // unmeasured
			o(OutcomePass, 50, 1000, narrow),     // a twentieth of a core still takes one slot
			{Result: OutcomePass, CPUMs: 12000, DurationMs: 1000, Charms: full.Charms, ArgsHash: full.ArgsHash}, // no claim recorded
		}},
		"magusfile/lint": {RecentOutcomes: []Outcome{o(OutcomePass, 3000, 1000, full)}},
		"go/lint":        {RecentOutcomes: []Outcome{o(OutcomePass, 5000, 1000, full), o(OutcomePass, 5000, 1000, full)}},
	}}}
	x := h.MeasuredRuns()

	same, all := x.Width(".", "scip", full)
	// p75 of [2.0 3.0 3.4 9.0] interpolates to 4.8, which takes 5 slots.
	assert.Equal(t, Measurement{Value: 5, Runs: 4}, same)
	// With the narrow run, p75 of [0.05 2.0 3.0 3.4 9.0] is 3.4.
	assert.Equal(t, Measurement{Value: 4, Runs: 5}, all, "every shape, the narrow run included")

	same, _ = x.Width(".", "scip", narrow)
	assert.Equal(t, Measurement{Value: 1, Runs: 1}, same)

	same, _ = x.Width(".", "lint", full)
	assert.Equal(t, Measurement{Value: 5, Runs: 1}, same, "the wider spell, over the least-observed spell's run count")

	same, all = x.Width(".", "test", full)
	assert.Zero(t, same)
	assert.Zero(t, all)
}

// TestMeasuredRunsWidthSaturation pins the ratchet a claim needs because a step runs Go
// only as wide as its slots: a run that kept 0.9 of its claim busy doubles the next
// claim until a wider claim comes back unsaturated, and only unsaturated runs feed the
// percentile.
func TestMeasuredRunsWidthSaturation(t *testing.T) {
	t.Parallel()

	shape := NewShape(nil, nil)
	run := func(slots int, cores float64) Outcome {
		return Outcome{Result: OutcomePass, CPUMs: int64(cores * 1000), DurationMs: 1000, Slots: slots, Charms: shape.Charms, ArgsHash: shape.ArgsHash}
	}
	width := func(runs ...Outcome) Measurement {
		h := History{Projects: map[string]map[string]Stats{".": {"go/test": {RecentOutcomes: runs}}}}
		same, _ := h.MeasuredRuns().Width(".", "test", shape)
		return same
	}

	assert.Equal(t, Measurement{Value: 2, Runs: 3}, width(run(1, 1), run(1, 1), run(1, 0.95)),
		"one slot kept busy is a floor, not a width: the claim doubles rather than settling at one")
	assert.Equal(t, Measurement{Value: 4, Runs: 4}, width(run(1, 1), run(1, 1), run(1, 1), run(2, 1.9)),
		"a saturated history keeps growing")
	assert.Equal(t, Measurement{Value: 3, Runs: 3}, width(run(4, 2), run(4, 2.5), run(4, 3)),
		"an unsaturated history settles at its p75: 2.75 cores, three slots")
	assert.Equal(t, Measurement{Value: 3, Runs: 4}, width(run(4, 2), run(4, 2.5), run(4, 3), run(3, 2.9)),
		"a run saturated at three slots is answered by unsaturated runs granted four")
	assert.Equal(t, Measurement{Value: 1, Runs: 2}, width(run(1, 1), run(2, 1)),
		"a single-threaded target granted two slots used one, so it settles back at one")
	assert.Equal(t, Measurement{Value: 8, Runs: 2}, width(run(4, 2), run(4, 3.8)),
		"saturated and unsaturated at one claim: the claim grows")
}

// syntheticHistory builds a History with n projects × 2 targets, each with
// SampleWindow recent duration observations — representative of a mid-size
// monorepo after several CI runs.
func syntheticHistory(n int) History {
	h := History{
		Version:   HistoryVersion,
		UpdatedAt: time.Now().UTC(),
		Constants: Constants{SetupP50Ms: 30_000, AlphaMs: 5_000},
		Projects:  make(map[string]map[string]Stats, n),
	}
	for i := range n {
		proj := fmt.Sprintf("apps/service-%03d", i)
		h.Projects[proj] = map[string]Stats{
			"build": syntheticStats(i, "build"),
			"test":  syntheticStats(i, "test"),
		}
	}
	return h
}

func syntheticStats(seed int, _ string) Stats {
	recent := make([]int64, SampleWindow)
	for j := range recent {
		recent[j] = int64(30_000 + seed*100 + j*50)
	}
	return Stats{
		P75Ms:       int64(30_000 + seed*100),
		Samples:     SampleWindow,
		LastUpdated: time.Now().UTC(),
		Recent:      recent,
		HitCount:    seed % 10,
		MissCount:   (seed + 3) % 10,
		HitRate:     float64(seed%10) / 10.0,
	}
}

// BenchmarkHistoryLoad measures the full Load path: reading, decoding and folding the
// lines of a 100-project history file.
func BenchmarkHistoryLoad(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	seedHistory(b, path, syntheticHistory(100))
	b.ResetTimer()
	for range b.N {
		var h2 History
		if err := h2.Load(context.Background(), path); err != nil {
			b.Fatal(err)
		}
	}
}
