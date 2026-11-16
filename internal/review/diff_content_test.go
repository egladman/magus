package review

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/diff"
	"github.com/egladman/magus/types/vcs"
)

// reviewFixture plants files in a working tree and returns the tree, a cache dir, and the
// changeset naming them.
func reviewFixture(t *testing.T, files map[string]string, roles map[string]string) (string, string, diff.Diff) {
	t.Helper()
	w := testkit.NewWorkspace(t)
	root, cache := w.Root(), t.TempDir()

	var rev diff.Diff
	for path, body := range files {
		w.Write(path, body)

		role := diff.RoleSource
		if r, ok := roles[path]; ok {
			role = r
		}
		rev.Files = append(rev.Files, diff.File{Path: path, Role: role})
	}
	return root, cache, rev
}

// attach folds the receipt store onto the changeset the way annotateDiff does, so these
// tests exercise the join the CLI and the console both go through rather than a second one.
func attach(t *testing.T, root, cache string, rev diff.Diff) diff.Diff {
	t.Helper()
	states, err := ReadStates(cache, ChangedPaths(rev), Content{root: root}.Digest)
	require.NoError(t, err)
	rev.AttachReadState(states)
	return rev
}

// readStates is the read state attach folds onto each file, by path.
func readStates(rev diff.Diff) map[string]string {
	out := map[string]string{}
	for _, f := range rev.Files {
		out[f.Path] = f.ReadState
	}
	return out
}

// TestAckChangesetFoldsOntoTheChangeset pins the join: what AckChangeset records is what
// attach reads back onto the changeset, the state impact's review section counts.
func TestAckChangesetFoldsOntoTheChangeset(t *testing.T) {
	t.Run("an unacknowledged changeset is unread", func(t *testing.T) {
		root, cache, rev := reviewFixture(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"}, nil)

		assert.Equal(t, map[string]string{"a.go": diff.ReadUnread, "b.go": diff.ReadUnread},
			readStates(attach(t, root, cache, rev)))
	})

	t.Run("acknowledging then re-reading reads it read", func(t *testing.T) {
		root, cache, rev := reviewFixture(t, map[string]string{"a.go": "package a\n"}, nil)

		n, err := AckChangeset(Content{root: root}, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		assert.Equal(t, map[string]string{"a.go": diff.ReadRead}, readStates(attach(t, root, cache, rev)))
	})

	// The property the whole feature turns on: acknowledging code and then changing it must
	// not leave the change looking reviewed.
	t.Run("editing after acknowledging goes stale, not read", func(t *testing.T) {
		root, cache, rev := reviewFixture(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n"}, nil)
		_, err := AckChangeset(Content{root: root}, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc G() {}\n"), 0o644))

		assert.Equal(t, map[string]string{"a.go": diff.ReadStale}, readStates(attach(t, root, cache, rev)))
	})

	// Reading a machine's restatement of an edit made elsewhere is not the review, which is
	// why the file list folds generated output away by default too.
	t.Run("generated output is not acknowledged", func(t *testing.T) {
		root, cache, rev := reviewFixture(t,
			map[string]string{"a.go": "package a\n", "gen/x.go": "package gen\n"},
			map[string]string{"gen/x.go": diff.RoleOutput})

		n, err := AckChangeset(Content{root: root}, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)
		assert.Equal(t, 1, n)
	})

	// A file nothing could fingerprint carries no state: a receipt against it would cover
	// every unreadable file forever.
	t.Run("a deleted file is not acknowledged and carries no state", func(t *testing.T) {
		root, cache := t.TempDir(), t.TempDir()
		rev := diff.Diff{Files: []diff.File{{Path: "gone.go", Role: diff.RoleSource}}}

		n, err := AckChangeset(Content{root: root}, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)
		assert.Equal(t, 0, n)
		assert.Equal(t, map[string]string{"gone.go": diff.ReadUnknown}, readStates(attach(t, root, cache, rev)))
	})
}

// TestReviewRequiredMatcher pins the scoping rule: globs are declared per project and
// matched against paths relative to it, so a project names its own files the same way its
// sources and outputs do.
func TestReviewRequiredMatcher(t *testing.T) {
	ws := &projectsWorkspace{projects: []*types.Project{
		{Path: ".", ReviewRequired: []string{"internal/secret/**"}},
		{Path: "console", ReviewRequired: []string{"src/auth/*.ts"}},
	}}
	match := RequiredMatcher(ws)
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
	ws := &projectsWorkspace{projects: []*types.Project{{Path: "."}}}
	assert.Nil(t, RequiredMatcher(ws))
}

// projectsWorkspace is the narrow slice of the reader the matcher uses.
type projectsWorkspace struct {
	types.WorkspaceReader
	projects []*types.Project
}

func (s *projectsWorkspace) All() []*types.Project { return s.projects }

// TestScopeAck is the editor-freedom path: a reader who read three files in vim records
// those three, without claiming the other thirty and without opening magus's viewer.
func TestScopeAck(t *testing.T) {
	rev := diff.Diff{Files: []diff.File{
		{Path: "a.go", Role: diff.RoleSource},
		{Path: "b.go", Role: diff.RoleSource},
		{Path: "c.go", Role: diff.RoleSource},
	}}

	t.Run("no paths covers the whole changeset", func(t *testing.T) {
		got, err := ScopeAck(rev, nil)
		require.NoError(t, err)
		assert.Len(t, got.Files, 3)
	})

	t.Run("named paths narrow it", func(t *testing.T) {
		got, err := ScopeAck(rev, []string{"a.go", "c.go"})
		require.NoError(t, err)
		require.Len(t, got.Files, 2)
		assert.Equal(t, "a.go", got.Files[0].Path)
		assert.Equal(t, "c.go", got.Files[1].Path)
	})

	t.Run("a leading ./ still matches", func(t *testing.T) {
		got, err := ScopeAck(rev, []string{"./a.go"})
		require.NoError(t, err)
		require.Len(t, got.Files, 1)
	})

	// A typo that quietly acknowledged nothing would leave the reader believing they had
	// recorded work they had not.
	t.Run("a path outside the changeset is an error, not a no-op", func(t *testing.T) {
		_, err := ScopeAck(rev, []string{"a.go", "nope.go"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a changed file")
	})
}

// TestReviewedContentDigestsTheRevisionNotTheCheckout is the one that matters for --rev.
//
// The bug it exists to catch is silent and permanent: minting a receipt for a colleague's branch
// from the reader's own working tree stamps content nobody read, and Covers then agrees with it
// forever. Both halves are asserted, because a digest function that returned "" for everything
// would pass the negative half alone.
func TestReviewedContentDigestsTheRevisionNotTheCheckout(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("what my checkout holds\n"), 0o644))

	atRevision := Content{
		root: root,
		at:   vcs.Checkpoint{Revision: "cafe1234", Branch: "feat/audience", VCS: "git"},
		read: func(rev, path string) (string, error) {
			assert.Equal(t, "cafe1234", rev, "the resolved id is what gets read, never the branch name")
			assert.Equal(t, "a.go", path)
			return "what the branch holds\n", nil
		},
	}

	assert.Equal(t, Digest([]byte("what the branch holds\n")), atRevision.Digest("a.go"))
	assert.NotEqual(t, DigestFile(filepath.Join(root, "a.go")), atRevision.Digest("a.go"),
		"a range receipt must not fingerprint the reader's own checkout")

	// The working tree is still the working tree when no revision is named.
	assert.Equal(t, DigestFile(filepath.Join(root, "a.go")), Content{root: root}.Digest("a.go"))
}

// A file absent at the revision has nothing anyone can have read, and must not become a receipt
// against "", which Covers would otherwise satisfy for every unreadable file forever.
func TestReviewedContentYieldsNothingForAFileAbsentAtTheRevision(t *testing.T) {
	c := Content{
		root: t.TempDir(),
		at:   vcs.Checkpoint{Revision: "cafe1234"},
		read: func(string, string) (string, error) { return "", fmt.Errorf("path does not exist at that revision") },
	}

	assert.Empty(t, c.Digest("gone.go"))
}
