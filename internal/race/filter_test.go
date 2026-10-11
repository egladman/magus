package race

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestTrackedFilter_AllowsTrackedRejectsUntracked(t *testing.T) {
	r := testkit.NewRepo(t, nil)
	root := r.Root()

	tracked := r.Write("tracked.txt", "x")
	r.Git("add", "tracked.txt")

	untracked := r.Write("untracked.txt", "y")

	f := newTrackedFilter(t.Context(), root)
	require.True(t, f.Allow(tracked), "staged file is tracked")
	require.False(t, f.Allow(untracked), "unstaged file is untracked")
	require.False(t, f.Allow(filepath.Join(root, "ghost.txt")), "nonexistent file is untracked")
}

func TestTrackedFilter_NonRepoAllowsNothing(t *testing.T) {
	root := t.TempDir()
	// No git init: the listing fails and the tracked set stays empty.
	f := newTrackedFilter(t.Context(), root)
	existing := filepath.Join(root, "file.txt")
	require.NoError(t, os.WriteFile(existing, []byte("x"), 0o644))
	require.False(t, f.Allow(existing))
}
