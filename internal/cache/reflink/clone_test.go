package reflink

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestClone_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	want := make([]byte, 1<<16) // 64 KiB
	if _, err := rand.Read(want); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Clone(src, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile(dst): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("content mismatch: got %d bytes, want %d", len(got), len(want))
	}
}

func TestClone_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	if err := os.WriteFile(src, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Clone(src, dst); err != nil {
		t.Fatalf("Clone empty: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(got))
	}
}

func TestClone_LargeFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	// 4 MiB — large enough to exercise the copy_file_range / io.Copy path.
	want := make([]byte, 4<<20)
	if _, err := rand.Read(want); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Clone(src, dst); err != nil {
		t.Fatalf("Clone large: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("large file content mismatch")
	}
}

func TestClone_IndependentWrites(t *testing.T) {
	// Verify that writing to dst after cloning does not modify src.
	// This is guaranteed by copy semantics on all paths; on CoW (btrfs/APFS)
	// it's structurally guaranteed, but we test it everywhere.
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	original := []byte("original content")
	if err := os.WriteFile(src, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Clone(src, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	if err := os.WriteFile(dst, []byte("modified"), 0o644); err != nil {
		t.Fatalf("WriteFile(dst): %v", err)
	}

	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("writing dst corrupted src: got %q, want %q", got, original)
	}
}

// TestClone_DstExists verifies that Clone returns an error (wrapping fs.ErrExist)
// when dst already exists, rather than silently truncating it.
func TestClone_DstExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Clone(src, dst)
	if err == nil {
		t.Fatal("Clone: expected error when dst exists, got nil")
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("Clone: expected fs.ErrExist, got %v", err)
	}

	// dst content must be unchanged.
	got, rerr := os.ReadFile(dst)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != "existing" {
		t.Errorf("Clone: dst was modified despite error: got %q", got)
	}
}

// TestClone_MissingSrc verifies that Clone returns an error when src does not
// exist, rather than creating a silent empty dst file.
func TestClone_MissingSrc(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "no-such-src")
	dst := filepath.Join(dir, "dst")

	err := Clone(src, dst)
	if err == nil {
		t.Fatal("Clone: expected error for missing src, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Clone: expected fs.ErrNotExist, got %v", err)
	}

	// dst must not have been created.
	if _, serr := os.Stat(dst); !errors.Is(serr, fs.ErrNotExist) {
		t.Errorf("Clone: dst should not exist after missing-src error, but Stat returned: %v", serr)
	}
}
