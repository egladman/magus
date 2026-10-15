package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// conformanceWorkspace answers the conformance lens with a fixed output, reached the way the check
// reaches it: by runtime assertion to types.InsightAnalyzer.
type conformanceWorkspace struct {
	hotWorkspace
	out types.ConformanceOutput
	err error
}

func (c conformanceWorkspace) Conformance(context.Context) (types.ConformanceOutput, error) {
	return c.out, c.err
}

var _ types.InsightAnalyzer = conformanceWorkspace{}

const ratchetPath = "hack/policy/conformance.jsonl"

// The file-lines threshold sits on line 3 and the known entry on line 5.
const ratchetRecord = `{"kind":"schema","schema":1}` + "\n" +
	`{"kind":"measured","rev":"abc123","date":"2026-10-14"}` + "\n" +
	`{"kind":"threshold","metric":"file-lines","population":"source","value":2000,"n":1130,"percentile":95,"why":"p95 at abc123"}` + "\n" +
	"\n" +
	`{"kind":"known","metric":"file-lines","reason":"split pending","items":["old.go"]}` + "\n"

var (
	fileLinesSource = types.ConformanceKey{Metric: types.ConformanceFileLines, Population: types.ConformancePopSource}
	funcLinesSource = types.ConformanceKey{Metric: types.ConformanceFuncLines, Population: types.ConformancePopSource}
)

func conformanceRunner(t *testing.T, ws types.WorkspaceReader) *runner {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Dir(ratchetPath)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ratchetPath), []byte(ratchetRecord), 0o644))

	var cfg config.Config
	cfg.Knowledge.Conformance = config.ConformanceConfig{
		Ratchet: ratchetPath,
		Checks: map[types.ConformanceMetric]map[types.ConformancePopulation]config.ConformanceLimitConfig{
			types.ConformanceFileLines: {types.ConformancePopSource: {Percentile: 95, Anchor: 2200}},
			types.ConformanceFuncLines: {types.ConformancePopSource: {Percentile: 99, Anchor: 92}},
		},
	}
	return &runner{opts: options{cfg: cfg}, root: root, ws: ws, ctx: t.Context()}
}

func foundAnswer() types.KnowledgeAnswer { return types.KnowledgeAnswer{Verdict: types.VerdictFound} }

func TestCheckConformanceRatchet(t *testing.T) {
	tests := []struct {
		name     string
		ws       types.WorkspaceReader
		status   types.CheckStatus
		evidence types.Evidence
		details  []string
	}{
		{
			name:     "no analyzer cannot measure",
			ws:       nil,
			status:   types.CheckOK,
			evidence: types.EvidenceUnknown,
			details:  []string{"build the symbol index with `magus graph build`, then run doctor again"},
		},
		{
			name:     "a lens error is a failure naming the cause",
			ws:       conformanceWorkspace{err: errors.New("conformance: hack/policy/conformance.jsonl:3: file-lines source: committed 3000 is looser than its anchor 2200")},
			status:   types.CheckFail,
			evidence: types.EvidenceUnknown,
			details:  []string{"conformance: hack/policy/conformance.jsonl:3: file-lines source: committed 3000 is looser than its anchor 2200"},
		},
		{
			name: "an unknown answer is not a pass",
			ws: conformanceWorkspace{out: types.ConformanceOutput{Answer: types.KnowledgeAnswer{
				Verdict: types.VerdictUnknown, Reason: types.ReasonSymbolIndexMissing,
			}}},
			status:   types.CheckOK,
			evidence: types.EvidenceUnknown,
			details:  []string{"build the symbol index with `magus graph build`, then run doctor again"},
		},
		{
			name: "a ratchet-source proposal names the line, the value, and what is enforced now",
			ws: conformanceWorkspace{out: types.ConformanceOutput{Answer: foundAnswer(), Rows: []types.ConformanceRow{
				{Key: fileLinesSource, N: 1130, Live: 938, Value: 992, Propose: 938, Source: types.ConformanceFromRatchet},
			}}},
			status:   types.CheckAdvice,
			evidence: types.EvidenceMeasured,
			details:  []string{"file-lines source: commit 938 (p95, n=1130) at hack/policy/conformance.jsonl:3, now 992"},
		},
		{
			name: "a ratchet raised past what the code measures is reported as slack on its line",
			ws: conformanceWorkspace{out: types.ConformanceOutput{Answer: foundAnswer(), Rows: []types.ConformanceRow{
				{Key: fileLinesSource, N: 1130, Live: 900, Value: 2000, Propose: 900, Source: types.ConformanceFromRatchet},
			}}},
			status:   types.CheckAdvice,
			evidence: types.EvidenceMeasured,
			details:  []string{"file-lines source: commit 900 (p95, n=1130) at hack/policy/conformance.jsonl:3, now 2000"},
		},
		{
			name: "an anchor-source proposal says to add a threshold row",
			ws: conformanceWorkspace{out: types.ConformanceOutput{Answer: foundAnswer(), Rows: []types.ConformanceRow{
				{Key: funcLinesSource, N: 4000, Live: 60, Value: 92, Propose: 60, Source: types.ConformanceFromAnchor},
			}}},
			status:   types.CheckAdvice,
			evidence: types.EvidenceMeasured,
			details: []string{"func-lines source: commit 60 (p99, n=4000), now 92 from the configured anchor; " +
				"add a threshold line to hack/policy/conformance.jsonl"},
		},
		{
			name: "a stale known entry names its line and the items to delete",
			ws: conformanceWorkspace{out: types.ConformanceOutput{Answer: foundAnswer(), Stale: []types.ConformanceKnown{
				{Metric: types.ConformanceFileLines, Items: []string{"old.go", "older.go"}, Reason: "split pending", Line: 5},
			}}},
			status:   types.CheckAdvice,
			evidence: types.EvidenceMeasured,
			details:  []string{"hack/policy/conformance.jsonl:5: known file-lines entry no longer violates, delete: old.go, older.go"},
		},
		{
			name: "rows with nothing to tighten and no stale entry are quiet",
			ws: conformanceWorkspace{out: types.ConformanceOutput{Answer: foundAnswer(), Rows: []types.ConformanceRow{
				{Key: fileLinesSource, N: 1130, Live: 1990, Value: 2000, Source: types.ConformanceFromRatchet},
			}}},
			status:   types.CheckOK,
			evidence: types.EvidenceMeasured,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := conformanceRunner(t, tc.ws).checkConformanceRatchet(nil)

			assert.Equal(t, "conformance-ratchet", got.Name)
			assert.Equal(t, tc.status, got.Status)
			assert.Equal(t, tc.evidence, got.Evidence)
			assert.Equal(t, tc.details, got.Details)
		})
	}
}

func TestCheckConformanceRatchetNeverReportsMeasuredWhenUnmeasured(t *testing.T) {
	for _, ws := range []types.WorkspaceReader{
		nil,
		conformanceWorkspace{err: errors.New("boom")},
		conformanceWorkspace{out: types.ConformanceOutput{Answer: types.KnowledgeAnswer{Verdict: types.VerdictUnknown}}},
	} {
		got := conformanceRunner(t, ws).checkConformanceRatchet(nil)

		assert.Equal(t, types.EvidenceUnknown, got.Evidence)
	}
}

func TestCheckConformanceRatchetKeepsTheFindingWhenTheRecordIsUnreadable(t *testing.T) {
	r := conformanceRunner(t, conformanceWorkspace{out: types.ConformanceOutput{Answer: foundAnswer(), Rows: []types.ConformanceRow{
		{Key: fileLinesSource, N: 1130, Live: 938, Value: 992, Propose: 938, Source: types.ConformanceFromRatchet},
	}}})
	require.NoError(t, os.Remove(filepath.Join(r.root, ratchetPath)))

	got := r.checkConformanceRatchet(nil)

	assert.Equal(t, types.CheckAdvice, got.Status)
	require.Len(t, got.Details, 2)
	assert.Equal(t, "file-lines source: commit 938 (p95, n=1130) at hack/policy/conformance.jsonl, now 992", got.Details[0])
	assert.Contains(t, got.Details[1], "ratchet lines unavailable")
}

func TestCheckConformanceRatchetNamesALineItCannotRead(t *testing.T) {
	r := conformanceRunner(t, conformanceWorkspace{out: types.ConformanceOutput{Answer: foundAnswer(), Rows: []types.ConformanceRow{
		{Key: fileLinesSource, N: 1130, Live: 938, Value: 992, Propose: 938, Source: types.ConformanceFromRatchet},
	}}})
	require.NoError(t, os.WriteFile(filepath.Join(r.root, ratchetPath), []byte(`{"kind":"schema","schema":1}`+"\n"+`{"kind":"extra"}`+"\n"), 0o644))

	got := r.checkConformanceRatchet(nil)

	require.Len(t, got.Details, 2)
	assert.Contains(t, got.Details[1], `hack/policy/conformance.jsonl:2: kind "extra": want schema, measured, threshold or known`)
}

func TestCheckConformanceRatchetWithNoRatchetConfiguredIsQuiet(t *testing.T) {
	r := conformanceRunner(t, conformanceWorkspace{out: types.ConformanceOutput{Answer: foundAnswer(), Rows: []types.ConformanceRow{
		{Key: fileLinesSource, N: 1130, Live: 2200, Value: 2200, Source: types.ConformanceFromAnchor},
	}}})
	r.opts.cfg.Knowledge.Conformance.Ratchet = ""

	got := r.checkConformanceRatchet(nil)

	assert.Equal(t, types.CheckOK, got.Status)
	assert.Equal(t, types.EvidenceMeasured, got.Evidence)
	assert.Empty(t, got.Details)
}
