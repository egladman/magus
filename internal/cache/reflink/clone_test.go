package reflink

import (
	"crypto/rand"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClone_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	want := make([]byte, 1<<16) // 64 KiB
	_, err := rand.Read(want)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(src, want, 0o644))

	require.NoError(t, Clone(src, dst))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestClone_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	require.NoError(t, os.WriteFile(src, nil, 0o644))
	require.NoError(t, Clone(src, dst))
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestClone_LargeFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	// 4 MiB — large enough to exercise the copy_file_range / io.Copy path.
	want := make([]byte, 4<<20)
	_, err := rand.Read(want)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(src, want, 0o644))
	require.NoError(t, Clone(src, dst))
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestClone_IndependentWrites(t *testing.T) {
	// Verify that writing to dst after cloning does not modify src.
	// This is guaranteed by copy semantics on all paths; on CoW (btrfs/APFS)
	// it's structurally guaranteed, but we test it everywhere.
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	original := []byte("original content")
	require.NoError(t, os.WriteFile(src, original, 0o644))
	require.NoError(t, Clone(src, dst))

	require.NoError(t, os.WriteFile(dst, []byte("modified"), 0o644))

	got, err := os.ReadFile(src)
	require.NoError(t, err)
	assert.Equal(t, original, got, "writing dst corrupted src")
}

// TestClone_DstExists verifies that Clone returns an error (wrapping fs.ErrExist)
// when dst already exists, rather than silently truncating it.
func TestClone_DstExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	require.NoError(t, os.WriteFile(src, []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(dst, []byte("existing"), 0o644))

	err := Clone(src, dst)
	require.Error(t, err, "expected error when dst exists")
	assert.ErrorIs(t, err, fs.ErrExist)

	// dst content must be unchanged.
	got, rerr := os.ReadFile(dst)
	require.NoError(t, rerr)
	assert.Equal(t, "existing", string(got), "dst was modified despite error")
}

// TestClone_MissingSrc verifies that Clone returns an error when src does not
// exist, rather than creating a silent empty dst file.
func TestClone_MissingSrc(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "no-such-src")
	dst := filepath.Join(dir, "dst")

	err := Clone(src, dst)
	require.Error(t, err, "expected error for missing src")
	assert.ErrorIs(t, err, fs.ErrNotExist)

	// dst must not have been created.
	_, serr := os.Stat(dst)
	assert.ErrorIs(t, serr, fs.ErrNotExist, "dst should not exist after missing-src error")
}
