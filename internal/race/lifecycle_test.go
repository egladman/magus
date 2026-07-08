package race

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRuntime_StartTrackFlush exercises the full watcher lifecycle: Start walks
// and watches the tree, TrackProject records an interval while a file is written,
// and Flush closes the watcher and persists a report without error.
func TestRuntime_StartTrackFlush(t *testing.T) {
	root := t.TempDir()
	// A nested, skipped, and hidden dir so addDir's recursion and skip logic run.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg", "sub"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))

	rt := NewRuntime(root)
	require.NoError(t, rt.Start(context.Background()))

	require.NoError(t, rt.TrackProject("pkg", "build", nil, func() error {
		return os.WriteFile(filepath.Join(root, "pkg", "out.txt"), []byte("x"), 0o644)
	}))

	require.NoError(t, rt.Flush(context.Background(), nil))

	// The report exists; a single writer produces no race finding.
	data, err := os.ReadFile(filepath.Join(root, ".magus", "cache", "race-report.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "\"total\":0")
}

func TestRuntime_StartOnMissingRootFails(t *testing.T) {
	rt := NewRuntime(filepath.Join(t.TempDir(), "does-not-exist"))
	assert.Error(t, rt.Start(context.Background()))
}
