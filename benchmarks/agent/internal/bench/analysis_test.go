package bench

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egladman/magus/benchmarks/agent/internal/pycompat"
	"github.com/egladman/magus/internal/json"
)

func loadTestPricing(t *testing.T) PriceTable {
	t.Helper()
	pricing, err := LoadPricing(filepath.Join("..", "..", "pricing.json"))
	if err != nil {
		t.Fatal(err)
	}
	return pricing
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

func boolp(b bool) *bool                         { return &b }
func int64p(i int64) *int64                      { return &i }
func floatp(f float64) *float64                  { return &f }
func numberp(n pycompat.Number) *pycompat.Number { return &n }
func almost(a, b, places float64) bool           { return math.Abs(a-b) < math.Pow(10, -places)/2 }

func syntheticRecord(arm, task string, rep int64, dollars float64, tokens int64, success bool) RunRecord {
	return RunRecord{Scored: &ScoredRun{
		RunID: fmt.Sprintf("%s-%s-r%d", arm, task, rep), Arm: arm, Task: task, Rep: rep, Model: "claude-opus-5",
		Dollars: dollars, TableDollarsUSD: dollars,
		Tokens: TokenCounts{Input: tokens, TotalBilled: tokens},
		Turns:  1, ToolCalls: 1, ToolCallsByName: map[string]int64{"Bash": 1},
		FileReads: 1, DistinctFilesRead: 1, ToolResultBytes: 10,
		CheckExit: int64p(map[bool]int64{true: 0, false: 1}[success]), Success: boolp(success),
		WallMs: numberp(pycompat.Int(1000)), TimeToFirstEditMs: numberp(pycompat.Int(100)), TimeToDoneMs: numberp(pycompat.Int(900)),
	}}
}

func controlRecord(kind string, success bool) RunRecord {
	return RunRecord{Control: &ControlRun{
		RunID: "rampant-task-a-r1-" + kind, Arm: "rampant", Task: "task-a", Rep: 1, Model: "claude-opus-5",
		Control: kind, CheckExit: int64p(map[bool]int64{true: 0, false: 1}[success]), Success: boolp(success),
	}}
}

func TestBenchFixtureExtraction(t *testing.T) {
	results, byID, _ := fixtureRecords(t)
	if len(byID) != 12 {
		t.Fatalf("run count = %d, want 12", len(byID))
	}
	fullA1 := scoredRecord(t, byID, "full", "task-a", 1)
	rampantA1 := scoredRecord(t, byID, "rampant", "task-a", 1)

	t.Run("token counters", func(t *testing.T) {
		want := TokenCounts{Input: 4000, Output: 800, CacheRead: 32000, CacheWrite: 4800, TotalBilled: 41600}
		if fullA1.Tokens != want {
			t.Errorf("full tokens = %+v, want %+v", fullA1.Tokens, want)
		}
		want = TokenCounts{Input: 10500, Output: 2100, CacheRead: 84000, CacheWrite: 10500, TotalBilled: 107100}
		if rampantA1.Tokens != want {
			t.Errorf("rampant tokens = %+v, want %+v", rampantA1.Tokens, want)
		}
	})
	t.Run("flat cache writes price at the 5m rate", func(t *testing.T) {
		if !fullA1.CacheWriteTTLAssumed || !almost(fullA1.Dollars, 0.086, 9) {
			t.Errorf("assumed=%v dollars=%v", fullA1.CacheWriteTTLAssumed, fullA1.Dollars)
		}
	})
	t.Run("split cache writes price each ttl", func(t *testing.T) {
		r := scoredRecord(t, byID, "full", "task-b", 1)
		if r.CacheWriteTTLAssumed || r.Tokens.CacheWrite != 4800 || !almost(r.Dollars, 0.092, 9) {
			t.Errorf("assumed=%v cache_write=%d dollars=%v", r.CacheWriteTTLAssumed, r.Tokens.CacheWrite, r.Dollars)
		}
	})
	t.Run("tool and read accounting", func(t *testing.T) {
		if fullA1.Turns != 4 || fullA1.ToolCalls != 8 || fullA1.FileReads != 4 || fullA1.DistinctFilesRead != 4 ||
			fullA1.ReReadRate != 0.0 || fullA1.ToolResultBytes != 4000 ||
			!reflect.DeepEqual(fullA1.ToolCallsByName, map[string]int64{"Bash": 4, "Read": 4}) {
			t.Errorf("got %+v", *fullA1)
		}
		if rampantA1.FileReads != 7 || rampantA1.DistinctFilesRead != 3 || !almost(rampantA1.ReReadRate, 4.0/7.0, 7) {
			t.Errorf("re-read: %+v", *rampantA1)
		}
	})
	t.Run("guard events present and zero differ from absent", func(t *testing.T) {
		if want := (GuardEvents{Denials: 2, Advisories: 1, SkillLoads: 2}); fullA1.GuardEvents == nil || *fullA1.GuardEvents != want {
			t.Errorf("full guard = %+v", fullA1.GuardEvents)
		}
		if want := (GuardEvents{}); rampantA1.GuardEvents == nil || *rampantA1.GuardEvents != want {
			t.Errorf("rampant guard = %+v", rampantA1.GuardEvents)
		}
		if scoredRecord(t, byID, "full", "task-a", 3).GuardEvents != nil {
			t.Error("no-trail run should carry nil guard events")
		}
	})
	t.Run("success comes from check.exit", func(t *testing.T) {
		failing := scoredRecord(t, byID, "rampant", "task-b", 2)
		if failing.Success == nil || *failing.Success || failing.CheckExit == nil || *failing.CheckExit != 1 {
			t.Errorf("failing run: %+v", *failing)
		}
		if passing := scoredRecord(t, byID, "full", "task-b", 2); passing.Success == nil || !*passing.Success {
			t.Errorf("passing run: %+v", *passing)
		}
	})
	t.Run("missing check is unknown, not failure", func(t *testing.T) {
		src := filepath.Join(results, fixtureRunID("full", "task-a", 2))
		dst := filepath.Join(t.TempDir(), "run")
		if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dst, "check.exit")); err != nil {
			t.Fatal(err)
		}
		r, err := extractRun(dst, loadTestPricing(t))
		if err != nil {
			t.Fatal(err)
		}
		if r.Scored.Success != nil || r.Scored.CheckExit != nil {
			t.Errorf("got success=%v check=%v", r.Scored.Success, r.Scored.CheckExit)
		}
	})
	t.Run("deleted test file is an invariant violation", func(t *testing.T) {
		if want := []string{"src/app.test.ts"}; !reflect.DeepEqual(rampantA1.InvariantViolations.TestsDeleted, want) {
			t.Errorf("got %v", rampantA1.InvariantViolations.TestsDeleted)
		}
		if len(fullA1.InvariantViolations.TestsDeleted) != 0 {
			t.Errorf("got %v", fullA1.InvariantViolations.TestsDeleted)
		}
	})
	t.Run("timing is carried through", func(t *testing.T) {
		got := []string{rampantA1.WallMs.String(), rampantA1.TimeToFirstEditMs.String(), rampantA1.TimeToDoneMs.String()}
		if want := []string{"241000", "60500", "239000"}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("metrics rows round trip through json", func(t *testing.T) {
		for _, r := range byID {
			line, err := r.JSON()
			if err != nil {
				t.Fatal(err)
			}
			back, err := runFromJSON(line)
			if err != nil {
				t.Fatal(err)
			}
			again, err := back.JSON()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(line, again) {
				t.Errorf("round trip changed %s:\n%s\n%s", r.ID(), line, again)
			}
		}
	})
	t.Run("scored row carries a null control key", func(t *testing.T) {
		line, err := RunRecord{Scored: fullA1}.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(line), `"control": null`) {
			t.Errorf("no null control key in %s", line)
		}
	})
}

// defectRun is a run directory with only a meta.json, to which each case adds
// the transcript it wants to see rejected or measured.
type defectRun struct {
	t   *testing.T
	dir string
}

func newDefectRun(t *testing.T) defectRun {
	t.Helper()
	d := defectRun{t, filepath.Join(t.TempDir(), "run")}
	if err := os.Mkdir(d.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	d.writeMeta(map[string]any{"run_id": "full-task-a-r1-x", "arm": "full", "task": "task-a", "rep": 1, "model": "claude-opus-5"})
	return d
}

func (d defectRun) writeMeta(meta map[string]any) {
	d.t.Helper()
	raw, err := json.Marshal(meta)
	if err != nil {
		d.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.dir, "meta.json"), raw, 0o644); err != nil {
		d.t.Fatal(err)
	}
}

func (d defectRun) readMeta() map[string]any {
	d.t.Helper()
	raw, err := os.ReadFile(filepath.Join(d.dir, "meta.json"))
	if err != nil {
		d.t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		d.t.Fatal(err)
	}
	return meta
}

func (d defectRun) writeTranscript(records ...any) {
	d.t.Helper()
	var out []byte
	for _, rec := range records {
		raw, err := json.Marshal(rec)
		if err != nil {
			d.t.Fatal(err)
		}
		out = append(append(out, raw...), '\n')
	}
	if err := os.WriteFile(filepath.Join(d.dir, "transcript.jsonl"), out, 0o644); err != nil {
		d.t.Fatal(err)
	}
}

func (d defectRun) writeSingleTurn(model string, usage map[string]any) {
	d.writeTranscript(map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "model": model, "usage": usage, "content": []any{}}})
}

func (d defectRun) extract() (RunRecord, error) {
	return extractRun(d.dir, loadTestPricing(d.t))
}

func (d defectRun) mustFail(want string) {
	d.t.Helper()
	_, err := d.extract()
	if err == nil {
		d.t.Fatalf("want an error mentioning %q, got none", want)
	}
	if !strings.Contains(err.Error(), want) {
		d.t.Fatalf("error %q does not mention %q", err, want)
	}
}

func (d defectRun) mustScore() *ScoredRun {
	d.t.Helper()
	r, err := d.extract()
	if err != nil {
		d.t.Fatal(err)
	}
	if r.Scored == nil {
		d.t.Fatal("want a scored run")
	}
	return r.Scored
}

func TestBenchTranscriptDefects(t *testing.T) {
	initRec := map[string]any{"type": "system", "subtype": "init", "model": "claude-opus-5"}
	t.Run("assistant record without usage", func(t *testing.T) {
		d := newDefectRun(t)
		d.writeTranscript(initRec,
			map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "content": []any{map[string]any{"type": "text", "text": "hi"}}}},
			map[string]any{"type": "result", "subtype": "success"})
		d.mustFail("usage")
	})
	t.Run("usage without token counters", func(t *testing.T) {
		d := newDefectRun(t)
		d.writeTranscript(map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "usage": map[string]any{"output_tokens": 5}, "content": []any{}}})
		d.mustFail("input_tokens")
	})
	t.Run("result record output tokens replace the chunk sum", func(t *testing.T) {
		d := newDefectRun(t)
		usage := map[string]any{"input_tokens": 10, "output_tokens": 2, "cache_read_input_tokens": 100}
		d.writeTranscript(initRec,
			map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "usage": usage, "content": []any{}}},
			map[string]any{"type": "assistant", "message": map[string]any{"id": "m2", "usage": usage, "content": []any{}}},
			map[string]any{"type": "result", "subtype": "success", "usage": map[string]any{"input_tokens": 20, "output_tokens": 900, "cache_read_input_tokens": 200}})
		r := d.mustScore()
		if r.Tokens.Output != 900 || r.Tokens.Input != 20 || r.Tokens.CacheRead != 200 {
			t.Errorf("tokens = %+v; input and cache still come from the turns", r.Tokens)
		}
	})
	t.Run("billed cost beats the table when the host recorded one", func(t *testing.T) {
		d := newDefectRun(t)
		usage := map[string]any{"input_tokens": 1000, "output_tokens": 100}
		d.writeTranscript(initRec,
			map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "usage": usage, "content": []any{}}},
			map[string]any{"type": "result", "subtype": "success", "total_cost_usd": 0.05, "usage": usage})
		r := d.mustScore()
		if r.Dollars != 0.05 || r.TableDollarsUSD <= 0 || r.TableDollarsUSD == 0.05 {
			t.Errorf("dollars=%v table=%v", r.Dollars, r.TableDollarsUSD)
		}
	})
	t.Run("transcript with no assistant records", func(t *testing.T) {
		d := newDefectRun(t)
		d.writeTranscript(initRec)
		d.mustFail("no assistant records")
	})
	t.Run("unpriced model", func(t *testing.T) {
		d := newDefectRun(t)
		d.writeSingleTurn("some-unlisted-model", map[string]any{"input_tokens": 10, "output_tokens": 2})
		d.mustFail("pricing table")
	})
	t.Run("dated model id is priced as its alias", func(t *testing.T) {
		d := newDefectRun(t)
		d.writeSingleTurn("claude-opus-5-20260301", map[string]any{"input_tokens": 1_000_000, "output_tokens": 0})
		r := d.mustScore()
		if want := loadTestPricing(t)["claude-opus-5"].Input; !almost(r.Dollars, want, 7) {
			t.Errorf("dollars = %v, want %v", r.Dollars, want)
		}
	})
	t.Run("dated id with no alias still fails", func(t *testing.T) {
		d := newDefectRun(t)
		d.writeSingleTurn("some-unlisted-model-20260301", map[string]any{"input_tokens": 10, "output_tokens": 2})
		d.mustFail("pricing table")
	})
	t.Run("scored run without a transcript", func(t *testing.T) {
		newDefectRun(t).mustFail("transcript.jsonl is missing")
	})
	t.Run("control run carries its verdict and no tokens", func(t *testing.T) {
		// Neither control writes a transcript, so the run must be recognized
		// from meta.json alone rather than from what is missing beside it.
		d := newDefectRun(t)
		meta := d.readMeta()
		meta["control"] = "golden"
		meta["exit_reason"] = "control_golden_ok"
		d.writeMeta(meta)
		if err := os.WriteFile(filepath.Join(d.dir, "check.exit"), []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r, err := d.extract()
		if err != nil {
			t.Fatal(err)
		}
		if r.Control == nil || r.Control.Control != controlGolden || r.Control.Success == nil || !*r.Control.Success {
			t.Fatalf("got %+v", r)
		}
		line, err := r.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(line), `"tokens"`) {
			t.Errorf("control row carries tokens: %s", line)
		}
	})
	t.Run("unknown control kind", func(t *testing.T) {
		d := newDefectRun(t)
		meta := d.readMeta()
		meta["control"] = "placebo"
		d.writeMeta(meta)
		d.mustFail("placebo")
	})
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
	// The bounds are CPython's own output for the same arithmetic, so a
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
	if row.NPairs != 3 || *row.DeltaMean != 10.0 || row.DeltaMedian.String() != "10" || *row.CILow != 10.0 || *row.CIHigh != 10.0 {
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
		t.Errorf("a = %v, want CPython's 0.09", *monotone["a"])
	}
}

func TestBenchDescribe(t *testing.T) {
	f := func(v float64) *pycompat.Number { return numberp(pycompat.Float(v)) }
	spread := describe([]*pycompat.Number{f(4.0), nil, f(1.0), f(3.0), f(2.0)})
	if spread.N != 4 || spread.Median.String() != "2.5" || spread.IQR[0].String() != "1.75" || spread.IQR[1].String() != "3.25" || *spread.Mean != 2.5 {
		t.Errorf("got %+v", spread)
	}
	if describe([]*pycompat.Number{nil}).N != 0 {
		t.Error("all-nil input must describe as empty")
	}
	// Counts keep their int form where a value is picked rather than
	// interpolated, exactly as the Python's statistics module returned them.
	i := func(v int64) *pycompat.Number { return numberp(pycompat.Int(v)) }
	ints := describe([]*pycompat.Number{i(5), i(1), i(3)})
	if ints.Median.String() != "3" || ints.IQR[0].String() != "2.0" || ints.IQR[1].String() != "4.0" || *ints.Mean != 3.0 {
		t.Errorf("ints: %+v", ints)
	}
	one := describe([]*pycompat.Number{i(7)})
	if one.Median.String() != "7" || one.IQR[0].String() != "7" || one.IQR[1].String() != "7" || *one.Mean != 7.0 {
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
// synthetic fixture (testdata/fixture-*, seed 20260902). The metrics and the
// analysis are what the Python pipeline wrote from the same tree, and their
// bootstrap CIs are the strongest check: they only match if the seeding, the
// draw sequence and the summation order all do. The report is this binary's
// own, regenerated whenever its rendering changes on purpose.
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
	raw, err := a.JSON()
	if err != nil {
		t.Fatal(err)
	}
	assertSameBytes(t, "analysis.json", raw)
	assertSameBytes(t, "report.md", []byte(Report(a)))
}

func assertSameBytes(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", "fixture-"+name))
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
