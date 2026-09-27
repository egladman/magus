package types

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// MergeKind is how a region both sides changed was settled; 0 is not settled.
type MergeKind int

const (
	// MergeContained keeps the larger side when the sides are identical, or one side's
	// lines are a subsequence of the other's and both removed the same base lines. A side
	// that only deleted lines never counts, or the other's replacement would keep what it
	// removed.
	MergeContained MergeKind = 1
	// MergeBothAdded keeps ours, then theirs, when both inserted lines into an empty base
	// region.
	MergeBothAdded MergeKind = 2
)

func (k MergeKind) String() string {
	switch k {
	case 0:
		return "not settled"
	case MergeContained:
		return "kind 1"
	case MergeBothAdded:
		return "kind 2"
	}
	return "MergeKind(" + strconv.Itoa(int(k)) + ")"
}

// MergeRegion is one region both sides changed: the locations their edits collide at,
// and how it settled.
type MergeRegion struct {
	Locations []Location
	Kind      MergeKind
}

func (r MergeRegion) String() string {
	names := make([]string, len(r.Locations))
	for i, l := range r.Locations {
		names[i] = l.String()
	}
	return strings.Join(names, ", ") + ": " + r.Kind.String()
}

// MergeResolution is a three-way merge.
type MergeResolution struct {
	// Content is the merge, nil when it did not settle.
	Content []byte
	// Regions has one entry per region both sides changed, in file order, settled or
	// not. It is empty when every region changed on one side only.
	Regions []MergeRegion
}

// Label names each region and how it settled, for a person reading a report:
// `CHANGELOG.md#(preamble): kind 2`, or "one side each" when no region needed either.
func (r MergeResolution) Label() string {
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
func (r MergeResolution) Unsettled() []Location {
	var out []Location
	for _, reg := range r.Regions {
		if reg.Kind == 0 {
			out = append(out, reg.Locations...)
		}
	}
	return out
}

// MergeThreeWay merges ours and theirs, both descended from base, settling only regions
// no reader could dispute, so `magus vcs merge-driver` and the merge queue agree byte for
// byte. Lines only one side changed merge as that side has them; a region both changed
// settles as a [MergeKind] or not at all. Regions are named in path with the diff driver
// its name routes to. Lines keep their terminators, so a CRLF or final-newline change is
// a change to the line. It reports false when a region settles by neither kind, with that
// region in Regions, and with an empty result for binary input or sides too far from the
// base to diff by line (Hunks).
func MergeThreeWay(path string, base, ours, theirs []byte) (MergeResolution, bool) {
	m, ok := alignMerge(path, base, ours, theirs)
	if !ok {
		return MergeResolution{}, false
	}
	out, regions, conflicted := m.walk(0)
	if conflicted {
		return MergeResolution{Regions: regions}, false
	}
	return MergeResolution{Content: out, Regions: regions}, true
}

// MergeThreeWayMarkers merges as MergeThreeWay does, and wraps each region neither kind
// settles in conflict markers markerSize characters wide (7 when not positive): ours,
// then base, then theirs, labelled so. It is what a merge tool leaves for a person when
// MergeThreeWay reports false. It reports false where MergeThreeWay refuses to merge
// lines at all.
func MergeThreeWayMarkers(base, ours, theirs []byte, markerSize int) ([]byte, bool) {
	if markerSize <= 0 {
		markerSize = 7
	}
	m, ok := alignMerge("", base, ours, theirs)
	if !ok {
		return nil, false
	}
	out, _, _ := m.walk(markerSize)
	return out, true
}

// mergeAlignment is a base and two sides with each side's matches to the base.
type mergeAlignment struct {
	path           string
	o, a, b        []string
	ma, mb         []int // base line -> the side's line it is kept as, or -1
	hunksA, hunksB []RegionChange
}

func alignMerge(path string, base, ours, theirs []byte) (mergeAlignment, bool) {
	driver, _ := DiffDriverFor(path)
	hunksA, ok := Hunks(path, base, ours, driver)
	if !ok {
		return mergeAlignment{}, false
	}
	hunksB, ok := Hunks(path, base, theirs, driver)
	if !ok {
		return mergeAlignment{}, false
	}
	m := mergeAlignment{path: path, o: SplitLines(base), a: SplitLines(ours), b: SplitLines(theirs),
		hunksA: hunksA, hunksB: hunksB}
	var okA, okB bool
	m.ma, okA = keptLines(m.o, m.a)
	m.mb, okB = keptLines(m.o, m.b)
	return m, okA && okB
}

// keptLines maps each base line to the side line it is kept as, or -1 where the side dropped
// it.
func keptLines(o, s []string) ([]int, bool) {
	pairs, ok := lineMatches(o, s)
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
func (m mergeAlignment) walk(markerSize int) (out []byte, regions []MergeRegion, conflicted bool) {
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
			settled, kind, ok := settleOverlap(oc, ac, bc, deletedBase(m.ma[iO:next]), deletedBase(m.mb[iO:next]))
			regions = append(regions, MergeRegion{Locations: m.locations(iO, next, iA, eA, iB, eB), Kind: kind})
			if !ok {
				conflicted = true
				if markerSize == 0 {
					return nil, regions, true
				}
				lines = append(lines, conflictMarked(oc, ac, bc, markerSize)...)
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
func (m mergeAlignment) locations(lo, hi, aLo, aHi, bLo, bHi int) []Location {
	a := hunksOver(m.hunksA, lo, hi, aLo, aHi)
	b := hunksOver(m.hunksB, lo, hi, bLo, bHi)
	if shared := Collisions(a, b); len(shared) > 0 {
		return shared
	}
	var all []Location
	for _, l := range slices.Concat(a, b) {
		all = append(all, l.Location())
	}
	slices.SortFunc(all, func(x, y Location) int {
		return cmp.Or(cmp.Compare(x.Path, y.Path), cmp.Compare(x.Declaration, y.Declaration))
	})
	all = slices.Compact(all)
	if len(all) == 0 {
		return []Location{{Path: m.path}}
	}
	return all
}

// hunksOver returns the hunks with lines in [lo, hi) of the base or [sLo, sHi) of the side.
func hunksOver(hunks []RegionChange, lo, hi, sLo, sHi int) []Locator {
	var out []Locator
	for _, h := range hunks {
		from, to := lo, hi
		if h.Side == RegionNew {
			from, to = sLo, sHi
		}
		if h.Lines[0]-1 < to && h.Lines[1]-1 >= from {
			out = append(out, h)
		}
	}
	return out
}

// settleOverlap settles a region both sides changed differently. da and db mark which base
// lines of the region each side deleted.
func settleOverlap(o, a, b []string, da, db []bool) ([]string, MergeKind, bool) {
	if slices.Equal(a, b) {
		return a, MergeContained, true
	}
	small, large, dSmall, dLarge := a, b, da, db
	if len(small) > len(large) {
		small, large, dSmall, dLarge = b, a, db, da
	}
	if slices.Equal(dSmall, dLarge) && isSubsequence(small, large) && (len(small) > 0 || len(o) == 0) {
		return large, MergeContained, true
	}
	if len(o) == 0 {
		return slices.Concat(a, b), MergeBothAdded, true
	}
	return nil, 0, false
}

// conflictMarked is one unsettled region between conflict markers. A section whose last line
// has no newline gets one, so no marker lands on a content line.
func conflictMarked(o, a, b []string, size int) []string {
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

// deletedBase reports, for each base line, whether its side dropped it.
func deletedBase(m []int) []bool {
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
