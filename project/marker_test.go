package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsIgnoreDir(t *testing.T) {
	// Any dot-directory is skipped, plus the well-known non-dot build/dep dirs.
	for _, name := range []string{
		".git", ".hg", ".jj", ".magus", ".build", ".claude", ".idea", ".vscode",
		"vendor", "node_modules", "target", "gen",
	} {
		assert.True(t, IsIgnoreDir(name), "IsIgnoreDir(%q) should be true", name)
	}
	for _, name := range []string{"src", "cmd", "pkg", "internal", "starter"} {
		assert.False(t, IsIgnoreDir(name), "IsIgnoreDir(%q) should be false", name)
	}
}

func TestIgnoreDirs_ContainsExpected(t *testing.T) {
	// The list holds only non-dot names; dot-dirs are covered by the prefix rule.
	for _, d := range []string{"vendor", "node_modules", "target", "gen"} {
		assert.Contains(t, IgnoreDirs, d, "IgnoreDirs missing %q", d)
	}
	for _, d := range IgnoreDirs {
		assert.False(t, d[0] == '.', "IgnoreDirs should not list dot-dirs (%q); the prefix rule covers them", d)
	}
}

func TestIsNestedWorktree(t *testing.T) {
	dir := t.TempDir()

	// A linked worktree: .git is a file pointing under the parent repo's
	// .git/worktrees/. It must be skipped so its checkout isn't re-discovered.
	wt := filepath.Join(dir, "worktree")
	require.NoError(t, os.Mkdir(wt, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: /repo/.git/worktrees/feature\n"), 0o644))
	assert.True(t, IsNestedWorktree(wt), "a linked worktree must be detected")

	// A submodule points under .git/modules/ - it is a real nested repo, not a
	// worktree, and must stay discoverable.
	sub := filepath.Join(dir, "submodule")
	require.NoError(t, os.Mkdir(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, ".git"),
		[]byte("gitdir: /repo/.git/modules/libfoo\n"), 0o644))
	assert.False(t, IsNestedWorktree(sub), "a submodule must not be treated as a worktree")

	// The main checkout has a .git directory, not a gitfile.
	main := filepath.Join(dir, "main")
	require.NoError(t, os.MkdirAll(filepath.Join(main, ".git"), 0o755))
	assert.False(t, IsNestedWorktree(main), "the main checkout (.git dir) is not a linked worktree")

	// A plain directory with no .git at all.
	assert.False(t, IsNestedWorktree(dir), "a directory without .git is not a worktree")
}
