package race

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlush_WritesEmptyReportWhenNoRaces(t *testing.T) {
	root := t.TempDir()
	rt := NewRuntime(root)
	// A single project with no concurrent peer cannot produce a finding.
	require.NoError(t, rt.TrackProject("api", "build", nil, func() error { return nil }))

	require.NoError(t, rt.Flush(context.Background(), nil))

	data, err := os.ReadFile(filepath.Join(root, ".magus", "cache", "race-report.json"))
	require.NoError(t, err)
	assert.Equal(t, "{\"schema\":3,\"summary\":{\"total\":0},\"findings\":[]}\n", string(data))
}

func TestFlush_CreatesCacheDir(t *testing.T) {
	root := t.TempDir()
	rt := NewRuntime(root)
	require.NoError(t, rt.Flush(context.Background(), nil))

	info, err := os.Stat(filepath.Join(root, ".magus", "cache"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}
