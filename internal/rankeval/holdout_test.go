package rankeval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHoldoutLedgerRefusesASecondRead(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "holdout-reads.jsonl")
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	first := HoldoutRead{Set: SetJudgedHoldout, Digest: "d1", Candidate: "k2-seed1", At: at}

	prior, err := RecordHoldoutRead(ledger, first)
	require.NoError(t, err)
	assert.Empty(t, prior)

	second := first
	second.Candidate, second.At = "k2-seed2", at.Add(time.Hour)
	prior, err = RecordHoldoutRead(ledger, second)
	require.ErrorIs(t, err, errHoldoutRead)
	assert.ErrorContains(t, err, "judged-holdout d1 was read for k2-seed1 at 2026-10-16T12:00:00Z")
	assert.Equal(t, []HoldoutRead{first}, prior)

	second.ReportOnly = true
	_, err = RecordHoldoutRead(ledger, second)
	require.NoError(t, err, "a report-only read is logged, never refused")

	other := HoldoutRead{Set: SetJudgedHoldout, Digest: "d2", Candidate: "k3", At: at.Add(2 * time.Hour)}
	prior, err = RecordHoldoutRead(ledger, other)
	require.NoError(t, err, "another topic set is another holdout")
	assert.Equal(t, []HoldoutRead{first, second}, prior)

	after, err := RecordHoldoutRead(ledger, first)
	require.ErrorIs(t, err, errHoldoutRead, "a report-only read does not reopen the set")
	assert.Equal(t, []HoldoutRead{first, second, other}, after)

	data, err := os.ReadFile(ledger)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Equal(t, []string{
		`{"eval_set":"judged-holdout","eval_digest":"d1","candidate":"k2-seed1","at":"2026-10-16T12:00:00Z"}`,
		`{"eval_set":"judged-holdout","eval_digest":"d1","candidate":"k2-seed2","at":"2026-10-16T13:00:00Z","report_only":true}`,
		`{"eval_set":"judged-holdout","eval_digest":"d2","candidate":"k3","at":"2026-10-16T14:00:00Z"}`,
	}, lines, "the refused reads appended nothing")
}

func TestHoldoutLedgerRejects(t *testing.T) {
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		read HoldoutRead
		want string
	}{
		"dev set":      {HoldoutRead{Set: SetJudgedDev, Digest: "d", Candidate: "c", At: at}, `holdout read of "judged-dev": only judged-holdout is a holdout`},
		"no digest":    {HoldoutRead{Set: SetJudgedHoldout, Candidate: "c", At: at}, "empty eval_digest"},
		"no candidate": {HoldoutRead{Set: SetJudgedHoldout, Digest: "d", At: at}, "empty candidate"},
		"no time":      {HoldoutRead{Set: SetJudgedHoldout, Digest: "d", Candidate: "c"}, "no time"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ledger := filepath.Join(t.TempDir(), "holdout-reads.jsonl")
			_, err := RecordHoldoutRead(ledger, tc.read)
			require.ErrorContains(t, err, tc.want)
			assert.NoFileExists(t, ledger)
		})
	}
}

func TestHoldoutLedgerNamesABadLine(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "holdout-reads.jsonl")
	good := `{"eval_set":"judged-holdout","eval_digest":"d1","candidate":"c","at":"2026-10-16T12:00:00Z"}`
	require.NoError(t, os.WriteFile(ledger, []byte(good+"\n"+`{"eval_set":"judged-holdout","eval_digest":"d1","candidate":"c","at":"2026-10-16T12:00:00Z","chose":true}`+"\n"), 0o644))

	_, err := RecordHoldoutRead(ledger, HoldoutRead{Set: SetJudgedHoldout, Digest: "d2", Candidate: "c", At: time.Now()})
	require.ErrorContains(t, err, ledger+" line 2")
}
