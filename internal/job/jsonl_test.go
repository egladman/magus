package job

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadLinesKeepsLineNumbersAndFlagsAnUnfinishedLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.jsonl")
	got, err := readLines(path)
	require.NoError(t, err)
	assert.Equal(t, jsonLines{}, got, "an absent file has no lines")

	require.NoError(t, os.WriteFile(path, []byte("{\"a\":1}\n\n{\"b\":2}\n{\"c\""), 0o644))
	got, err = readLines(path)
	require.NoError(t, err)
	assert.Equal(t, jsonLines{
		lines:   []numberedLine{{n: 1, text: []byte(`{"a":1}`)}, {n: 3, text: []byte(`{"b":2}`)}},
		partial: true,
	}, got)
}

func TestAppendAndRewriteLines(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dir", "x.jsonl")
	require.NoError(t, appendLines(path, [][]byte{[]byte("one"), []byte("two")}))
	require.NoError(t, appendLines(path, [][]byte{[]byte("three")}))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\nthree\n", string(raw))

	require.NoError(t, rewriteLines(path, [][]byte{[]byte("only")}))
	raw, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "only\n", string(raw))
}

// A probe history appends what changed, and is rewritten to the entries still kept when one
// is dropped, so a row no longer probed leaves the file.
func TestLandingProbesAppendAndRewriteOnADrop(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	probes := filepath.Join(filepath.Dir(path), landingProbesFile)

	s.writeLandingProbes(s.readLandingProbes(), map[string]landingProbe{"a": {At: 1, Updated: 1}, "b": {At: 1, Updated: 1}})
	s.writeLandingProbes(s.readLandingProbes(), map[string]landingProbe{"a": {At: 2, Updated: 1}, "b": {At: 1, Updated: 1}})
	got := s.readLandingProbes()
	assert.Equal(t, map[string]landingProbe{"a": {At: 2, Updated: 1}, "b": {At: 1, Updated: 1}}, got.probes)
	assert.Equal(t, 3, got.lines, "the changed entry was appended")

	s.writeLandingProbes(got, map[string]landingProbe{"b": {At: 1, Updated: 1}})
	raw, err := os.ReadFile(probes)
	require.NoError(t, err)
	assert.Equal(t, `{"id":"b","at":1,"updated":1}`+"\n", string(raw))
}

// The records-swept log is read by its last line and rewritten to one line once it holds
// recordsSweptKept passes.
func TestRecordsSweptKeepsItsLastPass(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	rows := []types.Job{{ID: "a"}}
	for at := range int64(recordsSweptKept + 1) {
		s.writeRecordsSwept(at, rows)
	}
	read, err := readLines(filepath.Join(filepath.Dir(path), recordsSweptFile))
	require.NoError(t, err)
	assert.Len(t, read.lines, 1, "the pass past the bound rewrote the log")
	assert.True(t, s.recordsSwept(recordsSweptKept, rows))
	assert.False(t, s.recordsSwept(recordsSweptKept, nil), "a row the last pass saw has gone")
}
