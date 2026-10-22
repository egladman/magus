//go:build !windows

package sockdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteTokenIsOwnerOnlyAndReplacesALeftover(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "x.sock")
	require.NoError(t, os.WriteFile(TokenPath(sock), []byte("stale"), 0o644))

	require.NoError(t, WriteToken(sock, "fresh"))

	got, err := os.ReadFile(TokenPath(sock))
	require.NoError(t, err)
	assert.Equal(t, "fresh", string(got))
	info, err := os.Stat(TokenPath(sock))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWriteTokenReplacesASymlinkWithoutFollowingIt(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "x.sock")
	victim := filepath.Join(dir, "victim")
	require.NoError(t, os.WriteFile(victim, []byte("keep"), 0o644))
	require.NoError(t, os.Symlink(victim, TokenPath(sock)))

	require.NoError(t, WriteToken(sock, "fresh"))

	got, err := os.ReadFile(victim)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(got))
	info, err := os.Lstat(TokenPath(sock))
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular())
}
