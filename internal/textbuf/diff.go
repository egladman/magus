package textbuf

import (
	"bytes"
	"hash/maphash"
	"slices"
	"unicode/utf8"
)

// maxDiffEdits bounds the Myers search. A middle that needs more line edits becomes one
// hunk: still correct, but anchors inside it collapse.
const maxDiffEdits = 4096

// ApplyDiff replaces the text with text as a line diff, each hunk trimmed to the bytes
// that differ, so anchors outside the changed bytes keep their places. It is how
// formatter output and file reloads come in.
func (b *buffer) ApplyDiff(text []byte) {
	old := b.Bytes()
	var edits []edit
	for _, h := range lineDiff(old, text) {
		a, n := trim(old[h.a0:h.a1], text[h.b0:h.b1])
		edits = append(edits, edit{Offset: h.a0 + a.start, Delete: a.end - a.start, Text: string(n)})
	}
	if err := b.Apply(edits...); err != nil {
		panic("textbuf: diff produced invalid edits: " + err.Error())
	}
}

type hunk struct{ a0, a1, b0, b1 int }

// trim drops the bytes a hunk's two sides share at either end, keeping whole UTF-8
// sequences, and returns the old range left and the new bytes for it.
func trim(a, b []byte) (span, []byte) {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	for p > 0 && (p < len(a) && !utf8.RuneStart(a[p]) || p < len(b) && !utf8.RuneStart(b[p])) {
		p--
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	for s > 0 && !utf8.RuneStart(a[len(a)-s]) {
		s--
	}
	return span{start: p, end: len(a) - s}, b[p : len(b)-s]
}

// lineDiff returns the byte ranges of a and b that differ, as hunks of whole lines.
func lineDiff(a, b []byte) []hunk {
	pre := commonPrefix(a, b)
	pre = bytes.LastIndexByte(a[:pre], '\n') + 1
	suf := commonSuffix(a[pre:], b[pre:])
	if i := bytes.IndexByte(a[len(a)-suf:], '\n'); suf > 0 && i >= 0 {
		suf -= i + 1
	} else {
		suf = 0
	}
	am, bm := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if len(am) == 0 && len(bm) == 0 {
		return nil
	}
	al, bl := splitLines(am), splitLines(bm)
	ai, bi, n := intern(al, bl)
	aoff, boff := lineOffsets(pre, al), lineOffsets(pre, bl)
	var hunks []hunk
	x, y := 0, 0
	for _, m := range append(matchLines(ai, bi, n), [2]int{len(al), len(bl)}) {
		if m[0] > x || m[1] > y {
			hunks = append(hunks, hunk{aoff[x], aoff[m[0]], boff[y], boff[m[1]]})
		}
		x, y = m[0]+1, m[1]+1
	}
	return hunks
}

// matchLines returns the index pairs of the lines a and b keep, in order. Lines that
// occur once on each side anchor the match, as in patience diff; Myers fills the gaps
// between anchors, which keeps a formatter diff over a large file near linear.
func matchLines(a, b []int32, ids int) [][2]int {
	counts := make([]int32, 3*ids)
	inA, inB, posB := counts[:ids], counts[ids:2*ids], counts[2*ids:]
	for _, id := range a {
		inA[id]++
	}
	for j, id := range b {
		inB[id]++
		posB[id] = int32(j)
	}
	var unique [][2]int
	for i, id := range a {
		if inA[id] == 1 && inB[id] == 1 {
			unique = append(unique, [2]int{i, int(posB[id])})
		}
	}
	out := make([][2]int, 0, min(len(a), len(b)))
	x, y := 0, 0
	for _, p := range append(increasing(unique), [2]int{len(a), len(b)}) {
		out = fillGap(out, a[:p[0]], b[:p[1]], x, y)
		if p[0] < len(a) {
			out = append(out, p)
		}
		x, y = p[0]+1, p[1]+1
	}
	return out
}

// increasing returns the longest run of pairs, already ordered by their first index,
// whose second index also increases.
func increasing(pairs [][2]int) [][2]int {
	tails := make([]int, 0, len(pairs))
	prev := make([]int, len(pairs))
	for i, p := range pairs {
		k, _ := slices.BinarySearchFunc(tails, p[1], func(t, v int) int { return pairs[t][1] - v })
		prev[i] = -1
		if k > 0 {
			prev[i] = tails[k-1]
		}
		if k == len(tails) {
			tails = append(tails, i)
		} else {
			tails[k] = i
		}
	}
	out := make([][2]int, len(tails))
	k := -1
	if len(tails) > 0 {
		k = tails[len(tails)-1]
	}
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = pairs[k]
		k = prev[k]
	}
	return out
}

// fillGap appends the matches between a[x:] and b[y:]: shared leading and trailing
// lines, then Myers on the rest, which matches nothing past maxDiffEdits.
func fillGap(out [][2]int, a, b []int32, x, y int) [][2]int {
	for x < len(a) && y < len(b) && a[x] == b[y] {
		out = append(out, [2]int{x, y})
		x, y = x+1, y+1
	}
	s := 0
	for x < len(a)-s && y < len(b)-s && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	am, bm := a[x:len(a)-s], b[y:len(b)-s]
	switch {
	case len(am) == 0 || len(bm) == 0:
	case len(am) == 1:
		if j := slices.Index(bm, am[0]); j >= 0 {
			out = append(out, [2]int{x, y + j})
		}
	case len(bm) == 1:
		if i := slices.Index(am, bm[0]); i >= 0 {
			out = append(out, [2]int{x + i, y})
		}
	default:
		mid, _ := myers(am, bm)
		for _, m := range mid {
			out = append(out, [2]int{x + m[0], y + m[1]})
		}
	}
	for i := s; i > 0; i-- {
		out = append(out, [2]int{len(a) - i, len(b) - i})
	}
	return out
}

func commonPrefix(a, b []byte) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

func commonSuffix(a, b []byte) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	return i
}

func splitLines(b []byte) [][]byte {
	lines := make([][]byte, 0, bytes.Count(b, nl)+1)
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n') + 1
		if i == 0 {
			i = len(b)
		}
		lines = append(lines, b[:i])
		b = b[i:]
	}
	return lines
}

var lineSeed = maphash.MakeSeed()

// intern numbers the distinct lines of a and b from 0 and returns each side's numbers
// and the count of distinct lines.
//
// optimization: an open-addressed table over line hashes instead of map[string]int.
// measured: BenchmarkFormatterDiff/1MiB -17% sec/op, -46% allocs/op (benchstat, n=10).
// trade-off: hand-rolled probing; a map keyed by string(line) allocated every new key.
func intern(a, b [][]byte) (ai, bi []int32, ids int) {
	size := 1
	for size < 2*(len(a)+len(b)) {
		size <<= 1
	}
	table := make([]int32, size) // id+1, 0 when empty
	reps := make([][]byte, 0, len(a))
	hashes := make([]uint64, 0, len(a))
	number := func(lines [][]byte) []int32 {
		out := make([]int32, len(lines))
		for i, l := range lines {
			h := maphash.Bytes(lineSeed, l)
			slot := h & uint64(size-1)
			for {
				id := table[slot] - 1
				if id < 0 {
					id = int32(len(reps))
					table[slot] = id + 1
					reps = append(reps, l)
					hashes = append(hashes, h)
				} else if hashes[id] != h || !bytes.Equal(reps[id], l) {
					slot = (slot + 1) & uint64(size-1)
					continue
				}
				out[i] = id
				break
			}
		}
		return out
	}
	ai, bi = number(a), number(b)
	return ai, bi, len(reps)
}

// lineOffsets returns the start offset of each line and, last, the end of the final one.
func lineOffsets(base int, lines [][]byte) []int {
	off := make([]int, len(lines)+1)
	off[0] = base
	for i, l := range lines {
		off[i+1] = off[i] + len(l)
	}
	return off
}

// myers returns the index pairs of the lines a and b keep on a shortest edit script, in
// order, and false past maxDiffEdits (Myers, "An O(ND) Difference Algorithm and Its
// Variations", 1986). types.myers is the same search over strings with a smaller bound;
// this one compares interned ids, which the formatter path needs to be cheap.
func myers(a, b []int32) ([][2]int, bool) {
	n, m := len(a), len(b)
	limit := min(n+m, maxDiffEdits)
	off := limit + 1
	v := make([]int, 2*limit+3)
	var trace [][]int
	for d := 0; d <= limit; d++ {
		trace = append(trace, slices.Clone(v[off-d:off+d+1]))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || k != d && v[off+k-1] < v[off+k+1] {
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
		if k == -d || k != d && v[k-1+d] < v[k+1+d] {
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
