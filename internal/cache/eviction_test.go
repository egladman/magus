package cache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/codec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runForProject runs c against a freshly created project at root/<projectPath>/
// whose single output file contains outContent. Two projects sharing
// outContent will land their outputs at the same CAS blob (content-addressed),
// which is the setup the shared-blob eviction tests rely on.
func runForProject(t *testing.T, c *Cache, root, projectPath, outContent string) {
	t.Helper()
	abs := filepath.Join(root, projectPath)
	require.NoError(t, os.MkdirAll(abs, 0o755), "mkdir %s", abs)
	src := filepath.Join(abs, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package "+filepath.Base(projectPath)), 0o644), "write source")
	out := filepath.Join(abs, "out.txt")
	step := Step{
		ProjectPath:   projectPath,
		Sources:       []string{filepath.Join(projectPath, "*.go")},
		Outputs:       []string{filepath.Join(projectPath, "out.txt")},
		WorkspaceRoot: root,
	}
	_, err := c.Run(context.Background(), step, func(_ context.Context) error {
		return os.WriteFile(out, []byte(outContent), 0o644)
	})
	require.NoError(t, err, "Run %s", projectPath)
}

func listManifests(t *testing.T, cdir string) []Manifest {
	t.Helper()
	var ms []Manifest
	md := filepath.Join(cdir, "manifests")
	_ = filepath.WalkDir(md, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		data, rerr := os.ReadFile(p)
		require.NoError(t, rerr, "read %s", p)
		var m Manifest
		require.NoError(t, codec.Unmarshal(data, &m), "unmarshal %s", p)
		ms = append(ms, m)
		return nil
	})
	return ms
}

func countBlobs(t *testing.T, cdir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(filepath.Join(cdir, "cas"), func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		return nil
	})
	return n
}

// assertReferencedBlobsExist checks the central invariant: every blob
// referenced by a surviving manifest must still exist on disk. This is
// the property that evictLRU and Prune used to violate by deleting
// shared blobs along with the older manifest.
func assertReferencedBlobsExist(t *testing.T, cdir string, manifests []Manifest) {
	t.Helper()
	casDir := filepath.Join(cdir, "cas")
	for _, m := range manifests {
		for _, out := range m.Outputs {
			if out.Blob == "" {
				continue
			}
			bp := filepath.Join(casDir, out.Blob[:2], out.Blob)
			_, err := os.Stat(bp)
			require.NoErrorf(t, err, "surviving manifest references missing blob %s", out.Blob)
		}
	}
}

// TestEvictLRU_SharedBlobsSurvive: two manifests reference the same blob
// (identical output content). Eviction drops the older one. The shared
// blob must survive because the newer manifest still references it.
func TestEvictLRU_SharedBlobsSurvive(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	t.Setenv("MAGUS_CACHE_MODE", "write")
	c, err := Open(cdir)
	require.NoError(t, err, "Open")

	runForProject(t, c, root, "a", "shared-content")
	// Distinct CreatedAt: evictLRU sorts by it, and a same-nanosecond
	// pair would make the test order-dependent on map iteration.
	time.Sleep(5 * time.Millisecond)
	runForProject(t, c, root, "b", "shared-content")

	assert.Equal(t, 1, countBlobs(t, cdir), "setup: want 1 shared blob in CAS")
	require.Len(t, listManifests(t, cdir), 2, "setup: want 2 manifests")

	// Cap to (total - 1): guarantees one eviction (the loop requires total > limit)
	// but stops after removing the oldest manifest file because subtracting its
	// size (≥ 1 byte) brings total to ≤ limit. The shared blob size is NOT
	// subtracted — blobRefs for the shared blob drops to 1, not 0 — so
	// eviction halts after one manifest, leaving the blob and newer manifest intact.
	total, _ := c.scanManifests()
	c.evictLRU(context.Background(), total-1)

	surviving := listManifests(t, cdir)
	require.NotEmpty(t, surviving, "evictLRU removed every manifest; cap was too aggressive for the test")
	assertReferencedBlobsExist(t, cdir, surviving)
}

// TestPrune_SharedBlobsSurvive: same scenario, exercised through Prune
// instead of evictLRU.
func TestPrune_SharedBlobsSurvive(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	t.Setenv("MAGUS_CACHE_MODE", "write")
	c, err := Open(cdir)
	require.NoError(t, err, "Open")

	runForProject(t, c, root, "a", "shared-content")
	cutoff := time.Now()
	time.Sleep(5 * time.Millisecond)
	runForProject(t, c, root, "b", "shared-content")

	assert.Equal(t, 1, countBlobs(t, cdir), "setup: want 1 shared blob in CAS")

	n, _, err := c.Prune(context.Background(), cutoff, false)
	require.NoError(t, err, "Prune")
	assert.Equal(t, 1, n, "Prune: want 1 entry removed")

	surviving := listManifests(t, cdir)
	require.Len(t, surviving, 1, "want 1 surviving manifest")
	assertReferencedBlobsExist(t, cdir, surviving)
}

// TestEvictLRU_OrphanBlobsCollected: when no surviving manifest references
// an evicted entry's blobs, those blobs must be removed (gcBlobs runs at
// the end of evictLRU). This is the non-shared-blob case — it confirms
// the fix doesn't over-retain.
func TestEvictLRU_OrphanBlobsCollected(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	t.Setenv("MAGUS_CACHE_MODE", "write")
	c, err := Open(cdir)
	require.NoError(t, err, "Open")

	runForProject(t, c, root, "a", "content-a")
	time.Sleep(5 * time.Millisecond)
	runForProject(t, c, root, "b", "content-b")

	assert.Equal(t, 2, countBlobs(t, cdir), "setup: want 2 distinct blobs")

	// Cap to a tiny non-zero value: evictLRU will evict every manifest
	// since the cache is much larger than 1 byte.
	c.evictLRU(context.Background(), 1)

	assert.Empty(t, listManifests(t, cdir), "want 0 surviving manifests")
	assert.Equal(t, 0, countBlobs(t, cdir), "want all blobs gc'd")
}
