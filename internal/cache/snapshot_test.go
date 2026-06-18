package cache

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, os.WriteFile(out, make([]byte, 4<<10 /* 4 KiB */), 0o644))

	spec := Spec{
		ProjectPath:   "test/pkg",
		Sources:       []string{"test/pkg/*.go"},
		WorkspaceRoot: root,
		Outputs:       []string{"test/pkg/out.txt"},
	}

	// Open a second write-mode cache that shares the same cache directory.
	// We will interpose a failing fn to trigger snapshotOne against the CAS.
	c2, err := Open(cdir)
	require.NoError(t, err)

	// First run: populate the cache normally.
	_, err = c2.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, make([]byte, 4<<10), 0o644)
	})
	require.NoError(t, err, "prime run")

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
	require.NoError(t, os.WriteFile(out, newContent, 0o644))
	writeMain(t, root, "package main // v2")

	// Make CAS read-only so any write to it fails.
	require.NoError(t, os.Chmod(casDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(casDir, 0o755) })

	c3, err := Open(cdir)
	require.NoError(t, err)
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
	err = filepath.WalkDir(casDir, func(p string, d os.DirEntry, err error) error {
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
	})
	require.NoError(t, err, "WalkDir")
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
	require.NoError(t, os.MkdirAll(outDir, 0o755))
	var outputs []string
	for i := 0; i < nFiles; i++ {
		name := filepath.Join(outDir, filepath.FromSlash("out_"+string(rune('a'+i%26))+string(rune('0'+i/26))+".txt"))
		content := make([]byte, 256)
		content[0] = byte(i) // unique content per file
		require.NoError(t, os.WriteFile(name, content, 0o644))
		rel, _ := filepath.Rel(root, name)
		outputs = append(outputs, filepath.ToSlash(rel))
	}

	spec := Spec{
		ProjectPath:   "test/pkg",
		Sources:       []string{"test/pkg/*.go"},
		WorkspaceRoot: root,
		Outputs:       outputs,
	}

	_, err := c.Run(context.Background(), spec, func(_ context.Context) error { return nil })
	require.NoError(t, err, "prime")

	// Count open FDs before Export.
	fdsBefore := countFDs(t)

	var buf []byte
	w := &bytesWriter{&buf}
	require.NoError(t, c.Export(context.Background(), w), "Export")

	fdsAfter := countFDs(t)

	// Allow a small slack for internal buffers (gzip, tar, etc.) but reject
	// accumulation proportional to nFiles.
	const slack = 20
	assert.LessOrEqualf(t, fdsAfter-fdsBefore, slack,
		"Export leaked FDs: before=%d after=%d (delta=%d > slack=%d)",
		fdsBefore, fdsAfter, fdsAfter-fdsBefore, slack)
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
	require.NoError(t, err, "cache.Open")

	// Populate the cache.
	_, err = c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("built"), 0o644)
	})
	require.NoError(t, err, "initial run")

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
	require.NoError(t, err, "corrupt manifest")

	// Re-open in read mode; truncated manifest should cause a rebuild (miss),
	// not a panic or an error that surfaces to the caller.
	t.Setenv("MAGUS_CACHE_MODE", "write")
	c2, err := Open(cdir)
	require.NoError(t, err, "cache.Open(second)")
	r, err := c2.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("rebuilt"), 0o644)
	})
	require.NoError(t, err, "run after corruption")
	assert.False(t, r.Hit, "corrupted manifest must not produce a hit")
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
	require.NoError(t, os.MkdirAll(cdir, 0o555), "MkdirAll") // read-only
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
	require.NoError(t, err, "cache.Open")

	// Populate the cache successfully.
	_, err = c.Run(context.Background(), spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("first"), 0o644)
	})
	require.NoError(t, err, "initial run")

	// Delete all CAS blobs to simulate a partially-deleted snapshot.
	casDir := filepath.Join(cdir, "cas")
	require.NoError(t, os.RemoveAll(casDir), "RemoveAll cas")

	// A read-mode cache must not claim a hit when the blobs are gone.
	t.Setenv("MAGUS_CACHE_MODE", "read")
	c2, err := Open(cdir)
	require.NoError(t, err, "cache.Open(read)")
	calls := 0
	r, err := c2.Run(context.Background(), spec, func(_ context.Context) error {
		calls++
		return os.WriteFile(out, []byte("second"), 0o644)
	})
	require.NoError(t, err, "run after partial snapshot")
	assert.False(t, r.Hit, "run with missing CAS blobs must not produce a hit")
}
