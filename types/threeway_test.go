package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// notesFile is the location of a region in a path no diff driver covers: the whole file.
var notesFile = []Location{{Path: "notes.txt"}}

func TestMergeThreeWay(t *testing.T) {
	cases := []struct {
		name               string
		base, ours, theirs string
		want               string
		kinds              []MergeKind
		unresolved         bool
	}{
		{name: "both added at one place keeps ours then theirs",
			base: "a\nz\n", ours: "a\np\nz\n", theirs: "a\nq\nz\n",
			want: "a\np\nq\nz\n", kinds: []MergeKind{MergeBothAdded}},
		{name: "both added at the end of the file",
			base: "a\n", ours: "a\np\n", theirs: "a\nq\n",
			want: "a\np\nq\n", kinds: []MergeKind{MergeBothAdded}},
		{name: "both created the file",
			base: "", ours: "p\n", theirs: "q\n",
			want: "p\nq\n", kinds: []MergeKind{MergeBothAdded}},
		{name: "identical edits on both sides",
			base: "a\nb\nc\n", ours: "a\nB\nc\n", theirs: "a\nB\nc\n",
			want: "a\nB\nc\n", kinds: []MergeKind{MergeContained}},
		{name: "theirs added what ours added and more",
			base: "a\nz\n", ours: "a\np\nz\n", theirs: "a\np\nq\nz\n",
			want: "a\np\nq\nz\n", kinds: []MergeKind{MergeContained}},
		{name: "ours holds theirs's edit and more",
			base: "a\nx\nz\n", ours: "a\nx2\nextra\nz\n", theirs: "a\nx2\nz\n",
			want: "a\nx2\nextra\nz\n", kinds: []MergeKind{MergeContained}},
		{name: "one side each merges with no kind",
			base: "a\nm\nz\n", ours: "A\nm\nz\n", theirs: "a\nm\nZ\n",
			want: "A\nm\nZ\n"},
		{name: "separate regions each settle",
			base: "a\nm\nz\n", ours: "a\np\nm\nz\nr\n", theirs: "a\nq\nm\nz\ns\n",
			want: "a\np\nq\nm\nz\nr\ns\n", kinds: []MergeKind{MergeBothAdded, MergeBothAdded}},
		{name: "both edited one line differently",
			base: "a\nx\nz\n", ours: "a\nx1\nz\n", theirs: "a\nx2\nz\n", kinds: []MergeKind{0}, unresolved: true},
		{name: "one deleted what the other edited",
			base: "a\nx\nz\n", ours: "a\nz\n", theirs: "a\ny\nz\n", kinds: []MergeKind{0}, unresolved: true},
		{name: "a deletion is not contained in an edit carrying it",
			base: "a\nx\nz\n", ours: "a\np\nx\nz\n", theirs: "a\np\nz\n", kinds: []MergeKind{0}, unresolved: true},
		{name: "edits on adjacent lines",
			base: "a\nx\ny\nz\n", ours: "a\nX\ny\nz\n", theirs: "a\nx\nY\nz\n", kinds: []MergeKind{0}, unresolved: true},
		{name: "one unresolvable region leaves the file unresolved",
			base: "a\nm\nx\nz\n", ours: "a\np\nm\nx1\nz\n", theirs: "a\nq\nm\nx2\nz\n", kinds: []MergeKind{MergeBothAdded, 0}, unresolved: true},
		{name: "CRLF both added keeps each line's ending",
			base: "a\r\nz\r\n", ours: "a\r\np\r\nz\r\n", theirs: "a\r\nq\r\nz\r\n",
			want: "a\r\np\r\nq\r\nz\r\n", kinds: []MergeKind{MergeBothAdded}},
		{name: "a line ending change is a change",
			base: "a\nx\nz\n", ours: "a\nx\r\nz\n", theirs: "a\nx2\nz\n", kinds: []MergeKind{0}, unresolved: true},
		{name: "no trailing newline survives an addition above it",
			base: "a\nz", ours: "p\na\nz", theirs: "q\na\nz",
			want: "p\nq\na\nz", kinds: []MergeKind{MergeBothAdded}},
		{name: "both appending past a missing final newline edit its last line",
			base: "a\nb", ours: "a\nb\nc", theirs: "a\nb\nd", kinds: []MergeKind{0}, unresolved: true},
		{name: "binary content", base: "a\x00\n", ours: "a\x00\np\n", theirs: "a\x00\nq\n", unresolved: true},
		{name: "a side past the edit cap",
			base: "a\n", ours: "a\n" + strings.Repeat("p\n", 2001), theirs: "a\nq\n", unresolved: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := MergeThreeWay("notes.txt", []byte(tc.base), []byte(tc.ours), []byte(tc.theirs))
			var regions []MergeRegion
			for _, k := range tc.kinds {
				regions = append(regions, MergeRegion{Locations: notesFile, Kind: k})
			}
			if tc.unresolved {
				assert.False(t, ok)
				assert.Equal(t, MergeResolution{Regions: regions}, got)
				return
			}
			assert.True(t, ok)
			assert.Equal(t, MergeResolution{Content: []byte(tc.want), Regions: regions}, got)
		})
	}
}

// Each side's edits are hunks placed by the file's diff driver, and a region both sides
// changed is named where those hunks collide: the declaration, as a job claim names it.
func TestMergeThreeWayNamesRegionsByDeclaration(t *testing.T) {
	base := "package a\n\nfunc A() {\n\treturn\n}\n\nfunc B() {\n\tx := 1\n\t_ = x\n}\n"
	ours := "package a\n\nfunc A() {\n\tlog()\n\treturn\n}\n\nfunc B() {\n\tx := 2\n\t_ = x\n}\n"
	theirs := "package a\n\nfunc A() {\n\ttrace()\n\treturn\n}\n\nfunc B() {\n\tx := 3\n\t_ = x\n}\n"
	got, ok := MergeThreeWay("a.go", []byte(base), []byte(ours), []byte(theirs))
	assert.False(t, ok)
	assert.Equal(t, []MergeRegion{
		{Locations: []Location{{Path: "a.go", Declaration: "func A() {"}}, Kind: MergeBothAdded},
		{Locations: []Location{{Path: "a.go", Declaration: "func B() {"}}, Kind: 0},
	}, got.Regions)
	assert.Equal(t, "a.go#func A() {: kind 2; a.go#func B() {: not settled", got.Label())
	assert.Equal(t, []Location{{Path: "a.go", Declaration: "func B() {"}}, got.Unsettled())

	md, ok := MergeThreeWay("CHANGELOG.md", []byte("# Log\n\n## Unreleased\n"), []byte("# Log\n\n## Unreleased\n- a\n"), []byte("# Log\n\n## Unreleased\n- b\n"))
	assert.True(t, ok)
	assert.Equal(t, "CHANGELOG.md### Unreleased: kind 2", md.Label(), "two entries appended under one heading")
}

func TestMergeThreeWayIsSymmetricOutsideBothAdded(t *testing.T) {
	base, small, large := "a\nz\n", "a\np\nz\n", "a\np\nq\nz\n"
	one, ok1 := MergeThreeWay("notes.txt", []byte(base), []byte(small), []byte(large))
	two, ok2 := MergeThreeWay("notes.txt", []byte(base), []byte(large), []byte(small))
	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.Equal(t, one, two)
}

// A merge tool whose VCS keeps its file as written must leave markers itself: git keeps
// %A as the driver left it when the driver fails.
func TestMergeThreeWayMarkersWrapOnlyTheRegionsNeitherKindSettles(t *testing.T) {
	base := "a\nm\nx\nz"
	ours := "a\np\nm\nx1\nz"
	theirs := "a\nq\nm\nx2\nz"
	got, ok := MergeThreeWayMarkers([]byte(base), []byte(ours), []byte(theirs), 3)
	assert.True(t, ok)
	assert.Equal(t, "a\np\nq\nm\n<<< ours\nx1\n||| base\nx\n===\nx2\n>>> theirs\nz", string(got))

	got, ok = MergeThreeWayMarkers([]byte("a\n"), []byte("a\np\n"), []byte("a\nq\n"), 0)
	assert.True(t, ok)
	assert.Equal(t, "a\np\nq\n", string(got), "a merge that settles has no markers")

	got, ok = MergeThreeWayMarkers([]byte("x"), []byte("x1"), []byte("x2"), 0)
	assert.True(t, ok)
	assert.Equal(t, "<<<<<<< ours\nx1\n||||||| base\nx\n=======\nx2\n>>>>>>> theirs\n", string(got), "a line without a newline gets one before a marker")

	_, ok = MergeThreeWayMarkers([]byte("a\x00"), []byte("b"), []byte("c"), 7)
	assert.False(t, ok, "binary is not merged by line")
}

func TestMergeResolutionLabel(t *testing.T) {
	assert.Equal(t, "one side each", MergeResolution{}.Label())
	assert.Equal(t, "notes.txt: kind 2; notes.txt: kind 1",
		MergeResolution{Regions: []MergeRegion{{Locations: notesFile, Kind: MergeBothAdded}, {Locations: notesFile, Kind: MergeContained}}}.Label())
	assert.Equal(t, "not settled", MergeKind(0).String())
	assert.Equal(t, "MergeKind(7)", MergeKind(7).String())
}
