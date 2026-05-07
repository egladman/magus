package flake_test

import (
	"math"
	"testing"
	"time"

	"github.com/egladman/magus/internal/ci/flake"
	"github.com/egladman/magus/internal/ci/forecast"
)

var testCfg = flake.Config{
	Enabled:          true,
	BootstrapSamples: 5,
	MinSamples:       5,
	Threshold: 0.05,
}

const (
	testProject = "proj"
	testTarget  = "test"
)

// buildRuntime constructs a Runtime with n recorded outcomes.
func buildRuntime(results []string, affected bool) *flake.Runtime {
	h := &forecast.History{}
	rt := flake.NewRuntime(h, "", testCfg, nil)
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
	if !d.Retry || d.Reason != flake.ReasonBootstrap {
		t.Errorf("bootstrap: Retry=%v Reason=%q, want Retry=true Reason=bootstrap", d.Retry, d.Reason)
	}
}

// TestShouldRetry_UnaffectedFailure verifies that an unaffected project
// failure is always retried regardless of score.
func TestShouldRetry_UnaffectedFailure(t *testing.T) {
	t.Parallel()
	// 10 clean passes → score = 0 (no flakes), well past bootstrap.
	rt := buildRuntime([]string{"pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass"}, true)
	d := rt.Decide(testProject, testTarget, false /*not affected*/)
	if !d.Retry || d.Reason != flake.ReasonUnaffectedFailure {
		t.Errorf("unaffected: Retry=%v Reason=%q, want Retry=true Reason=unaffected_failure", d.Retry, d.Reason)
	}
}

// TestShouldRetry_PredictedFlake verifies that a target above the flake
// threshold is retried.
func TestShouldRetry_PredictedFlake(t *testing.T) {
	t.Parallel()
	// 3 flakes, 7 passes → flake rate 30%; Wilson LB should be well above 5%.
	rt := buildRuntime([]string{"pass", "flake", "pass", "flake", "pass", "flake", "pass", "pass", "pass", "pass"}, true)
	d := rt.Decide(testProject, testTarget, true /*affected*/)
	if !d.Retry || d.Reason != flake.ReasonPredictedFlake {
		t.Errorf("predicted_flake: Retry=%v Reason=%q, want Retry=true Reason=predicted_flake", d.Retry, d.Reason)
	}
}

// TestShouldRetry_Skip verifies that a clean target with no flake history is
// not retried (likely a real failure).
func TestShouldRetry_Skip(t *testing.T) {
	t.Parallel()
	// 10 clean passes, no flakes → score = 0.
	rt := buildRuntime([]string{"pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass"}, true)
	d := rt.Decide(testProject, testTarget, true /*affected*/)
	if d.Retry {
		t.Errorf("skip: Retry=true, want false (no flake history, likely real failure)")
	}
}

// TestScore_WilsonMath checks the Wilson lower bound formula against a known value.
// With 3 flakes out of 10 total (p=0.30, n=10, z=1.96):
//
//	LB ≈ (0.30 + 1.96²/20 − 1.96·√((0.30·0.70 + 1.96²/40)/10)) / (1 + 1.96²/10)
//	   ≈ 0.115
func TestScore_WilsonMath(t *testing.T) {
	t.Parallel()
	rt := buildRuntime([]string{"pass", "flake", "pass", "flake", "pass", "flake", "pass", "pass", "pass", "pass"}, true)
	got := rt.Score(testProject, testTarget)
	// Hand-computed Wilson LB ≈ 0.115; allow ±0.02 tolerance.
	want := 0.115
	if math.Abs(got-want) > 0.02 {
		t.Errorf("Score = %.4f, want ~%.4f (±0.02)", got, want)
	}
}

// TestScore_ColdStart verifies that Score returns 0 below MinSamples.
func TestScore_ColdStart(t *testing.T) {
	t.Parallel()
	rt := buildRuntime([]string{"flake", "flake"}, true) // only 2 outcomes < MinSamples=5
	if got := rt.Score(testProject, testTarget); got != 0 {
		t.Errorf("cold start: Score = %.4f, want 0", got)
	}
}

// TestIsSuspectedRegression verifies the clean→fail pattern is detected.
func TestIsSuspectedRegression(t *testing.T) {
	t.Parallel()
	// Mostly passing history, then two consecutive affected failures.
	rt := buildRuntime([]string{"pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "fail", "fail"}, true)
	if !rt.IsRegression(testProject, testTarget) {
		t.Errorf("expected IsRegression=true for clean→fail pattern")
	}
}

// TestIsSuspectedRegression_FalsePositive verifies that a known flaky target
// is NOT flagged as a regression.
func TestIsSuspectedRegression_FalsePositive(t *testing.T) {
	t.Parallel()
	// High flake rate (5 flakes in 10) → score well above threshold → not a regression.
	rt := buildRuntime([]string{"flake", "pass", "flake", "pass", "flake", "pass", "flake", "pass", "fail", "fail"}, true)
	if rt.IsRegression(testProject, testTarget) {
		t.Errorf("expected IsRegression=false for known-flaky target")
	}
}

// TestIsSuspectedRegression_UnaffectedFails verifies that failures on unaffected
// projects are NOT treated as regressions.
func TestIsSuspectedRegression_UnaffectedFails(t *testing.T) {
	t.Parallel()
	h := &forecast.History{}
	rt := flake.NewRuntime(h, "", testCfg, nil)
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
	if rt.IsRegression(testProject, testTarget) {
		t.Errorf("expected IsRegression=false for unaffected failures")
	}
}

// TestRecordOutcome_Eviction verifies that the window caps at OutcomeWindow
// and counters are consistent with the retained window.
func TestRecordOutcome_Eviction(t *testing.T) {
	t.Parallel()
	h := &forecast.History{}
	rt := flake.NewRuntime(h, "", testCfg, nil)
	now := time.Now()
	total := forecast.OutcomeWindow + 10
	for i := range total {
		result := "pass"
		if i == 0 {
			result = "flake" // first entry should be evicted
		}
		rt.Record(testProject, testTarget, forecast.Outcome{
			Result: result, AffectedByDiff: true, DurationMs: 1000,
			At: now.Add(time.Duration(i) * time.Minute),
		})
	}
	s := rt.Stats(testProject, testTarget)
	if len(s.RecentOutcomes) != forecast.OutcomeWindow {
		t.Errorf("len(RecentOutcomes) = %d, want %d", len(s.RecentOutcomes), forecast.OutcomeWindow)
	}
	// The first "flake" entry should have been evicted; counters must match.
	if s.FlakeCount != 0 {
		t.Errorf("FlakeCount = %d, want 0 (flake entry should have been evicted)", s.FlakeCount)
	}
	if s.PassCount != forecast.OutcomeWindow {
		t.Errorf("PassCount = %d, want %d", s.PassCount, forecast.OutcomeWindow)
	}
}

// TestLastPassTime returns the timestamp of the most recent pass or flake.
func TestLastPassTime(t *testing.T) {
	t.Parallel()
	now := time.Now().Truncate(time.Second)
	h := &forecast.History{}
	rt := flake.NewRuntime(h, "", testCfg, nil)
	for i, r := range []string{"pass", "pass", "fail", "fail"} {
		rt.Record(testProject, testTarget, forecast.Outcome{
			Result: r, At: now.Add(time.Duration(i) * time.Minute),
		})
	}
	got := rt.LastPassTime(testProject, testTarget)
	want := now.Add(time.Minute) // second "pass" entry
	if !got.Equal(want) {
		t.Errorf("LastPassTime = %v, want %v", got, want)
	}
}
