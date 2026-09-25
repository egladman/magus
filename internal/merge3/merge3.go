// Package merge3 settles a three-way text merge, but only the regions whose outcome no
// reader could dispute. It backs `magus vcs merge-driver` and the merge queue's candidate
// merges, so a person's merge and the queue's agree byte for byte.
//
// Each side's edits against the merge base are types.RegionChange values (types.Hunks),
// the model a footprint and a job claim compare concurrent edits with, and where both
// sides changed the same lines their collisions (types.Collisions) name the region as a
// types.Location. Such a region settles in two ways, and in no other:
//
//   - [Contained]: the sides are identical, or one side's lines are a subsequence of the
//     other's and both removed the same base lines. The larger side is kept. A side that
//     only deleted lines never counts as contained: keeping the other side's replacement
//     would keep what that side removed.
//   - [BothAdded]: the base region is empty and both sides inserted lines there. Ours is
//     kept, then theirs.
//
// Any other region, where both sides edited or deleted base lines differently, leaves
// the whole file unresolved. Lines only one side changed merge as that side has them.
package merge3

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// Kind is how a region both sides changed was settled; 0 is not settled.
type Kind int

const (
	// Contained keeps the side whose change holds the other's.
	Contained Kind = 1
	// BothAdded keeps both sides' inserted lines, ours first.
	BothAdded Kind = 2
)

func (k Kind) String() string {
	switch k {
	case 0:
		return "not settled"
	case Contained:
		return "kind 1"
	case BothAdded:
		return "kind 2"
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Region is one region both sides changed: the locations their edits collide at, and how
// it settled.
type Region struct {
	Locations []types.Location
	Kind      Kind
}

func (r Region) String() string {
	names := make([]string, len(r.Locations))
	for i, l := range r.Locations {
		names[i] = l.String()
	}
	return strings.Join(names, ", ") + ": " + r.Kind.String()
}

// Resolution is a three-way merge.
type Resolution struct {
	// Content is the merge, nil when it did not settle.
	Content []byte
	// Regions has one entry per region both sides changed, in file order, settled or
	// not. It is empty when every region changed on one side only.
	Regions []Region
}

// Label names each region and how it settled, for a person reading a report:
// `CHANGELOG.md#(preamble): kind 2`, or "one side each" when no region needed either.
func (r Resolution) Label() string {
	if len(r.Regions) == 0 {
		return "one side each"
	}
	parts := make([]string, len(r.Regions))
	for i, reg := range r.Regions {
		parts[i] = reg.String()
	}
	return strings.Join(parts, "; ")
}

// Unsettled lists the locations of the regions that did not settle.
func (r Resolution) Unsettled() []types.Location {
	var out []types.Location
	for _, reg := range r.Regions {
		if reg.Kind == 0 {
			out = append(out, reg.Locations...)
		}
	}
	return out
}

// Resolve merges ours and theirs, both descended from base, line by line, naming regions
// in path with the diff driver its name routes to (types.DiffDriverFor). Lines keep their
// terminators, so CRLF and a missing final newline survive as the sides wrote them and a
// change to either is a change to the line. It reports false when a region both sides
// changed settles by neither kind, with that region in the result's Regions, and with an
// empty result for binary input (a NUL byte) or sides too far from the base to diff by
// line (types.Hunks).
func Resolve(path string, base, ours, theirs []byte) (Resolution, bool) {
	m, ok := align(path, base, ours, theirs)
	if !ok {
		return Resolution{}, false
	}
	out, regions, conflicted := m.walk(0)
	if conflicted {
		return Resolution{Regions: regions}, false
	}
	return Resolution{Content: out, Regions: regions}, true
}

// Markers merges as Resolve does, and wraps each region neither kind settles in conflict
// markers markerSize characters wide (7 when not positive): ours, then base, then theirs,
// labelled so. It is what a merge tool leaves for a person when Resolve reports false. It
// reports false where Resolve refuses to merge lines at all.
func Markers(base, ours, theirs []byte, markerSize int) ([]byte, bool) {
	if markerSize <= 0 {
		markerSize = 7
	}
	m, ok := align("", base, ours, theirs)
	if !ok {
		return nil, false
	}
	out, _, _ := m.walk(markerSize)
	return out, true
}

// alignment is a base and two sides with each side's matches to the base.
type alignment struct {
	path           string
	o, a, b        []string
	ma, mb         []int // base line -> the side's line it is kept as, or -1
	hunksA, hunksB []types.RegionChange
}

func align(path string, base, ours, theirs []byte) (alignment, bool) {
	driver, _ := types.DiffDriverFor(path)
	hunksA, ok := types.Hunks(path, base, ours, driver)
	if !ok {
		return alignment{}, false
	}
	hunksB, ok := types.Hunks(path, base, theirs, driver)
	if !ok {
		return alignment{}, false
	}
	m := alignment{path: path, o: types.SplitLines(base), a: types.SplitLines(ours), b: types.SplitLines(theirs),
		hunksA: hunksA, hunksB: hunksB}
	var okA, okB bool
	m.ma, okA = kept(m.o, m.a)
	m.mb, okB = kept(m.o, m.b)
	return m, okA && okB
}

// kept maps each base line to the side line it is kept as, or -1 where the side dropped
// it.
func kept(o, s []string) ([]int, bool) {
	pairs, ok := types.LineMatches(o, s)
	if !ok {
		return nil, false
	}
	m := make([]int, len(o))
	for i := range m {
		m[i] = -1
	}
	for _, p := range pairs {
		m[p[0]] = p[1]
	}
	return m, true
}

// walk merges region by region. With markerSize 0 it stops at the first region that does
// not settle and reports it conflicted; otherwise it writes that region between conflict
// markers and goes on.
func (m alignment) walk(markerSize int) (out []byte, regions []Region, conflicted bool) {
	var lines []string
	iO, iA, iB := 0, 0, 0
	for {
		// The next base line both sides kept is where the sides agree again.
		next := iO
		for next < len(m.o) && (m.ma[next] < 0 || m.mb[next] < 0) {
			next++
		}
		eA, eB := len(m.a), len(m.b)
		if next < len(m.o) {
			eA, eB = m.ma[next], m.mb[next]
		}
		oc, ac, bc := m.o[iO:next], m.a[iA:eA], m.b[iB:eB]
		switch {
		case slices.Equal(ac, oc):
			lines = append(lines, bc...)
		case slices.Equal(bc, oc):
			lines = append(lines, ac...)
		default:
			settled, kind, ok := overlap(oc, ac, bc, deleted(m.ma[iO:next]), deleted(m.mb[iO:next]))
			regions = append(regions, Region{Locations: m.locations(iO, next, iA, eA, iB, eB), Kind: kind})
			if !ok {
				conflicted = true
				if markerSize == 0 {
					return nil, regions, true
				}
				lines = append(lines, marked(oc, ac, bc, markerSize)...)
			} else {
				lines = append(lines, settled...)
			}
		}
		if next == len(m.o) {
			break
		}
		lines = append(lines, m.o[next])
		iO, iA, iB = next+1, eA+1, eB+1
	}
	return []byte(strings.Join(lines, "")), regions, conflicted
}

// locations names a region both sides changed: where ours's and theirs's hunks over it
// collide, else every location either side's hunks there name. Ranges are 0-based and
// half-open over base, ours and theirs.
func (m alignment) locations(lo, hi, aLo, aHi, bLo, bHi int) []types.Location {
	a := over(m.hunksA, lo, hi, aLo, aHi)
	b := over(m.hunksB, lo, hi, bLo, bHi)
	if shared := types.Collisions(a, b); len(shared) > 0 {
		return shared
	}
	var all []types.Location
	for _, l := range slices.Concat(a, b) {
		all = append(all, l.Location())
	}
	slices.SortFunc(all, func(x, y types.Location) int {
		return cmp.Or(cmp.Compare(x.Path, y.Path), cmp.Compare(x.Declaration, y.Declaration))
	})
	all = slices.Compact(all)
	if len(all) == 0 {
		return []types.Location{{Path: m.path}}
	}
	return all
}

// over returns the hunks with lines in [lo, hi) of the base or [sLo, sHi) of the side.
func over(hunks []types.RegionChange, lo, hi, sLo, sHi int) []types.Locator {
	var out []types.Locator
	for _, h := range hunks {
		from, to := lo, hi
		if h.Side == types.RegionNew {
			from, to = sLo, sHi
		}
		if h.Lines[0]-1 < to && h.Lines[1]-1 >= from {
			out = append(out, h)
		}
	}
	return out
}

// overlap settles a region both sides changed differently. da and db mark which base
// lines of the region each side deleted.
func overlap(o, a, b []string, da, db []bool) ([]string, Kind, bool) {
	if slices.Equal(a, b) {
		return a, Contained, true
	}
	small, large, dSmall, dLarge := a, b, da, db
	if len(small) > len(large) {
		small, large, dSmall, dLarge = b, a, db, da
	}
	if slices.Equal(dSmall, dLarge) && isSubsequence(small, large) && (len(small) > 0 || len(o) == 0) {
		return large, Contained, true
	}
	if len(o) == 0 {
		return slices.Concat(a, b), BothAdded, true
	}
	return nil, 0, false
}

// marked is one unsettled region between conflict markers. A section whose last line
// has no newline gets one, so no marker lands on a content line.
func marked(o, a, b []string, size int) []string {
	section := func(marker, label string, lines []string) []string {
		out := append([]string{strings.Repeat(marker, size) + label + "\n"}, lines...)
		if last := out[len(out)-1]; !strings.HasSuffix(last, "\n") {
			out[len(out)-1] = last + "\n"
		}
		return out
	}
	return slices.Concat(
		section("<", " ours", a),
		section("|", " base", o),
		section("=", "", b),
		[]string{strings.Repeat(">", size) + " theirs\n"},
	)
}

// deleted reports, for each base line, whether its side dropped it.
func deleted(m []int) []bool {
	out := make([]bool, len(m))
	for i, j := range m {
		out[i] = j < 0
	}
	return out
}

func isSubsequence(small, large []string) bool {
	i := 0
	for _, line := range large {
		if i < len(small) && small[i] == line {
			i++
		}
	}
	return i == len(small)
}
