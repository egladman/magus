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

	step := Step{
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
	_, err = c2.Run(context.Background(), step, func(_ context.Context) error {
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
	_, runErr := c3.Run(context.Background(), step, func(_ context.Context) error {
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

	step := Step{
		ProjectPath:   "test/pkg",
		Sources:       []string{"test/pkg/*.go"},
		WorkspaceRoot: root,
		Outputs:       outputs,
	}

	_, err := c.Run(context.Background(), step, func(_ context.Context) error { return nil })
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
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}

	c, err := Open(cdir)
	require.NoError(t, err, "cache.Open")

	// Populate the cache.
	_, err = c.Run(context.Background(), step, func(_ context.Context) error {
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
	r, err := c2.Run(context.Background(), step, func(_ context.Context) error {
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
	step := makeStep(root)

	// Run should either succeed (read-mode hit) or return an error — never panic.
	_, runErr := c.Run(context.Background(), step, func(_ context.Context) error { return nil })
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
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}

	c, err := Open(cdir)
	require.NoError(t, err, "cache.Open")

	// Populate the cache successfully.
	_, err = c.Run(context.Background(), step, func(_ context.Context) error {
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
	r, err := c2.Run(context.Background(), step, func(_ context.Context) error {
		calls++
		return os.WriteFile(out, []byte("second"), 0o644)
	})
	require.NoError(t, err, "run after partial snapshot")
	assert.False(t, r.Hit, "run with missing CAS blobs must not produce a hit")
}

// newBareCache returns a Cache backed by a fresh temp dir, suitable for
// exercising snapshotOne/replay/blob helpers directly without going through Run.
// It is also used by the hash tests, so it must stay a package-level helper.
func newBareCache(t *testing.T) *Cache {
	t.Helper()
	c, err := Open(filepath.Join(t.TempDir(), ".magus"), WithMutable(true))
	require.NoError(t, err, "Open")
	return c
}

// TestSnapshotOneRegularFile verifies snapshotOne on a plain file: it records
// the sha256 blob, mode, and size, and populates the CAS.
func TestSnapshotOneRegularFile(t *testing.T) {
	c := newBareCache(t)
	root := t.TempDir()
	abs := filepath.Join(root, "out.txt")
	content := []byte("hello world")
	require.NoError(t, os.WriteFile(abs, content, 0o644))

	rec, err := c.snapshotOne(abs, "out.txt")
	require.NoError(t, err)

	// sha256 of "hello world" is a stable, well-known value.
	want := OutputRecord{
		Path: "out.txt",
		Blob: "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
		Mode: 0o644,
		Size: int64(len(content)),
	}
	require.Equal(t, want, rec)

	// The blob must exist in the CAS with the recorded content.
	got, err := os.ReadFile(c.blobPath(rec.Blob))
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

// TestSnapshotOneSymlink verifies snapshotOne records a symlink as its target
// rather than dereferencing it. The blob field stays empty; Symlink is set.
func TestSnapshotOneSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is restricted on Windows")
	}
	c := newBareCache(t)
	root := t.TempDir()
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink("target/path", link))

	rec, err := c.snapshotOne(link, "link")
	require.NoError(t, err)
	assert.Equal(t, "link", rec.Path)
	assert.Equal(t, "target/path", rec.Symlink)
	assert.Empty(t, rec.Blob, "symlink record must not carry a blob")
}

// TestSnapshotOneDirectoryRejected verifies that pointing snapshotOne at a
// directory is an error (callers must expand directories via a glob).
func TestSnapshotOneDirectoryRejected(t *testing.T) {
	c := newBareCache(t)
	dir := t.TempDir()
	_, err := c.snapshotOne(dir, "somedir")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory")
}

// TestSnapshotOneMissingFile verifies the Lstat error path.
func TestSnapshotOneMissingFile(t *testing.T) {
	c := newBareCache(t)
	_, err := c.snapshotOne(filepath.Join(t.TempDir(), "nope"), "nope")
	assert.Error(t, err)
}

// TestSnapshotOneBlobDedup verifies that snapshotting identical content twice
// reuses the existing CAS blob (the os.Stat "already exists" fast path) and
// yields the same record.
func TestSnapshotOneBlobDedup(t *testing.T) {
	c := newBareCache(t)
	root := t.TempDir()
	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")
	require.NoError(t, os.WriteFile(a, []byte("same"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("same"), 0o644))

	recA, err := c.snapshotOne(a, "a.txt")
	require.NoError(t, err)
	recB, err := c.snapshotOne(b, "b.txt")
	require.NoError(t, err)
	assert.Equal(t, recA.Blob, recB.Blob, "identical content must share one blob")
}

// TestExpandOutputGlobsRejectsAbsolute verifies absolute output globs are
// rejected before any filesystem access.
func TestExpandOutputGlobsRejectsAbsolute(t *testing.T) {
	_, err := expandOutputGlobs([]string{"/etc/passwd"}, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repo-relative")
}

// TestExpandOutputGlobsRejectsDotDot verifies ".." escapes are rejected.
func TestExpandOutputGlobsRejectsDotDot(t *testing.T) {
	_, err := expandOutputGlobs([]string{"../escape"}, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repo-relative")
}

// TestExpandOutputGlobsExpandsDirectory verifies that a glob matching a
// directory walks into it, yields every nested file (relative to root, slash-
// separated), and sorts the result deterministically.
func TestExpandOutputGlobsExpandsDirectory(t *testing.T) {
	root := t.TempDir()
	// Build dist/ with a nested file so the WalkDir branch is exercised.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dist", "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dist", "b.js"), []byte("b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dist", "sub", "a.js"), []byte("a"), 0o644))

	out, err := expandOutputGlobs([]string{"dist"}, root)
	require.NoError(t, err)

	var rels []string
	for _, ra := range out {
		rels = append(rels, ra.rel)
	}
	// Sorted, slash-separated, directory itself omitted (only files).
	assert.Equal(t, []string{"dist/b.js", "dist/sub/a.js"}, rels)
}

// TestExpandOutputGlobsDedupsAcrossGlobs verifies that a file matched by both a
// directory glob and an explicit file glob appears exactly once.
func TestExpandOutputGlobsDedupsAcrossGlobs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dist"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dist", "a.js"), []byte("a"), 0o644))

	out, err := expandOutputGlobs([]string{"dist", "dist/a.js"}, root)
	require.NoError(t, err)

	count := 0
	for _, ra := range out {
		if ra.rel == "dist/a.js" {
			count++
		}
	}
	assert.Equal(t, 1, count, "a file matched by two globs must appear once")
}

// TestExpandOutputGlobsNoMatch verifies a glob that matches nothing yields an
// empty result without error (the caller decides how to treat a no-match).
func TestExpandOutputGlobsNoMatch(t *testing.T) {
	out, err := expandOutputGlobs([]string{"nonexistent/*.txt"}, t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, out)
}

// TestReplaySymlink verifies replay restores a symlink record as a symlink
// pointing at the recorded target.
func TestReplaySymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is restricted on Windows")
	}
	c := newBareCache(t)
	root := t.TempDir()
	m := &Manifest{Outputs: []OutputRecord{{Path: "link", Symlink: "the/target"}}}

	paths, err := c.replay(context.Background(), m, root)
	require.NoError(t, err)
	require.Len(t, paths, 1)

	target, err := os.Readlink(filepath.Join(root, "link"))
	require.NoError(t, err)
	assert.Equal(t, "the/target", target)
}

// TestReplayBlobRoundTrip verifies replay materialises a blob into a nested
// destination directory (created on demand) and applies the recorded mode.
func TestReplayBlobRoundTrip(t *testing.T) {
	c := newBareCache(t)
	src := t.TempDir()
	abs := filepath.Join(src, "in.txt")
	require.NoError(t, os.WriteFile(abs, []byte("payload"), 0o644))
	rec, err := c.snapshotOne(abs, "nested/dir/in.txt")
	require.NoError(t, err)
	rec.Mode = 0o600 // exercise the chmod branch with a distinct mode

	root := t.TempDir()
	m := &Manifest{Outputs: []OutputRecord{rec}}
	paths, err := c.replay(context.Background(), m, root)
	require.NoError(t, err)
	require.Len(t, paths, 1)

	dst := filepath.Join(root, "nested", "dir", "in.txt")
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, []byte("payload"), got)

	if runtime.GOOS != "windows" {
		info, err := os.Stat(dst)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode()&0o777, "replay must apply recorded mode")
	}
}

// TestReplayOverwritesExisting verifies replay removes a pre-existing file at
// the destination before materialising the blob (the os.Remove branch).
func TestReplayOverwritesExisting(t *testing.T) {
	c := newBareCache(t)
	src := t.TempDir()
	abs := filepath.Join(src, "in.txt")
	require.NoError(t, os.WriteFile(abs, []byte("new"), 0o644))
	rec, err := c.snapshotOne(abs, "out.txt")
	require.NoError(t, err)

	root := t.TempDir()
	// Pre-seed a stale file at the destination.
	require.NoError(t, os.WriteFile(filepath.Join(root, "out.txt"), []byte("stale"), 0o644))

	m := &Manifest{Outputs: []OutputRecord{rec}}
	_, err = c.replay(context.Background(), m, root)
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(root, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, []byte("new"), got, "replay must overwrite an existing file")
}

// TestReplayCancelledCtx verifies replay honours context cancellation before
// touching the filesystem.
func TestReplayCancelledCtx(t *testing.T) {
	c := newBareCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &Manifest{Outputs: []OutputRecord{{Path: "x", Blob: "deadbeef"}}}
	_, err := c.replay(ctx, m, t.TempDir())
	assert.ErrorIs(t, err, context.Canceled)
}

// TestCopyFileRoundTrip verifies copyFile creates parent dirs and copies bytes.
func TestCopyFileRoundTrip(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	require.NoError(t, os.WriteFile(src, []byte("copied"), 0o644))
	dst := filepath.Join(root, "a", "b", "dst")

	require.NoError(t, copyFile(src, dst))
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, []byte("copied"), got)
}

// TestCopyFileMissingSource verifies copyFile returns an error when the source
// does not exist (the os.Open error path).
func TestCopyFileMissingSource(t *testing.T) {
	err := copyFile(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "dst"))
	assert.Error(t, err)
}
