package bench

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
)

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
		got := []float64{*rampantA1.WallMs, *rampantA1.TimeToFirstEditMs, *rampantA1.TimeToDoneMs}
		if want := []float64{241000, 60500, 239000}; !reflect.DeepEqual(got, want) {
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
		if !strings.Contains(string(line), `"control":null`) {
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
	// The host's total_cost_usd is its own client-side estimate, priced from a
	// table compiled into its binary that has no entry for these models. It is
	// recorded for comparison and never substituted for the list-price total.
	t.Run("the table prices the run even when the host reported a cost", func(t *testing.T) {
		d := newDefectRun(t)
		usage := map[string]any{"input_tokens": 1000, "output_tokens": 100}
		d.writeTranscript(initRec,
			map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "usage": usage, "content": []any{}}},
			map[string]any{"type": "result", "subtype": "success", "total_cost_usd": 0.05, "usage": usage})
		r := d.mustScore()
		if r.Dollars != r.TableDollarsUSD || r.TableDollarsUSD <= 0 || r.Dollars == 0.05 {
			t.Errorf("dollars=%v table=%v", r.Dollars, r.TableDollarsUSD)
		}
		if r.ReportedCostUSD == nil || *r.ReportedCostUSD != 0.05 {
			t.Errorf("reported_cost_usd = %v; the host's estimate is still recorded", r.ReportedCostUSD)
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
		rates, err := loadTestPricing(t).Lookup("claude-opus-5")
		if err != nil {
			t.Fatal(err)
		}
		if want := rates.Input; !almost(r.Dollars, want, 7) {
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
