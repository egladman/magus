package bench

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
)

// fixture is the extracted fixture runs by id, with the two first-rep task-a runs most
// cases read.
func (s *benchSuite) fixture() (results string, byID map[string]RunRecord, fullA1, rampantA1 *ScoredRun) {
	t := s.T()
	results, byID, _ = fixtureRecords(t)
	s.Require().Len(byID, 12)
	return results, byID, scoredRecord(t, byID, "full", "task-a", 1), scoredRecord(t, byID, "rampant", "task-a", 1)
}

func (s *benchSuite) TestExtractSumsTokenCounters() {
	_, _, fullA1, rampantA1 := s.fixture()
	s.Equal(TokenCounts{Input: 4000, Output: 800, CacheRead: 32000, CacheWrite: 4800, TotalBilled: 41600}, fullA1.Tokens)
	s.Equal(TokenCounts{Input: 10500, Output: 2100, CacheRead: 84000, CacheWrite: 10500, TotalBilled: 107100}, rampantA1.Tokens)
}

func (s *benchSuite) TestExtractPricesFlatCacheWritesAtTheFiveMinuteRate() {
	_, _, fullA1, _ := s.fixture()
	s.True(fullA1.CacheWriteTTLAssumed)
	s.True(almost(fullA1.Dollars, 0.086, 9), "dollars = %v", fullA1.Dollars)
}

func (s *benchSuite) TestExtractPricesSplitCacheWritesAtEachTTL() {
	_, byID, _, _ := s.fixture()
	r := scoredRecord(s.T(), byID, "full", "task-b", 1)
	s.False(r.CacheWriteTTLAssumed)
	s.Equal(int64(4800), r.Tokens.CacheWrite)
	s.True(almost(r.Dollars, 0.092, 9), "dollars = %v", r.Dollars)
}

func (s *benchSuite) TestExtractCountsToolsAndReads() {
	_, _, fullA1, _ := s.fixture()
	want := *fullA1 // identity, pricing, guard and timing fields are pinned by the cases around this one
	want.Turns, want.ToolCalls = 4, 8
	want.FileReads, want.DistinctFilesRead = 4, 4
	want.ReReadRate, want.ToolResultBytes = 0.0, 4000
	want.ToolCallsByName = map[string]int64{"Bash": 4, "Read": 4}
	s.Equal(want, *fullA1)
}

func (s *benchSuite) TestExtractMeasuresTheReReadRate() {
	_, _, _, rampantA1 := s.fixture()
	s.True(almost(rampantA1.ReReadRate, 4.0/7.0, 7), "re-read rate = %v", rampantA1.ReReadRate)
	s.Equal(int64(7), rampantA1.FileReads)
	s.Equal(int64(3), rampantA1.DistinctFilesRead)
}

func (s *benchSuite) TestExtractTellsZeroGuardEventsFromAbsentOnes() {
	_, byID, fullA1, rampantA1 := s.fixture()
	s.Equal(&GuardEvents{Denials: 2, Advisories: 1, SkillLoads: 2}, fullA1.GuardEvents)
	s.Equal(&GuardEvents{}, rampantA1.GuardEvents)
	s.Nil(scoredRecord(s.T(), byID, "full", "task-a", 3).GuardEvents, "a run with no trail carries no guard events")
}

func (s *benchSuite) TestExtractReadsSuccessFromCheckExit() {
	_, byID, _, _ := s.fixture()
	failing := scoredRecord(s.T(), byID, "rampant", "task-b", 2)
	s.Require().NotNil(failing.Success)
	s.False(*failing.Success)
	s.Require().NotNil(failing.CheckExit)
	s.Equal(int64(1), *failing.CheckExit)
	passing := scoredRecord(s.T(), byID, "full", "task-b", 2)
	s.Require().NotNil(passing.Success)
	s.True(*passing.Success)
}

func (s *benchSuite) TestExtractCallsAMissingCheckUnknown() {
	results, byID, _, _ := s.fixture()
	dst := filepath.Join(s.T().TempDir(), "run")
	s.Require().NoError(os.CopyFS(dst, os.DirFS(filepath.Join(results, fixtureRunID("full", "task-a", 2)))))
	s.Require().NoError(os.Remove(filepath.Join(dst, "check.exit")))
	r, err := extractRun(dst, loadTestPricing(s.T()))
	s.Require().NoError(err)
	want := *scoredRecord(s.T(), byID, "full", "task-a", 2)
	want.Success, want.CheckExit = nil, nil
	s.Equal(want, *r.Scored, "only the verdict differs from the same run with check.exit")
}

func (s *benchSuite) TestExtractReportsADeletedTestFile() {
	_, _, fullA1, rampantA1 := s.fixture()
	s.Equal([]string{"src/app.test.ts"}, rampantA1.InvariantViolations.TestsDeleted)
	s.Empty(fullA1.InvariantViolations.TestsDeleted)
}

func (s *benchSuite) TestExtractCarriesTiming() {
	_, _, _, rampantA1 := s.fixture()
	s.Equal([]float64{241000, 60500, 239000}, []float64{*rampantA1.WallMs, *rampantA1.TimeToFirstEditMs, *rampantA1.TimeToDoneMs})
}

func (s *benchSuite) TestMetricsRowsRoundTripThroughJSON() {
	_, byID, _, _ := s.fixture()
	for _, r := range byID {
		line, err := r.JSON()
		s.Require().NoError(err)
		back, err := runFromJSON(line)
		s.Require().NoError(err)
		again, err := back.JSON()
		s.Require().NoError(err)
		s.True(bytes.Equal(line, again), "round trip changed %s:\n%s\n%s", r.ID(), line, again)
	}
}

func (s *benchSuite) TestScoredRowCarriesANullControlKey() {
	_, _, fullA1, _ := s.fixture()
	line, err := RunRecord{Scored: fullA1}.JSON()
	s.Require().NoError(err)
	s.Contains(string(line), `"control":null`)
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

var initRecord = map[string]any{"type": "system", "subtype": "init", "model": "claude-opus-5"}

func (s *benchSuite) TestExtractRefusesAnAssistantRecordWithoutUsage() {
	d := newDefectRun(s.T())
	d.writeTranscript(initRecord,
		map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "content": []any{map[string]any{"type": "text", "text": "hi"}}}},
		map[string]any{"type": "result", "subtype": "success"})
	d.mustFail("usage")
}

func (s *benchSuite) TestExtractRefusesUsageWithoutTokenCounters() {
	d := newDefectRun(s.T())
	d.writeTranscript(map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "usage": map[string]any{"output_tokens": 5}, "content": []any{}}})
	d.mustFail("input_tokens")
}

func (s *benchSuite) TestExtractTakesOutputTokensFromTheResultRecord() {
	d := newDefectRun(s.T())
	usage := map[string]any{"input_tokens": 10, "output_tokens": 2, "cache_read_input_tokens": 100}
	d.writeTranscript(initRecord,
		map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "usage": usage, "content": []any{}}},
		map[string]any{"type": "assistant", "message": map[string]any{"id": "m2", "usage": usage, "content": []any{}}},
		map[string]any{"type": "result", "subtype": "success", "usage": map[string]any{"input_tokens": 20, "output_tokens": 900, "cache_read_input_tokens": 200}})
	s.Equal(TokenCounts{Input: 20, Output: 900, CacheRead: 200, TotalBilled: 1120}, d.mustScore().Tokens)
}

// The host's total_cost_usd is its own client-side estimate, priced from a table
// compiled into its binary that has no entry for these models. It is recorded for
// comparison and never substituted for the list-price total.
func (s *benchSuite) TestExtractPricesFromTheTableWhenTheHostReportsACost() {
	d := newDefectRun(s.T())
	usage := map[string]any{"input_tokens": 1000, "output_tokens": 100}
	d.writeTranscript(initRecord,
		map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "usage": usage, "content": []any{}}},
		map[string]any{"type": "result", "subtype": "success", "total_cost_usd": 0.05, "usage": usage})
	r := d.mustScore()
	s.Positive(r.TableDollarsUSD)
	s.InDelta(r.TableDollarsUSD, r.Dollars, 0)
	s.NotEqual(0.05, r.Dollars)
	s.Require().NotNil(r.ReportedCostUSD)
	s.InDelta(0.05, *r.ReportedCostUSD, 0)
}

func (s *benchSuite) TestExtractRefusesATranscriptWithNoAssistantRecords() {
	d := newDefectRun(s.T())
	d.writeTranscript(initRecord)
	d.mustFail("no assistant records")
}

func (s *benchSuite) TestExtractRefusesAnUnpricedModel() {
	d := newDefectRun(s.T())
	d.writeSingleTurn("some-unlisted-model", map[string]any{"input_tokens": 10, "output_tokens": 2})
	d.mustFail("pricing table")
}

func (s *benchSuite) TestExtractPricesADatedModelIDAsItsAlias() {
	d := newDefectRun(s.T())
	d.writeSingleTurn("claude-opus-5-20260301", map[string]any{"input_tokens": 1_000_000, "output_tokens": 0})
	r := d.mustScore()
	rates, err := loadTestPricing(s.T()).Lookup("claude-opus-5")
	s.Require().NoError(err)
	s.True(almost(r.Dollars, rates.Input, 7), "dollars = %v, want %v", r.Dollars, rates.Input)
}

func (s *benchSuite) TestExtractRefusesADatedIDWithNoAlias() {
	d := newDefectRun(s.T())
	d.writeSingleTurn("some-unlisted-model-20260301", map[string]any{"input_tokens": 10, "output_tokens": 2})
	d.mustFail("pricing table")
}

func (s *benchSuite) TestExtractRefusesAScoredRunWithoutATranscript() {
	newDefectRun(s.T()).mustFail("transcript.jsonl is missing")
}

// Neither control writes a transcript, so the run must be recognized from meta.json
// alone rather than from what is missing beside it.
func (s *benchSuite) TestExtractCarriesAControlVerdictAndNoTokens() {
	d := newDefectRun(s.T())
	meta := d.readMeta()
	meta["control"] = "golden"
	meta["exit_reason"] = "control_golden_ok"
	d.writeMeta(meta)
	s.Require().NoError(os.WriteFile(filepath.Join(d.dir, "check.exit"), []byte("0\n"), 0o644))
	r, err := d.extract()
	s.Require().NoError(err)
	s.Require().NotNil(r.Control)
	s.Equal(controlGolden, r.Control.Control)
	s.Require().NotNil(r.Control.Success)
	s.True(*r.Control.Success)
	line, err := r.JSON()
	s.Require().NoError(err)
	s.NotContains(string(line), `"tokens"`)
}

func (s *benchSuite) TestExtractRefusesAnUnknownControlKind() {
	d := newDefectRun(s.T())
	meta := d.readMeta()
	meta["control"] = "placebo"
	d.writeMeta(meta)
	d.mustFail("placebo")
}

func (s *benchSuite) TestExtractReadsAnswerCountsForAnInterrogationTask() {
	_, byID, _ := fixtureRecords(s.T())
	s.Equal(&AnswerCounts{TP: 4, FP: 0, FN: 1}, scoredRecord(s.T(), byID, "full", answerTask, 1).Answer)
	s.Equal(&AnswerCounts{TP: 3, FP: 2, FN: 2}, scoredRecord(s.T(), byID, "rampant", answerTask, 2).Answer)
}

func (s *benchSuite) TestExtractLeavesAnswerNilWithoutAnswerJSON() {
	_, byID, _ := fixtureRecords(s.T())
	s.Nil(scoredRecord(s.T(), byID, "full", "task-a", 1).Answer, "no answer.json is no answer, not a zero score")
}

func (s *benchSuite) TestExtractRefusesANegativeAnswerCount() {
	d := newDefectRun(s.T())
	d.writeSingleTurn("claude-opus-5", map[string]any{"input_tokens": 1, "output_tokens": 1})
	s.Require().NoError(os.WriteFile(filepath.Join(d.dir, "answer.json"), []byte(`{"tp":1,"fp":-1,"fn":0}`), 0o644))
	d.mustFail("negative count")
}
