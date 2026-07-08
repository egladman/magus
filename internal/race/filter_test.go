package race

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		require.NoError(t, cmd.Run(), "git %v", args)
	}
}

func TestGitFilter_AllowsTrackedRejectsUntracked(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)

	tracked := filepath.Join(root, "tracked.txt")
	require.NoError(t, os.WriteFile(tracked, []byte("x"), 0o644))
	require.NoError(t, exec.Command("git", "-C", root, "add", "tracked.txt").Run())

	untracked := filepath.Join(root, "untracked.txt")
	require.NoError(t, os.WriteFile(untracked, []byte("y"), 0o644))

	f := newGitFilter(root)
	require.True(t, f.Allow(tracked), "staged file is tracked")
	require.False(t, f.Allow(untracked), "unstaged file is untracked")
	require.False(t, f.Allow(filepath.Join(root, "ghost.txt")), "nonexistent file is untracked")
}

func TestGitFilter_NonRepoAllowsNothing(t *testing.T) {
	root := t.TempDir()
	// No git init: build() fails and the tracked set stays empty.
	f := newGitFilter(root)
	existing := filepath.Join(root, "file.txt")
	require.NoError(t, os.WriteFile(existing, []byte("x"), 0o644))
	require.False(t, f.Allow(existing))
}
