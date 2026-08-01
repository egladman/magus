package magus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheOperationsWithoutOpenCache(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	m, err := Inspect(context.Background(), root)
	require.NoError(t, err)
	workspace := m.(*Magus)

	workspace.LogScope("test", "unit")
	workspace.LogCharms("rw")
	_, _, err = workspace.PruneCache(context.Background(), time.Now(), false)
	assert.ErrorIs(t, err, types.ErrNoCache)
	assert.ErrorIs(t, workspace.PruneRemoteCache(context.Background(), time.Hour, 1, false), types.ErrNoCache)
	assert.ErrorIs(t, workspace.ExportCache(context.Background(), &strings.Builder{}), types.ErrNoCache)
	assert.ErrorIs(t, workspace.ImportCache(context.Background(), strings.NewReader("")), types.ErrNoCache)
	_, err = workspace.TailLog(".", "")
	assert.ErrorIs(t, err, types.ErrNoCache)

	stats := workspace.CacheStats()
	collector, enabled := workspace.MetricsCollector()
	metrics, err := workspace.MetricsSnapshot(context.Background())
	require.NoError(t, err)
	assert.Nil(t, collector)
	assert.False(t, enabled)
	assert.Equal(t, struct {
		Stats     cache.Stats
		DiskBytes int64
		Metrics   []byte
		CacheDir  string
		Workspace string
	}{
		Stats:     cache.Stats{},
		Metrics:   nil,
		CacheDir:  workspace.CacheDir(),
		Workspace: workspace.Root(),
	}, struct {
		Stats     cache.Stats
		DiskBytes int64
		Metrics   []byte
		CacheDir  string
		Workspace string
	}{
		Stats:     stats,
		DiskBytes: workspace.CacheDiskBytes(),
		Metrics:   metrics,
		CacheDir:  workspace.CacheDir(),
		Workspace: workspace.Root(),
	})
}

func TestResolveCacheDir_DoesNotNeedWorkspaceLoad(t *testing.T) {
	root := t.TempDir()
	// No magusfile is needed: this deliberately resolves only config/cache placement, not projects.
	got, err := ResolveCacheDir(root)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, ".magus"), got)

	got, err = ResolveCacheDir(root, WithLoadedConfig(config.Config{Cache: config.Cache{Dir: "cache"}}))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "cache"), got)
}
