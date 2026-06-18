package race

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldSkipDir(t *testing.T) {
	assert.True(t, shouldSkipDir(".git"))
	assert.True(t, shouldSkipDir("node_modules"))
	assert.True(t, shouldSkipDir(".pnpm-store"))
	assert.True(t, shouldSkipDir("vendor"))
	assert.True(t, shouldSkipDir("target"))
	assert.True(t, shouldSkipDir("dist"))
	assert.True(t, shouldSkipDir("build"))
	assert.True(t, shouldSkipDir(".cache"))
	assert.True(t, shouldSkipDir(".hidden"))
	assert.False(t, shouldSkipDir("src"))
	assert.False(t, shouldSkipDir("cmd"))
	assert.False(t, shouldSkipDir("pkg"))
}

func TestIsDir(t *testing.T) {
	dir := t.TempDir()
	assert.True(t, isDir(dir), "isDir should be true for temp dir")

	f := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	assert.False(t, isDir(f), "isDir should be false for regular file")
	assert.False(t, isDir(filepath.Join(dir, "nosuchfile")), "isDir should be false for nonexistent path")
}

func TestReadDirNames(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte{}, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.go"), []byte{}, 0o644))

	names, err := readDirNames(dir)
	require.NoError(t, err)
	assert.Len(t, names, 2)
}
