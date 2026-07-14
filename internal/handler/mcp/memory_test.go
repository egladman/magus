package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRepoIdentityWorktree proves every worktree of one repo resolves to the
// same identity, so they share one memory directory - the reason the key is
// not the checkout path.
func TestRepoIdentityWorktree(t *testing.T) {
	main := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(main, ".git"), 0o755))

	wt := t.TempDir()
	gitfile := "gitdir: " + filepath.Join(main, ".git", "worktrees", "feature-x") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte(gitfile), 0o644))

	assert.Equal(t, main, repoIdentity(main), "a plain checkout identifies as itself")
	assert.Equal(t, main, repoIdentity(wt), "a linked worktree identifies as the main repo")

	// No .git at all (other VCS, bare dir): the root is the identity.
	other := t.TempDir()
	assert.Equal(t, other, repoIdentity(other))
}

func TestMemoryDirIsOutsideRepoAndStable(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := t.TempDir()

	dir, err := memoryDir(root)
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(dir))
	assert.Contains(t, dir, filepath.Join(state, "magus", "memory"))
	assert.NotContains(t, dir, root, "memory must not live under the repo")
	assert.Contains(t, filepath.Base(dir), filepath.Base(root)+"-", "dir name leads with the repo basename for human legibility")

	again, err := memoryDir(root)
	require.NoError(t, err)
	assert.Equal(t, dir, again, "the key is deterministic")
}

func TestMemoryAppendIsDateStamped(t *testing.T) {
	dir := t.TempDir()
	_, err := scratchpadOpFile(dir, "progress.md", "append", "## 2026-01-02\n\nshipped the thing\n")
	require.NoError(t, err)
	// The Invoke path stamps before calling scratchpadOpFile; this test covers
	// the file semantics the stamp rides on: appends accumulate, reads return all.
	res, err := scratchpadOpFile(dir, "progress.md", "append", "## 2026-01-03\n\nfixed the other thing\n")
	require.NoError(t, err)
	assert.Contains(t, res.Content, "2026-01-02")
	assert.Contains(t, res.Content, "2026-01-03")

	read, err := scratchpadOpFile(dir, "progress.md", "read", "")
	require.NoError(t, err)
	assert.Equal(t, res.Content, read.Content)
}
