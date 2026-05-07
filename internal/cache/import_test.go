package cache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// makeTar writes a gzip-compressed tar containing one file of size n bytes.
func makeTar(t *testing.T, name string, size int64) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Size:     size,
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := io.Copy(tw, io.LimitReader(zeroReader{}, size)); err != nil {
		t.Fatalf("tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return &buf
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// TestImportMaxBytesCapsTarBomb verifies that a tar entry larger than
// WithMaxImportBytes is truncated rather than filling the disk. Import
// accepts arbitrary input from CI/S3; the tar header's reported size
// cannot be trusted to bound writes without io.LimitReader.
func TestImportMaxBytesCapsTarBomb(t *testing.T) {
	t.Parallel()

	cdir := t.TempDir()
	c, err := Open(cdir, WithMaxImportBytes(1024))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Create a "bomb": a single entry that would be 1 MiB without the cap.
	const entrySize = 1 << 20
	archive := makeTar(t, "manifests/test/entry", entrySize)

	if err := c.Import(context.Background(), archive); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// The written file must not exceed the cap.
	dest := filepath.Join(cdir, "manifests", "test", "entry")
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() > 1024 {
		t.Errorf("file size = %d, want ≤ 1024 (the cap)", fi.Size())
	}
}

// TestImportDefaultCapApplied ensures Import works normally when no cap
// option is set — the default cap must be large enough for real archives.
func TestImportDefaultCapApplied(t *testing.T) {
	t.Parallel()

	cdir := t.TempDir()
	c, err := Open(cdir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// A small legitimate entry — must pass through untruncated.
	const entrySize = 4096
	archive := makeTar(t, "manifests/test/small", entrySize)

	if err := c.Import(context.Background(), archive); err != nil {
		t.Fatalf("Import: %v", err)
	}

	dest := filepath.Join(cdir, "manifests", "test", "small")
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() != entrySize {
		t.Errorf("file size = %d, want %d", fi.Size(), entrySize)
	}
}
