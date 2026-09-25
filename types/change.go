package types

import (
	"cmp"
	"slices"
	"strings"
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

// SplitClaim is the one parser of a write path's claim grammar: `<path>#<declaration>`
// claims one declaration of a file, and an entry with no `#` claims the path whole.
// It cuts at the FIRST `#`, so a declaration may itself hold one (`doc.md### Usage`), and
// trims both halves. declaration is "" for an entry with no `#`.
//
// A path that itself contains `#` is spelled so the `#` cannot start a claim: with a
// leading `./` (`./notes/a#b.md`, the whole entry is the path), or with the `#` escaped
// (`notes/a\#b.md`, and `notes/a\#b.md#Intro` claims a declaration of it). path comes
// back unescaped and without the `./`.
func SplitClaim(entry string) (path, declaration string) {
	path, declaration, _ = cutClaim(entry)
	return path, declaration
}

// HasClaim reports whether entry claims a declaration, spelled or not: `run.go#` does,
// with an empty one, while `./a#b.md` and `a\#b.md` do not. See SplitClaim.
func HasClaim(entry string) bool {
	_, _, found := cutClaim(entry)
	return found
}

func cutClaim(entry string) (path, declaration string, found bool) {
	entry = strings.TrimSpace(entry)
	if rest, ok := strings.CutPrefix(entry, "./"); ok && strings.Contains(rest, "#") {
		return rest, "", false
	}
	for i := 0; i < len(entry); i++ {
		switch entry[i] {
		case '\\':
			i++
		case '#':
			return unescapeClaimPath(entry[:i]), strings.TrimSpace(entry[i+1:]), true
		}
	}
	return unescapeClaimPath(entry), "", false
}

func unescapeClaimPath(p string) string {
	return strings.TrimSpace(strings.ReplaceAll(p, `\#`, "#"))
}

// NamesDeclaration reports whether a claimed declaration names the declaration line a diff
// driver matched (a [RegionChange] Declaration). The claim names it when the claim is the
// whole line, or any run of it whose ends do not split an identifier: `executeStages`,
// `Magus) executeStages` and the full line all name
// `func (m *Magus) executeStages(ctx context.Context) error {`, and `execute` does not.
//
// The words are the ones git's own funcname pattern matched, so no parser of any language
// is involved; the cost is that a claim on a word every line in a file shares (`ctx`)
// names all of them. [Preamble] is named only by itself.
func NamesDeclaration(claimed, declaration string) bool {
	claimed, declaration = strings.TrimSpace(claimed), strings.TrimSpace(declaration)
	if claimed == "" || declaration == "" {
		return false
	}
	if claimed == declaration {
		return true
	}
	if declaration == Preamble {
		return false
	}
	for from := 0; ; {
		i := strings.Index(declaration[from:], claimed)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(claimed)
		startOK := start == 0 || !identifierByte(declaration[start-1]) || !identifierByte(claimed[0])
		endOK := end == len(declaration) || !identifierByte(declaration[end]) || !identifierByte(claimed[len(claimed)-1])
		if startOK && endOK {
			return true
		}
		from = start + 1
	}
}

func identifierByte(b byte) bool {
	return b == '_' || b == '$' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
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
