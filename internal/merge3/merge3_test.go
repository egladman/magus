package merge3

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// file is the location of a region in a path no diff driver covers: the whole file.
var file = []types.Location{{Path: "notes.txt"}}

func TestResolve(t *testing.T) {
	cases := []struct {
		name               string
		base, ours, theirs string
		want               string
		kinds              []Kind
		unresolved         bool
	}{
		{name: "both added at one place keeps ours then theirs",
			base: "a\nz\n", ours: "a\np\nz\n", theirs: "a\nq\nz\n",
			want: "a\np\nq\nz\n", kinds: []Kind{BothAdded}},
		{name: "both added at the end of the file",
			base: "a\n", ours: "a\np\n", theirs: "a\nq\n",
			want: "a\np\nq\n", kinds: []Kind{BothAdded}},
		{name: "both created the file",
			base: "", ours: "p\n", theirs: "q\n",
			want: "p\nq\n", kinds: []Kind{BothAdded}},
		{name: "identical edits on both sides",
			base: "a\nb\nc\n", ours: "a\nB\nc\n", theirs: "a\nB\nc\n",
			want: "a\nB\nc\n", kinds: []Kind{Contained}},
		{name: "theirs added what ours added and more",
			base: "a\nz\n", ours: "a\np\nz\n", theirs: "a\np\nq\nz\n",
			want: "a\np\nq\nz\n", kinds: []Kind{Contained}},
		{name: "ours holds theirs's edit and more",
			base: "a\nx\nz\n", ours: "a\nx2\nextra\nz\n", theirs: "a\nx2\nz\n",
			want: "a\nx2\nextra\nz\n", kinds: []Kind{Contained}},
		{name: "one side each merges with no kind",
			base: "a\nm\nz\n", ours: "A\nm\nz\n", theirs: "a\nm\nZ\n",
			want: "A\nm\nZ\n"},
		{name: "separate regions each settle",
			base: "a\nm\nz\n", ours: "a\np\nm\nz\nr\n", theirs: "a\nq\nm\nz\ns\n",
			want: "a\np\nq\nm\nz\nr\ns\n", kinds: []Kind{BothAdded, BothAdded}},
		{name: "both edited one line differently",
			base: "a\nx\nz\n", ours: "a\nx1\nz\n", theirs: "a\nx2\nz\n", kinds: []Kind{0}, unresolved: true},
		{name: "one deleted what the other edited",
			base: "a\nx\nz\n", ours: "a\nz\n", theirs: "a\ny\nz\n", kinds: []Kind{0}, unresolved: true},
		{name: "a deletion is not contained in an edit carrying it",
			base: "a\nx\nz\n", ours: "a\np\nx\nz\n", theirs: "a\np\nz\n", kinds: []Kind{0}, unresolved: true},
		{name: "edits on adjacent lines",
			base: "a\nx\ny\nz\n", ours: "a\nX\ny\nz\n", theirs: "a\nx\nY\nz\n", kinds: []Kind{0}, unresolved: true},
		{name: "one unresolvable region leaves the file unresolved",
			base: "a\nm\nx\nz\n", ours: "a\np\nm\nx1\nz\n", theirs: "a\nq\nm\nx2\nz\n", kinds: []Kind{BothAdded, 0}, unresolved: true},
		{name: "CRLF both added keeps each line's ending",
			base: "a\r\nz\r\n", ours: "a\r\np\r\nz\r\n", theirs: "a\r\nq\r\nz\r\n",
			want: "a\r\np\r\nq\r\nz\r\n", kinds: []Kind{BothAdded}},
		{name: "a line ending change is a change",
			base: "a\nx\nz\n", ours: "a\nx\r\nz\n", theirs: "a\nx2\nz\n", kinds: []Kind{0}, unresolved: true},
		{name: "no trailing newline survives an addition above it",
			base: "a\nz", ours: "p\na\nz", theirs: "q\na\nz",
			want: "p\nq\na\nz", kinds: []Kind{BothAdded}},
		{name: "both appending past a missing final newline edit its last line",
			base: "a\nb", ours: "a\nb\nc", theirs: "a\nb\nd", kinds: []Kind{0}, unresolved: true},
		{name: "binary content", base: "a\x00\n", ours: "a\x00\np\n", theirs: "a\x00\nq\n", unresolved: true},
		{name: "a side past the edit cap",
			base: "a\n", ours: "a\n" + strings.Repeat("p\n", 2001), theirs: "a\nq\n", unresolved: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Resolve("notes.txt", []byte(tc.base), []byte(tc.ours), []byte(tc.theirs))
			var regions []Region
			for _, k := range tc.kinds {
				regions = append(regions, Region{Locations: file, Kind: k})
			}
			if tc.unresolved {
				assert.False(t, ok)
				assert.Equal(t, Resolution{Regions: regions}, got)
				return
			}
			assert.True(t, ok)
			assert.Equal(t, Resolution{Content: []byte(tc.want), Regions: regions}, got)
		})
	}
}

// Each side's edits are hunks placed by the file's diff driver, and a region both sides
// changed is named where those hunks collide: the declaration, as a job claim names it.
func TestResolveNamesRegionsByDeclaration(t *testing.T) {
	base := "package a\n\nfunc A() {\n\treturn\n}\n\nfunc B() {\n\tx := 1\n\t_ = x\n}\n"
	ours := "package a\n\nfunc A() {\n\tlog()\n\treturn\n}\n\nfunc B() {\n\tx := 2\n\t_ = x\n}\n"
	theirs := "package a\n\nfunc A() {\n\ttrace()\n\treturn\n}\n\nfunc B() {\n\tx := 3\n\t_ = x\n}\n"
	got, ok := Resolve("a.go", []byte(base), []byte(ours), []byte(theirs))
	assert.False(t, ok)
	assert.Equal(t, []Region{
		{Locations: []types.Location{{Path: "a.go", Declaration: "func A() {"}}, Kind: BothAdded},
		{Locations: []types.Location{{Path: "a.go", Declaration: "func B() {"}}, Kind: 0},
	}, got.Regions)
	assert.Equal(t, "a.go#func A() {: kind 2; a.go#func B() {: not settled", got.Label())
	assert.Equal(t, []types.Location{{Path: "a.go", Declaration: "func B() {"}}, got.Unsettled())

	md, ok := Resolve("CHANGELOG.md", []byte("# Log\n\n## Unreleased\n"), []byte("# Log\n\n## Unreleased\n- a\n"), []byte("# Log\n\n## Unreleased\n- b\n"))
	assert.True(t, ok)
	assert.Equal(t, "CHANGELOG.md### Unreleased: kind 2", md.Label(), "two entries appended under one heading")
}

func TestResolveIsSymmetricOutsideBothAdded(t *testing.T) {
	base, small, large := "a\nz\n", "a\np\nz\n", "a\np\nq\nz\n"
	one, ok1 := Resolve("notes.txt", []byte(base), []byte(small), []byte(large))
	two, ok2 := Resolve("notes.txt", []byte(base), []byte(large), []byte(small))
	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.Equal(t, one, two)
}

// A merge tool whose VCS keeps its file as written must leave markers itself: git keeps
// %A as the driver left it when the driver fails.
func TestMarkersWrapOnlyTheRegionsNeitherKindSettles(t *testing.T) {
	base := "a\nm\nx\nz"
	ours := "a\np\nm\nx1\nz"
	theirs := "a\nq\nm\nx2\nz"
	got, ok := Markers([]byte(base), []byte(ours), []byte(theirs), 3)
	assert.True(t, ok)
	assert.Equal(t, "a\np\nq\nm\n<<< ours\nx1\n||| base\nx\n===\nx2\n>>> theirs\nz", string(got))

	got, ok = Markers([]byte("a\n"), []byte("a\np\n"), []byte("a\nq\n"), 0)
	assert.True(t, ok)
	assert.Equal(t, "a\np\nq\n", string(got), "a merge that settles has no markers")

	got, ok = Markers([]byte("x"), []byte("x1"), []byte("x2"), 0)
	assert.True(t, ok)
	assert.Equal(t, "<<<<<<< ours\nx1\n||||||| base\nx\n=======\nx2\n>>>>>>> theirs\n", string(got), "a line without a newline gets one before a marker")

	_, ok = Markers([]byte("a\x00"), []byte("b"), []byte("c"), 7)
	assert.False(t, ok, "binary is not merged by line")
}

func TestLabel(t *testing.T) {
	assert.Equal(t, "one side each", Resolution{}.Label())
	assert.Equal(t, "notes.txt: kind 2; notes.txt: kind 1",
		Resolution{Regions: []Region{{Locations: file, Kind: BothAdded}, {Locations: file, Kind: Contained}}}.Label())
	assert.Equal(t, "not settled", Kind(0).String())
	assert.Equal(t, "Kind(7)", Kind(7).String())
}
