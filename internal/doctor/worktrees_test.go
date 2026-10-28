package doctor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkWorktree creates a checkout directory under .claude/worktrees. When live, it
// gets the .git link file git writes into a real worktree; when not, it is the
// pruned-but-not-deleted leftover this check exists to find.
func mkWorktree(t *testing.T, root, name string, live bool) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "worktrees", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	if live {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /somewhere\n"), 0o600))
	}
}

func TestCheckStaleWorktrees(t *testing.T) {
	t.Run("no worktrees dir is fine", func(t *testing.T) {
		c := checkStaleWorktrees(t.TempDir())
		assert.Equal(t, types.CheckOK, c.Status)
	})

	t.Run("live worktrees pass", func(t *testing.T) {
		root := t.TempDir()
		mkWorktree(t, root, "feature-a", true)
		mkWorktree(t, root, "feature-b", true)
		assert.Equal(t, types.CheckOK, checkStaleWorktrees(root).Status)
	})

	// The case that actually happened: `git worktree prune` clears git's registry but
	// leaves the directory, so a second copy of every spell source stays in the tree.
	t.Run("pruned but undeleted directory is caught", func(t *testing.T) {
		root := t.TempDir()
		mkWorktree(t, root, "live-one", true)
		mkWorktree(t, root, "graph-explorer-viz-8008f6", false)

		c := checkStaleWorktrees(root)
		require.Equal(t, types.CheckFail, c.Status)
		assert.Contains(t, c.Message, "MGS1002", "the message must name what it breaks, not just that it is stale")
		assert.Contains(t, c.Details, filepath.Join(".claude/worktrees", "graph-explorer-viz-8008f6"))
		assert.NotContains(t, c.Details, filepath.Join(".claude/worktrees", "live-one"))
		assert.Contains(t, c.Details[len(c.Details)-1], "git worktree add", "a finding without a remedy is a complaint")
	})

	t.Run("files are not mistaken for worktrees", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".claude", "worktrees"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, ".claude", "worktrees", "README"), []byte("x"), 0o600))
		assert.Equal(t, types.CheckOK, checkStaleWorktrees(root).Status)
	})
}

const gib = int64(1) << 30

func sampleAt(hoursAgo float64, freeGiB int64, action string) types.DiskSample {
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC).Add(-time.Duration(hoursAgo * float64(time.Hour)))
	return types.DiskSample{At: float64(at.UnixMilli()), Path: "/w", TotalBytes: 500 * gib, FreeBytes: freeGiB * gib, Action: action}
}

func TestDiskTrendWithNoSamplesSaysWhereTheyComeFrom(t *testing.T) {
	got := diskTrend(nil, config.Defaults().Disk)
	assert.Equal(t, types.CheckOK, got.Status)
	assert.Contains(t, got.Message, "no disk samples yet")
	assert.Contains(t, got.Message, "disk.min_free_mb 51200 (the default)")
}

func TestDiskTrendIsAdviceBelowTheFloor(t *testing.T) {
	got := diskTrend([]types.DiskSample{sampleAt(2, 40, "sample")}, config.Defaults().Disk)
	assert.Equal(t, types.CheckAdvice, got.Status)
	assert.Contains(t, got.Message, "40960 MB free of 512000 MB on /w")
}

func TestDiskTrendIsAdviceWhenFreeSpaceFallsFast(t *testing.T) {
	cfg := config.Defaults().Disk
	cfg.GrowthMBPerHour = 1024
	got := diskTrend([]types.DiskSample{sampleAt(48, 400, "sample"), sampleAt(4, 300, "sample"), sampleAt(0, 290, "sample")}, cfg)
	assert.Equal(t, types.CheckAdvice, got.Status, "10 GiB in 4h is past 1 GiB an hour; the 48h-old sample is outside the day")
	assert.Contains(t, got.Message, "changed -2560 MB an hour over 4.0h")
	assert.Contains(t, got.Message, "disk.growth_mb_per_hour 1024")
	assert.NotContains(t, got.Message, "1024 (the default)", "a set threshold is not called the default")
}

func TestDiskTrendNamesTheLastReclaimChunks(t *testing.T) {
	done := sampleAt(1, 300, "reclaimed")
	done.Detail = "removed 2 worktrees"
	got := diskTrend([]types.DiskSample{sampleAt(2, 300, "sample"), done}, config.Defaults().Disk)
	assert.Equal(t, types.CheckOK, got.Status)
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "removed 2 worktrees")
}

func TestReadDiskSamplesNamesAMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"at":1,"path":"/w","totalBytes":2,"freeBytes":1,"action":"sample","detail":""}`+"\nnot json\n"), 0o644))
	_, err := readDiskSamples(path)
	assert.ErrorContains(t, err, "line 2")
}
