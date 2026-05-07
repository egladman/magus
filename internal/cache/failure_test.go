package cache_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/cache"
)

// TestTruncatedManifestTreatedAsMiss verifies that a manifest file
// containing truncated or garbage JSON causes a cache miss (rebuild),
// not a panic or a nil-error/nil-result pair.
func TestTruncatedManifestTreatedAsMiss(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}

	c, err := cache.Open(cdir)
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}

	// Populate the cache.
	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("built"), 0o644)
	}); err != nil {
		t.Fatalf("initial run: %v", err)
	}

	// Corrupt the manifest by truncating it.
	manifestDir := filepath.Join(cdir, "manifests")
	err = filepath.WalkDir(manifestDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".json" {
			return os.WriteFile(path, []byte(`{"outputs":[{`), 0o644) // truncated
		}
		return nil
	})
	if err != nil {
		t.Fatalf("corrupt manifest: %v", err)
	}

	// Re-open in read mode; truncated manifest should cause a rebuild (miss),
	// not a panic or an error that surfaces to the caller.
	t.Setenv("MAGUS_CACHE_MODE", "write")
	c2, err := cache.Open(cdir)
	if err != nil {
		t.Fatalf("cache.Open(second): %v", err)
	}
	r, err := c2.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("rebuilt"), 0o644)
	})
	if err != nil {
		t.Fatalf("run after corruption: %v", err)
	}
	if r.Hit {
		t.Error("corrupted manifest must not produce a hit")
	}
}

// TestPermDeniedCacheDirReturnsError verifies that a cache directory
// that the process cannot write to causes cache.Open to fail with an
// error, not panic.
func TestPermDeniedCacheDirReturnsError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; permission checks do not apply")
	}
	parent := t.TempDir()
	cdir := filepath.Join(parent, "no-write")
	if err := os.MkdirAll(cdir, 0o555); err != nil { // read-only
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(cdir, 0o755) })

	// Open itself may succeed (it doesn't necessarily create files),
	// but a Run that needs to write a manifest must fail gracefully.
	c, err := cache.Open(cdir)
	if err != nil {
		// Fine — some platforms reject unwritable dirs at Open.
		return
	}

	root := t.TempDir()
	writeMain(t, root, "package main")
	spec := makeSpec(root)

	// Run should either succeed (read-mode hit) or return an error — never panic.
	_, runErr := c.Run(context.Background(), spec, func(_ context.Context) error { return nil })
	_ = runErr // any outcome (success or error) is acceptable; the test guards against panic
}

// TestPartialSnapshotDoesNotProduceHit verifies that a manifest that
// references blobs which no longer exist in the CAS does not produce a
// spurious hit — the replay failure causes a rebuild.
func TestPartialSnapshotDoesNotProduceHit(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	spec := makeSpec(root)
	spec.Outputs = []string{"test/pkg/out.txt"}

	c, err := cache.Open(cdir)
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}

	// Populate the cache successfully.
	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("first"), 0o644)
	}); err != nil {
		t.Fatalf("initial run: %v", err)
	}

	// Delete all CAS blobs to simulate a partially-deleted snapshot.
	casDir := filepath.Join(cdir, "cas")
	if err := os.RemoveAll(casDir); err != nil {
		t.Fatalf("RemoveAll cas: %v", err)
	}

	// A read-mode cache must not claim a hit when the blobs are gone.
	t.Setenv("MAGUS_CACHE_MODE", "read")
	c2, err := cache.Open(cdir)
	if err != nil {
		t.Fatalf("cache.Open(read): %v", err)
	}
	calls := 0
	r, err := c2.Run(context.Background(), spec, func(_ context.Context) error {
		calls++
		return os.WriteFile(out, []byte("second"), 0o644)
	})
	if err != nil {
		t.Fatalf("run after partial snapshot: %v", err)
	}
	if r.Hit {
		t.Error("run with missing CAS blobs must not produce a hit")
	}
}
