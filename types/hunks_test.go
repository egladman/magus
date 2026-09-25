package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffDriverFor(t *testing.T) {
	for path, want := range map[string]string{"a/b.go": "golang", "x.tsx": "typescript", "docs/CHANGELOG.md": "markdown", "f.buzz": "buzz"} {
		d, ok := DiffDriverFor(path)
		require.True(t, ok, path)
		assert.Equal(t, want, d.Name, path)
	}
	_, ok := DiffDriverFor("notes.txt")
	assert.False(t, ok)
}

// A declaration names every line from it down to the next one, as git's hunk header
// does: group 1 when the pattern has one, trailing space trimmed, cut at 80 bytes.
func TestDeclarations(t *testing.T) {
	golang, _ := DiffDriverFor("a.go")
	long := "func F(" + strings.Repeat("a int, ", 20) + ") {\n"
	lines := []string{"package a\n", "\n", "func A() {  \n", "\treturn\n", "}\n", long, "\tx()\n"}
	assert.Equal(t, []string{"", "", "func A() {", "func A() {", "func A() {", long[:80], long[:80]}, golang.Declarations(lines))

	ts, _ := DiffDriverFor("a.ts")
	assert.Equal(t, []string{"function f() {", "function f() {"}, ts.Declarations([]string{"function f() {\n", "  if (x) {\n"}),
		"a rejecting pattern names nothing, so the declaration above stands")

	assert.Equal(t, []string{"", ""}, DiffDriver{}.Declarations([]string{"func A() {\n", "}\n"}))
}

func TestHunks(t *testing.T) {
	golang, _ := DiffDriverFor("a.go")
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
