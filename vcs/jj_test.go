package vcs

import (
	"os/exec"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseJJConflicts pins the `jj resolve --list` parse against output captured from
// jj 0.44.0. The "including 1 deletion" clause is load-bearing: it is the only signal
// that a side has no content, and getting it wrong would settle a modify/delete as a
// content conflict, which no regeneration can fix.
func TestParseJJConflicts(t *testing.T) {
	got := parseJJConflicts("f.txt       2-sided conflict\ngone.txt    2-sided conflict including 1 deletion\n")
	require.Len(t, got, 2)
	assert.Equal(t, types.Conflict{Path: "f.txt", Kind: types.ConflictKindContent}, got[0])
	assert.Equal(t, types.Conflict{Path: "gone.txt", Kind: types.ConflictKindDeleted}, got[1])

	t.Run("a path with spaces keeps them", func(t *testing.T) {
		got := parseJJConflicts("docs/my notes.md    2-sided conflict\n")
		require.Len(t, got, 1)
		assert.Equal(t, "docs/my notes.md", got[0].Path)
	})
	t.Run("clean tree yields none", func(t *testing.T) {
		assert.Empty(t, parseJJConflicts(""))
	})
	t.Run("higher arity still parses", func(t *testing.T) {
		got := parseJJConflicts("a.txt    3-sided conflict including 2 deletions\n")
		require.Len(t, got, 1)
		assert.Equal(t, types.ConflictKindDeleted, got[0].Kind)
	})
}

// TestConflictsDoesNotSwallowRealFailures pins the stderr-based discriminator.
//
// The check used to read `out`, which is STDOUT - and vcsOutputRaw returns ("", err) on any
// failure, so `strings.Contains(out, "No conflicts") || out == ""` was unconditionally true
// on the error path and the real-failure branch below it was unreachable. jj missing from
// PATH, a directory that is not a jj repo, a cancelled context and a permission error all
// reported "no conflicts", and `magus vcs resolve` then called the merge settled.
//
// A plain temp directory is the cheapest way to provoke a genuine failure; jj writes its
// "No conflicts found" notice to stderr, which is where the real discriminator now looks.
func TestConflictsDoesNotSwallowRealFailures(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not available")
	}
	_, err := jjVCS{}.Conflicts(t.Context(), t.TempDir())
	require.Error(t, err, "a directory that is not a jj repo must not report 'no conflicts'")
}
