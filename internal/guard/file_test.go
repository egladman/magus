package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// populate writes dir with name, so a fixture reads as the directory it describes.
func populate(t *testing.T, root, dir string, names ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, name), []byte("x\n"), 0o644))
	}
}

// The case this rule exists for: a name picked by analogy with the file the unit came
// from, in a directory whose own names it never read.
func TestAdviseNewFileNameListsTheSiblingsItWillJoin(t *testing.T) {
	root := inWorkspace(t)
	populate(t, root, filepath.Join("internal", "guard"), "advisory.go", "cachedir.go", "focus.go")

	got := adviseNewFileName(filepath.Join("internal", "guard", "lease.go"))

	assert.Contains(t, got, "NEW FILE")
	assert.Contains(t, got, "advisory.go, cachedir.go, focus.go", "the siblings ARE the advisory")
}

// The rule runs BEFORE the write, so a file that exists is an edit. An edit chooses no
// name, and firing on one would repeat the advice on every later write to the file.
func TestAdviseNewFileNameIsSilentOnAnEdit(t *testing.T) {
	root := inWorkspace(t)
	populate(t, root, "pkg", "existing.go", "other.go")

	assert.Empty(t, adviseNewFileName(filepath.Join("pkg", "existing.go")))
}

// The empty directory belongs to adviseNewSourceDir, which asks the bigger question.
// Neither rule may answer for the other, and a directory of subdirectories is neither's.
func TestAdviseNewFileNameLeavesTheEmptyDirectoryToTheBoundaryRule(t *testing.T) {
	root := inWorkspace(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "cache"), 0o755))

	assert.Empty(t, adviseNewFileName(filepath.Join("newpkg", "thing.go")), "nothing there yet")
	assert.Empty(t, adviseNewFileName(filepath.Join("internal", "helper.go")), "internal/ holds packages, not files")
	assert.Contains(t, adviseNewSourceDir(filepath.Join("newpkg", "thing.go")), "creates a NEW DIRECTORY",
		"the case this rule declines is the case that one takes")
}

// Wrong-firing case 1: the name was computed from a sibling, not chosen, so there is no
// convention left to read and the session's one firing would buy nothing.
func TestAdviseNewFileNameIsSilentOnADerivedName(t *testing.T) {
	root := inWorkspace(t)
	populate(t, root, filepath.Join("internal", "guard"), "sourcedir.go", "advisory.go")

	assert.Empty(t, adviseNewFileName(filepath.Join("internal", "guard", "sourcedir_test.go")))
}

// A name hung off the directory's EPONYMOUS file is one somebody picked, so the
// derived-name suppression must not reach it and the list still has something to say.
func TestAdviseNewFileNameStillSpeaksForANameOffTheEponymousFile(t *testing.T) {
	root := inWorkspace(t)
	populate(t, root, filepath.Join("internal", "guard"), "guard.go", "advisory.go", "cachedir.go")

	got := adviseNewFileName(filepath.Join("internal", "guard", "guard_thing.go"))

	assert.Contains(t, got, "NEW FILE")
	assert.Contains(t, got, "advisory.go, cachedir.go, guard.go", "the list is what names the convention")
}

// The host sends an ABSOLUTE path and this repo is routinely checked out under
// .claude/worktrees/<name>. Scanning the absolute form finds `.claude`, calls it hidden,
// and disables the rule in the layout the repo's own workflow uses, which is how the
// sibling rule once shipped inert with a green suite.
func TestAdviseNewFileNameHandlesTheAbsolutePathTheHostSends(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, ".claude", "worktrees", "feature-x")
	require.NoError(t, os.MkdirAll(ws, 0o755))
	t.Chdir(ws)
	populate(t, ws, filepath.Join("internal", "guard"), "advisory.go")

	assert.Contains(t, adviseNewFileName(filepath.Join(ws, "internal", "guard", "lease.go")), "NEW FILE")
}

// Fixtures, pruned trees and anything outside the workspace are not places anyone is
// choosing a name, and new files appear in them constantly.
func TestAdviseNewFileNameIgnoresFixtureAndPrunedTrees(t *testing.T) {
	root := inWorkspace(t)
	for _, dir := range []string{"testdata", "fixtures", "__snapshots__", "gen", "vendor", "node_modules", ".hidden"} {
		populate(t, root, filepath.Join(dir, "case"), "existing.json")
		assert.Empty(t, adviseNewFileName(filepath.Join(dir, "case", "added.json")), dir)
	}
	populate(t, root, ".", "main.go")
	assert.Empty(t, adviseNewFileName("other.go"), "the workspace root is not a directory anyone is choosing")
	assert.Empty(t, adviseNewFileName(filepath.Join("..", "sibling", "x.go")), "outside the tree is not this workspace's business")
}

// A wall of names is a wall nobody reads, and the advisory's whole cost is what it
// spends of the session's one firing.
func TestAdviseNewFileNameBoundsTheList(t *testing.T) {
	root := inWorkspace(t)
	names := make([]string, 0, 20)
	for _, letter := range "abcdefghijklmnopqrst" {
		names = append(names, string(letter)+".go")
	}
	populate(t, root, "wide", names...)

	got := adviseNewFileName(filepath.Join("wide", "zzz.go"))

	assert.Contains(t, got, "already holds 20")
	assert.Contains(t, got, "and 8 more")
	assert.NotContains(t, got, "t.go", "past the cap")
	assert.Less(t, strings.Count(got, ".go,"), siblingsShown+1)
}
