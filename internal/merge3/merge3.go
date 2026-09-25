// Package merge3 settles a three-way text merge, but only the regions whose outcome no
// reader could dispute. It backs `magus vcs merge-driver` for paths a workspace declares
// eligible and the merge queue's candidate merges, so a person's merge and the queue's
// agree byte for byte.
//
// A region both sides changed settles in two ways, and in no other:
//
//   - [Contained]: the sides are identical, or one side's lines are a subsequence of the
//     other's and both removed the same base lines. The larger side is kept. A side that
//     only deleted lines never counts as contained: keeping the other side's replacement
//     would keep what that side removed.
//   - [BothAdded]: the base region is empty and both sides inserted lines there. Ours is
//     kept, then theirs.
//
// Any other region, where both sides edited or deleted base lines differently, leaves
// the whole file unresolved.
package merge3

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
)

// Kind is how a region both sides changed was settled.
type Kind int

const (
	// Contained keeps the side whose change holds the other's.
	Contained Kind = 1
	// BothAdded keeps both sides' inserted lines, ours first.
	BothAdded Kind = 2
)

func (k Kind) String() string {
	switch k {
	case Contained:
		return "contained"
	case BothAdded:
		return "both added"
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Resolution is a settled merge.
type Resolution struct {
	Content []byte
	// Kinds has one entry per region both sides changed, in file order. It is empty when
	// every region changed on one side only.
	Kinds []Kind
}

// Label names the kinds used, for a person reading a report: "kind 2", "kinds 1, 2", or
// "one side each" when no region needed either.
func (r Resolution) Label() string {
	kinds := slices.Compact(slices.Sorted(slices.Values(r.Kinds)))
	switch len(kinds) {
	case 0:
		return "one side each"
	case 1:
		return "kind " + strconv.Itoa(int(kinds[0]))
	}
	nums := make([]string, len(kinds))
	for i, k := range kinds {
		nums[i] = strconv.Itoa(int(k))
	}
	return "kinds " + strings.Join(nums, ", ")
}

// maxEdits bounds the edit script between the base and either side. The trace a diff
// keeps grows with its square, and a side that far from the base is no low-risk merge.
const maxEdits = 2000

// Resolve merges ours and theirs, both descended from base, line by line. Lines keep
// their terminators, so CRLF and a missing final newline survive as the sides wrote them
// and a change to either is a change to the line. It reports false when a region both
// sides changed settles by neither kind, when any input holds a NUL byte (binary), or
// when either side is more than maxEdits lines from the base.
func Resolve(base, ours, theirs []byte) (Resolution, bool) {
	if bytes.IndexByte(base, 0) >= 0 || bytes.IndexByte(ours, 0) >= 0 || bytes.IndexByte(theirs, 0) >= 0 {
		return Resolution{}, false
	}
	o, a, b := splitLines(base), splitLines(ours), splitLines(theirs)
	ma, ok := matches(o, a)
	if !ok {
		return Resolution{}, false
	}
	mb, ok := matches(o, b)
	if !ok {
		return Resolution{}, false
	}
	var out []string
	var kinds []Kind
	settle := func(lo, hi, aLo, aHi, bLo, bHi int) bool {
		oc, ac, bc := o[lo:hi], a[aLo:aHi], b[bLo:bHi]
		switch {
		case slices.Equal(ac, oc):
			out = append(out, bc...)
		case slices.Equal(bc, oc):
			out = append(out, ac...)
		default:
			lines, kind, ok := overlap(oc, ac, bc, deleted(ma[lo:hi]), deleted(mb[lo:hi]))
			if !ok {
				return false
			}
			out = append(out, lines...)
			kinds = append(kinds, kind)
		}
		return true
	}
	iO, iA, iB := 0, 0, 0
	for {
		// The next base line both sides kept is where the sides agree again.
		next := iO
		for next < len(o) && (ma[next] < 0 || mb[next] < 0) {
			next++
		}
		eA, eB := len(a), len(b)
		if next < len(o) {
			eA, eB = ma[next], mb[next]
		}
		if !settle(iO, next, iA, eA, iB, eB) {
			return Resolution{}, false
		}
		if next == len(o) {
			break
		}
		out = append(out, o[next])
		iO, iA, iB = next+1, eA+1, eB+1
	}
	return Resolution{Content: []byte(strings.Join(out, "")), Kinds: kinds}, true
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

// splitLines splits after every '\n', keeping it; a final line without one is kept as is.
func splitLines(b []byte) []string {
	var lines []string
	s := string(b)
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}
	return lines
}

// matches maps each line of o to the line of s it is kept as on a shortest edit script,
// or -1 where s dropped it. It reports false past maxEdits.
func matches(o, s []string) ([]int, bool) {
	m := make([]int, len(o))
	for i := range m {
		m[i] = -1
	}
	pre := 0
	for pre < len(o) && pre < len(s) && o[pre] == s[pre] {
		m[pre] = pre
		pre++
	}
	suf := 0
	for suf < len(o)-pre && suf < len(s)-pre && o[len(o)-1-suf] == s[len(s)-1-suf] {
		m[len(o)-1-suf] = len(s) - 1 - suf
		suf++
	}
	pairs, ok := myers(o[pre:len(o)-suf], s[pre:len(s)-suf])
	if !ok {
		return nil, false
	}
	for _, p := range pairs {
		m[pre+p[0]] = pre + p[1]
	}
	return m, true
}

// myers returns the matched index pairs of a shortest edit script from a to b, in order
// (Myers, "An O(ND) Difference Algorithm and Its Variations", 1986).
func myers(a, b []string) ([][2]int, bool) {
	n, m := len(a), len(b)
	limit := min(n+m, maxEdits)
	off := limit + 1
	v := make([]int, 2*limit+3)
	// trace[d] is v as step d found it, over diagonals -d..d.
	var trace [][]int
	for d := 0; d <= limit; d++ {
		trace = append(trace, slices.Clone(v[off-d:off+d+1]))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m), true
			}
		}
	}
	return nil, false
}

func backtrack(trace [][]int, n, m int) [][2]int {
	var pairs [][2]int
	x, y := n, m
	for d := len(trace) - 1; d > 0; d-- {
		v := trace[d]
		k := x - y
		prev := k - 1
		if k == -d || (k != d && v[k-1+d] < v[k+1+d]) {
			prev = k + 1
		}
		px := v[prev+d]
		py := px - prev
		for x > px && y > py {
			pairs = append(pairs, [2]int{x - 1, y - 1})
			x, y = x-1, y-1
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		pairs = append(pairs, [2]int{x - 1, y - 1})
		x, y = x-1, y-1
	}
	slices.Reverse(pairs)
	return pairs
}
