package job

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// The records-swept log is read by its last line and compacted to one line once it is due.
func TestRecordsSweptKeepsItsLastPass(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	path, err := s.Path()
	require.NoError(t, err)
	rows := []types.Job{{ID: "a"}}
	passes := int64(2 + file.CompactSlack + 1)
	for at := range passes {
		s.writeRecordsSwept(at, rows)
	}
	read, err := file.ReadLines(filepath.Join(filepath.Dir(path), recordsSweptFile))
	require.NoError(t, err)
	assert.Len(t, read.Lines, 1, "the pass that made the log due rewrote it")
	assert.True(t, s.recordsSwept(passes-1, rows))
	assert.False(t, s.recordsSwept(passes-1, nil), "a row the last pass saw has gone")
}
