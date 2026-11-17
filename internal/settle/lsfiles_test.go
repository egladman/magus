package settle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		filter    LsFilesFilter
		want      []string
		excluded  []string
		unmatched []string
	}{
		{name: "default", want: base,
			excluded: []string{"held", "staged", "untracked", "unclaimed", "unexplained", "generated"}},
		{name: "include untracked", filter: LsFilesFilter{Include: map[string]bool{LsGroupUntracked: true}},
			want: []string{"libs/x/b.go", "src/a.go", "src/deleted.go", "src/new.go"}},
		{name: "include generated", filter: LsFilesFilter{Include: map[string]bool{LsGroupGenerated: true}},
			want: []string{"gen/out.go", "libs/x/b.go", "src/a.go", "src/deleted.go"}},
		{name: "include unexplained", filter: LsFilesFilter{Include: map[string]bool{LsGroupUnexplained: true}},
			want: []string{"docs/gen/page.html", "libs/x/b.go", "src/a.go", "src/deleted.go"}},
		{name: "include unclaimed covers maintained", filter: LsFilesFilter{Include: map[string]bool{LsGroupUnclaimed: true}},
			want: []string{".gitattributes", "libs/x/b.go", "scratch.txt", "src/a.go", "src/deleted.go"}},
		{name: "include held", filter: LsFilesFilter{Include: map[string]bool{LsGroupHeld: true}},
			want: []string{"libs/x/b.go", "src/a.go", "src/deleted.go", "web/c.ts"}},
		{name: "include staged", filter: LsFilesFilter{Include: map[string]bool{LsGroupStaged: true}},
			want: []string{"libs/x/b.go", "src/a.go", "src/deleted.go", "src/staged.go"}},
		{name: "a pattern narrows", filter: LsFilesFilter{Patterns: []string{"src/**"}},
			want: []string{"src/a.go", "src/deleted.go"}},
		{name: "a pattern never forces a path in", filter: LsFilesFilter{Patterns: []string{"scratch.txt"}},
			want: []string{}, excluded: []string{"unclaimed"}},
		{name: "a literal directory covers what is under it", filter: LsFilesFilter{Patterns: []string{"libs"}},
			want: []string{"libs/x/b.go"}},
		{name: "exclude removes matches", filter: LsFilesFilter{Exclude: []string{"src/deleted.go", "libs/**"}},
			want: []string{"src/a.go"}},
		{name: "project scopes to the owning project", filter: LsFilesFilter{Projects: []string{"libs/x"}},
			want: []string{"libs/x/b.go"}},
		{name: "a pattern matching no dirty path is named", filter: LsFilesFilter{Patterns: []string{"src/a.go", "nothing/**"}},
			want: []string{"src/a.go"}, unmatched: []string{"nothing/**"}},
		{name: "a project with no dirty path is named", filter: LsFilesFilter{Projects: []string{"api"}},
			want: []string{}, unmatched: []string{"api"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, unmatched := selectLsFiles(paths, tt.filter, "v0.0.0-test")
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
	}, LsFilesFilter{}, "v0.0.0-test")

	require.Len(t, got.Excluded, 2)
	assert.Equal(t, map[string]string{"web/c.ts": "sibling"}, got.Excluded[0].Holders)
	assert.Equal(t, []string{"docs/gen/page.html"}, got.Excluded[1].Paths)
	assert.NotEmpty(t, got.Excluded[1].Code)
	assert.NotEmpty(t, got.Excluded[1].URL)
}

// TestLsFilesRefusesStateTheBackendLacks: an --include flag for a state the backend does
// not keep is refused rather than answered with nothing.
func TestLsFilesRefusesStateTheBackendLacks(t *testing.T) {
	repo := testkit.NewRepository(t, nil)
	git := resolveGitDriver(t, repo.Root())
	require.NoError(t, LsFilesRefusal(git, map[string]bool{LsGroupStaged: true, LsGroupUntracked: true}))

	for _, name := range []string{"hg", "jj"} {
		res, err := vcs.Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{Name: name})
		require.NoError(t, err)
		err = LsFilesRefusal(res.VCS, map[string]bool{LsGroupStaged: true})
		require.Error(t, err, "%s keeps no staging index", name)
		assert.Contains(t, err.Error(), "--include-staged")
	}

	res, err := vcs.Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{Name: "jj"})
	require.NoError(t, err)
	err = LsFilesRefusal(res.VCS, map[string]bool{LsGroupUntracked: true})
	require.Error(t, err, "jj tracks every file in the working copy")
	assert.Contains(t, err.Error(), "--include-untracked")

	res, err = vcs.Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{Name: "hg"})
	require.NoError(t, err)
	require.NoError(t, LsFilesRefusal(res.VCS, map[string]bool{LsGroupUntracked: true}))
}

// TestWorkspaceRelPatterns pins that a pattern is read from where you stand, and that a
// path holding a literal `#` keeps the write-path grammar's escape through the move.
func TestWorkspaceRelPatterns(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "internal")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	t.Chdir(sub)

	got, err := WorkspaceRelPatterns(root, []string{"guard/**", "*.go", "./a#b.md", `c\#d.md`, "."})
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/guard/**", "internal/*.go", `internal/a\#b.md`, `internal/c\#d.md`, "internal"}, got)

	_, err = WorkspaceRelPatterns(root, []string{"../../elsewhere/**"})
	require.Error(t, err)
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
