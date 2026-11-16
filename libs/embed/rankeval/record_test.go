package rankeval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func sampleRecord(system string, value float64) EvalRecord {
	return EvalRecord{System: system, TableDigest: "abc", Set: "csn-go", Metric: "ndcg@10",
		Value: value, Low: value - 0.01, High: value + 0.01, Queries: 100, Export: "0123456789abcdef"}
}

func TestEvalRecordRoundTripLastRowWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evals.jsonl")
	for _, rec := range []EvalRecord{sampleRecord("bm25", 0.30), sampleRecord("potion", 0.40), sampleRecord("bm25", 0.35)} {
		require.NoError(t, AppendEvalRecord(path, rec))
	}
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	got, err := ReadEvalRecords(f)
	require.NoError(t, err)
	require.Equal(t, []EvalRecord{sampleRecord("bm25", 0.35), sampleRecord("potion", 0.40)}, got)
}

func TestClusterRatioIntervalFollowsTheClusters(t *testing.T) {
	rate, low, high, trials, err := ClusterRatio([]Cluster{{100, 10}, {100, 10}, {100, 10}})
	require.NoError(t, err)
	require.InDelta(t, 0.1, rate, 1e-12)
	require.Equal(t, 300, trials)
	require.InDelta(t, 0.1, low, 1e-12, "identical clusters leave no spread")
	require.InDelta(t, 0.1, high, 1e-12)

	rate, low, high, _, err = ClusterRatio([]Cluster{{100, 0}, {100, 40}})
	require.NoError(t, err)
	require.InDelta(t, 0.2, rate, 1e-12)
	require.Less(t, low, rate)
	require.Greater(t, high, rate)
	require.GreaterOrEqual(t, low, 0.0)
	require.LessOrEqual(t, high, 1.0)

	_, _, _, _, err = ClusterRatio(nil)
	require.Error(t, err, "no trials means no rate")
}

func TestEvalRecordRefusesBadRows(t *testing.T) {
	dir := t.TempDir()
	require.Error(t, AppendEvalRecord(filepath.Join(dir, "a.jsonl"), sampleRecord("", 0.3)))
	outside := sampleRecord("x", 0.3)
	outside.High = 0.2
	require.Error(t, AppendEvalRecord(filepath.Join(dir, "b.jsonl"), outside))

	_, err := ReadEvalRecords(strings.NewReader(`{"system":"x","bogus":1}` + "\n"))
	require.ErrorContains(t, err, "eval records line 1")
}
