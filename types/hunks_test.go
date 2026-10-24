package types

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffDriverFor(t *testing.T) {
	for path, want := range map[string]string{"a/b.go": "golang", "x.tsx": "typescript", "docs/CHANGELOG.md": "markdown", "f.buzz": "buzz", "magus.yaml": "yaml", "ci.yml": "yaml"} {
		d, ok := MatchDiffDriver(path)
		require.True(t, ok, path)
		assert.Equal(t, want, d.Name, path)
	}
	_, ok := MatchDiffDriver("notes.txt")
	assert.False(t, ok)
}

// A declaration names every line from it down to the next one, as git's hunk header
// does: group 1 when the pattern has one, trailing space trimmed, cut at 80 bytes.
func TestDeclarations(t *testing.T) {
	golang, _ := MatchDiffDriver("a.go")
	long := fmt.Sprintf("func F(%s) {\n", strings.Repeat("a int, ", 20))
	lines := []string{"package a\n", "\n", "func A() {  \n", "\treturn\n", "}\n", long, "\tx()\n"}
	assert.Equal(t, []string{"", "", "func A() {", "func A() {", "func A() {", long[:80], long[:80]}, golang.Declarations(lines))

	ts, _ := MatchDiffDriver("a.ts")
	assert.Equal(t, []string{"function f() {", "function f() {"}, ts.Declarations([]string{"function f() {\n", "  if (x) {\n"}),
		"a rejecting pattern names nothing, so the declaration above stands")

	assert.Equal(t, []string{"", ""}, DiffDriver{}.Declarations([]string{"func A() {\n", "}\n"}))
}

// A magusfile's project call and a YAML file are named by key path, so a claim holds one
// key of them; merge risk reads these names, and vcs's git placement reads the same code.
func TestDeclarationsNameKeyPaths(t *testing.T) {
	buzz, _ := MatchDiffDriver("magusfile.buzz")
	magusfile := SplitLines([]byte("import \"magus\";\n\nmagus\\project({\n    \"name\": \"x\",\n    \"sources\": [\n        \"a.txt\",\n    ],\n" +
		"    \"targets\": {\n        \"test\": {\n            \"slots\": 4,\n        },\n        \"go-test\": {\"slots\": 4},\n    },\n});\n\n" +
		"export fun build() > void {\n}\n"))
	assert.Equal(t, []string{
		"", "", "project", "project.name", "project.sources", "project.sources", "project.sources",
		"project.targets", "project.targets.test", "project.targets.test", "project.targets.test", "project.targets.go-test", "project.targets", "project",
		"project", "export fun build() > void {", "export fun build() > void {",
	}, buzz.Declarations(magusfile))

	yaml, ok := MatchDiffDriver("magus.yml")
	require.True(t, ok)
	config := SplitLines([]byte("# config\nversion: 1\njobs:\n  stale_after: 2h\n  nested:\n    deep: 1\nlist:\n  - a\nblock: |\n  text: here\n"))
	assert.Equal(t, []string{
		"", "version", "jobs", "jobs.stale_after", "jobs.nested", "jobs.nested", "list", "list", "block", "block",
	}, yaml.Declarations(config))
}

// Buzz or Go held in a raw string, or in a block comment, is text: its column-0 `var` and
// `func` lines declare nothing, so the lines stay with the Go declaration around them.
func TestDeclarationsSkipGoStringsAndComments(t *testing.T) {
	golang, _ := MatchDiffDriver("a.go")
	lines := SplitLines([]byte("package a\n" +
		"\n" +
		"const script = `\n" +
		"var i = 0;\n" +
		"fun main() > void {}\n" +
		"`\n" +
		"\n" +
		"func A() {\n" +
		"\trun(`\n" +
		"func fake() {\n" +
		"type T struct {\n" +
		"`)\n" +
		"}\n" +
		"\n" +
		"/*\n" +
		"func commented() {\n" +
		"*/\n" +
		"\n" +
		"var after = 1\n"))
	const script, a, after = "const script = `", "func A() {", "var after = 1"
	assert.Equal(t, []string{
		"", "",
		script, script, script, script, script,
		a, a, a, a, a, a, a, a, a, a, a,
		after,
	}, golang.Declarations(lines))
}

// The doc comment directly above a declaration is the declaration's, as it is when git
// places the lines (vcs's TestPurePlacementMatchesGit); a comment a blank line away is not.
func TestDeclarationsHandDocCommentsDown(t *testing.T) {
	golang, _ := MatchDiffDriver("a.go")
	const a, b, c = "func A() {", "func B() {", "var c = 3"
	lines := SplitLines([]byte("func A() {\n}\n\n// apart\n\n// B does it.\n// Twice.\nfunc B() {\n}\n// c\nvar c = 3\n"))
	assert.Equal(t, []string{a, a, a, a, a, b, b, b, b, c, c}, golang.Declarations(lines))
}

// A hunk that adds a blank line, a doc comment and a declaration starts with the blank
// line, which belongs to the declaration it leads into and not to the unchanged one above.
func TestPlaceHunk(t *testing.T) {
	golang, _ := MatchDiffDriver("a.go")
	const keep, fresh = "const A = 1", "const C = 3"
	lines := SplitLines([]byte("const A = 1\n\n// C is new.\nconst C = 3\nconst B = 2\n"))
	decls := golang.Declarations(lines)
	assert.Equal(t, []string{fresh, fresh, fresh}, PlaceHunk(lines, decls, 1, 4), "the hunk's leading blank line")
	assert.Equal(t, []string{keep, keep}, PlaceHunk(lines, decls, 0, 2), "a blank line that ends the range keeps its name")
	assert.Equal(t, []string{keep}, PlaceHunk(lines, decls, 1, 2), "nothing follows the blank line")
	assert.Empty(t, PlaceHunk(lines, decls, 2, 2))
}

func TestHunksChargeALeadingBlankLineToTheNewDeclaration(t *testing.T) {
	golang, _ := MatchDiffDriver("a.go")
	appended, ok := Hunks("a.go", []byte("func C() {\n}\n"), []byte("func C() {\n}\n\nfunc D() {\n}\n"), golang)
	require.True(t, ok)
	assert.Equal(t, []RegionChange{{File: FileChange{Path: "a.go"}, Side: RegionNew, Lines: [2]int{3, 5}, Declaration: "func D() {", Driver: "golang"}},
		appended, "a function appended after another")
	for name, tc := range map[string]struct {
		insert string
		decl   string
	}{
		"a const with a doc comment": {"\n// C is new.\nconst C = 3\n", "const C = 3"},
		"a func with a doc comment":  {"\n// C does it.\nfunc C() {\n}\n", "func C() {"},
		"a func with no doc comment": {"\nfunc C() {\n}\n", "func C() {"},
	} {
		t.Run(name, func(t *testing.T) {
			old := "const A = 1\nconst B = 2\n"
			cur := fmt.Sprintf(`const A = 1
%sconst B = 2
`, tc.insert)
			got, ok := Hunks("a.go", []byte(old), []byte(cur), golang)
			require.True(t, ok)
			require.Len(t, got, 1)
			assert.Equal(t, RegionChange{File: FileChange{Path: "a.go"}, Side: RegionNew, Lines: [2]int{2, 1 + strings.Count(tc.insert, "\n")},
				Declaration: tc.decl, Driver: "golang"}, got[0])
		})
	}
}

func TestHunks(t *testing.T) {
	golang, _ := MatchDiffDriver("a.go")
	old := "package a\n\nfunc A() {\n\treturn\n}\n\nfunc B() {\n\treturn\n}\n"
	cur := "package a\n\nfunc A() {\n\tx()\n\treturn\n}\n\nfunc B() {\n}\n"
	got, ok := Hunks("a.go", []byte(old), []byte(cur), golang)
	require.True(t, ok)
	assert.Equal(t, []RegionChange{
		{File: FileChange{Path: "a.go"}, Side: RegionNew, Lines: [2]int{4, 4}, Declaration: "func A() {", Driver: "golang"},
		{File: FileChange{Path: "a.go"}, Side: RegionOld, Lines: [2]int{8, 8}, Declaration: "func B() {", Driver: "golang"},
	}, got)

	got, ok = Hunks("notes.txt", []byte("a\nb\n"), []byte("a\nc\n"), DiffDriver{})
	require.True(t, ok)
	assert.Equal(t, []RegionChange{
		{File: FileChange{Path: "notes.txt"}, Side: RegionOld, Lines: [2]int{2, 2}},
		{File: FileChange{Path: "notes.txt"}, Side: RegionNew, Lines: [2]int{2, 2}},
	}, got, "no driver: line ranges only, each the whole file as a Location")
	assert.Equal(t, Location{Path: "notes.txt"}, got[0].Location())

	_, ok = Hunks("b", []byte("a\x00"), []byte("b"), DiffDriver{})
	assert.False(t, ok, "binary")
	_, ok = Hunks("b", []byte("a\n"), []byte(strings.Repeat("x\n", maxHunkEdits+1)), DiffDriver{})
	assert.False(t, ok, "past the edit cap")
}
