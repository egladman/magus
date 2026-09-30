//go:build unix || js

package vcs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOwnerIsTheCurrentUser(t *testing.T) {
	dir := t.TempDir()
	assert.True(t, pathOwnedByCurrentUser(dir), "a directory this process created is its own")
	assert.False(t, pathOwnedByCurrentUser(filepath.Join(dir, "missing")), "a path that cannot be read is not vouched for")
	if os.Geteuid() != 0 {
		assert.False(t, pathOwnedByCurrentUser("/"), "the filesystem root belongs to root")
	}
}

func TestOwnerDeviceMatchesWithinADirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(sub, 0o755))
	a, ok := pathDevice(dir)
	require.True(t, ok)
	b, ok := pathDevice(sub)
	require.True(t, ok)
	assert.Equal(t, a, b, "a subdirectory shares its parent's device")
	_, ok = pathDevice(filepath.Join(dir, "missing"))
	assert.False(t, ok)
}
