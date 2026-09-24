package file

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFileAtomic_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.txt")
	data := []byte("hello atomic")

	require.NoError(t, WriteFileAtomic(path, data, 0o644))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(got))
}

func TestWriteFileAtomic_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.txt")

	require.NoError(t, WriteFileAtomic(path, []byte("old"), 0o644))
	require.NoError(t, WriteFileAtomic(path, []byte("new"), 0o644))

	got, _ := os.ReadFile(path)
	assert.Equal(t, "new", string(got))
}

// ReplaceFile gives up only durability: it still replaces by rename, leaves no temp file
// behind, and applies perm.
func TestReplaceFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "output.txt")

	require.NoError(t, ReplaceFile(path, []byte("old"), 0o600))
	require.NoError(t, ReplaceFile(path, []byte("new"), 0o600))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", string(got))
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temp file left beside the target")
	if fi, err := os.Stat(path); assert.NoError(t, err) && runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}
}
