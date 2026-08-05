package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkWorktree creates a checkout directory under .claude/worktrees. When live, it
// gets the .git link file git writes into a real worktree; when not, it is the
// pruned-but-not-deleted leftover this check exists to find.
func mkWorktree(t *testing.T, root, name string, live bool) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "worktrees", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	if live {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /somewhere\n"), 0o600))
	}
}

func TestCheckStaleWorktrees(t *testing.T) {
	t.Run("no worktrees dir is fine", func(t *testing.T) {
		c := checkStaleWorktrees(t.TempDir())
		assert.Equal(t, types.DoctorOK, c.Status)
	})

	t.Run("live worktrees pass", func(t *testing.T) {
		root := t.TempDir()
		mkWorktree(t, root, "feature-a", true)
		mkWorktree(t, root, "feature-b", true)
		assert.Equal(t, types.DoctorOK, checkStaleWorktrees(root).Status)
	})

	// The case that actually happened: `git worktree prune` clears git's registry but
	// leaves the directory, so a second copy of every spell source stays in the tree.
	t.Run("pruned but undeleted directory is caught", func(t *testing.T) {
		root := t.TempDir()
		mkWorktree(t, root, "live-one", true)
		mkWorktree(t, root, "graph-explorer-viz-8008f6", false)

		c := checkStaleWorktrees(root)
		require.Equal(t, types.DoctorFail, c.Status)
		assert.Contains(t, c.Message, "MGS1002", "the message must name what it breaks, not just that it is stale")
		assert.Contains(t, c.Details, filepath.Join(".claude/worktrees", "graph-explorer-viz-8008f6"))
		assert.NotContains(t, c.Details, filepath.Join(".claude/worktrees", "live-one"))
		assert.Contains(t, c.Details[len(c.Details)-1], "git worktree add", "a finding without a remedy is a complaint")
	})

	t.Run("files are not mistaken for worktrees", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".claude", "worktrees"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, ".claude", "worktrees", "README"), []byte("x"), 0o600))
		assert.Equal(t, types.DoctorOK, checkStaleWorktrees(root).Status)
	})
}
