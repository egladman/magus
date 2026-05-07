package cache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/codec"
)

// runForProject runs c against a freshly created project at root/<projectPath>/
// whose single output file contains outContent. Two projects sharing
// outContent will land their outputs at the same CAS blob (content-addressed),
// which is the setup the shared-blob eviction tests rely on.
func runForProject(t *testing.T, c *Cache, root, projectPath, outContent string) {
	t.Helper()
	abs := filepath.Join(root, projectPath)
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", abs, err)
	}
	src := filepath.Join(abs, "main.go")
	if err := os.WriteFile(src, []byte("package "+filepath.Base(projectPath)), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	out := filepath.Join(abs, "out.txt")
	spec := Spec{
		ProjectPath:   projectPath,
		Sources:       []string{filepath.Join(projectPath, "*.go")},
		Outputs:       []string{filepath.Join(projectPath, "out.txt")},
		WorkspaceRoot: root,
	}
	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte(outContent), 0o644)
	}); err != nil {
		t.Fatalf("Run %s: %v", projectPath, err)
	}
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
		if rerr != nil {
			t.Fatalf("read %s: %v", p, rerr)
		}
		var m Manifest
		if jerr := codec.Unmarshal(data, &m); jerr != nil {
			t.Fatalf("unmarshal %s: %v", p, jerr)
		}
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
			if _, err := os.Stat(bp); err != nil {
				t.Fatalf("surviving manifest references missing blob %s: %v", out.Blob, err)
			}
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
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	runForProject(t, c, root, "a", "shared-content")
	// Distinct CreatedAt: evictLRU sorts by it, and a same-nanosecond
	// pair would make the test order-dependent on map iteration.
	time.Sleep(5 * time.Millisecond)
	runForProject(t, c, root, "b", "shared-content")

	if got := countBlobs(t, cdir); got != 1 {
		t.Fatalf("setup: want 1 shared blob in CAS, got %d", got)
	}
	if got := len(listManifests(t, cdir)); got != 2 {
		t.Fatalf("setup: want 2 manifests, got %d", got)
	}

	// Cap to (total - 1): guarantees one eviction (the loop requires total > limit)
	// but stops after removing the oldest manifest file because subtracting its
	// size (≥ 1 byte) brings total to ≤ limit. The shared blob size is NOT
	// subtracted — blobRefs for the shared blob drops to 1, not 0 — so
	// eviction halts after one manifest, leaving the blob and newer manifest intact.
	total, _ := c.scanManifests()
	c.evictLRU(context.Background(), total-1)

	surviving := listManifests(t, cdir)
	if len(surviving) == 0 {
		t.Fatal("evictLRU removed every manifest; cap was too aggressive for the test")
	}
	assertReferencedBlobsExist(t, cdir, surviving)
}

// TestPrune_SharedBlobsSurvive: same scenario, exercised through Prune
// instead of evictLRU.
func TestPrune_SharedBlobsSurvive(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	t.Setenv("MAGUS_CACHE_MODE", "write")
	c, err := Open(cdir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	runForProject(t, c, root, "a", "shared-content")
	cutoff := time.Now()
	time.Sleep(5 * time.Millisecond)
	runForProject(t, c, root, "b", "shared-content")

	if got := countBlobs(t, cdir); got != 1 {
		t.Fatalf("setup: want 1 shared blob in CAS, got %d", got)
	}

	n, _, err := c.Prune(context.Background(), cutoff, false)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("Prune: want 1 entry removed, got %d", n)
	}

	surviving := listManifests(t, cdir)
	if len(surviving) != 1 {
		t.Fatalf("want 1 surviving manifest, got %d", len(surviving))
	}
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
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	runForProject(t, c, root, "a", "content-a")
	time.Sleep(5 * time.Millisecond)
	runForProject(t, c, root, "b", "content-b")

	if got := countBlobs(t, cdir); got != 2 {
		t.Fatalf("setup: want 2 distinct blobs, got %d", got)
	}

	// Cap to a tiny non-zero value: evictLRU will evict every manifest
	// since the cache is much larger than 1 byte.
	c.evictLRU(context.Background(), 1)

	if got := len(listManifests(t, cdir)); got != 0 {
		t.Fatalf("want 0 surviving manifests, got %d", got)
	}
	if got := countBlobs(t, cdir); got != 0 {
		t.Fatalf("want all blobs gc'd, %d still present", got)
	}
}
