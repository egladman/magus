package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/embed/rankeval"
	"github.com/egladman/magus/libs/embed/train/teacher"
)

func TestBenchDenseRunRanksByDotProduct(t *testing.T) {
	queries := []teacher.Text{{ID: "q1"}, {ID: "q2"}}
	docs := []teacher.Text{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "zero"}}
	qv := [][]float32{{1, 0}, {0, 0}}
	dv := [][]float32{{0.6, 0.8}, {1, 0}, {0.6, -0.8}, {0, 0}}
	run := denseRun("dense", queries, qv, docs, dv)
	assert.Equal(t, "dense", run.Tag)
	require.Contains(t, run.Queries, "q1")
	assert.NotContains(t, run.Queries, "q2", "a zero query ranks nothing")
	got := run.Queries["q1"]
	require.Len(t, got, 3, "a zero doc is never ranked")
	assert.Equal(t, "b", got[0].Doc)
	assert.Equal(t, rankeval.Rank([]rankeval.Hit{got[2], got[1]}, 2), got[1:], "a and c tie and go by hash")
}

func TestBenchExportDigest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "queries.jsonl"), []byte("q\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "corpus.jsonl"), []byte("c\n"), 0o644))
	got, err := exportDigest(dir)
	require.NoError(t, err)
	sum := sha256.Sum256([]byte("q\nc\n"))
	assert.Equal(t, hex.EncodeToString(sum[:])[:16], got)
	require.NoError(t, os.Remove(filepath.Join(dir, "corpus.jsonl")))
	_, err = exportDigest(dir)
	assert.Error(t, err)
}

func TestBenchWriteTextsLeavesAnEqualFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queries-prompted.jsonl")
	texts := []teacher.Text{{ID: "q1", Text: benchQueryPrompt + "a <b> & c"}}
	require.NoError(t, writeTexts(path, texts))
	back, err := teacher.ReadTexts(path)
	require.NoError(t, err)
	assert.Equal(t, texts, back)
	before, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, writeTexts(path, texts))
	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime())
}

func TestBenchRefusesAnotherModel(t *testing.T) {
	err := runBench([]string{"-subset", t.TempDir(), "-model", "potion-code-16M-v2"})
	assert.ErrorContains(t, err, "is not CodeRankEmbed")
	assert.ErrorContains(t, runBench([]string{"-model", "CodeRankEmbed"}), "-subset is required")
}
