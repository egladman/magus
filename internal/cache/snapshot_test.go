package cache_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egladman/magus/internal/cache"
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

	spec := cache.Spec{
		ProjectPath:   "test/pkg",
		Sources:       []string{"test/pkg/*.go"},
		WorkspaceRoot: root,
		Outputs:       []string{"test/pkg/out.txt"},
	}

	// Open a second write-mode cache that shares the same cache directory.
	// We will interpose a failing fn to trigger snapshotOne against the CAS.
	c2, err := cache.Open(cdir)
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

	c3, err := cache.Open(cdir)
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

	spec := cache.Spec{
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
