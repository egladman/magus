package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The porcelain parse lives with the driver that produces those lines: DirtyFiles returns
// paths, so there is nothing here to parse. See vcs.TestGitStatusPaths for the shape table
// and vcs.TestParityDirtyFilesReturnsPaths for the cross-backend rule.

// TestSplitVCSVerb pins that the subcommand is found past a leading flag. Reading args[0]
// alone let `magus vcs -q merge-driver ...` miss the merge-driver dispatch profile, which
// keeps a workspace load - and its .gitattributes write - out of git's index
// manipulation.
func TestSplitVCSVerb(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantVerb string
		wantRest []string
	}{
		{"plain", []string{"resolve"}, "resolve", nil},
		{"verb then args", []string{"add", "a.go"}, "add", []string{"a.go"}},
		{"flag before verb", []string{"-q", "merge-driver", "%O"}, "merge-driver", []string{"-q", "%O"}},
		{"long flag before verb", []string{"--silent", "add"}, "add", []string{"--silent"}},
		{"help flag is its own verb", []string{"-h"}, "-h", nil},
		{"help flag after a flag", []string{"-q", "--help"}, "--help", []string{"-q"}},
		{"no verb at all", []string{"-q"}, "", []string{"-q"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verb, rest := splitVCSVerb(tt.args)
			assert.Equal(t, tt.wantVerb, verb)
			if tt.wantRest == nil {
				assert.Empty(t, rest)
				return
			}
			assert.Equal(t, tt.wantRest, rest)
		})
	}
}

func TestWorkspaceRelPaths(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "cmd", "magus")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	t.Chdir(sub)

	got, err := workspaceRelPaths(root, []string{"vcs.go", "../../README.md"})
	require.NoError(t, err)
	assert.Equal(t, []string{"cmd/magus/vcs.go", "README.md"}, got,
		"paths a user types are relative to their cwd, not the workspace root")

	got, err = workspaceRelPaths(root, []string{filepath.Join(root, "a.go")})
	require.NoError(t, err)
	assert.Equal(t, []string{"a.go"}, got, "an absolute path is made workspace-relative")

	_, err = workspaceRelPaths(root, []string{"../../../outside.go"})
	require.Error(t, err, "a path escaping the workspace is refused rather than silently addressed")

	got, err = workspaceRelPaths(root, nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

// TestClassifyForStaging pins the grouping, and the doctrine behind it: a generated
// output belongs in the SAME commit as the source that moved it, so both are staged;
// an undeclared path is the `git add -A` hazard, so it is reported instead.
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

func TestSplitMaintained(t *testing.T) {
	maintained, unclaimed := splitMaintained([]string{".gitattributes", "scratch.txt"})
	assert.Equal(t, []string{".gitattributes"}, maintained,
		"a file magus's own core writes is not residue, and must not be reported as such")
	assert.Equal(t, []string{"scratch.txt"}, unclaimed)
}

// TestFilterStageable pins the fix for a data-loss bug. `vcs add` hands a project's
// declared outputs to one `git add`; if one path no longer exists on disk (a renamed
// directory with a stale declaration), git fails that pathspec with "did not match any
// files" and aborts the WHOLE invocation, staging nothing. You think you staged and did
// not.
//
// So: keep every path that exists; keep a path gone from disk but still tracked, since
// that is how a deletion or rename gets recorded; drop only what is neither.
func TestFilterStageable(t *testing.T) {
	dir := initGitRepo(t)

	// tracked.txt is committed, then deleted from disk without `git rm` - the "old half
	// of a rename" case: gone from disk, still tracked.
	trackedPath := filepath.Join(dir, "tracked.txt")
	require.NoError(t, os.WriteFile(trackedPath, []byte("x"), 0o644))
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-m", "seed")
	require.NoError(t, os.Remove(trackedPath))

	// present.txt exists on disk and was never committed - the ordinary new-file case.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "present.txt"), []byte("y"), 0o644))

	// stale.txt is a declared output that never existed on disk and was never tracked -
	// e.g. libs/diag/MAGUS.md after libs/diag was renamed to libs/diagnostics. This is
	// the path that must be dropped, not fed to `git add`.
	paths := []string{"tracked.txt", "present.txt", "stale.txt"}

	stageable, dropped, err := filterStageable(context.Background(), dir, "git", paths)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"tracked.txt", "present.txt"}, stageable)
	assert.Equal(t, []string{"stale.txt"}, dropped)
}

// TestFilterStageableNonGit pins that the probe is skipped for backends without git's
// abort-the-whole-invocation behavior, rather than shelling out to a missing command.
func TestFilterStageableNonGit(t *testing.T) {
	dir := t.TempDir()
	stageable, dropped, err := filterStageable(context.Background(), dir, "hg", []string{"gone.txt"})
	require.NoError(t, err)
	assert.Equal(t, []string{"gone.txt"}, stageable)
	assert.Empty(t, dropped)
}

// TestStagePathsSurvivesStalePath proves the end-to-end fix: one nonexistent, untracked
// path in the batch must not abort the rest. The field failure:
//
//	staged 3 undeclared file(s) (--untracked):
//	  .gitattributes
//	  libs/diag/go.mod
//	  libs/diagnostics/
//	fatal: pathspec 'libs/diag/MAGUS.md' did not match any files
//	[error] vcs add: git add: exit status 128
//
// where every one of the first three paths was silently left unstaged.
func TestStagePathsSurvivesStalePath(t *testing.T) {
	dir := initGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "present.txt"), []byte("y"), 0o644))

	staged, dropped, err := stagePaths(context.Background(), dir, "git", gitConflictResolver(t, dir), []string{"present.txt", "stale.txt"})
	require.NoError(t, err)
	assert.Equal(t, []string{"present.txt"}, staged)
	assert.Equal(t, []string{"stale.txt"}, dropped, "the stale path is reported, not silently discarded")

	out, err := exec.Command("git", "-C", dir, "diff", "--cached", "--name-only").Output()
	require.NoError(t, err)
	assert.Equal(t, "present.txt\n", string(out),
		"present.txt must be staged even though stale.txt was handed to the same call")
}

// gitConflictResolver returns the git driver as the capability staging uses.
func gitConflictResolver(t *testing.T, dir string) types.ConflictResolver {
	t.Helper()
	res, err := vcs.Resolve(context.Background(), dir, "", types.VCSOptions{})
	require.NoError(t, err)
	cr, ok := res.VCS.(types.ConflictResolver)
	require.True(t, ok, "the git driver must implement types.ConflictResolver")
	return cr
}

// TestWorkspaceRelPathsRejectsEmptyRoot pins the guard on a caller bug that read as a user
// error: vcsAddCmd passed the --root OVERRIDE (empty unless given) instead of the resolved
// workspace root, so `magus vcs add <path>` answered `is outside the workspace at ` with
// nothing after "at", blaming the path.
func TestWorkspaceRelPathsRejectsEmptyRoot(t *testing.T) {
	_, err := workspaceRelPaths("", []string{"docs/foo.md"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workspace root resolved")
	assert.NotContains(t, err.Error(), "outside the workspace",
		"an unresolved root must not be reported as a bad path")
}

// TestSplitExplainedOutputs proves vcs add can tell a regenerated output apart from one
// nothing in this change accounts for. Classifying by glob alone made those identical.
//
// nil for the second argument is the working-tree-only case; the committed half is
// covered by TestSplitExplainedOutputsCountsCommittedSources.
func TestSplitExplainedOutputs(t *testing.T) {
	files := []types.FileEntry{
		{Path: "internal/foo.go", Role: "source", SourceOf: []string{"."}},
		{Path: "MAGUS.md", Role: "output", OutputOf: []string{"."}},
		{Path: "docs/gen/index.html", Role: "output", OutputOf: []string{"docs"}},
	}

	explained, unexplained := types.SplitExplainedOutputs(files, nil)

	assert.Equal(t, []string{"MAGUS.md"}, explained,
		"a root source moved, so the root's regenerated output belongs in the same commit")
	assert.Equal(t, []string{"docs/gen/index.html"}, unexplained,
		"nothing in this change feeds docs, so its output moved for some other reason")
}

// TestSplitExplainedOutputsGeneratedFileCannotExplainItself guards the trap that makes the
// naive version useless here: docs declares `**/*.md` as sources AND generates
// `reference/**/*.md` into that same tree, so a generated page matches both globs. Reading
// SourceOf off it anyway would let the docs tree account for its own regeneration, and
// every output would be "explained" forever.
func TestSplitExplainedOutputsGeneratedFileCannotExplainItself(t *testing.T) {
	files := []types.FileEntry{
		{Path: "docs/reference/cli.md", Role: "output", OutputOf: []string{"docs"}, SourceOf: []string{"docs"}},
		{Path: "docs/gen/index.html", Role: "output", OutputOf: []string{"docs"}},
	}

	explained, unexplained := types.SplitExplainedOutputs(files, nil)

	assert.Empty(t, explained, "a generated file is not a source change")
	assert.Equal(t, []string{"docs/reference/cli.md", "docs/gen/index.html"}, unexplained)
}

// TestProjectKeyIsPathNotLabel pins the contract settledPaths depends on: the rebuild set
// is keyed by project PATH, and for the ROOT that key is "." - never the display label,
// which Display renders as the directory basename. Keying one side by label was a real
// bug: the set held "." while the lookup asked for "magus" (or a worktree's own directory
// name), so it missed every time and every root-owned regenerated output was left
// modified and unstaged, which is what makes `git rebase --continue` refuse.
func TestProjectKeyIsPathNotLabel(t *testing.T) {
	root := &types.Project{Path: "", Dir: "/repos/magus"}
	dotted := &types.Project{Path: ".", Dir: "/repos/magus"}
	nested := &types.Project{Path: "docs", Dir: "/repos/magus/docs"}

	assert.Equal(t, ".", projectKey(root), "the root keys as \".\", whatever its directory is called")
	assert.Equal(t, ".", projectKey(dotted), "a root spelled \".\" keys the same as one spelled \"\"")
	assert.Equal(t, "docs", projectKey(nested))

	// The label is what this must NOT be, and only the root can tell the two apart.
	assert.NotEqual(t, types.ProjectLabel(root.Path, root.Dir), projectKey(root),
		"keying the root by its label is the bug; Display gives the directory basename")
	assert.Equal(t, types.ProjectLabel(nested.Path, nested.Dir), projectKey(nested),
		"a nested project's label and path agree, which is why the bug hid")
}

// TestRebuiltProjectsFindsRoot proves the two sides now meet: a plan filled the way
// planResolution fills it is readable the way settledPaths reads it, for the root project.
func TestRebuiltProjectsFindsRoot(t *testing.T) {
	root := &types.Project{Path: "", Dir: "/repos/magus"}
	plan := resolutionPlan{rebuild: map[string][]string{"generate": {projectKey(root)}}}

	assert.True(t, plan.rebuiltProjects()[projectKey(root)],
		"a root-owned regenerated output must be recognized as covered by the rebuild")
}

// initGitRepo creates a temp git repo with an identity, so a commit can be made in it.
// Skip-on-missing-git follows TestParseBisectCulprit.
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

// TestSplitExplainedOutputsCountsCommittedSources pins the fix for a false positive that
// fired on the two most ordinary workflows: committing the source and then the generated
// output, and pulling and then regenerating. In both, the source change is already in a
// commit, so a check that reads only the working tree calls the output unaccounted for
// and refuses to stage it - on a commit that is entirely correct.
func TestSplitExplainedOutputsCountsCommittedSources(t *testing.T) {
	// Only the output is dirty; the source that produced it is already committed.
	files := []types.FileEntry{
		{Path: "MAGUS.md", Role: "output", OutputOf: []string{"."}},
	}

	_, unexplained := types.SplitExplainedOutputs(files, nil)
	assert.Equal(t, []string{"MAGUS.md"}, unexplained,
		"with nothing but the working tree to go on, it looks unaccounted for")

	explained, unexplained := types.SplitExplainedOutputs(files, map[string]bool{".": true})
	assert.Equal(t, []string{"MAGUS.md"}, explained,
		"a source change committed since the base ref accounts for it just as well")
	assert.Empty(t, unexplained)
}
