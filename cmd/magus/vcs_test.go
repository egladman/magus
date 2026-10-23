package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/testkit"
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
// keeps a workspace load (and its .gitattributes write) out of git's index
// manipulation.
func TestSplitVCSVerb(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantVerb string
		wantRest []string
	}{
		{"plain", []string{"resolve"}, "resolve", nil},
		{"verb then args", []string{"ls-files", "a.go"}, "ls-files", []string{"a.go"}},
		{"flag before verb", []string{"-q", "merge-driver", "%O"}, "merge-driver", []string{"-q", "%O"}},
		{"long flag before verb", []string{"--silent", "ls-files"}, "ls-files", []string{"--silent"}},
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

// TestClassifyByRole pins the three-way split a session brief shows: anything not
// declared source or output is undeclared, including an empty role.
func TestClassifyByRole(t *testing.T) {
	out := []types.FileEntry{
		{Path: "cmd/magus/agent.go", Role: "source"},
		{Path: "MAGUS.md", Role: "output"},
		{Path: "docs/gen/index.html", Role: "output"},
		{Path: "scratch.txt", Role: "unclaimed"},
		{Path: "notes.md", Role: ""},
	}

	sources, outputs, undeclared := classifyByRole(out)
	assert.Equal(t, []string{"cmd/magus/agent.go"}, sources)
	assert.Equal(t, []string{"MAGUS.md", "docs/gen/index.html"}, outputs)
	assert.Equal(t, []string{"scratch.txt", "notes.md"}, undeclared)
}

// TestSelectLsFiles pins the default (tracked dirty sources nobody else holds, not yet
// staged) and that each --include flag adds exactly its own group back.
func TestSelectLsFiles(t *testing.T) {
	paths := []lsFilesPath{
		{Path: "src/a.go", Role: "source", Project: "."},
		{Path: "src/deleted.go", Role: "source", Project: "."},
		{Path: "libs/x/b.go", Role: "source", Project: "libs/x"},
		{Path: "src/new.go", Role: "source", Project: ".", Untracked: true},
		{Path: "gen/out.go", Role: "output", Project: "."},
		{Path: "docs/gen/page.html", Role: "output", Project: "docs", Unexplained: true},
		{Path: "scratch.txt", Role: "unclaimed", Project: "."},
		{Path: ".gitattributes", Role: "maintained", Project: "."},
		{Path: "web/c.ts", Role: "source", Project: ".", Held: "sibling"},
		{Path: "src/staged.go", Role: "source", Project: ".", Staged: true},
	}
	base := []string{"libs/x/b.go", "src/a.go", "src/deleted.go"}
	tests := []struct {
		name      string
		filter    lsFilesFilter
		want      []string
		excluded  []string
		unmatched []string
	}{
		{name: "default", want: base,
			excluded: []string{"held", "staged", "untracked", "unclaimed", "unexplained", "generated"}},
		{name: "include untracked", filter: lsFilesFilter{Include: map[string]bool{lsGroupUntracked: true}},
			want: []string{"libs/x/b.go", "src/a.go", "src/deleted.go", "src/new.go"}},
		{name: "include generated", filter: lsFilesFilter{Include: map[string]bool{lsGroupGenerated: true}},
			want: []string{"gen/out.go", "libs/x/b.go", "src/a.go", "src/deleted.go"}},
		{name: "include unexplained", filter: lsFilesFilter{Include: map[string]bool{lsGroupUnexplained: true}},
			want: []string{"docs/gen/page.html", "libs/x/b.go", "src/a.go", "src/deleted.go"}},
		{name: "include unclaimed covers maintained", filter: lsFilesFilter{Include: map[string]bool{lsGroupUnclaimed: true}},
			want: []string{".gitattributes", "libs/x/b.go", "scratch.txt", "src/a.go", "src/deleted.go"}},
		{name: "include held", filter: lsFilesFilter{Include: map[string]bool{lsGroupHeld: true}},
			want: []string{"libs/x/b.go", "src/a.go", "src/deleted.go", "web/c.ts"}},
		{name: "include staged", filter: lsFilesFilter{Include: map[string]bool{lsGroupStaged: true}},
			want: []string{"libs/x/b.go", "src/a.go", "src/deleted.go", "src/staged.go"}},
		{name: "a pattern narrows", filter: lsFilesFilter{Patterns: []string{"src/**"}},
			want: []string{"src/a.go", "src/deleted.go"}},
		{name: "a pattern never forces a path in", filter: lsFilesFilter{Patterns: []string{"scratch.txt"}},
			want: []string{}, excluded: []string{"unclaimed"}},
		{name: "a literal directory covers what is under it", filter: lsFilesFilter{Patterns: []string{"libs"}},
			want: []string{"libs/x/b.go"}},
		{name: "exclude removes matches", filter: lsFilesFilter{Exclude: []string{"src/deleted.go", "libs/**"}},
			want: []string{"src/a.go"}},
		{name: "project scopes to the owning project", filter: lsFilesFilter{Projects: []string{"libs/x"}},
			want: []string{"libs/x/b.go"}},
		{name: "a pattern matching no dirty path is named", filter: lsFilesFilter{Patterns: []string{"src/a.go", "nothing/**"}},
			want: []string{"src/a.go"}, unmatched: []string{"nothing/**"}},
		{name: "a project with no dirty path is named", filter: lsFilesFilter{Projects: []string{"api"}},
			want: []string{}, unmatched: []string{"api"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, unmatched := selectLsFiles(paths, tt.filter)
			assert.Equal(t, tt.want, got.Files)
			assert.Equal(t, tt.unmatched, unmatched)
			if tt.excluded == nil {
				return
			}
			groups := make([]string, 0, len(got.Excluded))
			for _, e := range got.Excluded {
				groups = append(groups, e.Group)
				assert.Equal(t, "--include-"+e.Group, e.Flag)
			}
			assert.Equal(t, tt.excluded, groups)
		})
	}
}

// TestSelectLsFilesNamesWhatItLeftOut pins the record a caller acts on: the held paths
// carry their holder, and the unexplained ones the drift code.
func TestSelectLsFilesNamesWhatItLeftOut(t *testing.T) {
	got, _ := selectLsFiles([]lsFilesPath{
		{Path: "web/c.ts", Role: "source", Held: "sibling"},
		{Path: "docs/gen/page.html", Role: "output", Unexplained: true},
	}, lsFilesFilter{})

	require.Len(t, got.Excluded, 2)
	assert.Equal(t, map[string]string{"web/c.ts": "sibling"}, got.Excluded[0].Holders)
	assert.Equal(t, []string{"docs/gen/page.html"}, got.Excluded[1].Paths)
	assert.NotEmpty(t, got.Excluded[1].Code)
	assert.NotEmpty(t, got.Excluded[1].URL)
}

// TestLsFilesRefusesStateTheBackendLacks: an --include flag for a state the backend does
// not keep is refused rather than answered with nothing.
func TestLsFilesRefusesStateTheBackendLacks(t *testing.T) {
	repo := testkit.NewRepo(t, nil)
	git := resolveGitDriver(t, repo.Root())
	require.NoError(t, lsFilesRefusal(git, map[string]bool{lsGroupStaged: true, lsGroupUntracked: true}))

	for _, name := range []string{"hg", "jj"} {
		res, err := vcs.Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{Name: name})
		require.NoError(t, err)
		err = lsFilesRefusal(res.VCS, map[string]bool{lsGroupStaged: true})
		require.Error(t, err, "%s keeps no staging index", name)
		assert.Contains(t, err.Error(), "--include-staged")
	}

	res, err := vcs.Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{Name: "jj"})
	require.NoError(t, err)
	err = lsFilesRefusal(res.VCS, map[string]bool{lsGroupUntracked: true})
	require.Error(t, err, "jj tracks every file in the working copy")
	assert.Contains(t, err.Error(), "--include-untracked")

	res, err = vcs.Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{Name: "hg"})
	require.NoError(t, err)
	require.NoError(t, lsFilesRefusal(res.VCS, map[string]bool{lsGroupUntracked: true}))
}

// TestWorkspaceRelPatterns pins that a pattern is read from where you stand, and that a
// path holding a literal `#` keeps the write-path grammar's escape through the move.
func TestWorkspaceRelPatterns(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "internal")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	t.Chdir(sub)

	got, err := workspaceRelPatterns(root, []string{"guard/**", "*.go", "./a#b.md", `c\#d.md`, "."})
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/guard/**", "internal/*.go", `internal/a\#b.md`, `internal/c\#d.md`, "internal"}, got)

	_, err = workspaceRelPatterns(root, []string{"../../elsewhere/**"})
	require.Error(t, err)
}

// TestFilterStageable pins the fix for a data-loss bug. Staging hands a project's
// declared outputs to one `git add`; if one path no longer exists on disk (a renamed
// directory with a stale declaration), git fails that pathspec with "did not match any
// files" and aborts the WHOLE invocation, staging nothing. You think you staged and did
// not.
//
// So: keep every path that exists; keep a path gone from disk but still tracked, since
// that is how a deletion or rename gets recorded; drop only what is neither.
func TestFilterStageable(t *testing.T) {
	repo := testkit.NewRepo(t, nil)
	dir := repo.Root()

	// tracked.txt is committed, then deleted from disk without `git rm`, the "old half
	// of a rename" case: gone from disk, still tracked.
	trackedPath := repo.Write("tracked.txt", "x")
	repo.Commit("seed", nil)
	require.NoError(t, os.Remove(trackedPath))

	// present.txt exists on disk and was never committed: the ordinary new-file case.
	repo.Write("present.txt", "y")

	// stale.txt is a declared output that never existed on disk and was never tracked,
	// e.g. libs/diag/MAGUS.md after libs/diag was renamed to libs/diagnostics. This is
	// the path that must be dropped, not fed to `git add`.
	paths := []string{"tracked.txt", "present.txt", "stale.txt"}

	stageable, dropped, err := filterStageable(context.Background(), dir, resolveGitDriver(t, dir), paths)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"tracked.txt", "present.txt"}, stageable)
	assert.Equal(t, []string{"stale.txt"}, dropped)
}

// TestFilterStageableNonGit pins that the probe is skipped for backends without git's
// abort-the-whole-invocation behavior, rather than shelling out to a missing command.
func TestFilterStageableNonGit(t *testing.T) {
	dir := t.TempDir()
	res, err := vcs.Resolve(context.Background(), dir, "", types.VCSOptions{Name: "hg"})
	require.NoError(t, err)
	stageable, dropped, err := filterStageable(context.Background(), dir, res.VCS, []string{"gone.txt"})
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
	repo := testkit.NewRepo(t, nil)
	dir := repo.Root()
	repo.Write("present.txt", "y")

	staged, dropped, err := stagePaths(context.Background(), dir, resolveGitDriver(t, dir), []string{"present.txt", "stale.txt"})
	require.NoError(t, err)
	assert.Equal(t, []string{"present.txt"}, staged)
	assert.Equal(t, []string{"stale.txt"}, dropped, "the stale path is reported, not silently discarded")

	assert.Equal(t, "present.txt", repo.Git("diff", "--cached", "--name-only"),
		"present.txt must be staged even though stale.txt was handed to the same call")
}

// TestWorkspaceRelPathsRejectsEmptyRoot pins the guard on a caller bug that read as a user
// error: a caller passed the --root OVERRIDE (empty unless given) instead of the resolved
// workspace root, so a named path answered `is outside the workspace at ` with
// nothing after "at", blaming the path.
func TestWorkspaceRelPathsRejectsEmptyRoot(t *testing.T) {
	_, err := workspaceRelPaths("", []string{"docs/foo.md"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workspace root resolved")
	assert.NotContains(t, err.Error(), "outside the workspace",
		"an unresolved root must not be reported as a bad path")
}

// TestSplitExplainedOutputs proves vcs ls-files can tell a regenerated output apart from one
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

// TestMergeDriverArgCount covers the protocol contract: git always passes five placeholders,
// so anything shorter is a human at a prompt and must not report "resolved".
func TestMergeDriverArgCount(t *testing.T) {
	for name, args := range map[string][]string{
		"none":  {},
		"one":   {"ancestor"},
		"four":  {"ancestor", "result", "other", "7"},
		"empty": nil,
	} {
		t.Run(name, func(t *testing.T) {
			err := mergeDriverCmd(context.Background(), t.TempDir(), args)
			require.Error(t, err)
			assert.ErrorContains(t, err, "expected 5 arguments")
		})
	}
}

// TestMergeDriverRelPath covers both callers' path conventions: git passes a repo-relative
// path, hg an absolute one.
func TestMergeDriverRelPath(t *testing.T) {
	t.Run("git passes a repo-relative path through", func(t *testing.T) {
		got, err := mergeDriverRelPath(t.TempDir(), filepath.Join("gen", "catalog.md"))
		require.NoError(t, err)
		assert.Equal(t, "gen/catalog.md", got, "must be normalized to a slash path")
	})

	t.Run("hg passes an absolute path, made workspace-relative", func(t *testing.T) {
		w := testkit.NewWorkspace(t)
		w.Magusfile("magus.project({})\n")
		root := w.Root()

		got, err := mergeDriverRelPath(root, filepath.Join(root, "gen", "catalog.md"))
		require.NoError(t, err)
		assert.Equal(t, "gen/catalog.md", got)
	})
}

// TestSplitExplainedOutputsCountsCommittedSources pins the fix for a false positive that
// fired on the two most ordinary workflows: committing the source and then the generated
// output, and pulling and then regenerating. In both, the source change is already in a
// commit, so a check that reads only the working tree calls the output unaccounted for
// and refuses to stage it, on a commit that is entirely correct.
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

// TestOtherLeasesPaths: a whole-tree stage swept a sibling lease's uncommitted sources out
// of a shared checkout into the caller's commit (2026-10-14).
func TestOtherLeasesPaths(t *testing.T) {
	root := t.TempDir()
	rows := []types.Job{
		{ID: "mine", State: types.StateRunning, CheckoutRoot: root, WritePaths: []string{"api/**"}},
		{ID: "sibling", State: types.StateRunning, CheckoutRoot: root, WritePaths: []string{"web/**"}},
		{ID: "elsewhere", State: types.StateRunning, CheckoutRoot: t.TempDir(), WritePaths: []string{"docs/**"}},
		{ID: "ended", State: types.StatePass, CheckoutRoot: root, WritePaths: []string{"cli/**"}},
	}
	paths := []string{"api/a.go", "web/b.ts", "docs/c.md", "cli/d.go", "README.md"}

	assert.Equal(t, map[string]string{"web/b.ts": "sibling"}, otherLeasesPaths(rows, root, "mine", paths),
		"only a live lease writing in this checkout holds a path back from its sibling")
	assert.Equal(t, map[string]string{"api/a.go": "mine", "web/b.ts": "sibling"}, otherLeasesPaths(rows, root, "", paths),
		"a caller with no lease stages no lease's work")
}

// TestCheckpointLineCarriesThePreservedHandle covers the promise --preserve makes in its
// own usage text. The default rendering used to be byte-identical with and without the
// flag while one of the two spellings minted an object in the user's repository, so the
// handle that reaches it appeared nowhere a caller was told to look.
func TestCheckpointLineCarriesThePreservedHandle(t *testing.T) {
	cp := types.VCSCheckpoint{Revision: "abc123", Branch: "main", Dirty: true, PatchDigest: "d1d1"}

	assert.Equal(t, "abc123 main dirty d1d1", checkpointLine(cp),
		"a checkpoint that preserved nothing gained a field")

	cp.Preserved = "e5e5e5e5"
	assert.Equal(t, "abc123 main dirty d1d1 preserved e5e5e5e5", checkpointLine(cp))
}

// A settle hook never rebuilds ./magus, so when the merge outran the binary the failure has
// to name the command that does; any other load failure keeps the plain wording.
func TestSettleLoadFailureNamesTheBootstrapForAStaleBinary(t *testing.T) {
	stale := types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, `unknown built-in rule "architecture-unbriefed"`)
	got := settleLoadFailure(stale)
	assert.ErrorIs(t, got, types.WorkspaceNeedsNewerMagus, "the cause stays in the chain")
	assert.Contains(t, got.Error(), "older than the merged tree")
	assert.Contains(t, got.Error(), "`GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .`")

	plain := settleLoadFailure(errors.New("magusfile: syntax error"))
	assert.Equal(t, "the workspace did not load, so nothing regenerated: magusfile: syntax error", plain.Error())
}

// TestJobsWriting pins who settling by claims asks about a path: every job writing it, a
// whole-file or glob writer included so it can refuse the settle, but only a path some job
// claims by declaration is asked about at all.
func TestJobsWriting(t *testing.T) {
	rows := []types.Job{
		{ID: "a", WritePaths: []string{"a.go#A"}},
		{ID: "glob", WritePaths: []string{"**/*.go"}},
		{ID: "other", WritePaths: []string{"b.go#B", "docs/**"}},
	}
	writers, claimed := jobsWriting(rows, "a.go")
	assert.True(t, claimed)
	require.Len(t, writers, 2)
	assert.Equal(t, []string{"a", "glob"}, []string{writers[0].ID, writers[1].ID})

	writers, claimed = jobsWriting(rows, "c.go")
	assert.False(t, claimed, "only the glob writes c.go, and it claims no declaration")
	assert.Len(t, writers, 1)
}
