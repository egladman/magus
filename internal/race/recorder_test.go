package race

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecorder_IntervalLifecycle(t *testing.T) {
	r := &recorder{}
	r.startInterval("api", "build")
	r.endInterval("api", "build")

	snap := r.snapshot()
	require.Len(t, snap.intervals, 1)
	iv := snap.intervals[0]
	assert.Equal(t, "api", iv.Project)
	assert.Equal(t, "build", iv.Target)
	assert.False(t, iv.StartedAt.IsZero(), "StartedAt set by startInterval")
	assert.False(t, iv.EndedAt.IsZero(), "EndedAt set by endInterval")
	assert.False(t, iv.EndedAt.Before(iv.StartedAt), "EndedAt should not precede StartedAt")
}

func TestRecorder_SnapshotSealsUnendedIntervals(t *testing.T) {
	r := &recorder{}
	r.startInterval("api", "build") // never ended (e.g. an error path)

	snap := r.snapshot()
	require.Len(t, snap.intervals, 1)
	assert.False(t, snap.intervals[0].EndedAt.IsZero(), "snapshot seals open intervals with now")
}

func TestRecorder_SetWrittenPaths_MatchesMostRecentInterval(t *testing.T) {
	r := &recorder{}
	r.startInterval("api", "build")
	r.setWrittenPaths("api", "build", []string{"/repo/api/out.js"})

	snap := r.snapshot()
	require.Len(t, snap.intervals, 1)
	assert.Equal(t, []string{"/repo/api/out.js"}, snap.intervals[0].WrittenPaths)
}

func TestRecorder_SetWrittenPaths_EmptyIsNoop(t *testing.T) {
	r := &recorder{}
	r.startInterval("api", "build")
	r.setWrittenPaths("api", "build", nil)

	snap := r.snapshot()
	require.Len(t, snap.intervals, 1)
	assert.Nil(t, snap.intervals[0].WrittenPaths)
}

func TestRecorder_SnapshotIsACopy(t *testing.T) {
	r := &recorder{}
	r.startInterval("api", "build")

	snap := r.snapshot()
	r.startInterval("web", "build") // mutate after snapshot

	assert.Len(t, snap.intervals, 1, "prior snapshot must not observe later appends")
}

func TestTrackProject_RecordsWrittenPaths(t *testing.T) {
	out := t.TempDir()
	rt := NewRuntime(t.TempDir())

	written := filepath.Join(out, "artifact.txt")
	err := rt.TrackProject("api", "build", []string{out}, func() error {
		return os.WriteFile(written, []byte("hi"), 0o644)
	})
	require.NoError(t, err)

	assert.Equal(t, map[string][]string{"api": {written}}, rt.WrittenPaths())
}

func TestTrackProject_PropagatesError(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	sentinel := assert.AnError
	err := rt.TrackProject("api", "build", nil, func() error { return sentinel })
	assert.ErrorIs(t, err, sentinel)
}

func TestWrittenPaths_NilRuntimeSafe(t *testing.T) {
	var rt *Runtime
	assert.Nil(t, rt.WrittenPaths())
}

func TestWrittenPaths_EmptyWhenNothingWritten(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	assert.Empty(t, rt.WrittenPaths())
}
