package vcs

import (
	"os"
	"path/filepath"
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

func TestChangedRegions(t *testing.T) {
	const (
		declA = "func A() int {"
		declB = "func B() int {"
		declC = "func C() {"
	)
	was, now := types.RegionOld, types.RegionNew
	golang := func(side types.RegionSide, from, to int, decl string) types.ChangedRegion {
		return types.ChangedRegion{Path: "a.go", Side: side, Lines: [2]int{from, to}, Declaration: decl, Driver: "golang"}
	}
	cases := []struct {
		name   string
		config map[string]string
		edit   func(t *testing.T, dir string)
		paths  []string
		want   []types.ChangedRegion
	}{
		{
			name: "a modified body is its own function on both sides",
			edit: func(t *testing.T, dir string) { replaceIn(t, dir, "a.go", "return 1", "return 10") },
			want: []types.ChangedRegion{golang(was, 6, 6, declA), golang(now, 6, 6, declA)},
		},
		{
			name: "a new function is named as itself and its neighbours are not reported",
			edit: func(t *testing.T, dir string) {
				replaceIn(t, dir, "a.go", "func B", "func New() int {\n\treturn 3\n}\n\nfunc B")
			},
			want: []types.ChangedRegion{golang(now, 9, 12, "func New() int {")},
		},
		{
			name: "a deleted function is named on the old side only",
			edit: func(t *testing.T, dir string) {
				replaceIn(t, dir, "a.go", "func B() int {\n\treturn 2\n}\n\n", "")
			},
			want: []types.ChangedRegion{golang(was, 9, 12, declB)},
		},
		{
			name: "an edit above the first declaration has no declaration",
			edit: func(t *testing.T, dir string) { replaceIn(t, dir, "a.go", `"fmt"`, `"os"`) },
			want: []types.ChangedRegion{golang(was, 3, 3, ""), golang(now, 3, 3, "")},
		},
		{
			name: "two separate edits in one file stay two regions",
			edit: func(t *testing.T, dir string) {
				replaceIn(t, dir, "a.go", "return 1", "return 10")
				replaceIn(t, dir, "a.go", `"c"`, `"see"`)
			},
			want: []types.ChangedRegion{
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
			want: []types.ChangedRegion{
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
			want: []types.ChangedRegion{
				{Path: "doc.md", Side: was, Lines: [2]int{8, 8}, Declaration: "## Two", Driver: "markdown"},
				{Path: "doc.md", Side: now, Lines: [2]int{6, 6}, Declaration: "## One", Driver: "markdown"},
				{Path: "doc.md", Side: now, Lines: [2]int{9, 9}, Declaration: "## Two", Driver: "markdown"},
			},
		},
		{
			// git's default funcname would name "func A() int {" here.
			name: "a path with no driver reports its lines only",
			edit: func(t *testing.T, dir string) { replaceIn(t, dir, "plain.txt", "return 1", "return 10") },
			want: []types.ChangedRegion{
				{Path: "plain.txt", Side: was, Lines: [2]int{6, 6}},
				{Path: "plain.txt", Side: now, Lines: [2]int{6, 6}},
			},
		},
		{
			name:   "a driver defined in repository config is honoured",
			config: map[string]string{"diff.spell.xfuncname": "^target ([a-z]+)"},
			edit:   func(t *testing.T, dir string) { replaceIn(t, dir, "x.spell", "run two", "run 2") },
			want: []types.ChangedRegion{
				{Path: "x.spell", Side: was, Lines: [2]int{4, 4}, Declaration: "two", Driver: "spell"},
				{Path: "x.spell", Side: now, Lines: [2]int{4, 4}, Declaration: "two", Driver: "spell"},
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
			want: []types.ChangedRegion{
				{Path: "added.go", Side: now, Lines: [2]int{1, 2}, Driver: "golang"},
				{Path: "added.go", Side: now, Lines: [2]int{3, 3}, Declaration: "func D() {}", Driver: "golang"},
				{Path: "doc.md", Side: was, Lines: [2]int{1, 3}, Declaration: "# Title", Driver: "markdown"},
				{Path: "doc.md", Side: was, Lines: [2]int{4, 6}, Declaration: "## One", Driver: "markdown"},
				{Path: "doc.md", Side: was, Lines: [2]int{7, 8}, Declaration: "## Two", Driver: "markdown"},
				{Path: "untracked.go", Side: now, Lines: [2]int{1, 2}, Driver: "golang"},
				{Path: "untracked.go", Side: now, Lines: [2]int{3, 4}, Declaration: "func E() {", Driver: "golang"},
			},
		},
		{
			name: "paths keep only what they name, a directory keeping what is under it",
			edit: func(t *testing.T, dir string) {
				replaceIn(t, dir, "a.go", "return 1", "return 10")
				replaceIn(t, dir, "sub/s.txt", "s", "S")
				writeRepoFile(t, dir, "sub/new.txt", "n\n")
				writeRepoFile(t, dir, "elsewhere.txt", "e\n")
			},
			paths: []string{"sub"},
			want: []types.ChangedRegion{
				{Path: "sub/new.txt", Side: now, Lines: [2]int{1, 1}},
				{Path: "sub/s.txt", Side: was, Lines: [2]int{1, 1}},
				{Path: "sub/s.txt", Side: now, Lines: [2]int{1, 1}},
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

			got, err := gitVCS{}.ChangedRegions(t.Context(), dir, "HEAD", tc.paths)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestChangedRegionsRefusesABaseItCannotResolve(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	writeRepoFile(t, dir, "a.txt", "b\n")

	_, err := gitVCS{}.ChangedRegions(t.Context(), dir, "no-such-rev", nil)
	require.Error(t, err)
	_, err = gitVCS{}.ChangedRegions(t.Context(), dir, "", nil)
	require.Error(t, err, "an empty base is refused, not read as the working tree against itself")
}

// Regions refine ChangedFiles: every region's path is one ChangedFiles reports, even on a
// branch behind its base, where diffing against base itself would report base's own
// commits reversed.
func TestChangedRegionsStayInsideChangedFiles(t *testing.T) {
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

	files, err := gitVCS{}.ChangedFiles(t.Context(), dir, "main")
	require.NoError(t, err)
	regions, err := gitVCS{}.ChangedRegions(t.Context(), dir, "main", nil)
	require.NoError(t, err)

	var paths []string
	for _, r := range regions {
		paths = append(paths, r.Path)
	}
	require.ElementsMatch(t, []string{"mine.txt", "new.txt"}, files)
	require.Subset(t, files, paths, "a region outside ChangedFiles")
	require.NotContains(t, paths, "theirs.txt", "main's own commit read as this branch's change")
}

func TestChangedRegionsIgnoresConfigThatChangesTheParse(t *testing.T) {
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

	got, err := gitVCS{}.ChangedRegions(t.Context(), dir, "HEAD", nil)
	require.NoError(t, err)
	assert.Equal(t, []types.ChangedRegion{
		{Path: "a.go", Side: types.RegionOld, Lines: [2]int{6, 6}, Declaration: "func A() int {", Driver: "golang"},
		{Path: "a.go", Side: types.RegionOld, Lines: [2]int{14, 14}, Declaration: "func C() {", Driver: "golang"},
		{Path: "a.go", Side: types.RegionNew, Lines: [2]int{6, 6}, Declaration: "func A() int {", Driver: "golang"},
		{Path: "a.go", Side: types.RegionNew, Lines: [2]int{14, 14}, Declaration: "func C() {", Driver: "golang"},
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
