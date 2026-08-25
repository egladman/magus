package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reviewFixture plants files in a working tree and returns the tree, a cache dir, and the
// changeset naming them.
func reviewFixture(t *testing.T, files map[string]string, roles map[string]string) (string, string, types.Diff) {
	t.Helper()
	root, cache := t.TempDir(), t.TempDir()

	var rev types.Diff
	for path, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))

		role := types.DiffRoleSource
		if r, ok := roles[path]; ok {
			role = r
		}
		rev.Files = append(rev.Files, types.DiffFile{Path: path, Role: role})
	}
	return root, cache, rev
}

// attach folds the receipt store onto the changeset the way annotateDiff does, so these
// tests exercise the join the CLI and the console both go through rather than a second one.
func attach(t *testing.T, root, cache string, rev types.Diff) types.Diff {
	t.Helper()
	states, err := review.States(root, cache, diffPaths(rev))
	require.NoError(t, err)
	rev.AttachReadState(states)
	return rev
}

func TestCollectReview(t *testing.T) {
	t.Run("an unacknowledged changeset reports every file unread", func(t *testing.T) {
		root, cache, rev := reviewFixture(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"}, nil)

		got := collectReview(attach(t, root, cache, rev))
		require.NotNil(t, got)
		assert.Equal(t, 2, got.Files)
		assert.Equal(t, 0, got.Read)
		assert.Len(t, got.Unread, 2)
	})

	t.Run("acknowledging then re-reading reports it read", func(t *testing.T) {
		root, cache, rev := reviewFixture(t, map[string]string{"a.go": "package a\n"}, nil)

		n, err := ackChangeset(root, cache, rev, time.Now())
		require.NoError(t, err)
		assert.Equal(t, 1, n)

		got := collectReview(attach(t, root, cache, rev))
		require.NotNil(t, got)
		assert.Equal(t, 1, got.Read)
		assert.Empty(t, got.Unread)
	})

	// The property the whole feature turns on: acknowledging code and then changing it must
	// not leave the change looking reviewed.
	t.Run("editing after acknowledging goes stale, not read", func(t *testing.T) {
		root, cache, rev := reviewFixture(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n"}, nil)
		_, err := ackChangeset(root, cache, rev, time.Now())
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc G() {}\n"), 0o644))

		got := collectReview(attach(t, root, cache, rev))
		require.NotNil(t, got)
		assert.Equal(t, 0, got.Read)
		assert.Equal(t, 1, got.Stale)
		assert.Contains(t, got.Unread[0], "read, then changed")
	})

	// Reading a machine's restatement of an edit made elsewhere is not the review, which is
	// why the file list folds generated output away by default too.
	t.Run("generated output is not something to have read", func(t *testing.T) {
		root, cache, rev := reviewFixture(t,
			map[string]string{"a.go": "package a\n", "gen/x.go": "package gen\n"},
			map[string]string{"gen/x.go": types.DiffRoleOutput})

		got := collectReview(attach(t, root, cache, rev))
		require.NotNil(t, got)
		assert.Equal(t, 1, got.Files)

		n, err := ackChangeset(root, cache, rev, time.Now())
		require.NoError(t, err)
		assert.Equal(t, 1, n)
	})

	// A file nothing could fingerprint carries no state, and a changeset of only those was
	// not measured. Reporting it as unread would accuse somebody of skipping a deletion.
	t.Run("a deleted file is unmeasured, not unread", func(t *testing.T) {
		root, cache := t.TempDir(), t.TempDir()
		rev := types.Diff{Files: []types.DiffFile{{Path: "gone.go", Role: types.DiffRoleSource}}}

		n, err := ackChangeset(root, cache, rev, time.Now())
		require.NoError(t, err)
		assert.Equal(t, 0, n)

		assert.Nil(t, collectReview(attach(t, root, cache, rev)))
	})
}

func TestPreflightReviewLines(t *testing.T) {
	// The empty form is the half that matters: a silent section reads as a clean bill of
	// health, and here that would mean "somebody read this".
	t.Run("unmeasured says so rather than saying unread", func(t *testing.T) {
		lines := preflightReviewLines(nil)
		require.Len(t, lines, 1)
		assert.Contains(t, lines[0], "unavailable")
	})

	t.Run("names the unread files and caps the list", func(t *testing.T) {
		r := &preflightReview{Files: 30, Read: 0}
		for i := range 30 {
			r.Unread = append(r.Unread, string(rune('a'+i%26))+".go")
		}
		lines := preflightReviewLines(r)
		assert.Contains(t, lines[0], "0 of 30")
		assert.Contains(t, lines[unreadShown+1], "and 20 more")
	})

	t.Run("stale is called out separately from never read", func(t *testing.T) {
		lines := preflightReviewLines(&preflightReview{Files: 4, Read: 1, Stale: 2})
		assert.Contains(t, lines[0], "1 of 4")
		assert.Contains(t, lines[0], "2 were read and then edited")
	})
}
