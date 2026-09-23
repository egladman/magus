package bench

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/pricing"
)

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
	return RunRecord{Control: &ControlRun{
		RunID: "rampant-task-a-r1-" + kind, Arm: "rampant", Task: "task-a", Rep: 1, Model: "claude-opus-5",
		Control: kind, CheckExit: int64p(map[bool]int64{true: 0, false: 1}[success]), Success: boolp(success),
	}}
}

func TestBenchControlsMustDiscriminate(t *testing.T) {
	scored := []RunRecord{
		syntheticRecord("rampant", "task-a", 1, 1.0, 100, true),
		syntheticRecord("full", "task-a", 1, 1.0, 100, true),
	}
	goldenOK := controlRecord(controlGolden, true)
	nullFails := controlRecord(controlNull, false)
	nullPasses := controlRecord(controlNull, true)
	good, err := Analyze(append(append([]RunRecord{}, scored...), goldenOK, nullFails), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !good.Controls["task-a"].Discriminates || good.Runs != 2 {
		t.Errorf("good: discriminates=%v runs=%d (controls are not scored runs)", good.Controls["task-a"].Discriminates, good.Runs)
	}
	lax, err := Analyze(append(append([]RunRecord{}, scored...), goldenOK, nullPasses), 1)
	if err != nil {
		t.Fatal(err)
	}
	if lax.Controls["task-a"].Discriminates {
		t.Error("a passing null control must not discriminate")
	}
	unverified, err := Analyze(scored, 1)
	if err != nil {
		t.Fatal(err)
	}
	if unverified.Controls["task-a"].Discriminates || unverified.Controls["task-a"].Golden.N != 0 {
		t.Errorf("unverified: %+v", unverified.Controls["task-a"])
	}
}

func TestBenchWilsonInterval(t *testing.T) {
	// The bounds are the Wilson score interval to full precision, so a
	// reordered operation shows up as a last-bit difference here.
	cases := []struct {
		successes, total int64
		low, high        float64
	}{
		{0, 10, 0.0, 0.2775327998628892},
		{5, 10, 0.236593090512564, 0.7634069094874361},
		{10, 10, 0.7224672001371107, 0.9999999999999999},
		{3, 3, 0.4385029682449546, 1.0},
	}
	for _, c := range cases {
		got := wilsonInterval(c.successes, c.total)
		if *got[0] != c.low || *got[1] != c.high {
			t.Errorf("wilson(%d, %d) = [%v, %v], want [%v, %v]", c.successes, c.total, *got[0], *got[1], c.low, c.high)
		}
	}
	if got := wilsonInterval(0, 0); got[0] != nil || got[1] != nil {
		t.Errorf("wilson(0, 0) = %v", got)
	}
}

func TestBenchCostOfPass(t *testing.T) {
	var failing []*ScoredRun
	for rep := int64(1); rep <= 3; rep++ {
		failing = append(failing, syntheticRecord("rampant", "task-a", rep, 0.5, 100, false).Scored)
	}
	summary := armSummary(failing)
	if summary.PassRate != 0.0 || !summary.CostOfPassUSD.Infinite || summary.CostOfPassUSD.Value != nil {
		t.Errorf("zero pass rate: %+v", summary.CostOfPassUSD)
	}
	mixed := []*ScoredRun{
		syntheticRecord("full", "task-a", 1, 1.0, 100, true).Scored,
		syntheticRecord("full", "task-a", 2, 2.0, 100, true).Scored,
		syntheticRecord("full", "task-a", 3, 3.0, 100, false).Scored,
	}
	summary = armSummary(mixed)
	if *summary.Dollars.Mean != 2.0 || !almost(summary.PassRate, 2.0/3.0, 7) || !almost(*summary.CostOfPassUSD.Value, 3.0, 7) {
		t.Errorf("mean=%v rate=%v cost=%v", *summary.Dollars.Mean, summary.PassRate, *summary.CostOfPassUSD.Value)
	}
}

func pairedRow(t *testing.T, records []RunRecord, seed int64, metric string) PairedDelta {
	t.Helper()
	a, err := Analyze(records, seed)
	if err != nil {
		t.Fatal(err)
	}
	return a.Paired[metric]["task-a"]
}

func TestBenchDeltasPairRepIWithRepI(t *testing.T) {
	// Per-rep values differ widely but each pair differs by exactly 10, so a
	// pairing that ignored the rep field would not produce a zero-width CI.
	var records []RunRecord
	for _, c := range []struct{ rep, base int64 }{{1, 100}, {2, 200}, {3, 300}} {
		records = append(records, syntheticRecord("rampant", "task-a", c.rep, float64(c.base)/100.0, c.base, true))
	}
	for _, c := range []struct{ rep, base int64 }{{3, 310}, {1, 110}, {2, 210}} {
		records = append(records, syntheticRecord("full", "task-a", c.rep, float64(c.base)/100.0, c.base, true))
	}
	row := pairedRow(t, records, 7, "total_billed_tokens")
	if row.NPairs != 3 || *row.DeltaMean != 10.0 || *row.DeltaMedian != 10.0 || *row.CILow != 10.0 || *row.CIHigh != 10.0 {
		t.Errorf("got %+v", row)
	}
}

func TestBenchVerdictNeedsBothACleanCIAndATenPercentDelta(t *testing.T) {
	var records []RunRecord
	for _, c := range []struct{ rep, base int64 }{{1, 1000}, {2, 1010}, {3, 1020}} {
		records = append(records, syntheticRecord("rampant", "task-a", c.rep, 1.0, c.base, true))
		records = append(records, syntheticRecord("full", "task-a", c.rep, 1.0, c.base-20, true))
	}
	row := pairedRow(t, records, 7, "total_billed_tokens")
	if !row.CIExcludesZero || math.Abs(*row.Relative) >= minRelativeDelta || row.Verdict != verdictInconclusive {
		t.Errorf("got %+v", row)
	}
}

func TestBenchVerdictFiresWhenTheDeltaIsLargeAndClean(t *testing.T) {
	var records []RunRecord
	for _, c := range []struct{ rep, base int64 }{{1, 1000}, {2, 1100}, {3, 1200}} {
		records = append(records, syntheticRecord("rampant", "task-a", c.rep, 1.0, c.base, true))
		records = append(records, syntheticRecord("full", "task-a", c.rep, 0.5, c.base/2, true))
	}
	if row := pairedRow(t, records, 7, "total_billed_tokens"); row.Verdict != verdictLower {
		t.Errorf("got %+v", row)
	}
}

func TestBenchHolm(t *testing.T) {
	adjusted := holm(map[string]*float64{"task-a": floatp(0.01), "task-b": floatp(0.04), "task-c": nil})
	if !almost(*adjusted["task-a"], 0.02, 7) || !almost(*adjusted["task-b"], 0.04, 7) || adjusted["task-c"] != nil {
		t.Errorf("got a=%v b=%v c=%v", *adjusted["task-a"], *adjusted["task-b"], adjusted["task-c"])
	}
	monotone := holm(map[string]*float64{"a": floatp(0.03), "b": floatp(0.031), "c": floatp(0.032)})
	if *monotone["a"] > *monotone["b"] || *monotone["b"] > *monotone["c"] {
		t.Errorf("not monotone: %v %v %v", *monotone["a"], *monotone["b"], *monotone["c"])
	}
	if *monotone["a"] != 0.09 {
		t.Errorf("a = %v, want 0.09 (3 * 0.03, the smallest p at family size 3)", *monotone["a"])
	}
}

func TestBenchDescribe(t *testing.T) {
	spread := describe([]*float64{floatp(4), nil, floatp(1), floatp(3), floatp(2)})
	if spread.N != 4 || *spread.Median != 2.5 || *spread.IQR[0] != 1.75 || *spread.IQR[1] != 3.25 || *spread.Mean != 2.5 {
		t.Errorf("got %+v", spread)
	}
	if describe([]*float64{nil}).N != 0 {
		t.Error("all-nil input must describe as empty")
	}
	// An odd count picks a value rather than interpolating, so the median is one
	// of the inputs while the quartiles still land between them.
	ints := describe([]*float64{floatp(5), floatp(1), floatp(3)})
	if *ints.Median != 3 || *ints.IQR[0] != 2 || *ints.IQR[1] != 4 || *ints.Mean != 3 {
		t.Errorf("ints: %+v", ints)
	}
	one := describe([]*float64{floatp(7)})
	if *one.Median != 7 || *one.IQR[0] != 7 || *one.IQR[1] != 7 || *one.Mean != 7 {
		t.Errorf("single: %+v", one)
	}
}

// analysisJSON analyzes at seed 42, the seed every determinism check shares.
func analysisJSON(t *testing.T, records []RunRecord) string {
	t.Helper()
	a, err := Analyze(records, 42)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := a.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestBenchDeterminism(t *testing.T) {
	_, _, records := fixtureRecords(t)
	t.Run("same seed gives identical output", func(t *testing.T) {
		first := analysisJSON(t, records)
		second := analysisJSON(t, records)
		if first != second {
			t.Error("two runs with one seed differ")
		}
	})
	t.Run("record order does not change the analysis", func(t *testing.T) {
		reversed := make([]RunRecord, len(records))
		for i, r := range records {
			reversed[len(records)-1-i] = r
		}
		if analysisJSON(t, records) != analysisJSON(t, reversed) {
			t.Error("reversing the records changed the analysis")
		}
	})
	t.Run("the point estimate does not depend on the seed", func(t *testing.T) {
		one := pairedRow(t, records, 1, "dollars")
		two := pairedRow(t, records, 2, "dollars")
		if *one.DeltaMean != *two.DeltaMean {
			t.Errorf("means differ: %v %v", *one.DeltaMean, *two.DeltaMean)
		}
		for _, row := range []PairedDelta{one, two} {
			if *row.CILow > *row.DeltaMean || *row.DeltaMean > *row.CIHigh {
				t.Errorf("CI [%v, %v] does not cover the mean %v", *row.CILow, *row.CIHigh, *row.DeltaMean)
			}
		}
	})
	t.Run("analysis round trips through json", func(t *testing.T) {
		text := analysisJSON(t, records)
		file := filepath.Join(t.TempDir(), "analysis.json")
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		back, err := LoadAnalysis(file)
		if err != nil {
			t.Fatal(err)
		}
		again, err := back.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != text {
			t.Error("round trip changed the analysis")
		}
		if Report(back) != Report(mustAnalyze(t, records, 42)) {
			t.Error("report differs from memory and from disk")
		}
	})
	t.Run("report caveats are generated from the data", func(t *testing.T) {
		text := Report(mustAnalyze(t, records, 42))
		for _, want := range []string{"cost-of-pass", "pass^k", "null, not zero", fixtureRunID("full", "task-a", 3), "test file deleted", "n=3"} {
			if !strings.Contains(text, want) {
				t.Errorf("report lacks %q", want)
			}
		}
	})
}

func mustAnalyze(t *testing.T, records []RunRecord, seed int64) *Analysis {
	t.Helper()
	a, err := Analyze(records, seed)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestBenchPipelineMatchesPinnedOutput pins every stage's output over the
// synthetic fixture (testdata/fixture-*, seed 20260902) against checked-in bytes.
//
// A regression pin on THIS binary: the fixtures were produced by it and are
// regenerated whenever its arithmetic changes on purpose.
//
// It does NOT cover the resampler. MEASURED 2026-09-20: replacing the RNG
// outright moved no bound in these fixtures, because every paired delta here is
// constant across reps, and resampling identical values returns that value
// whatever the draw order. A draw-sequence regression needs a fixture whose
// deltas vary.
//
// analysis.json is compared to a tolerance rather than byte for byte, because
// its floats carry platform-dependent last bits; metrics.jsonl and report.md
// are exact.
func TestBenchPipelineMatchesPinnedOutput(t *testing.T) {
	_, _, records := fixtureRecords(t)
	metrics, err := RecordsJSONL(records)
	if err != nil {
		t.Fatal(err)
	}
	assertSameBytes(t, "metrics.jsonl", metrics)
	metricsFile := filepath.Join(t.TempDir(), "metrics.jsonl")
	if err := os.WriteFile(metricsFile, metrics, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRecords(metricsFile)
	if err != nil {
		t.Fatal(err)
	}
	a := mustAnalyze(t, loaded, 20260902)
	// The real platform is whatever regenerated the fixture, so pinning its
	// bytes would fail everywhere else. Its EFFECT is what the pin has to
	// tolerate, which assertSameNumbers does.
	a.Platform = "fixture"
	raw, err := a.JSON()
	if err != nil {
		t.Fatal(err)
	}
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
			compareJSON(t, path+"/"+k, g[k], w[k])
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
