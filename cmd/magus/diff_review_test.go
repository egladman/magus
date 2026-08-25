package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

		got := collectReview(attach(t, root, cache, rev), nil, nil)
		require.NotNil(t, got)
		assert.Equal(t, 2, got.Files)
		assert.Equal(t, 0, got.Read)
		assert.Len(t, got.Unread, 2)
	})

	t.Run("acknowledging then re-reading reports it read", func(t *testing.T) {
		root, cache, rev := reviewFixture(t, map[string]string{"a.go": "package a\n"}, nil)

		n, err := ackChangeset(root, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)
		assert.Equal(t, 1, n)

		got := collectReview(attach(t, root, cache, rev), nil, nil)
		require.NotNil(t, got)
		assert.Equal(t, 1, got.Read)
		assert.Empty(t, got.Unread)
	})

	// The property the whole feature turns on: acknowledging code and then changing it must
	// not leave the change looking reviewed.
	t.Run("editing after acknowledging goes stale, not read", func(t *testing.T) {
		root, cache, rev := reviewFixture(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n"}, nil)
		_, err := ackChangeset(root, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc G() {}\n"), 0o644))

		got := collectReview(attach(t, root, cache, rev), nil, nil)
		require.NotNil(t, got)
		assert.Equal(t, 0, got.Read)
		// Stale is its own list, not an annotated entry in the unopened one: it is a
		// different finding and it leads the section.
		assert.Equal(t, []string{"a.go"}, got.Stale)
		assert.Empty(t, got.Unread)
	})

	// Reading a machine's restatement of an edit made elsewhere is not the review, which is
	// why the file list folds generated output away by default too.
	t.Run("generated output is not something to have read", func(t *testing.T) {
		root, cache, rev := reviewFixture(t,
			map[string]string{"a.go": "package a\n", "gen/x.go": "package gen\n"},
			map[string]string{"gen/x.go": types.DiffRoleOutput})

		got := collectReview(attach(t, root, cache, rev), nil, nil)
		require.NotNil(t, got)
		assert.Equal(t, 1, got.Files)

		n, err := ackChangeset(root, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)
		assert.Equal(t, 1, n)
	})

	// A file nothing could fingerprint carries no state, and a changeset of only those was
	// not measured. Reporting it as unread would accuse somebody of skipping a deletion.
	t.Run("a deleted file is unmeasured, not unread", func(t *testing.T) {
		root, cache := t.TempDir(), t.TempDir()
		rev := types.Diff{Files: []types.DiffFile{{Path: "gone.go", Role: types.DiffRoleSource}}}

		n, err := ackChangeset(root, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)
		assert.Equal(t, 0, n)

		assert.Nil(t, collectReview(attach(t, root, cache, rev), nil, nil))
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

	t.Run("names the unopened files and caps the list", func(t *testing.T) {
		r := &preflightReview{Files: 30, Read: 0}
		for i := range 30 {
			r.Unread = append(r.Unread, fmt.Sprintf("f%d.go", i))
		}
		lines := preflightReviewLines(r)
		assert.Contains(t, lines[0], "30 file(s) you have not opened")
		assert.Contains(t, lines[unreadShown+1], "and 20 more")
	})

	// No ratio, anywhere. A count with a target is one that gets cleared rather than
	// satisfied, and clearing it takes one keystroke and no reading.
	t.Run("reports no read ratio", func(t *testing.T) {
		lines := preflightReviewLines(&preflightReview{
			Files: 40, Read: 12,
			Stale:  []string{"internal/cache/key.go"},
			Unread: []string{"a.go", "b.go"},
		})
		for _, l := range lines {
			assert.NotContains(t, l, " of 40")
			assert.NotContains(t, l, "12")
		}
	})

	// Stale is the finding, so it leads whatever else the section holds: it is derived
	// from content rather than from a claim, so no amount of stamping produces it.
	t.Run("stale leads and is named", func(t *testing.T) {
		lines := preflightReviewLines(&preflightReview{
			Files: 4, Read: 1,
			Stale:  []string{"internal/cache/key.go"},
			Unread: []string{"a.go"},
		})
		assert.Contains(t, lines[0], "1 file(s) changed after you read them")
		assert.Equal(t, "      internal/cache/key.go", lines[1])
	})

	// A small undisturbed change needs no reading plan, and printing one there is how a
	// reader learns to skip the section before meeting a change big enough to need it.
	t.Run("says nothing about a small undisturbed change", func(t *testing.T) {
		assert.Nil(t, preflightReviewLines(&preflightReview{
			Files: 3, Unread: []string{"a.go", "b.go", "c.go"},
		}))
	})

	// ...but a small change where something moved under the reader is exactly when the
	// section earns its line.
	t.Run("a small change still reports stale", func(t *testing.T) {
		lines := preflightReviewLines(&preflightReview{Files: 2, Stale: []string{"a.go"}})
		require.NotEmpty(t, lines)
		assert.Contains(t, lines[0], "changed after you read them")
	})

	t.Run("says nothing when everything has been read", func(t *testing.T) {
		assert.Nil(t, preflightReviewLines(&preflightReview{Files: 30, Read: 30}))
	})
}

// TestReviewRequiredMatcher pins the scoping rule: globs are declared per project and
// matched against paths relative to it, so a project names its own files the same way its
// sources and outputs do.
func TestReviewRequiredMatcher(t *testing.T) {
	ws := &stubReviewWorkspace{projects: []*types.Project{
		{Path: ".", ReviewRequired: []string{"internal/secret/**"}},
		{Path: "console", ReviewRequired: []string{"src/auth/*.ts"}},
	}}
	match := reviewRequiredMatcher(ws)
	require.NotNil(t, match)

	assert.True(t, match("internal/secret/value.go"))
	assert.True(t, match("console/src/auth/token.ts"))
	assert.False(t, match("internal/cache/key.go"))
	// The console's glob must not reach outside the project that declared it.
	assert.False(t, match("src/auth/token.ts"))
}

// A workspace declaring nothing gets no matcher, which the report reads as "single nothing
// out" rather than as "everything matters".
func TestReviewRequiredMatcherNilWhenUndeclared(t *testing.T) {
	ws := &stubReviewWorkspace{projects: []*types.Project{{Path: "."}}}
	assert.Nil(t, reviewRequiredMatcher(ws))
}

func TestCollectReviewSeparatesRequiredPaths(t *testing.T) {
	root, cache, rev := reviewFixture(t, map[string]string{
		"internal/secret/value.go": "package secret\n",
		"internal/cache/key.go":    "package cache\n",
	}, nil)

	got := collectReview(attach(t, root, cache, rev), func(p string) bool {
		return strings.HasPrefix(p, "internal/secret/")
	}, nil)
	require.NotNil(t, got)
	assert.Equal(t, []string{"internal/secret/value.go"}, got.Required)
	assert.Len(t, got.Unread, 2)
}

// A bulk cover is echoed so a file stamped in one keystroke does not read as one somebody
// sat down with. It is a note the reader left themselves, not a toll they paid.
func TestPreflightReviewLinesReportsBulkReasons(t *testing.T) {
	lines := preflightReviewLines(&preflightReview{
		Files: 8, Read: 4,
		Unread:  []string{"a.go"},
		Reasons: []string{"codemod output, spot-checked 3 of 40"},
	})
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "covered in bulk")
	assert.Contains(t, joined, "codemod output")
}

func TestPreflightReviewLinesListsRequiredUncapped(t *testing.T) {
	r := &preflightReview{Files: 30, Read: 0}
	for i := range unreadShown + 5 {
		r.Required = append(r.Required, fmt.Sprintf("internal/secret/f%d.go", i))
	}
	r.Unread = append([]string{}, r.Required...)
	lines := preflightReviewLines(r)
	assert.Contains(t, lines[0], "15 unopened in review_required paths")
	// Uncapped, unlike the general list: the workspace said these cost something.
	assert.Contains(t, lines[15], "internal/secret/f14.go")
}

// A path the workspace flagged is named once, under review_required, and not again in the
// general list. Saying it twice reads as two findings.
func TestPreflightReviewLinesDoesNotRepeatRequiredPaths(t *testing.T) {
	lines := preflightReviewLines(&preflightReview{
		Files:    8,
		Required: []string{"internal/secret/value.go"},
		Unread:   []string{"internal/secret/value.go", "a.go"},
	})
	joined := strings.Join(lines, "\n")
	assert.Equal(t, 1, strings.Count(joined, "internal/secret/value.go"))
	assert.Contains(t, joined, "1 file(s) you have not opened")
}

// stubReviewWorkspace is the narrow slice of the reader the matcher uses.
type stubReviewWorkspace struct {
	types.WorkspaceReader
	projects []*types.Project
}

func (s *stubReviewWorkspace) All() []*types.Project { return s.projects }
