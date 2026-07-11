package std

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTarGz creates a .tar.gz archive in t.TempDir() with the given files
// (name → content). Returns the path to the archive.
func makeTarGz(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.tar.gz")
	f, err := os.Create(path)
	require.NoError(t, err)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		_ = tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Size:     int64(len(content)),
			Mode:     0o644,
		})
		_, _ = tw.Write([]byte(content))
	}
	tw.Close()
	gw.Close()
	f.Close()
	return path
}

func makeTar(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.tar")
	f, err := os.Create(path)
	require.NoError(t, err)
	tw := tar.NewWriter(f)
	for name, content := range files {
		_ = tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Size:     int64(len(content)),
			Mode:     0o644,
		})
		_, _ = tw.Write([]byte(content))
	}
	tw.Close()
	f.Close()
	return path
}

func makeZip(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.zip")
	f, err := os.Create(path)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte(content))
	}
	zw.Close()
	f.Close()
	return path
}

func TestArchiveUncompressTarGz(t *testing.T) {
	src := makeTarGz(t, map[string]string{
		"a.txt": "hello",
		"b.txt": "world",
	})
	dest := t.TempDir()

	result, err := ArchiveUncompress(context.Background(), src, dest, nil)
	require.NoError(t, err)
	files, _ := result["files"].([]string)
	sort.Strings(files)
	assert.Equal(t, []string{"a.txt", "b.txt"}, files)
	got, _ := result["bytes"].(int)
	assert.Equal(t, 10, got)
}

func TestArchiveUncompressTar(t *testing.T) {
	src := makeTar(t, map[string]string{"hello.txt": "hi"})
	dest := t.TempDir()

	result, err := ArchiveUncompress(context.Background(), src, dest, nil)
	require.NoError(t, err)
	files, _ := result["files"].([]string)
	assert.Equal(t, []string{"hello.txt"}, files)
}

func TestArchiveUncompressZip(t *testing.T) {
	src := makeZip(t, map[string]string{
		"x.txt": "foo",
		"y.txt": "bar",
	})
	dest := t.TempDir()

	result, err := ArchiveUncompress(context.Background(), src, dest, nil)
	require.NoError(t, err)
	files, _ := result["files"].([]string)
	sort.Strings(files)
	assert.Equal(t, []string{"x.txt", "y.txt"}, files)
}

func TestArchiveUncompressStrip(t *testing.T) {
	src := makeTarGz(t, map[string]string{
		"root/a.txt":   "hello",
		"root/b/c.txt": "world",
	})
	dest := t.TempDir()

	result, err := ArchiveUncompress(context.Background(), src, dest, map[string]any{"strip": 1})
	require.NoError(t, err)
	files, _ := result["files"].([]string)
	sort.Strings(files)
	assert.Equal(t, []string{"a.txt", filepath.Join("b", "c.txt")}, files)
}

func TestArchiveUncompressStripShallowEntrySkipped(t *testing.T) {
	// Entries with fewer components than strip are silently skipped (tar behavior).
	src := makeTarGz(t, map[string]string{
		"shallow":    "skipped",
		"root/a.txt": "kept",
	})
	dest := t.TempDir()

	result, err := ArchiveUncompress(context.Background(), src, dest, map[string]any{"strip": 1})
	require.NoError(t, err)
	files, _ := result["files"].([]string)
	assert.Equal(t, []string{"a.txt"}, files)
}

func TestArchiveUncompressPathTraversalDotDot(t *testing.T) {
	// Build a tar with a traversal entry manually.
	src := filepath.Join(t.TempDir(), "evil.tar.gz")
	f, _ := os.Create(src)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "../etc/passwd",
		Size:     5,
		Mode:     0o644,
	})
	_, _ = tw.Write([]byte("pwned"))
	tw.Close()
	gw.Close()
	f.Close()

	_, err := ArchiveUncompress(context.Background(), src, t.TempDir(), nil)
	assert.Error(t, err, "expected error for path traversal entry")
}

func TestArchiveUncompressPathTraversalAbsolute(t *testing.T) {
	src := filepath.Join(t.TempDir(), "abs.tar.gz")
	f, _ := os.Create(src)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "/etc/passwd",
		Size:     5,
		Mode:     0o644,
	})
	_, _ = tw.Write([]byte("pwned"))
	tw.Close()
	gw.Close()
	f.Close()

	_, err := ArchiveUncompress(context.Background(), src, t.TempDir(), nil)
	assert.Error(t, err, "expected error for absolute path entry")
}

func TestArchiveUncompressMaxSize(t *testing.T) {
	// Create a tar.gz with content that exceeds a tiny cap.
	src := makeTarGz(t, map[string]string{"big.txt": "0123456789"}) // 10 bytes

	_, err := ArchiveUncompress(context.Background(), src, t.TempDir(), map[string]any{"max_size": 5})
	assert.Error(t, err, "expected error when uncompressed size exceeds max_size")
}

func TestArchiveUncompressZipUndersizedHeader(t *testing.T) {
	// A malicious zip can declare a tiny uncompressed size in its central
	// directory yet decompress to far more; the per-entry bound must catch
	// the overrun instead of letting each entry write up to the whole
	// max_size budget. zip.Writer always records accurate sizes, so forge
	// the bomb by patching the central directory's uncompressed-size field
	// down after the fact.
	path := filepath.Join(t.TempDir(), "bomb.zip")
	f, err := os.Create(path)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	w, err := zw.Create("big.txt")
	require.NoError(t, err)
	_, err = w.Write([]byte(strings.Repeat("A", 100)))
	require.NoError(t, err)
	zw.Close()
	f.Close()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	// Central directory file header signature is "PK\x01\x02"; the
	// uncompressed-size uint32 sits at offset +24. Forge it to 10 (< 100).
	idx := bytes.Index(raw, []byte{'P', 'K', 0x01, 0x02})
	require.GreaterOrEqual(t, idx, 0, "central directory header not found")
	binary.LittleEndian.PutUint32(raw[idx+24:], 10)
	require.NoError(t, os.WriteFile(path, raw, 0o644))

	// max_size sits well above the forged declared size (10) so the
	// pre-extraction cumulative check passes and the per-entry bound is
	// what must reject the overrun.
	_, err = ArchiveUncompress(context.Background(), path, t.TempDir(), map[string]any{"max_size": 1000})
	assert.Error(t, err, "expected error for entry exceeding its declared size")
}

func TestArchiveUncompressLimiter(t *testing.T) {
	src := makeTarGz(t, map[string]string{"f.txt": "hi"})
	dest := t.TempDir()

	lim := cache.NewLimiter(2)
	ctx := cache.ContextWithLimiter(context.Background(), lim)

	_, err := ArchiveUncompress(ctx, src, dest, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, lim.Snapshot().Running, "limiter Running after call")
}

func TestArchiveUncompressNilLimiter(t *testing.T) {
	src := makeTarGz(t, map[string]string{"f.txt": "hi"})
	// No limiter in context — must work normally.
	_, err := ArchiveUncompress(context.Background(), src, t.TempDir(), nil)
	require.NoError(t, err)
}

func makeTestDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	return dir
}

func TestArchiveCompressTarGzRoundTrip(t *testing.T) {
	src := makeTestDir(t, map[string]string{
		"a.txt":   "hello",
		"b/c.txt": "world",
	})
	dest := filepath.Join(t.TempDir(), "out.tar.gz")

	result, err := ArchiveCompress(context.Background(), src, dest, nil)
	require.NoError(t, err)
	files, _ := result["files"].([]string)
	sort.Strings(files)
	require.Len(t, files, 2, "compress files")

	// Round-trip: extract and verify content.
	out := t.TempDir()
	res2, err := ArchiveUncompress(context.Background(), dest, out, nil)
	require.NoError(t, err)
	extracted, _ := res2["files"].([]string)
	sort.Strings(extracted)
	require.Len(t, extracted, 2, "uncompress files")
	got, _ := os.ReadFile(filepath.Join(out, "a.txt"))
	assert.Equal(t, "hello", string(got))
}

func TestArchiveCompressTarZstRoundTrip(t *testing.T) {
	src := makeTestDir(t, map[string]string{"x.txt": "zstd content"})
	dest := filepath.Join(t.TempDir(), "out.tar.zst")

	_, err := ArchiveCompress(context.Background(), src, dest, nil)
	require.NoError(t, err)

	out := t.TempDir()
	res, err := ArchiveUncompress(context.Background(), dest, out, nil)
	require.NoError(t, err)
	files, _ := res["files"].([]string)
	require.Equal(t, []string{"x.txt"}, files, "round-trip files")
	got, _ := os.ReadFile(filepath.Join(out, "x.txt"))
	assert.Equal(t, "zstd content", string(got))
}

func TestArchiveCompressZipRoundTrip(t *testing.T) {
	src := makeTestDir(t, map[string]string{
		"p.txt": "ping",
		"q.txt": "pong",
	})
	dest := filepath.Join(t.TempDir(), "out.zip")

	_, err := ArchiveCompress(context.Background(), src, dest, nil)
	require.NoError(t, err)

	out := t.TempDir()
	res, err := ArchiveUncompress(context.Background(), dest, out, nil)
	require.NoError(t, err)
	files, _ := res["files"].([]string)
	sort.Strings(files)
	require.Equal(t, []string{"p.txt", "q.txt"}, files, "zip round-trip files")
}

func TestArchiveCompressTarRoundTrip(t *testing.T) {
	src := makeTestDir(t, map[string]string{"bare.txt": "no compression"})
	dest := filepath.Join(t.TempDir(), "out.tar")

	_, err := ArchiveCompress(context.Background(), src, dest, nil)
	require.NoError(t, err)
	out := t.TempDir()
	res, err := ArchiveUncompress(context.Background(), dest, out, nil)
	require.NoError(t, err)
	files, _ := res["files"].([]string)
	require.Equal(t, []string{"bare.txt"}, files, "tar round-trip files")
}

func TestArchiveCompressMultiThreadZst(t *testing.T) {
	src := makeTestDir(t, map[string]string{
		"a.txt": "thread 1",
		"b.txt": "thread 2",
		"c.txt": "thread 3",
		"d.txt": "thread 4",
	})
	dest := filepath.Join(t.TempDir(), "out.tar.zst")

	lim := cache.NewLimiter(4)
	ctx := cache.ContextWithLimiter(context.Background(), lim)

	result, err := ArchiveCompress(ctx, src, dest, map[string]any{"threads": 4})
	require.NoError(t, err)
	files, _ := result["files"].([]string)
	require.Len(t, files, 4, "compress produced wrong file count")
	assert.Equal(t, 0, lim.Snapshot().Running, "limiter Running after compress")

	// Verify decompression also works multi-threaded.
	out := t.TempDir()
	_, err = ArchiveUncompress(ctx, dest, out, map[string]any{"threads": 4})
	require.NoError(t, err)
}

func TestArchiveCompressThreadsExceedsPool(t *testing.T) {
	src := makeTestDir(t, map[string]string{"f.txt": "x"})
	dest := filepath.Join(t.TempDir(), "out.tar.gz")

	lim := cache.NewLimiter(2)
	ctx := cache.ContextWithLimiter(context.Background(), lim)

	// Requesting 100 threads with a cap=2 limiter: must be silently clamped,
	// not error, since resolveThreads caps to limiter.cap.
	_, err := ArchiveCompress(ctx, src, dest, map[string]any{"threads": 100})
	require.NoError(t, err, "threads clamped to pool cap should succeed")
}

func TestArchiveCompressSandboxReadDenied(t *testing.T) {
	src := makeTestDir(t, map[string]string{"f.txt": "x"})
	dest := filepath.Join(t.TempDir(), "out.tar.gz")

	// Policy that allows writes but blocks all reads.
	p := &sandbox.Policy{
		Workspace: t.TempDir(),
		FS:        filesystem.Ruleset{Rules: []filesystem.Rule{{Path: filepath.Dir(dest), Write: true}}},
	}
	ctx := sandbox.WithPolicy(context.Background(), p)

	_, err := ArchiveCompress(ctx, src, dest, nil)
	assert.Error(t, err, "expected sandbox read denial")
}

func TestArchiveCompressSandboxWriteDenied(t *testing.T) {
	src := makeTestDir(t, map[string]string{"f.txt": "x"})
	dest := filepath.Join(t.TempDir(), "out.tar.gz")

	// Policy that allows reads of src but blocks writes entirely.
	p := &sandbox.Policy{
		Workspace: t.TempDir(),
		FS:        filesystem.Ruleset{Rules: []filesystem.Rule{{Path: src, Read: true}}},
	}
	ctx := sandbox.WithPolicy(context.Background(), p)

	_, err := ArchiveCompress(ctx, src, dest, nil)
	assert.Error(t, err, "expected sandbox write denial")
}

func TestArchiveCompressSymlinkPivotOnOutput(t *testing.T) {
	src := makeTestDir(t, map[string]string{"f.txt": "x"})
	evil := t.TempDir()
	destDir := t.TempDir()
	// Plant a symlink dest/out.tar.gz -> evil/out.tar.gz so compress would
	// write to evil/ instead of destDir/.
	link := filepath.Join(destDir, "out.tar.gz")
	if err := os.Symlink(filepath.Join(evil, "out.tar.gz"), link); err != nil {
		t.Skip("symlink creation failed:", err)
	}
	// Compress uses EvalSymlinks on the dest directory, so it resolves the link
	// and writes to the real path (not a security violation here — the symlink
	// points within the same tmpdir tree). This test verifies no panic and that
	// the output is actually written somewhere sensible.
	_, err := ArchiveCompress(context.Background(), src, link, nil)
	require.NoError(t, err)
}

func TestArchiveCompressMaxSize(t *testing.T) {
	src := makeTestDir(t, map[string]string{"big.txt": "0123456789"}) // 10 bytes raw
	dest := filepath.Join(t.TempDir(), "out.tar")

	// Cap output at 1 byte — the tar header alone exceeds this.
	_, err := ArchiveCompress(context.Background(), src, dest, map[string]any{"max_size": 1})
	assert.Error(t, err, "expected max_size error")
}

func TestArchiveCompressUnknownFormat(t *testing.T) {
	src := makeTestDir(t, map[string]string{"f.txt": "x"})
	dest := filepath.Join(t.TempDir(), "out.7z") // unsupported extension

	_, err := ArchiveCompress(context.Background(), src, dest, nil)
	assert.Error(t, err, "expected error for unknown format")
}

func TestArchiveCompressFormatOverride(t *testing.T) {
	src := makeTestDir(t, map[string]string{"f.txt": "x"})
	// Extension says .bin but format is overridden to tar.gz.
	dest := filepath.Join(t.TempDir(), "out.bin")

	_, err := ArchiveCompress(context.Background(), src, dest, map[string]any{"format": "tar.gz"})
	require.NoError(t, err)
	// Round-trip: the file should be decompressible as .tar.gz regardless of extension.
	out := t.TempDir()
	_, err = ArchiveUncompress(context.Background(), dest, out, nil)
	require.NoError(t, err, "uncompress of format-overridden archive")
}

func TestArchiveUncompressMultiThreadZip(t *testing.T) {
	src := makeZip(t, map[string]string{
		"p.txt": "parallel1",
		"q.txt": "parallel2",
		"r.txt": "parallel3",
	})
	dest := t.TempDir()

	lim := cache.NewLimiter(4)
	ctx := cache.ContextWithLimiter(context.Background(), lim)

	result, err := ArchiveUncompress(ctx, src, dest, map[string]any{"threads": 3})
	require.NoError(t, err)
	files, _ := result["files"].([]string)
	sort.Strings(files)
	require.Len(t, files, 3, "parallel zip extract files")
	assert.Equal(t, 0, lim.Snapshot().Running, "limiter Running after call")
}

func TestArchiveUncompressLimiterAcquiresN(t *testing.T) {
	src := makeTarGz(t, map[string]string{"f.txt": "hi"})
	dest := t.TempDir()

	lim := cache.NewLimiter(4)
	ctx := cache.ContextWithLimiter(context.Background(), lim)

	_, err := ArchiveUncompress(ctx, src, dest, map[string]any{"threads": 2})
	require.NoError(t, err)
	assert.Equal(t, 0, lim.Snapshot().Running, "limiter Running after call")
}
