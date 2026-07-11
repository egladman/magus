package volatility

import (
	"testing"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/stretchr/testify/assert"
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
	rt := NewRuntime(h, "", testCfg, nil)
	now := time.Now()
	for i, r := range results {
		rt.Record(testProject, testTarget, forecast.Outcome{
			Result:         r,
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
	d := rt.Decide(testProject, testTarget, true)
	assert.True(t, d.Retry)
	assert.Equal(t, ReasonBootstrap, d.Reason)
}

// TestShouldRetry_UnaffectedFailure verifies that an unaffected project
// failure is always retried regardless of score.
func TestShouldRetry_UnaffectedFailure(t *testing.T) {
	t.Parallel()
	// 10 clean passes → score = 0 (no volatile outcomes), well past bootstrap.
	rt := buildRuntime([]string{"pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass"}, true)
	d := rt.Decide(testProject, testTarget, false /*not affected*/)
	assert.True(t, d.Retry)
	assert.Equal(t, ReasonUnaffectedFailure, d.Reason)
}

// TestShouldRetry_PredictedVolatile verifies that a target above the volatile
// threshold is retried.
func TestShouldRetry_PredictedVolatile(t *testing.T) {
	t.Parallel()
	// 3 volatile, 7 passes → volatility rate 30%; Wilson LB should be well above 5%.
	rt := buildRuntime([]string{"pass", "volatile", "pass", "volatile", "pass", "volatile", "pass", "pass", "pass", "pass"}, true)
	d := rt.Decide(testProject, testTarget, true /*affected*/)
	assert.True(t, d.Retry)
	assert.Equal(t, ReasonPredictedVolatile, d.Reason)
}

// TestShouldRetry_Skip verifies that a clean target with no volatility history is
// not retried (likely a real failure).
func TestShouldRetry_Skip(t *testing.T) {
	t.Parallel()
	// 10 clean passes, no volatile outcomes → score = 0.
	rt := buildRuntime([]string{"pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass"}, true)
	d := rt.Decide(testProject, testTarget, true /*affected*/)
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
	rt := NewRuntime(h, "", testCfg, nil)
	results := []string{"pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "fail", "fail"}
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
	rt := NewRuntime(h, "", testCfg, nil)
	now := time.Now()
	total := forecast.OutcomeWindow + 10
	for i := range total {
		result := "pass"
		if i == 0 {
			result = "volatile" // first entry should be evicted
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
	rt := NewRuntime(h, "", testCfg, nil)
	for i, r := range []string{"pass", "pass", "fail", "fail"} {
		rt.Record(testProject, testTarget, forecast.Outcome{
			Result: r, At: now.Add(time.Duration(i) * time.Minute),
		})
	}
	got := rt.LastPassTime(testProject, testTarget)
	want := now.Add(time.Minute) // second "pass" entry
	assert.True(t, got.Equal(want), "LastPassTime = %v, want %v", got, want)
}
