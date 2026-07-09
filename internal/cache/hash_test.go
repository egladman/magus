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

// TestHashFileKnownVector pins hashFile to the stdlib SHA256 of a known input.
// magus hashes with crypto/sha256 (not BLAKE3), so the digest of "hello world"
// must match the canonical sha256 hex.
func TestHashFileKnownVector(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.txt")
	require.NoError(t, os.WriteFile(p, []byte("hello world"), 0o644))
	got, err := hashFile(p)
	require.NoError(t, err)
	assert.Equal(t, "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9", got)
}

// TestHashFileStableAcrossRuns verifies the same content yields the same digest
// on repeated calls, and different content yields a different digest.
func TestHashFileStableAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	require.NoError(t, os.WriteFile(a, []byte("content"), 0o644))
	h1, err := hashFile(a)
	require.NoError(t, err)
	h2, err := hashFile(a)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "hash must be stable across runs")

	require.NoError(t, os.WriteFile(a, []byte("different"), 0o644))
	h3, err := hashFile(a)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h3, "changed content must change the hash")
}

// TestHashFileMissing verifies the os.Open error path is wrapped with the path.
func TestHashFileMissing(t *testing.T) {
	_, err := hashFile(filepath.Join(t.TempDir(), "absent"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hashFile")
}

// TestHashFileWithMtimeReusesStoredHash verifies the mtime fast-path: the
// first call hashes and records the fingerprint; a second call with an
// unchanged file returns the stored hash without re-hashing (proved by
// corrupting the file content while keeping the stored fingerprint valid).
func TestHashFileWithMtimeReusesStoredHash(t *testing.T) {
	c := newBareCache(t)
	c.mtimes.load(context.Background()) // hashFileWithMtime.set needs initialised shards
	dir := t.TempDir()
	p := filepath.Join(dir, "src.go")
	require.NoError(t, os.WriteFile(p, []byte("v1"), 0o644))

	h1, err := c.hashFileWithMtime(p)
	require.NoError(t, err)
	require.NotEmpty(t, h1)

	// Overwrite content but restore the exact prior (mtime,size) fingerprint so
	// the store still matches. A correct fast-path returns the stale stored hash
	// rather than re-hashing the new bytes; this proves the store was consulted.
	info, err := os.Stat(p)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, []byte("v2"), 0o644)) // same 2-byte size
	require.NoError(t, os.Chtimes(p, info.ModTime(), info.ModTime()))

	h2, err := c.hashFileWithMtime(p)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "matching fingerprint must return the stored hash")
}

// TestHashFileWithMtimeRehashesOnChange verifies that a genuine size change
// invalidates the fingerprint and triggers a re-hash producing a new digest.
func TestHashFileWithMtimeRehashesOnChange(t *testing.T) {
	c := newBareCache(t)
	c.mtimes.load(context.Background()) // hashFileWithMtime.set needs initialised shards
	p := filepath.Join(t.TempDir(), "src.go")
	require.NoError(t, os.WriteFile(p, []byte("short"), 0o644))
	h1, err := c.hashFileWithMtime(p)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(p, []byte("a much longer body"), 0o644))
	h2, err := c.hashFileWithMtime(p)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "size change must invalidate the fast-path and re-hash")
}

// TestHashFileWithMtimeMissing verifies the os.Stat error path.
func TestHashFileWithMtimeMissing(t *testing.T) {
	c := newBareCache(t)
	_, err := c.hashFileWithMtime(filepath.Join(t.TempDir(), "absent"))
	assert.Error(t, err)
}

// TestExpandSourcesSkipsSymlinks verifies that a symlink matching a source glob
// is not recorded (symlinked sources are skipped to avoid following escapes).
func TestExpandSourcesSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is restricted on Windows")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real.go")
	require.NoError(t, os.WriteFile(real, []byte("package main"), 0o644))
	require.NoError(t, os.Symlink("real.go", filepath.Join(root, "alias.go")))

	out, err := expandSources([]string{"*.go"}, root, nil)
	require.NoError(t, err)

	var rels []string
	for _, ra := range out {
		rels = append(rels, ra.rel)
	}
	assert.Equal(t, []string{"real.go"}, rels, "symlinked source must be skipped")
}

// TestExpandSourcesExcludesOutputs verifies that files under an output glob are
// pruned from the source set (the output tree is never an input).
func TestExpandSourcesExcludesOutputs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dist"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.js"), []byte("k"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dist", "bundle.js"), []byte("b"), 0o644))

	out, err := expandSources([]string{"**/*.js"}, root, []string{"dist/**"})
	require.NoError(t, err)

	var rels []string
	for _, ra := range out {
		rels = append(rels, ra.rel)
	}
	assert.Equal(t, []string{"keep.js"}, rels, "files under an output glob must be excluded")
}

// TestExpandSourcesEmptyGlobs verifies the len(globs) == 0 early return.
func TestExpandSourcesEmptyGlobs(t *testing.T) {
	out, err := expandSources(nil, t.TempDir(), nil)
	require.NoError(t, err)
	assert.Nil(t, out)
}

// TestHashFilesEmpty verifies the len(files) == 0 fast path returns nils.
func TestHashFilesEmpty(t *testing.T) {
	c := newBareCache(t)
	hashes, modes, err := c.hashFiles(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, hashes)
	assert.Nil(t, modes)
}

// TestStaticDirPrefix covers the metacharacter, no-slash, and no-metacharacter
// branches of staticDirPrefix.
func TestStaticDirPrefix(t *testing.T) {
	cases := []struct {
		glob string
		want string
	}{
		{"dist/**", "dist"},
		{"build/js/*.map", "build/js"},
		{"*.js", ""},       // metacharacter with no preceding slash
		{"exact/path", ""}, // no metacharacter at all
		{"a/b/c/**/*.o", "a/b/c"},
	}
	for _, tc := range cases {
		assert.Equalf(t, tc.want, staticDirPrefix(tc.glob), "staticDirPrefix(%q)", tc.glob)
	}
}

// TestShortHash covers both branches: short input returned as-is, long input
// truncated to 8 hex chars.
func TestShortHash(t *testing.T) {
	assert.Equal(t, "abc", shortHash("abc"), "input <= 8 chars returned unchanged")
	assert.Equal(t, "deadbeef", shortHash("deadbeefcafef00d"), "long input truncated to 8")
}
