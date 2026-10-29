package bench

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/benchmarks/agent/internal/pricing"
	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/suite"
)

type benchSuite struct{ suite.Suite }

func TestBenchSuite(t *testing.T) { suite.Run(t, new(benchSuite)) }

func loadTestPricing(t *testing.T) pricing.Table {
	t.Helper()
	table, err := pricing.Default()
	if err != nil {
		t.Fatal(err)
	}
	return table
}

// fixtureRecords builds the synthetic tree in a temp dir and extracts it,
// keyed by run id.
func fixtureRecords(t *testing.T) (string, map[string]RunRecord, []RunRecord) {
	t.Helper()
	root := t.TempDir()
	if err := WriteFixture(root, "claude-opus-5"); err != nil {
		t.Fatal(err)
	}
	results := filepath.Join(root, "results")
	records, err := Extract(results, loadTestPricing(t))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]RunRecord{}
	for _, r := range records {
		byID[r.ID()] = r
	}
	return results, byID, records
}

func scoredRecord(t *testing.T, byID map[string]RunRecord, arm, task string, rep int64) *ScoredRun {
	t.Helper()
	r, ok := byID[fixtureRunID(arm, task, rep)]
	if !ok || r.Scored == nil {
		t.Fatalf("no scored run %s/%s/%d", arm, task, rep)
	}
	return r.Scored
}

func boolp(b bool) *bool { return &b }

func int64p(i int64) *int64 { return &i }

func floatp(f float64) *float64 { return &f }

func stringp(s string) *string { return &s }

func almost(a, b, places float64) bool { return math.Abs(a-b) < math.Pow(10, -places)/2 }

func syntheticRecord(arm, task string, rep int64, dollars float64, tokens int64, success bool) RunRecord {
	return RunRecord{Scored: &ScoredRun{
		RunID: fmt.Sprintf("%s-%s-r%d", arm, task, rep), Arm: arm, Task: task, Rep: rep, Model: "claude-opus-5",
		Dollars: dollars, TableDollarsUSD: dollars,
		Tokens: TokenCounts{Input: tokens, TotalBilled: tokens},
		Turns:  1, ToolCalls: 1, ToolCallsByName: map[string]int64{"Bash": 1},
		FileReads: 1, DistinctFilesRead: 1, ToolResultBytes: 10,
		CheckExit: int64p(map[bool]int64{true: 0, false: 1}[success]), Success: boolp(success),
		WallMs: floatp(1000), TimeToFirstEditMs: floatp(100), TimeToDoneMs: floatp(900),
	}}
}

func controlRecord(kind string, success bool) RunRecord {
	return RunRecord{Control: &controlRun{
		RunID: "rampant-task-a-r1-" + kind, Arm: "rampant", Task: "task-a", Rep: 1, Model: "claude-opus-5",
		Control: kind, CheckExit: int64p(map[bool]int64{true: 0, false: 1}[success]), Success: boolp(success),
	}}
}

// taskPairs is one run per arm for each of n tasks, full spending fullTokens
// and rampant rampantTokens plus a per-task spread, so the deltas vary and the
// t interval has width.
func taskPairs(n int, fullTokens, rampantTokens func(task int) int64) []RunRecord {
	var records []RunRecord
	for i := range n {
		task := fmt.Sprintf("task-%02d", i)
		records = append(records,
			syntheticRecord("full", task, 1, float64(fullTokens(i))/1000, fullTokens(i), true),
			syntheticRecord("rampant", task, 1, float64(rampantTokens(i))/1000, rampantTokens(i), true))
	}
	return records
}

func (s *benchSuite) analyze(records []RunRecord) *Analysis {
	a, err := Analyze(records, 7)
	s.Require().NoError(err)
	return a
}

func (s *benchSuite) TestControlsDiscriminateWhenGoldenPassesAndNullFails() {
	scored := []RunRecord{
		syntheticRecord("rampant", "task-a", 1, 1.0, 100, true),
		syntheticRecord("full", "task-a", 1, 1.0, 100, true),
	}
	a := s.analyze(append(scored, controlRecord(controlGolden, true), controlRecord(controlNull, false)))
	s.True(a.Controls["task-a"].Discriminates)
	s.Equal(int64(2), a.Runs, "controls are not scored runs")
}

func (s *benchSuite) TestControlsDoNotDiscriminateWhenNullPasses() {
	scored := []RunRecord{
		syntheticRecord("rampant", "task-a", 1, 1.0, 100, true),
		syntheticRecord("full", "task-a", 1, 1.0, 100, true),
	}
	a := s.analyze(append(scored, controlRecord(controlGolden, true), controlRecord(controlNull, true)))
	s.False(a.Controls["task-a"].Discriminates)
}

func (s *benchSuite) TestControlsAreUnverifiedWithoutAControlRun() {
	a := s.analyze([]RunRecord{
		syntheticRecord("rampant", "task-a", 1, 1.0, 100, true),
		syntheticRecord("full", "task-a", 1, 1.0, 100, true),
	})
	s.False(a.Controls["task-a"].Discriminates)
	s.Zero(a.Controls["task-a"].Golden.N)
}

func (s *benchSuite) TestWilsonIntervalIsNilWhenNothingIsGraded() {
	got := wilsonInterval(0, 0)
	s.Nil(got[0])
	s.Nil(got[1])
}

func (s *benchSuite) TestWilsonIntervalOfThreePasses() {
	got := wilsonInterval(3, 3)
	s.InDelta(0.4385029682449546, *got[0], 1e-12)
	s.InDelta(1.0, *got[1], 1e-12)
}

// Two passes of three: pass@k is 1 - C(1,k)/C(3,k), pass^k C(2,k)/C(3,k).
func (s *benchSuite) TestPassAtKAndPassPowKOfTwoPassesInThree() {
	cell := cellStats([]*ScoredRun{
		syntheticRecord("full", "t", 1, 1, 1, true).Scored,
		syntheticRecord("full", "t", 2, 1, 1, false).Scored,
		syntheticRecord("full", "t", 3, 1, 1, true).Scored,
	})
	s.InDeltaSlice([]float64{2.0 / 3, 1, 1}, cell.PassAtK, 1e-15)
	s.InDeltaSlice([]float64{2.0 / 3, 1.0 / 3, 0}, cell.PassPowK, 1e-15)
}

func (s *benchSuite) TestPassPowKIsOneOnlyWhenEveryRunPasses() {
	s.Equal(1.0, passPowK(3, 3, 3))
	s.Equal(0.0, passPowK(3, 2, 3))
	s.Equal(0.0, passAtK(3, 0, 3))
}

func (s *benchSuite) TestCellExcludesUnknownOutcomesFromPassAtK() {
	unknown := syntheticRecord("full", "t", 2, 1, 1, true).Scored
	unknown.Success, unknown.CheckExit = nil, nil
	cell := cellStats([]*ScoredRun{syntheticRecord("full", "t", 1, 1, 1, true).Scored, unknown})
	s.Len(cell.PassAtK, 1, "k runs only to the graded count")
	s.Equal(int64(1), cell.UnknownOutcomes)
}

// task-a passes 2 of 2 and task-b 1 of 4, so the macro pass@1 (0.625) differs
// from the pooled 3 of 6, and k stops at 2, the smaller task's graded count.
func (s *benchSuite) TestArmPassRatesAverageTasksNotTrials() {
	records := []RunRecord{
		syntheticRecord("full", "task-a", 1, 1, 1, true),
		syntheticRecord("full", "task-a", 2, 1, 1, true),
		syntheticRecord("full", "task-b", 1, 1, 1, true),
		syntheticRecord("full", "task-b", 2, 1, 1, false),
		syntheticRecord("full", "task-b", 3, 1, 1, false),
		syntheticRecord("full", "task-b", 4, 1, 1, false),
	}
	summary := s.analyze(records).ArmSummaries["full"]
	s.InDelta(0.625, *summary.PassAt1, 1e-15)
	s.Equal(int64(2), summary.K)
	s.InDeltaSlice([]float64{0.625, 0.75}, summary.PassAtK, 1e-15)
	s.InDeltaSlice([]float64{0.625, 0.5}, summary.PassPowK, 1e-15)
	s.NotNil(summary.PassAt1CI[0], "two tasks give a t interval over their rates")
}

func (s *benchSuite) TestArmPassAt1HasNoIntervalFromOneTask() {
	summary := s.analyze([]RunRecord{syntheticRecord("full", "task-a", 1, 1, 1, true)}).ArmSummaries["full"]
	s.Nil(summary.PassAt1CI[0], "one task is no spread to interval")
}

func (s *benchSuite) TestCostOfPassIsInfiniteWithNoPasses() {
	var failing []*ScoredRun
	for rep := int64(1); rep <= 3; rep++ {
		failing = append(failing, syntheticRecord("rampant", "task-a", rep, 0.5, 100, false).Scored)
	}
	cost := armSummary(failing, nil).CostOfPassUSD
	s.True(cost.Infinite)
	s.Nil(cost.Value)
}

func (s *benchSuite) TestCostOfPassIsGradedDollarsOverPasses() {
	summary := armSummary([]*ScoredRun{
		syntheticRecord("full", "task-a", 1, 1.0, 100, true).Scored,
		syntheticRecord("full", "task-a", 2, 2.0, 100, true).Scored,
		syntheticRecord("full", "task-a", 3, 3.0, 100, false).Scored,
	}, nil)
	s.InDelta(3.0, *summary.CostOfPassUSD.Value, 1e-15, "6 dollars over 2 passes")
}

// Per-rep values differ widely and the reps are listed out of order; only the
// task means pair, so the delta is exactly 10 however reps are labeled.
func (s *benchSuite) TestComparisonPairsByTaskNotByRep() {
	var records []RunRecord
	for _, c := range []struct{ rep, tokens int64 }{{1, 100}, {2, 200}, {3, 300}} {
		records = append(records, syntheticRecord("rampant", "task-a", c.rep, 1, c.tokens, true))
	}
	for _, c := range []struct{ rep, tokens int64 }{{1, 310}, {2, 110}, {3, 210}} {
		records = append(records, syntheticRecord("full", "task-a", c.rep, 1, c.tokens, true))
	}
	row := s.analyze(records).Comparisons["total_billed_tokens"]
	s.Equal(int64(1), row.Tasks)
	s.Equal([]TaskDelta{{Task: "task-a", Treated: 210, Baseline: 200, Delta: 10}}, row.Deltas)
}

// The 2026-09-10 pilot's shape: a large consistent difference over too few
// tasks. An exact test over 3 tasks cannot go below 0.25, and must say so.
func (s *benchSuite) TestThreeTasksCannotEarnAVerdict() {
	a := s.analyze(taskPairs(3, func(int) int64 { return 1000 }, func(i int) int64 { return 2000 + int64(i) }))
	row := a.Comparisons["total_billed_tokens"]
	s.Equal(0.25, *row.P)
	s.Equal(0.25, *row.MinAttainableP)
	s.Equal(verdictInconclusive, row.Verdict)
	s.Contains(row.Reason, "smallest p an exact test can reach is 0.25")
}

func (s *benchSuite) TestReportNeverPrintsAZeroP() {
	text := Report(s.analyze(taskPairs(3, func(int) int64 { return 1000 }, func(i int) int64 { return 2000 + int64(i) })))
	s.NotContains(text, "(Holm) 0.000")
	s.NotContains(text, "| 0.000 |")
	s.Contains(text, "p (Holm) 1")
	s.Contains(text, "bottoms out at 0.25")
}

// Eight tasks halve tokens and dollars: p = 2/256 each, Holm across the four
// family members lifts it to 4 * 2/256, still under 0.05.
func (s *benchSuite) TestEightTasksWithALargeDifferenceEarnAVerdict() {
	a := s.analyze(taskPairs(8, func(i int) int64 { return 1000 + 10*int64(i) }, func(i int) int64 { return 2000 + 30*int64(i) }))
	row := a.Comparisons["total_billed_tokens"]
	s.Equal(2.0/256, *row.P)
	s.InDelta(4*2.0/256, *row.PHolm, 1e-15)
	s.Equal(verdictLower, row.Verdict)
	s.Empty(row.Reason)
	s.NotNil(row.MinDetectable)
	s.NotNil(row.EffectSize)
}

func (s *benchSuite) TestASmallDifferenceIsInconclusiveWhateverItsP() {
	a := s.analyze(taskPairs(8, func(i int) int64 { return 980 + int64(i) }, func(i int) int64 { return 1000 + 2*int64(i) }))
	row := a.Comparisons["total_billed_tokens"]
	s.LessOrEqual(*row.PHolm, 0.05)
	s.Equal(verdictInconclusive, row.Verdict)
	s.Contains(row.Reason, "under 10%")
}

func (s *benchSuite) TestHolmCoversOnlyTheHeadlineFamily() {
	a := s.analyze(taskPairs(8, func(i int) int64 { return 1000 + 10*int64(i) }, func(i int) int64 { return 2000 + 30*int64(i) }))
	turns := a.Comparisons["turns"]
	s.Nil(turns.PHolm)
	s.Equal(verdictDescriptive, turns.Verdict)
	s.NotNil(a.Comparisons[passAt1Metric].PHolm, "pass@1 is in the family")
}

func (s *benchSuite) TestConstantDeltasLeaveEffectSizeAndDetectableEffectNil() {
	a := s.analyze(taskPairs(4, func(int) int64 { return 1000 }, func(int) int64 { return 2000 }))
	row := a.Comparisons["total_billed_tokens"]
	s.Nil(row.EffectSize)
	s.Nil(row.MinDetectable)
	s.Equal(-1000.0, *row.CILow, "a zero-spread t interval is the point")
}

func (s *benchSuite) TestComparisonIsRefusedWhenTheModelDiffers() {
	records := taskPairs(8, func(int) int64 { return 1000 }, func(int) int64 { return 2000 })
	records[0].Scored.Model = "claude-sonnet-5"
	a := s.analyze(records)
	s.Contains(a.Refusal, "model differs across the runs (claude-opus-5, claude-sonnet-5)")
	s.Empty(a.Comparisons)
	s.Contains(Report(a), "No comparison: model differs")
}

func (s *benchSuite) TestComparisonIsRefusedWhenTheEffortDiffers() {
	records := taskPairs(4, func(int) int64 { return 1000 }, func(int) int64 { return 2000 })
	records[1].Scored.Effort = stringp("high")
	s.Contains(s.analyze(records).Refusal, `effort differs across the runs ("high", unrecorded)`)
}

func (s *benchSuite) TestRefusalIgnoresArmsOutsideTheComparison() {
	records := taskPairs(4, func(int) int64 { return 1000 }, func(int) int64 { return 2000 })
	selftest := syntheticRecord("_selftest", "task-00", 1, 1, 1, true)
	selftest.Scored.Model = "claude-sonnet-5"
	s.Empty(s.analyze(append(records, selftest)).Refusal)
}

func (s *benchSuite) TestAnswersReportPrecisionAndRecall() {
	run := syntheticRecord("full", "task-a", 1, 1, 1, true).Scored
	run.Answer = &answerCounts{TP: 4, FP: 0, FN: 1}
	other := syntheticRecord("full", "task-a", 2, 1, 1, true).Scored
	other.Answer = &answerCounts{TP: 2, FP: 2, FN: 0}
	score := answerScore([]*ScoredRun{run, other})
	s.Equal(int64(2), score.Runs)
	s.InDelta(6.0/8, *score.Precision, 1e-15)
	s.InDelta(6.0/7, *score.Recall, 1e-15)
	s.InDelta(2*(6.0/8)*(6.0/7)/(6.0/8+6.0/7), *score.F1, 1e-15)
}

func (s *benchSuite) TestAnswerPrecisionIsNilWhenNothingWasNamed() {
	run := syntheticRecord("full", "task-a", 1, 1, 1, true).Scored
	run.Answer = &answerCounts{FN: 3}
	score := answerScore([]*ScoredRun{run})
	s.Nil(score.Precision)
	s.Equal(0.0, *score.Recall)
	s.Nil(score.F1)
}

func (s *benchSuite) TestAnswersAreNilWithoutAnInterrogationTask() {
	s.Nil(answerScore([]*ScoredRun{syntheticRecord("full", "task-a", 1, 1, 1, true).Scored}))
}

func (s *benchSuite) TestDescribeDropsNilsAndInterpolatesQuartiles() {
	spread := describe([]*float64{floatp(4), nil, floatp(1), floatp(3), floatp(2)})
	s.Equal(Spread{N: 4, Median: floatp(2.5), IQR: [2]*float64{floatp(1.75), floatp(3.25)}, Mean: floatp(2.5)}, spread)
}

func (s *benchSuite) TestDescribeOfOnlyNilsIsEmpty() {
	s.Zero(describe([]*float64{nil}).N)
}

// An odd count picks a value rather than interpolating, so the median is one of
// the inputs while the quartiles still land between them.
func (s *benchSuite) TestDescribeOddCountPicksTheMiddle() {
	spread := describe([]*float64{floatp(5), floatp(1), floatp(3)})
	s.Equal(Spread{N: 3, Median: floatp(3), IQR: [2]*float64{floatp(2), floatp(4)}, Mean: floatp(3)}, spread)
}

func (s *benchSuite) TestDescribeOfOneValue() {
	spread := describe([]*float64{floatp(7)})
	s.Equal(Spread{N: 1, Median: floatp(7), IQR: [2]*float64{floatp(7), floatp(7)}, Mean: floatp(7)}, spread)
}

// analysisJSON analyzes at seed 42, the seed every determinism check shares.
func (s *benchSuite) analysisJSON(records []RunRecord) string {
	a, err := Analyze(records, 42)
	s.Require().NoError(err)
	raw, err := a.JSON()
	s.Require().NoError(err)
	return string(raw)
}

func (s *benchSuite) TestSameSeedGivesIdenticalOutput() {
	_, _, records := fixtureRecords(s.T())
	s.Equal(s.analysisJSON(records), s.analysisJSON(records))
}

func (s *benchSuite) TestRecordOrderDoesNotChangeTheAnalysis() {
	_, _, records := fixtureRecords(s.T())
	reversed := make([]RunRecord, len(records))
	for i, r := range records {
		reversed[len(records)-1-i] = r
	}
	s.Equal(s.analysisJSON(records), s.analysisJSON(reversed))
}

func (s *benchSuite) TestAnalysisRoundTripsThroughJSON() {
	_, _, records := fixtureRecords(s.T())
	text := s.analysisJSON(records)
	file := filepath.Join(s.T().TempDir(), "analysis.json")
	s.Require().NoError(os.WriteFile(file, []byte(text), 0o644))
	back, err := LoadAnalysis(file)
	s.Require().NoError(err)
	again, err := back.JSON()
	s.Require().NoError(err)
	s.Equal(text, string(again))
	s.Equal(Report(s.analyzeAt(records, 42)), Report(back), "report differs from memory and from disk")
}

func (s *benchSuite) TestReportCaveatsAreGeneratedFromTheData() {
	_, _, records := fixtureRecords(s.T())
	text := Report(s.analyzeAt(records, 42))
	for _, want := range []string{"cost-of-pass", "pass^k", "null, not zero", fixtureRunID("full", "task-a", 3),
		"test file deleted", "n=3", "precision", "bottoms out at 0.5"} {
		s.Contains(text, want)
	}
}

func (s *benchSuite) analyzeAt(records []RunRecord, seed uint64) *Analysis {
	a, err := Analyze(records, seed)
	s.Require().NoError(err)
	return a
}

// TestPipelineMatchesPinnedOutput pins every stage over the synthetic fixture
// (testdata/fixture-*, seed 20260902), regenerated when the arithmetic changes
// on purpose. analysis.json compares to a tolerance, its floats carrying
// platform-dependent last bits; metrics.jsonl and report.md are exact.
func (s *benchSuite) TestPipelineMatchesPinnedOutput() {
	t := s.T()
	_, _, records := fixtureRecords(t)
	metrics, err := RecordsJSONL(records)
	s.Require().NoError(err)
	assertSameBytes(t, "metrics.jsonl", metrics)
	metricsFile := filepath.Join(t.TempDir(), "metrics.jsonl")
	s.Require().NoError(os.WriteFile(metricsFile, metrics, 0o644))
	loaded, err := LoadRecords(metricsFile)
	s.Require().NoError(err)
	a := s.analyzeAt(loaded, 20260902)
	// The real platform is whatever regenerated the fixture, so pinning its
	// bytes would fail everywhere else. Its EFFECT is what the pin has to
	// tolerate, which assertSameNumbers does.
	a.Platform = "fixture"
	raw, err := a.JSON()
	s.Require().NoError(err)
	assertSameNumbers(t, "analysis.json", raw)
	// The report rounds to a fixed number of digits, so it is byte-stable
	// across platforms even where analysis.json is not.
	assertSameBytes(t, "report.md", []byte(Report(a)))
}

// assertSameNumbers is assertSameBytes for a document whose floats carry
// platform-dependent last bits: same shape, same keys, every number equal to
// within a relative 1e-12. An exact byte pin here would be red on any machine
// but the one that wrote it (see Analysis.Platform).
func assertSameNumbers(t *testing.T, name string, got []byte) {
	t.Helper()
	file := filepath.Join("testdata", "fixture-"+name)
	if os.Getenv("UPDATE_BENCH_FIXTURES") != "" {
		if err := os.WriteFile(file, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", file)
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var gotDoc, wantDoc any
	if err := json.Unmarshal(got, &gotDoc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantDoc); err != nil {
		t.Fatal(err)
	}
	compareJSON(t, name, gotDoc, wantDoc)
}

func compareJSON(t *testing.T, path string, got, want any) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("%s: got %T, want object", path, got)
		}
		for k := range w {
			if _, ok := g[k]; !ok {
				t.Fatalf("%s/%s: missing from the analysis", path, k)
			}
		}
		for k := range g {
			if _, ok := w[k]; !ok {
				t.Fatalf("%s/%s: absent from the pinned output", path, k)
			}
			compareJSON(t, fmt.Sprintf("%s/%s", path, k), g[k], w[k])
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			t.Fatalf("%s: got %T, want array", path, got)
		}
		if len(g) != len(w) {
			t.Fatalf("%s: length %d, pinned %d", path, len(g), len(w))
		}
		for i := range w {
			compareJSON(t, fmt.Sprintf("%s[%d]", path, i), g[i], w[i])
		}
	case float64:
		g, ok := got.(float64)
		if !ok {
			t.Fatalf("%s: got %T, want number", path, got)
		}
		if !almost(g, w, 12) && (w == 0 || math.Abs(g-w)/math.Abs(w) > 1e-12) {
			t.Fatalf("%s: %v, pinned %v", path, g, w)
		}
	default:
		if got != want {
			t.Fatalf("%s: %v, pinned %v", path, got, want)
		}
	}
}

// UPDATE_BENCH_FIXTURES rewrites the pinned bytes instead of comparing against
// them, for the case the pin exists to make visible: arithmetic or formatting
// that changed on purpose. Run it, then READ the diff; a fixture updated without
// anyone looking is a pin that has stopped pinning.
//
// An env var rather than a test flag, like UPDATE_MAGUS_API_LOCK: `magus run
// test` appends forwarded args after the package list, where a flag the go
// command does not know is dropped before any test binary sees it.
func assertSameBytes(t *testing.T, name string, got []byte) {
	t.Helper()
	file := filepath.Join("testdata", "fixture-"+name)
	if os.Getenv("UPDATE_BENCH_FIXTURES") != "" {
		if err := os.WriteFile(file, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", file)
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, want) {
		return
	}
	gotLines, wantLines := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	for i := range max(len(gotLines), len(wantLines)) {
		var g, w string
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("%s differs from the pinned output at line %d:\n got %s\nwant %s", name, i+1, g, w)
		}
	}
	t.Fatalf("%s differs from the pinned output in length only", name)
}
