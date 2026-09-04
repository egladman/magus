package volatility

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testCfg = Config{
	Enabled:          true,
	BootstrapSamples: 5,
	MinSamples:       5,
	Threshold:        0.05,
}

const (
	testProject = "proj"
	testTarget  = "test"
)

// buildRuntime constructs a Runtime with n recorded outcomes.
func buildRuntime(results []string, affected bool) *Runtime {
	h := &forecast.History{}
	rt := NewRuntime(h, "", testCfg, nil, true)
	now := time.Now()
	for i, r := range results {
		rt.Record(testProject, testTarget, forecast.Outcome{
			Result:         forecast.OutcomeResult(r),
			AffectedByDiff: affected,
			DurationMs:     1000,
			At:             now.Add(time.Duration(i) * time.Minute),
			Attempts:       1,
		})
	}
	return rt
}

// TestShouldRetry_Bootstrap verifies that all failures are retried during
// the bootstrap phase (fewer than cfg.BootstrapSamples outcomes).
func TestShouldRetry_Bootstrap(t *testing.T) {
	t.Parallel()
	rt := buildRuntime([]string{"pass", "pass"}, true) // only 2 outcomes < 5
	d := rt.Decide(testProject, testTarget, true, true /*eligible*/)
	assert.True(t, d.Retry)
	assert.Equal(t, ReasonBootstrap, d.Reason)
}

// TestShouldRetry_UnaffectedFailure verifies that an unaffected project
// failure is always retried regardless of score.
func TestShouldRetry_UnaffectedFailure(t *testing.T) {
	t.Parallel()
	// 10 clean passes → score = 0 (no volatile outcomes), well past bootstrap.
	rt := buildRuntime([]string{"pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass"}, true)
	d := rt.Decide(testProject, testTarget, false /*not affected*/, true /*eligible*/)
	assert.True(t, d.Retry)
	assert.Equal(t, ReasonUnaffectedFailure, d.Reason)
}

// TestShouldRetry_PredictedVolatile verifies that a target above the volatile
// threshold is retried.
func TestShouldRetry_PredictedVolatile(t *testing.T) {
	t.Parallel()
	// 3 volatile, 7 passes → volatility rate 30%; Wilson LB should be well above 5%.
	rt := buildRuntime([]string{"pass", "volatile", "pass", "volatile", "pass", "volatile", "pass", "pass", "pass", "pass"}, true)
	d := rt.Decide(testProject, testTarget, true /*affected*/, true /*eligible*/)
	assert.True(t, d.Retry)
	assert.Equal(t, ReasonPredictedVolatile, d.Reason)
}

// TestShouldRetry_Skip verifies that a clean target with no volatility history is
// not retried (likely a real failure).
func TestShouldRetry_Skip(t *testing.T) {
	t.Parallel()
	// 10 clean passes, no volatile outcomes → score = 0.
	rt := buildRuntime([]string{"pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass"}, true)
	d := rt.Decide(testProject, testTarget, true /*affected*/, true /*eligible*/)
	assert.False(t, d.Retry, "no volatility history, likely real failure")
}

// TestScore_WilsonMath checks the Wilson lower bound formula against a known value.
// With 3 volatile out of 10 total (p=0.30, n=10, z=1.96):
//
//	LB ≈ (0.30 + 1.96²/20 − 1.96·√((0.30·0.70 + 1.96²/40)/10)) / (1 + 1.96²/10)
//	   ≈ 0.115
func TestScore_WilsonMath(t *testing.T) {
	t.Parallel()
	rt := buildRuntime([]string{"pass", "volatile", "pass", "volatile", "pass", "volatile", "pass", "pass", "pass", "pass"}, true)
	got := rt.Score(testProject, testTarget)
	// Hand-computed Wilson LB ≈ 0.115; allow ±0.02 tolerance.
	assert.InDelta(t, 0.115, got, 0.02)
}

// TestScore_ColdStart verifies that Score returns 0 below MinSamples.
func TestScore_ColdStart(t *testing.T) {
	t.Parallel()
	rt := buildRuntime([]string{"volatile", "volatile"}, true) // only 2 outcomes < MinSamples=5
	assert.Zero(t, rt.Score(testProject, testTarget), "cold start")
}

// TestIsSuspectedRegression verifies the clean→fail pattern is detected.
func TestIsSuspectedRegression(t *testing.T) {
	t.Parallel()
	// Mostly passing history, then two consecutive affected failures.
	rt := buildRuntime([]string{"pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "fail", "fail"}, true)
	assert.True(t, rt.IsRegression(testProject, testTarget), "clean→fail pattern")
}

// TestIsSuspectedRegression_FalsePositive verifies that a known volatile target
// is NOT flagged as a regression.
func TestIsSuspectedRegression_FalsePositive(t *testing.T) {
	t.Parallel()
	// High volatility rate (5 volatile in 10) → score well above threshold → not a regression.
	rt := buildRuntime([]string{"volatile", "pass", "volatile", "pass", "volatile", "pass", "volatile", "pass", "fail", "fail"}, true)
	assert.False(t, rt.IsRegression(testProject, testTarget), "known-volatile target")
}

// TestIsSuspectedRegression_UnaffectedFails verifies that failures on unaffected
// projects are NOT treated as regressions.
func TestIsSuspectedRegression_UnaffectedFails(t *testing.T) {
	t.Parallel()
	h := &forecast.History{}
	rt := NewRuntime(h, "", testCfg, nil, true)
	results := []forecast.OutcomeResult{
		forecast.OutcomePass, forecast.OutcomePass, forecast.OutcomePass, forecast.OutcomePass,
		forecast.OutcomePass, forecast.OutcomePass, forecast.OutcomePass, forecast.OutcomePass,
		forecast.OutcomeFail, forecast.OutcomeFail,
	}
	now := time.Now()
	for i, r := range results {
		affected := true
		if i >= 8 { // last two are unaffected
			affected = false
		}
		rt.Record(testProject, testTarget, forecast.Outcome{
			Result:         r,
			AffectedByDiff: affected,
			DurationMs:     1000,
			At:             now.Add(time.Duration(i) * time.Minute),
			Attempts:       1,
		})
	}
	assert.False(t, rt.IsRegression(testProject, testTarget), "unaffected failures")
}

// TestRecordOutcome_Eviction verifies that the window caps at OutcomeWindow
// and counters are consistent with the retained window.
func TestRecordOutcome_Eviction(t *testing.T) {
	t.Parallel()
	h := &forecast.History{}
	rt := NewRuntime(h, "", testCfg, nil, true)
	now := time.Now()
	total := forecast.OutcomeWindow + 10
	for i := range total {
		result := forecast.OutcomePass
		if i == 0 {
			result = forecast.OutcomeVolatile // first entry should be evicted
		}
		rt.Record(testProject, testTarget, forecast.Outcome{
			Result: result, AffectedByDiff: true, DurationMs: 1000,
			At: now.Add(time.Duration(i) * time.Minute),
		})
	}
	s := rt.Stats(testProject, testTarget)
	assert.Len(t, s.RecentOutcomes, forecast.OutcomeWindow)
	// The first "volatile" entry should have been evicted; counters must match.
	assert.Zero(t, s.VolatileCount, "volatile entry should have been evicted")
	assert.Equal(t, forecast.OutcomeWindow, s.PassCount)
}

// TestLastPassTime returns the timestamp of the most recent pass or volatile.
func TestLastPassTime(t *testing.T) {
	t.Parallel()
	now := time.Now().Truncate(time.Second)
	h := &forecast.History{}
	rt := NewRuntime(h, "", testCfg, nil, true)
	for i, r := range []forecast.OutcomeResult{
		forecast.OutcomePass, forecast.OutcomePass, forecast.OutcomeFail, forecast.OutcomeFail,
	} {
		rt.Record(testProject, testTarget, forecast.Outcome{
			Result: r, At: now.Add(time.Duration(i) * time.Minute),
		})
	}
	got := rt.LastPassTime(testProject, testTarget)
	want := now.Add(time.Minute) // second "pass" entry
	assert.True(t, got.Equal(want), "LastPassTime = %v, want %v", got, want)
}

// TestBuildReportEmptyPath returns just the configured threshold, no error, when no
// history file is configured - "no history yet" is a valid empty state.
func TestBuildReportEmptyPath(t *testing.T) {
	t.Parallel()
	report, err := BuildReport(context.Background(), "", Config{Threshold: 0.2})
	require.NoError(t, err)
	assert.Equal(t, types.VolatilityReport{Threshold: 0.2}, report)
}

// TestBuildReport scores every recorded (project, target) pair and returns them sorted
// by (project, target), with the threshold and per-target tallies carried through.
func TestBuildReport(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	st := forecast.Stats{
		PassCount: 2, FailCount: 1, VolatileCount: 1,
		RecentOutcomes: []forecast.Outcome{
			{Result: "pass", At: now.Add(-3 * time.Hour)},
			{Result: "volatile", At: now.Add(-2 * time.Hour)},
			{Result: "pass", At: now.Add(-1 * time.Hour)},
			{Result: "fail", At: now},
		},
	}
	hist := forecast.History{
		Version: forecast.HistoryVersion,
		Projects: map[string]map[string]forecast.Stats{
			"proj/b": {"test": st},
			"proj/a": {"test": st},
		},
	}
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, hist.Save(context.Background(), path))

	cfg := Config{Enabled: true, BootstrapSamples: 4, MinSamples: 4, Threshold: 0.01}
	report, err := BuildReport(context.Background(), path, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0.01, report.Threshold)
	require.Len(t, report.Targets, 2)

	// Deterministic (project, target) ordering.
	assert.Equal(t, "proj/a", report.Targets[0].Project)
	assert.Equal(t, "proj/b", report.Targets[1].Project)

	got := report.Targets[0]
	assert.Equal(t, "test", got.Target)
	assert.Equal(t, 2, got.Pass)
	assert.Equal(t, 1, got.Fail)
	assert.Equal(t, 1, got.VolatileCount)
	assert.Equal(t, 4, got.Samples)
	assert.Equal(t, now.Add(-1*time.Hour), got.LastPass)
	assert.Greater(t, got.Score, 0.0, "4 samples at MinSamples=4 produce a non-zero Wilson score")
	assert.True(t, got.Volatile, "score exceeds the 0.01 threshold")
}

// TestRuntimeRecordsWithoutRetryingWhenNotOptedIn pins the separation this type
// was given when recording stopped being gated on the retry policy.
//
// Recording has to be unconditional: the history it writes is the same store the
// shard forecaster reads, and gating it on RetryOnVolatile meant a workspace
// where no target opts in recorded nothing, leaving the forecaster to predict a
// flat default for every project. Retrying must stay opted-in, or widening the
// recorder would silently turn auto-retry on for every target in every
// workspace - a behaviour change nobody asked for, hidden inside a metrics fix.
func TestRuntimeRecordsWithoutRetryingWhenNotOptedIn(t *testing.T) {
	t.Parallel()
	h := &forecast.History{}
	rt := NewRuntime(h, "", testCfg, nil, false)

	// Bootstrap conditions: with no history at all, shouldRetry would return
	// Retry=true for an opted-in target. This one did not opt in.
	d := rt.Decide("svc/api", "go/test", true, false /*not eligible*/)
	assert.False(t, d.Retry, "a target that never opted into RetryOnVolatile must never be retried")
	assert.Equal(t, ReasonDisabled, d.Reason)

	// ...and yet the outcome is still recorded, because the forecaster needs it.
	rt.Record("svc/api", "go/test", forecast.Outcome{
		Result: "pass", DurationMs: 1200, MaxRSSBytes: 512 << 20, At: time.Now(),
	})
	got := rt.Stats("svc/api", "go/test")
	require.Len(t, got.RecentOutcomes, 1, "recording must not depend on the retry policy")
	assert.Equal(t, int64(512<<20), got.RecentOutcomes[0].MaxRSSBytes)

	// The same runtime WITH the opt-in does retry, so the gate is the policy and
	// nothing else.
	assert.True(t, NewRuntime(h, "", testCfg, nil, true).Decide("svc/api", "go/test", true, true).Retry)

	// Both halves of the gate are required, and each one alone refuses. The run-wide
	// half carries --no-volatility-retry; the per-pair half is the target's own
	// policy, and it is passed in because the Runtime holds one answer for a run that
	// selects many targets with different ones.
	assert.False(t, NewRuntime(h, "", testCfg, nil, false).Decide("svc/api", "go/test", true, true).Retry,
		"--no-volatility-retry must refuse an opted-in target")
	assert.False(t, NewRuntime(h, "", testCfg, nil, true).Decide("svc/api", "go/test", true, false).Retry,
		"a run where some other target opted in must not make this one retryable")
}

// Recording an outcome must feed the DURATION model as well as the volatility counters.
// This was the missing wiring: run.go measured a duration into Outcome.DurationMs,
// nothing read it, and the forecaster predicted DefaultDurationMs for every project
// forever while LPT packed uniform weights.
func TestRecordOutcomeFeedsTheDurationModel(t *testing.T) {
	h := forecast.History{}
	rt := NewRuntime(&h, "", Config{Enabled: true, MinSamples: 2}, nil, false)
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

	for i, ms := range []int64{1000, 2000, 3000} {
		rt.Record("libs/foo", "test", forecast.Outcome{
			Result: "pass", DurationMs: ms, At: at.Add(time.Duration(i) * time.Minute),
		})
	}

	st := h.Projects["libs/foo"]["test"]
	if st.Samples != 3 {
		t.Fatalf("Samples = %d, want 3: the predictor's tier-3 gate needs >= 3", st.Samples)
	}
	if st.P75Ms <= 0 {
		t.Fatalf("P75Ms = %d, want a measured percentile", st.P75Ms)
	}
	// And the prediction stops being the hardcoded default.
	if got := h.PredictDuration("libs/foo", "test", nil); got == time.Duration(forecast.DefaultDurationMs)*time.Millisecond {
		t.Fatalf("PredictDuration returned the default %v; the recorded durations were not read", got)
	}
}

// A run that reported no duration is not a sample. Folding a zero in would drag the
// percentile toward zero and make a heavy target look cheap to the shard planner.
func TestRecordOutcomeIgnoresAnUnmeasuredRun(t *testing.T) {
	h := forecast.History{}
	rt := NewRuntime(&h, "", Config{Enabled: true}, nil, false)
	rt.Record("libs/foo", "test", forecast.Outcome{Result: "fail", DurationMs: 0, At: time.Now()})

	st := h.Projects["libs/foo"]["test"]
	if st.Samples != 0 || st.P75Ms != 0 {
		t.Fatalf("Samples=%d P75Ms=%d, want an unmeasured run to contribute neither", st.Samples, st.P75Ms)
	}
	if st.FailCount != 1 {
		t.Fatalf("FailCount = %d, want the volatility counter still updated", st.FailCount)
	}
}

// Merge resolves collisions on LastUpdated, which recordOutcome never set - so every
// shard's entry compared equal and merge-history kept whichever file it read first,
// silently discarding the rest of the run.
func TestMergeKeepsTheNewerShardsOutcomes(t *testing.T) {
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	build := func(ms int64, when time.Time) *forecast.History {
		h := &forecast.History{}
		rt := NewRuntime(h, "", Config{Enabled: true}, nil, false)
		rt.Record("libs/foo", "test", forecast.Outcome{Result: "pass", DurationMs: ms, At: when})
		return h
	}
	older, newer := build(1000, at), build(9000, at.Add(time.Hour))

	older.Merge(newer)
	got := older.Projects["libs/foo"]["test"]
	if got.P75Ms != 9000 {
		t.Fatalf("P75Ms = %d, want 9000: the newer shard's outcome was discarded", got.P75Ms)
	}
	if !got.LastUpdated.Equal(at.Add(time.Hour)) {
		t.Fatalf("LastUpdated = %v, want the newer shard's timestamp", got.LastUpdated)
	}
}

// The vocabulary is what the counters are computed from, so it has to stay exactly the
// three strings a cached history file already holds: retyping the field must not have
// moved a byte on the wire.
func TestOutcomeResultsKeepTheirWireSpelling(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "pass", string(forecast.OutcomePass))
	assert.Equal(t, "fail", string(forecast.OutcomeFail))
	assert.Equal(t, "volatile", string(forecast.OutcomeVolatile))
}

// A result this magus does not recognize - a newer writer's vocabulary, or a hand-edited
// file - must move no counter. Counting it as a pass or a fail would score a target on a
// verdict nothing here can read.
func TestRecordIgnoresAnUnrecognizedResult(t *testing.T) {
	t.Parallel()

	h := &forecast.History{}
	rt := NewRuntime(h, "", testCfg, nil, true)
	now := time.Now()
	rt.Record(testProject, testTarget, forecast.Outcome{Result: forecast.OutcomePass, At: now})
	rt.Record(testProject, testTarget, forecast.Outcome{Result: "quarantined", At: now.Add(time.Minute)})

	s := rt.Stats(testProject, testTarget)
	assert.Len(t, s.RecentOutcomes, 2, "the row is kept; only the counters decline to read it")
	assert.Equal(t, 1, s.PassCount)
	assert.Zero(t, s.FailCount)
	assert.Zero(t, s.VolatileCount)
}
