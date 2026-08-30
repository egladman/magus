package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inWorkspace puts the test in a working directory the rule will judge, the way the
// hook runs with the workspace as its cwd.
func inWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	return root
}

// The case this rule exists for: a boundary nobody asked for, about to be created
// with the skill that would have questioned it installed and unread.
func TestAdviseNewSourceDirFiresOnTheFirstFile(t *testing.T) {
	inWorkspace(t)
	got := adviseNewSourceDir(filepath.Join("kvdir", "kvdir.go"))

	assert.Contains(t, got, "creates a NEW DIRECTORY")
	assert.Contains(t, got, "magus-architecture-review", "the advisory has to name the installed skill exactly, or it routes into a wall")
}

// The host sends an ABSOLUTE path, and this repo is routinely checked out under
// .claude/worktrees/<name>. Scanning the absolute form finds `.claude`, calls it
// hidden, and disables the rule in the layout the repo's own workflow uses - which
// is how this shipped inert while every test here passed.
func TestAdviseNewSourceDirHandlesTheAbsolutePathTheHostSends(t *testing.T) {
	root := t.TempDir()
	// A workspace living under a dot-directory, as a worktree checkout does.
	ws := filepath.Join(root, ".claude", "worktrees", "feature-x")
	require.NoError(t, os.MkdirAll(ws, 0o755))
	t.Chdir(ws)

	got := adviseNewSourceDir(filepath.Join(ws, "internal", "newpkg", "x.go"))
	assert.Contains(t, got, "creates a NEW DIRECTORY",
		"a dot-directory ABOVE the workspace is not part of the path being judged")
}

// Language-agnostic by construction: the boundary is the directory, so the rule
// never needs to know what a source file looks like in your language.
func TestAdviseNewSourceDirIsLanguageAgnostic(t *testing.T) {
	inWorkspace(t)
	for i, name := range []string{"mod.rs", "__init__.py", "index.ts", "Main.java", "lib.buzz"} {
		dir := filepath.Join("newthing", string(rune('a'+i)))
		assert.Contains(t, adviseNewSourceDir(filepath.Join(dir, name)), "creates a NEW DIRECTORY", name)
	}
}

// A directory holding only SUBDIRECTORIES is a tree of packages, not a new boundary.
// internal/, cmd/ and libs/ in this repo all hold zero plain files.
func TestAdviseNewSourceDirIsSilentOnADirectoryOfPackages(t *testing.T) {
	root := inWorkspace(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "cache"), 0o755))

	assert.Empty(t, adviseNewSourceDir(filepath.Join("internal", "helper.go")),
		"internal/ holds 40 packages; it is not new")
}

func TestAdviseNewSourceDirIsSilentOnAnExistingDirectory(t *testing.T) {
	root := inWorkspace(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkg", "existing.go"), []byte("package p\n"), 0o644))

	assert.Empty(t, adviseNewSourceDir(filepath.Join("pkg", "another.go")))
}

// Editing the sole file of a one-file package must stay silent. The rule runs BEFORE
// the write, so a file that already exists means the directory already had it -
// treating it as absent re-fires the advice on every later edit, forever.
func TestAdviseNewSourceDirIsSilentEditingASoleFile(t *testing.T) {
	root := inWorkspace(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cmd", "tool"), 0o755))
	path := filepath.Join("cmd", "tool", "main.go")
	require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte("package main\n"), 0o644))

	assert.Empty(t, adviseNewSourceDir(path))
}

// Test data and generated trees are not boundaries anyone is choosing, and new ones
// appear constantly during ordinary work.
func TestAdviseNewSourceDirIgnoresFixtureAndPrunedTrees(t *testing.T) {
	inWorkspace(t)
	for _, rel := range []string{
		"testdata/case1/input.json",
		"fixtures/thing/x.yaml",
		"__snapshots__/a/b.snap",
		"gen/types/out.go",
		"vendor/x/y.go",
		"node_modules/pkg/index.js",
		".hidden/thing/x.go",
	} {
		assert.Empty(t, adviseNewSourceDir(rel), rel)
	}
	assert.Empty(t, adviseNewSourceDir("main.go"), "the workspace root is not a boundary anyone is choosing")
}

// A path outside this tree is not a decision about this workspace. An existing
// --path test pins this: the guard fails open on anything it cannot classify.
func TestAdviseNewSourceDirIgnoresPathsOutsideTheTree(t *testing.T) {
	inWorkspace(t)
	assert.Empty(t, adviseNewSourceDir("/nonexistent/elsewhere.txt"))
	assert.Empty(t, adviseNewSourceDir(filepath.Join("..", "sibling", "x.go")))
}
