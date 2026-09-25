package types

import (
	"cmp"
	"slices"
)

// Location is where a change landed: a file, or a declaration inside one.
type Location struct {
	// Path is repository-relative with forward slashes.
	Path string `json:"path"`
	// Declaration is empty for the whole file.
	Declaration string `json:"declaration,omitempty"`
}

// String is the bare path for a whole file, else `<path>#<declaration>`.
func (l Location) String() string {
	if l.Declaration == "" {
		return l.Path
	}
	return l.Path + "#" + l.Declaration
}

// Locator is anything that says where it changed.
type Locator interface {
	Location() Location
}

// FileChange is a whole file a diff or a commit touched.
type FileChange struct {
	// Path is repository-relative with forward slashes, the name AFTER the change.
	Path string `json:"path"`
	// PrevPath is set only on a rename a commit made and carries the name before it, which
	// is the edge a reader follows to reassemble a file's lineage.
	PrevPath string `json:"prev_path,omitempty"`
	// Status is what the commit did to the path, for churn attribution; empty when the
	// change came from a diff that does not say.
	Status ChangeStatus `json:"status,omitempty"`
}

// Location is the whole file at Path.
func (f FileChange) Location() Location { return Location{Path: f.Path} }

// RegionChange is the lines of one hunk that fall inside one declaration of a file.
type RegionChange struct {
	// File is the file this region refines.
	File FileChange `json:"file"`
	// Side is RegionOld for lines only base's version has (a deletion) and RegionNew for
	// lines in the working tree's.
	Side RegionSide `json:"side"`
	// Lines is the first and last line on Side, 1-based and inclusive.
	Lines [2]int `json:"lines"`
	// Declaration is the enclosing declaration's line as the diff driver matched it,
	// trimmed (`func (m *Magus) executeStages(ctx context.Context) error {`). Empty for
	// lines above the file's first declaration, or when the path has no driver.
	Declaration string `json:"declaration,omitempty"`
	// Driver is the diff driver that named Declaration (`golang`, `markdown`, `buzz`),
	// empty when the path has none and the region says only which lines changed.
	Driver string `json:"driver,omitempty"`
}

// Preamble is the Declaration of the lines above a file's first declaration (a package
// clause, imports, a document's opening paragraph), which collide only with each other.
const Preamble = "(preamble)"

// Location is the region's declaration. Lines a driver placed above the first declaration
// are the file's Preamble; lines no driver placed could be anywhere, so they are the whole
// file.
func (r RegionChange) Location() Location {
	switch {
	case r.Driver == "":
		return Location{Path: r.File.Path}
	case r.Declaration == "":
		return Location{Path: r.File.Path, Declaration: Preamble}
	}
	return Location{Path: r.File.Path, Declaration: r.Declaration}
}

// RegionSide says which version of a file a RegionChange's lines are numbered in.
type RegionSide string

const (
	RegionOld RegionSide = "old"
	RegionNew RegionSide = "new"
)

// Collisions returns the locations a and b share, sorted and deduplicated. Two locations
// collide when they share a Path and either Declaration is empty (a whole file covers every
// declaration in it) or the Declarations are equal. A whole file on one side reports each
// of the other side's locations in that file.
func Collisions(a, b []Locator) []Location {
	byPath := func(locators []Locator) map[string][]Location {
		out := map[string][]Location{}
		for _, l := range locators {
			loc := l.Location()
			out[loc.Path] = append(out[loc.Path], loc)
		}
		return out
	}
	isWhole := func(l Location) bool { return l.Declaration == "" }
	inA, inB := byPath(a), byPath(b)
	var shared []Location
	for path, locsA := range inA {
		locsB, ok := inB[path]
		if !ok {
			continue
		}
		wholeA, wholeB := slices.ContainsFunc(locsA, isWhole), slices.ContainsFunc(locsB, isWhole)
		if wholeA {
			shared = append(shared, locsB...)
		}
		if wholeB {
			shared = append(shared, locsA...)
		}
		if !wholeA && !wholeB {
			for _, l := range locsA {
				if slices.Contains(locsB, l) {
					shared = append(shared, l)
				}
			}
		}
	}
	slices.SortFunc(shared, func(x, y Location) int {
		return cmp.Or(cmp.Compare(x.Path, y.Path), cmp.Compare(x.Declaration, y.Declaration))
	})
	return slices.Compact(shared)
}
