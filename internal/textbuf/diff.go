package textbuf

import (
	"bytes"
	"slices"
	"unicode/utf8"
)

// maxDiffEdits bounds the Myers search. A middle that needs more line edits becomes one
// hunk: still correct, but anchors inside it collapse.
const maxDiffEdits = 4096

// ApplyDiff replaces the text with text as a line diff, each hunk trimmed to the bytes
// that differ, so anchors outside the changed bytes keep their places. It is how
// formatter output and file reloads come in.
func (b *Buffer) ApplyDiff(text []byte) {
	old := b.Bytes()
	var edits []Edit
	for _, h := range lineDiff(old, text) {
		a, n := trim(old[h.a0:h.a1], text[h.b0:h.b1])
		edits = append(edits, Edit{Offset: h.a0 + a.start, Delete: a.end - a.start, Text: string(n)})
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
	ids := make(map[string]int, len(al)+len(bl))
	ai, bi := intern(ids, al), intern(ids, bl)
	matches, ok := myers(ai, bi)
	if !ok {
		return []hunk{{pre, pre + len(am), pre, pre + len(bm)}}
	}
	aoff, boff := lineOffsets(pre, al), lineOffsets(pre, bl)
	var hunks []hunk
	x, y := 0, 0
	for _, m := range append(matches, [2]int{len(al), len(bl)}) {
		if m[0] > x || m[1] > y {
			hunks = append(hunks, hunk{aoff[x], aoff[m[0]], boff[y], boff[m[1]]})
		}
		x, y = m[0]+1, m[1]+1
	}
	return hunks
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
	var lines [][]byte
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

func intern(ids map[string]int, lines [][]byte) []int {
	out := make([]int, len(lines))
	for i, l := range lines {
		id, ok := ids[string(l)]
		if !ok {
			id = len(ids)
			ids[string(l)] = id
		}
		out[i] = id
	}
	return out
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
func myers(a, b []int) ([][2]int, bool) {
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
