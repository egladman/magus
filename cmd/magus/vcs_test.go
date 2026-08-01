package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusPaths pins the porcelain parse.
//
// This exists because the bug it prevents was silent and total: DirtyFiles
// returns status LINES despite its name, every existing caller only tested the
// result for emptiness, and handing those lines to the classifier unparsed made
// every entry look like " M foo". Nothing matched a declared glob, so the first
// working version would have reported the entire workspace as one undeclared
// blob - or, with --untracked, staged it.
func TestStatusPaths(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string
	}{
		{"modified", []string{" M cmd/magus/agent.go"}, []string{"cmd/magus/agent.go"}},
		{"staged add", []string{"A  docs/new.md"}, []string{"docs/new.md"}},
		{"untracked", []string{"?? scratch.txt"}, []string{"scratch.txt"}},
		{"both columns", []string{"MM internal/agent/catalog.go"}, []string{"internal/agent/catalog.go"}},
		// A rename must stage the NEW name; the old one no longer exists.
		{"rename", []string{"R  old/path.go -> new/path.go"}, []string{"new/path.go"}},
		// git quotes a path containing whitespace or unusual bytes.
		{"quoted path", []string{` M "docs/a file.md"`}, []string{"docs/a file.md"}},
		{"several", []string{" M a.go", "?? b.go"}, []string{"a.go", "b.go"}},
		{"clean tree", nil, []string{}},
		{"short line ignored", []string{"x"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, statusPaths(tt.lines))
		})
	}
}

// TestClassifyForStaging pins the grouping, and with it the staging doctrine:
// a generated output belongs in the SAME commit as the source that moved it, so
// both are staged; an undeclared path is the hazard `git add -A` poses, so it is
// reported instead.
func TestClassifyForStaging(t *testing.T) {
	out := []types.FileEntry{
		{Path: "cmd/magus/agent.go", Role: "source"},
		{Path: "MAGUS.md", Role: "output"},
		{Path: "docs/gen/index.html", Role: "output"},
		{Path: "scratch.txt", Role: "unclaimed"},
		{Path: "notes.md", Role: ""},
	}

	sources, outputs, undeclared := classifyForStaging(out)
	assert.Equal(t, []string{"cmd/magus/agent.go"}, sources)
	assert.Equal(t, []string{"MAGUS.md", "docs/gen/index.html"}, outputs)
	assert.Equal(t, []string{"scratch.txt", "notes.md"}, undeclared,
		"anything not declared source or output is undeclared, including an empty role")
}

// TestFilterStageable pins the fix for a data-loss bug: `magus vcs add`
// collects a project's DECLARED outputs and hands the whole list to one `git
// add` call. If one declared path no longer exists on disk - e.g. a directory
// was renamed and a stale declaration still points at the old location -
// `git add` fails on that ONE pathspec with "did not match any files", and
// git aborts the WHOLE invocation before staging anything else. The user
// believes they staged their work and did not.
//
// filterStageable must keep every path that still exists, keep a path that is
// gone from disk but still tracked by git (that is how a deletion or a rename
// gets recorded - dropping it would make `vcs add` unable to ever commit a
// removal), and drop only a path that is neither on disk nor known to git.
func TestFilterStageable(t *testing.T) {
	dir := initGitRepo(t)

	// tracked.txt is committed, then deleted from disk without `git rm` -
	// the "old half of a rename" case: gone from disk, still tracked.
	trackedPath := filepath.Join(dir, "tracked.txt")
	require.NoError(t, os.WriteFile(trackedPath, []byte("x"), 0o644))
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-m", "seed")
	require.NoError(t, os.Remove(trackedPath))

	// present.txt exists on disk and was never committed - the ordinary
	// new-file case.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "present.txt"), []byte("y"), 0o644))

	// stale.txt is a declared output that never existed on disk and was
	// never tracked - e.g. libs/diag/MAGUS.md after libs/diag was renamed to
	// libs/diagnostics. This is the path that must be dropped, not fed to
	// `git add`.
	paths := []string{"tracked.txt", "present.txt", "stale.txt"}

	stageable, dropped, err := filterStageable(context.Background(), dir, paths)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"tracked.txt", "present.txt"}, stageable)
	assert.Equal(t, []string{"stale.txt"}, dropped)
}

// TestStagePathsSurvivesStalePath proves the end-to-end fix: a `git add` call
// that includes one nonexistent, untracked path must still stage the other
// paths, rather than aborting with exit 128 and staging nothing. This is the
// exact failure observed in the field:
//
//	staged 3 undeclared file(s) (--untracked):
//	  .gitattributes
//	  libs/diag/go.mod
//	  libs/diagnostics/
//	fatal: pathspec 'libs/diag/MAGUS.md' did not match any files
//	[error] vcs add: git add: exit status 128
//
// where every one of the first three paths was silently left unstaged.
// Without the fix in stagePaths, this test fails with exactly that error and
// an empty `git diff --cached --name-only`.
func TestStagePathsSurvivesStalePath(t *testing.T) {
	dir := initGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "present.txt"), []byte("y"), 0o644))

	err := stagePaths(context.Background(), dir, []string{"present.txt", "stale.txt"})
	require.NoError(t, err)

	out, err := exec.Command("git", "-C", dir, "diff", "--cached", "--name-only").Output()
	require.NoError(t, err)
	assert.Equal(t, "present.txt\n", string(out),
		"present.txt must be staged even though stale.txt was handed to the same git add call")
}

// initGitRepo creates a fresh git repo in a temp dir with an identity
// configured, so a commit can be made in it. Mirrors the skip-on-missing-git
// convention in TestParseBisectCulprit.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v\n%s", err, out)
	}
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}
