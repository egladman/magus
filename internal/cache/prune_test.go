package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openMutableCache(t *testing.T) *Cache {
	t.Helper()
	c, err := Open(t.Context(), t.TempDir(), WithMutable(true))
	require.NoError(t, err)
	return c
}

func TestPrune_EmptyCacheIsNoop(t *testing.T) {
	c := openMutableCache(t)
	n, freed, err := c.Prune(context.Background(), time.Now(), false)
	require.NoError(t, err)
	assert.Zero(t, n, "Prune on empty cache: n should be 0")
	assert.Zero(t, freed, "Prune on empty cache: freed should be 0")
}

func TestPrune_DryRun_NothingDeleted(t *testing.T) {
	c := openMutableCache(t)

	// Populate one entry with a no-op function.
	step := Step{
		WorkspaceRoot: t.TempDir(),
		ProjectPath:   "api/",
		Target:        "build",
	}
	_, err := c.Run(context.Background(), step, func(ctx context.Context) error { return nil })
	require.NoError(t, err)

	n, freed, err := c.Prune(context.Background(), time.Now().Add(time.Hour), true)
	require.NoError(t, err)
	assert.NotZero(t, n, "dry-run Prune: expected to count at least one entry")
	_ = freed // non-zero because at least manifest exists

	// Real prune after dry-run: should also remove entries.
	n2, _, err := c.Prune(context.Background(), time.Now().Add(time.Hour), false)
	require.NoError(t, err)
	assert.NotZero(t, n2, "real Prune: expected to remove at least one entry")
}

// TestPrune_SharedBlobNotDoubleCounted verifies Prune's freed-bytes count only
// credits a blob when gcBlobs will actually remove it. Two projects that
// produce byte-identical output content share one CAS blob (content-addressed
// storage dedupes it). Pruning the entry created before cutoff must not count
// that shared blob's bytes: the surviving (newer) manifest still references
// it, so gcBlobs keeps it on disk - counting it as freed overstates what was
// actually reclaimed. freed is checked against an exact expectation (its own
// manifest.json plus its own recorded run output, neither of them shared)
// rather than a loose bound, since freed legitimately includes bytes larger
// than the blob itself.
func TestPrune_SharedBlobNotDoubleCounted(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	c, err := Open(t.Context(), cdir)
	require.NoError(t, err, "Open")

	runForProject(t, c, root, "a", "shared-content")
	cutoff := time.Now()
	time.Sleep(5 * time.Millisecond)
	runForProject(t, c, root, "b", "shared-content")

	assert.Equal(t, 1, countBlobs(t, cdir), "setup: want 1 shared blob in CAS")
	_, entries := c.scanManifests()
	require.Len(t, entries, 2, "setup: want 2 manifests")

	// The pre-cutoff entry is project "a"'s.
	var entryA *manifestEntry
	for i, e := range entries {
		if e.createdAt.Before(cutoff) {
			entryA = &entries[i]
		}
	}
	require.NotNil(t, entryA, "setup: one entry must predate cutoff")
	require.Len(t, entryA.blobs, 1)
	blob := entryA.blobs[0]

	manifestInfo, statErr := os.Stat(entryA.manifestPath)
	require.NoError(t, statErr)
	wantFreed := manifestInfo.Size() + c.outputsSizeForKey(entryA.hash)

	n, freed, err := c.Prune(context.Background(), cutoff, false)
	require.NoError(t, err)
	require.Equal(t, 1, n, "only the pre-cutoff entry (project a) must be pruned")

	// The blob must survive: project b's manifest still references it.
	_, statErr = os.Stat(c.blobPath(blob))
	require.NoError(t, statErr, "shared blob must survive pruning since project b's manifest still references it")

	assert.Equal(t, wantFreed, freed,
		"freed must equal only the pruned entry's own manifest + outputs, excluding the surviving shared blob")
}
