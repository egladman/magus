package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitClaim(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		entry, path, declaration string
	}{
		{"run.go", "run.go", ""},
		{"run.go#executeStages", "run.go", "executeStages"},
		{" run.go # executeStages ", "run.go", "executeStages"},
		{"docs/scope.md#The knobs", "docs/scope.md", "The knobs"},
		{"docs/scope.md### Usage", "docs/scope.md", "## Usage"},
		{"run.go#", "run.go", ""},
		{"#A", "", "A"},
		{"", "", ""},
		{"./notes/a#b.md", "notes/a#b.md", ""},
		{`notes/a\#b.md`, "notes/a#b.md", ""},
		{`notes/a\#b.md#Intro`, "notes/a#b.md", "Intro"},
		{"./run.go", "./run.go", ""},
	} {
		path, declaration := SplitClaim(tc.entry)
		assert.Equal(t, [2]string{tc.path, tc.declaration}, [2]string{path, declaration}, "%q", tc.entry)
	}
}

func TestHasClaim(t *testing.T) {
	t.Parallel()

	for entry, want := range map[string]bool{
		"run.go#A": true, "run.go#": true, "run.go": false,
		"./a#b.md": false, `a\#b.md`: false, `a\#b.md#A`: true,
	} {
		assert.Equal(t, want, HasClaim(entry), "%q", entry)
	}
}

func TestNamesDeclaration(t *testing.T) {
	t.Parallel()

	const method = "func (m *Magus) executeStages(ctx context.Context) error {"
	for _, tc := range []struct {
		claimed, declaration string
		want                 bool
	}{
		{"executeStages", method, true},
		{"Magus) executeStages", method, true},
		{method, method, true},
		{"Magus", method, true},
		{"execute", method, false},
		{"Stages", method, false},
		{"executeStages(ctx", method, true},
		{"The knobs", "## The knobs", true},
		{"knob", "## The knobs", false},
		{"$init", "const $init = () => {", true},
		{"init", "const $init = () => {", false},
		{"target", `test "target" {`, true},
		{Preamble, Preamble, true},
		{"preamble", Preamble, false},
		{"", method, false},
		{"executeStages", "", false},
	} {
		assert.Equal(t, tc.want, NamesDeclaration(tc.claimed, tc.declaration), "%q names %q", tc.claimed, tc.declaration)
	}
}

func TestLocation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		locator Locator
		want    Location
		str     string
	}{
		{"a file is its whole file", FileChange{Path: "run.go", Status: ChangeModified}, Location{Path: "run.go"}, "run.go"},
		{
			"a region is its declaration",
			RegionChange{File: FileChange{Path: "run.go"}, Side: RegionNew, Lines: [2]int{3, 9}, Declaration: "func executeStages", Driver: "golang"},
			Location{Path: "run.go", Declaration: "func executeStages"}, "run.go#func executeStages",
		},
		{
			"a region a driver placed above the first declaration is the preamble",
			RegionChange{File: FileChange{Path: "run.go"}, Side: RegionNew, Lines: [2]int{3, 4}, Driver: "golang"},
			Location{Path: "run.go", Declaration: Preamble}, "run.go#(preamble)",
		},
		{
			"a region no driver placed is its whole file",
			RegionChange{File: FileChange{Path: "notes.txt"}, Side: RegionOld, Lines: [2]int{1, 2}},
			Location{Path: "notes.txt"}, "notes.txt",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.locator.Location())
			assert.Equal(t, tc.str, tc.locator.Location().String())
		})
	}
}

func TestCollisions(t *testing.T) {
	t.Parallel()

	region := func(path, decl string, side RegionSide) Locator {
		return RegionChange{File: FileChange{Path: path}, Side: side, Lines: [2]int{1, 2}, Declaration: decl, Driver: "golang"}
	}
	undriven := func(path string, side RegionSide) Locator {
		return RegionChange{File: FileChange{Path: path}, Side: side, Lines: [2]int{1, 2}}
	}
	file := func(path string) Locator { return FileChange{Path: path} }
	for _, tc := range []struct {
		name string
		a, b []Locator
		want []Location
	}{
		{
			name: "equal declarations collide, whatever their side",
			a:    []Locator{region("a.go", "func X() {", RegionNew)},
			b:    []Locator{region("a.go", "func X() {", RegionOld)},
			want: []Location{{Path: "a.go", Declaration: "func X() {"}},
		},
		{
			name: "different declarations in one file do not",
			a:    []Locator{region("a.go", "func X() {", RegionNew)},
			b:    []Locator{region("a.go", "func Y() {", RegionNew)},
		},
		{
			name: "a file collides with every region of its file",
			a:    []Locator{file("a.go")},
			b: []Locator{
				region("a.go", "func X() {", RegionNew),
				region("a.go", "func Y() {", RegionOld),
				region("b.go", "func Z() {", RegionNew),
			},
			want: []Location{{Path: "a.go", Declaration: "func X() {"}, {Path: "a.go", Declaration: "func Y() {"}},
		},
		{
			name: "a region no driver placed covers its file from either side",
			a: []Locator{
				region("a.go", "func X() {", RegionNew),
				region("b.go", "func Z() {", RegionNew),
			},
			b:    []Locator{undriven("a.go", RegionNew)},
			want: []Location{{Path: "a.go", Declaration: "func X() {"}},
		},
		{
			name: "two whole files collide on the file",
			a:    []Locator{file("a.go")},
			b:    []Locator{undriven("a.go", RegionOld)},
			want: []Location{{Path: "a.go"}},
		},
		{
			name: "preamble edits collide with each other and with no declaration",
			a:    []Locator{region("a.go", "", RegionNew), region("b.go", "", RegionNew)},
			b:    []Locator{region("a.go", "", RegionOld), region("b.go", "func Y() {", RegionNew)},
			want: []Location{{Path: "a.go", Declaration: Preamble}},
		},
		{
			name: "different files never collide",
			a:    []Locator{file("a.go"), region("b.go", "func X() {", RegionNew)},
			b:    []Locator{file("c.go"), region("d.go", "func X() {", RegionNew)},
		},
		{
			name: "sorted and deduplicated",
			a: []Locator{
				region("z.go", "func Z() {", RegionNew),
				region("a.go", "func A() {", RegionNew),
				region("a.go", "func A() {", RegionOld),
			},
			b: []Locator{
				region("a.go", "func A() {", RegionNew),
				region("a.go", "func A() {", RegionNew),
				region("z.go", "func Z() {", RegionNew),
			},
			want: []Location{{Path: "a.go", Declaration: "func A() {"}, {Path: "z.go", Declaration: "func Z() {"}},
		},
		{name: "nothing collides with nothing", a: []Locator{file("a.go")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Collisions(tc.a, tc.b))
			assert.Equal(t, tc.want, Collisions(tc.b, tc.a), "Collisions is symmetric")
		})
	}
}
