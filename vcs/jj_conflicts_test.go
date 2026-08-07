package vcs

import (
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
