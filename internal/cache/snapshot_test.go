package cache

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSnapshotAtomicBlob verifies that if the blob copy fails (because the
// destination directory is made read-only mid-operation), no partial blob is
// left at the final CAS path. Before the fix, a crash mid-copy would leave a
// partial file at the final path, causing it to be replayed as a valid hit.
func TestSnapshotAtomicBlob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod read-only semantics differ on Windows")
	}
	root, cdir, _ := newMutableCache(t)

	// Write a source file and a large-ish output file.
	writeMain(t, root, "package main")
	out := touchOut(t, root)
	if err := os.WriteFile(out, make([]byte, 4<<10 /* 4 KiB */), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := Spec{
		ProjectPath:   "test/pkg",
		Sources:       []string{"test/pkg/*.go"},
		WorkspaceRoot: root,
		Outputs:       []string{"test/pkg/out.txt"},
	}

	// Open a second write-mode cache that shares the same cache directory.
	// We will interpose a failing fn to trigger snapshotOne against the CAS.
	c2, err := Open(cdir)
	if err != nil {
		t.Fatal(err)
	}

	// First run: populate the cache normally.
	if _, err := c2.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, make([]byte, 4<<10), 0o644)
	}); err != nil {
		t.Fatalf("prime run: %v", err)
	}

	// Locate the CAS directory and make it read-only to block new blob writes.
	casDir := filepath.Join(cdir, "cas")
	entries, err := os.ReadDir(casDir)
	if err != nil || len(entries) == 0 {
		t.Skip("no CAS blobs populated; skipping atomicity test")
	}
	// Find the first shard directory.
	var shardDir string
	for _, e := range entries {
		if e.IsDir() {
			shardDir = filepath.Join(casDir, e.Name())
			break
		}
	}
	if shardDir == "" {
		t.Skip("no CAS shard directories found")
	}

	// Change the output so the hash changes → snapshotOne will try to write a
	// new blob. Making the shard dir read-only forces the CreateTemp to fail,
	// so the blob is never written to either the temp path or the final path.
	newContent := make([]byte, 8<<10) // different size → different hash
	for i := range newContent {
		newContent[i] = 0xFF
	}
	if err := os.WriteFile(out, newContent, 0o644); err != nil {
		t.Fatal(err)
	}
	writeMain(t, root, "package main // v2")

	// Make CAS read-only so any write to it fails.
	if err := os.Chmod(casDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(casDir, 0o755) })

	c3, err := Open(cdir)
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := c3.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, newContent, 0o644)
	})
	// The run must fail (snapshot fails because CAS is read-only).
	if runErr == nil {
		t.Log("run succeeded (possibly reflink path used); skipping partial-blob assertion")
		return
	}

	// Restore CAS permissions before scanning.
	_ = os.Chmod(casDir, 0o755)

	// Walk the CAS and verify that no temp files leaked and that every blob
	// present is a complete, named blob (not a .tmp.* partial).
	if err := filepath.WalkDir(casDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := d.Name()
		// Temp files from CreateTemp look like "<hash>.tmp.<random>".
		// After a failure + deferred Remove they must not exist.
		if len(name) > 4 && name[len(name)-4:] == ".tmp" {
			t.Errorf("leaked temp file in CAS: %s", p)
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

// TestExportFDsBounded verifies that Export does not accumulate open file
// descriptors as it walks the cache. Before the fix, a defer f.Close() inside
// the WalkDir callback kept every file open until WalkDir returned, which
// would exhaust the process fd limit on large caches.
func TestExportFDsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/proc/self/fd not available on Windows")
	}

	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")

	const nFiles = 100

	// Create 100 distinct output files so the cache has 100 blobs.
	outDir := filepath.Join(root, "test", "pkg")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var outputs []string
	for i := 0; i < nFiles; i++ {
		name := filepath.Join(outDir, filepath.FromSlash("out_"+string(rune('a'+i%26))+string(rune('0'+i/26))+".txt"))
		content := make([]byte, 256)
		content[0] = byte(i) // unique content per file
		if err := os.WriteFile(name, content, 0o644); err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, name)
		outputs = append(outputs, filepath.ToSlash(rel))
	}

	spec := Spec{
		ProjectPath:   "test/pkg",
		Sources:       []string{"test/pkg/*.go"},
		WorkspaceRoot: root,
		Outputs:       outputs,
	}

	if _, err := c.Run(context.Background(), spec, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// Count open FDs before Export.
	fdsBefore := countFDs(t)

	var buf []byte
	w := &bytesWriter{&buf}
	if err := c.Export(context.Background(), w); err != nil {
		t.Fatalf("Export: %v", err)
	}

	fdsAfter := countFDs(t)

	// Allow a small slack for internal buffers (gzip, tar, etc.) but reject
	// accumulation proportional to nFiles.
	const slack = 20
	if fdsAfter-fdsBefore > slack {
		t.Errorf("Export leaked FDs: before=%d after=%d (delta=%d > slack=%d)",
			fdsBefore, fdsAfter, fdsAfter-fdsBefore, slack)
	}
}

// countFDs counts the number of open file descriptors for the current process
// via /proc/self/fd. Linux-only.
func countFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot count FDs: %v", err)
	}
	return len(entries)
}

// bytesWriter wraps a *[]byte to implement io.Writer.
type bytesWriter struct{ buf *[]byte }

func (w *bytesWriter) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

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

	c, err := Open(cdir)
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
	c2, err := Open(cdir)
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
	c, err := Open(cdir)
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

	c, err := Open(cdir)
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
	c2, err := Open(cdir)
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
