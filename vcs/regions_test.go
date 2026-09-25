package vcs

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const regionsGo = `package a

import "fmt"

func A() int {
	return 1
}

func B() int {
	return 2
}

func C() {
	fmt.Println("c")
}
`

const regionsMarkdown = `# Title
intro

## One
one

## Two
two
`

// changedFileChanges is what a caller hands Regions: ChangedFiles for base, as FileChanges.
func changedFileChanges(t *testing.T, dir, base string) []types.FileChange {
	t.Helper()
	paths, err := gitVCS{}.ChangedFiles(t.Context(), dir, base)
	require.NoError(t, err)
	files := make([]types.FileChange, len(paths))
	for i, p := range paths {
		files[i] = types.FileChange{Path: p}
	}
	return files
}

func region(path string, side types.RegionSide, from, to int, decl, driver string) types.RegionChange {
	return types.RegionChange{File: types.FileChange{Path: path}, Side: side, Lines: [2]int{from, to}, Declaration: decl, Driver: driver}
}

func TestRegions(t *testing.T) {
	const (
		declA = "func A() int {"
		declB = "func B() int {"
		declC = "func C() {"
	)
	was, now := types.RegionOld, types.RegionNew
	golang := func(side types.RegionSide, from, to int, decl string) types.RegionChange {
		return region("a.go", side, from, to, decl, "golang")
	}
	cases := []struct {
		name   string
		config map[string]string
		edit   func(t *testing.T, dir string)
		// files, when set, replaces ChangedFiles as the files to refine.
		files []types.FileChange
		want  []types.RegionChange
	}{
		{
			name: "a modified body is its own function on both sides",
			edit: func(t *testing.T, dir string) { replaceIn(t, dir, "a.go", "return 1", "return 10") },
			want: []types.RegionChange{golang(was, 6, 6, declA), golang(now, 6, 6, declA)},
		},
		{
			name: "a new function is named as itself and its neighbours are not reported",
			edit: func(t *testing.T, dir string) {
				replaceIn(t, dir, "a.go", "func B", "func New() int {\n\treturn 3\n}\n\nfunc B")
			},
			want: []types.RegionChange{golang(now, 9, 12, "func New() int {")},
		},
		{
			name: "a deleted function is named on the old side only",
			edit: func(t *testing.T, dir string) {
				replaceIn(t, dir, "a.go", "func B() int {\n\treturn 2\n}\n\n", "")
			},
			want: []types.RegionChange{golang(was, 9, 12, declB)},
		},
		{
			name: "an edit above the first declaration has no declaration",
			edit: func(t *testing.T, dir string) { replaceIn(t, dir, "a.go", `"fmt"`, `"os"`) },
			want: []types.RegionChange{golang(was, 3, 3, ""), golang(now, 3, 3, "")},
		},
		{
			name: "two separate edits in one file stay two regions",
			edit: func(t *testing.T, dir string) {
				replaceIn(t, dir, "a.go", "return 1", "return 10")
				replaceIn(t, dir, "a.go", `"c"`, `"see"`)
			},
			want: []types.RegionChange{
				golang(was, 6, 6, declA), golang(was, 14, 14, declC),
				golang(now, 6, 6, declA), golang(now, 14, 14, declC),
			},
		},
		{
			name: "a hunk spanning two declarations splits at the boundary",
			edit: func(t *testing.T, dir string) {
				replaceIn(t, dir, "a.go", "\treturn 1\n}\n\nfunc B() int {\n\treturn 2",
					"\treturn 11\n} // A\n// between\nfunc B2() int {\n\treturn 22")
			},
			want: []types.RegionChange{
				golang(was, 6, 8, declA), golang(was, 9, 10, declB),
				golang(now, 6, 8, declA), golang(now, 9, 10, "func B2() int {"),
			},
		},
		{
			name: "markdown lines land under their heading",
			edit: func(t *testing.T, dir string) {
				replaceIn(t, dir, "doc.md", "one\n", "one\nmore\n")
				replaceIn(t, dir, "doc.md", "two", "TWO")
			},
			want: []types.RegionChange{
				region("doc.md", was, 8, 8, "## Two", "markdown"),
				region("doc.md", now, 6, 6, "## One", "markdown"),
				region("doc.md", now, 9, 9, "## Two", "markdown"),
			},
		},
		{
			// git's default funcname would name "func A() int {" here.
			name: "a path with no driver reports its lines only",
			edit: func(t *testing.T, dir string) { replaceIn(t, dir, "plain.txt", "return 1", "return 10") },
			want: []types.RegionChange{
				region("plain.txt", was, 6, 6, "", ""),
				region("plain.txt", now, 6, 6, "", ""),
			},
		},
		{
			name:   "a driver defined in repository config is honoured",
			config: map[string]string{"diff.spell.xfuncname": "^target ([a-z]+)"},
			edit:   func(t *testing.T, dir string) { replaceIn(t, dir, "x.spell", "run two", "run 2") },
			want: []types.RegionChange{
				region("x.spell", was, 4, 4, "two", "spell"),
				region("x.spell", now, 4, 4, "two", "spell"),
			},
		},
		{
			name: "new, untracked, deleted and binary files",
			edit: func(t *testing.T, dir string) {
				writeRepoFile(t, dir, "added.go", "package a\n\nfunc D() {}\n")
				gitRun(t, dir, "add", "--", "added.go")
				writeRepoFile(t, dir, "untracked.go", "package a\n\nfunc E() {\n}\n")
				require.NoError(t, os.Remove(filepath.Join(dir, "doc.md")))
				writeRepoFile(t, dir, "blob.bin", "\x00\x01\x02changed")
			},
			want: []types.RegionChange{
				region("added.go", now, 1, 2, "", "golang"),
				region("added.go", now, 3, 3, "func D() {}", "golang"),
				region("doc.md", was, 1, 3, "# Title", "markdown"),
				region("doc.md", was, 4, 6, "## One", "markdown"),
				region("doc.md", was, 7, 8, "## Two", "markdown"),
				region("untracked.go", now, 1, 2, "", "golang"),
				region("untracked.go", now, 3, 4, "func E() {", "golang"),
			},
		},
		{
			name: "only the files given are refined, and an unchanged one yields nothing",
			edit: func(t *testing.T, dir string) {
				replaceIn(t, dir, "a.go", "return 1", "return 10")
				replaceIn(t, dir, "sub/s.txt", "s", "S")
				writeRepoFile(t, dir, "sub/new.txt", "n\n")
				writeRepoFile(t, dir, "elsewhere.txt", "e\n")
			},
			files: []types.FileChange{{Path: "sub/new.txt"}, {Path: "sub/s.txt"}, {Path: "plain.txt"}},
			want: []types.RegionChange{
				region("sub/new.txt", now, 1, 1, "", ""),
				region("sub/s.txt", was, 1, 1, "", ""),
				region("sub/s.txt", now, 1, 1, "", ""),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateGitConfig(t)
			dir := t.TempDir()
			gitInitRepo(t, dir, map[string]string{
				".gitattributes": "*.go diff=golang\n*.md diff=markdown\n*.spell diff=spell\n",
				"a.go":           regionsGo,
				"plain.txt":      regionsGo,
				"doc.md":         regionsMarkdown,
				"x.spell":        "target one\n\trun one\ntarget two\n\trun two\n",
				"sub/s.txt":      "s\n",
				"blob.bin":       "\x00\x01\x02",
			})
			for k, v := range tc.config {
				gitRun(t, dir, "config", k, v)
			}
			tc.edit(t, dir)

			files := tc.files
			if files == nil {
				files = changedFileChanges(t, dir, "HEAD")
			}
			got, err := gitVCS{}.Regions(t.Context(), dir, "HEAD", files)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Regions refine files, so no files is nothing to refine, never the whole tree.
func TestRegionsOfNoFilesIsNothing(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	writeRepoFile(t, dir, "a.txt", "b\n")

	got, err := gitVCS{}.Regions(t.Context(), dir, "HEAD", nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestRegionsRefusesABaseItCannotResolve(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	writeRepoFile(t, dir, "a.txt", "b\n")
	files := []types.FileChange{{Path: "a.txt"}}

	_, err := gitVCS{}.Regions(t.Context(), dir, "no-such-rev", files)
	require.Error(t, err)
	_, err = gitVCS{}.Regions(t.Context(), dir, "", files)
	require.Error(t, err, "an empty base is refused, not read as the working tree against itself")
	_, err = gitVCS{}.Regions(t.Context(), dir, "", nil)
	require.Error(t, err, "an empty base is refused even with no files to refine")
}

// Every region refines a file it was given, and is measured from the merge base as
// ChangedFiles is: on a branch behind its base, a file only base changed is handed in and
// still yields nothing, where diffing against base itself would report base's own commit
// reversed.
func TestRegionsStayInsideTheFilesGiven(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"mine.txt": "a\n", "theirs.txt": "a\n"})
	gitRun(t, dir, "branch", "-M", "main")
	gitRun(t, dir, "switch", "-q", "-c", "work")
	gitRun(t, dir, "switch", "-q", "main")
	writeRepoFile(t, dir, "theirs.txt", "b\n")
	gitRun(t, dir, "commit", "-q", "-am", "main moves on")
	gitRun(t, dir, "switch", "-q", "work")
	writeRepoFile(t, dir, "mine.txt", "b\n")
	writeRepoFile(t, dir, "new.txt", "c\n")

	changed := changedFileChanges(t, dir, "main")
	require.ElementsMatch(t, []types.FileChange{{Path: "mine.txt"}, {Path: "new.txt"}}, changed)
	given := slices.Concat(changed, []types.FileChange{{Path: "theirs.txt"}})
	regions, err := gitVCS{}.Regions(t.Context(), dir, "main", given)
	require.NoError(t, err)

	var files []types.FileChange
	for _, r := range regions {
		files = append(files, r.File)
	}
	require.NotEmpty(t, files)
	require.Subset(t, given, files, "a region outside the files given")
	require.Subset(t, changed, files, "a region outside ChangedFiles")
	require.NotContains(t, files, types.FileChange{Path: "theirs.txt"}, "main's own commit read as this branch's change")
}

func TestRegionsIgnoresConfigThatChangesTheParse(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{".gitattributes": "*.go diff=golang\n", "a.go": regionsGo})
	for k, v := range map[string]string{
		"diff.noprefix": "true", "diff.relative": "true", "diff.interHunkContext": "10",
		"diff.algorithm": "myers", "diff.indentHeuristic": "false", "color.ui": "always",
		"diff.srcPrefix": "x/", "diff.dstPrefix": "y/",
	} {
		gitRun(t, dir, "config", k, v)
	}
	replaceIn(t, dir, "a.go", "return 1", "return 10")
	replaceIn(t, dir, "a.go", `"c"`, `"see"`)

	got, err := gitVCS{}.Regions(t.Context(), dir, "HEAD", []types.FileChange{{Path: "a.go"}})
	require.NoError(t, err)
	assert.Equal(t, []types.RegionChange{
		region("a.go", types.RegionOld, 6, 6, "func A() int {", "golang"),
		region("a.go", types.RegionOld, 14, 14, "func C() {", "golang"),
		region("a.go", types.RegionNew, 6, 6, "func A() int {", "golang"),
		region("a.go", types.RegionNew, 14, 14, "func C() {", "golang"),
	}, got)
}

func TestParseZeroContextPatch(t *testing.T) {
	patch := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1,2 @@\n--- not a header\n+++ nor this\n+x\n" +
		"diff --git \"a/sp ace\\\"q\" \"b/sp ace\\\"q\"\n--- \"a/sp ace\\\"q\"\t\n+++ /dev/null\n@@ -3,2 +2,0 @@ ctx\n-a\n-b\n\\ No newline at end of file\n"
	got, err := parseZeroContextPatch(patch)
	require.NoError(t, err)
	assert.Equal(t, []patchFile{
		{path: "x", hunks: []patchHunk{{oldStart: 1, oldCount: 1, newStart: 1, newCount: 2}}},
		{path: "sp ace\"q", hunks: []patchHunk{{oldStart: 3, oldCount: 2, newStart: 2, newCount: 0}}},
	}, got)
}

// replaceIn replaces the single occurrence of from in the repository file name.
func replaceIn(t *testing.T, dir, name, from, to string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(body), from), "%q in %s", from, name)
	writeRepoFile(t, dir, name, strings.Replace(string(body), from, to, 1))
}
