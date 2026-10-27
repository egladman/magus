package textbuf

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrRange reports an offset or point outside the text.
var ErrRange = errors.New("position out of range")

const (
	chunkCap = 128
	chunkMin = chunkCap / 2
)

// chunk holds up to chunkCap bytes with their summary precomputed, so measuring a chunk
// never rescans it.
type chunk struct {
	n    uint8
	rows uint8
	col  uint8
	b    [chunkCap]byte
}

func newChunk(b []byte) chunk {
	var c chunk
	copy(c.b[:], b)
	c.set(len(b))
	return c
}

// set makes the first n bytes the chunk's content and recounts its summary.
func (c *chunk) set(n int) {
	b := c.b[:n]
	c.n = uint8(n)
	c.rows = uint8(bytes.Count(b, nl))
	c.col = uint8(n - bytes.LastIndexByte(b, '\n') - 1)
}

func (c *chunk) bytes() []byte { return c.b[:c.n:c.n] }

var nl = []byte{'\n'}

// textSum counts bytes and newlines; col is the byte length of the last line, which makes
// add a monoid over (row, column) points.
type textSum struct {
	bytes int
	rows  int
	col   int
}

func (s textSum) add(o textSum) textSum {
	if o.rows > 0 {
		return textSum{s.bytes + o.bytes, s.rows + o.rows, o.col}
	}
	return textSum{s.bytes + o.bytes, s.rows, s.col + o.col}
}

func (textSum) measure(c *chunk) textSum {
	return textSum{int(c.n), int(c.rows), int(c.col)}
}

// Point is a zero-based line and a byte column within it.
type Point struct {
	Line   int
	Column int
}

// rope is the text, a sum tree of chunks after Zed's crates/rope (GPL-3.0-or-later, see
// NOTICE). Its exported methods read; Buffer and Snapshot promote them.
type rope struct {
	t tree[chunk, textSum]
}

func newRope(text []byte) rope {
	return rope{newTree[chunk, textSum](chunkify(nil, text))}
}

// chunkify appends text to dst as the fewest chunks of even size.
func chunkify(dst []chunk, text []byte) []chunk {
	if len(text) == 0 {
		return dst
	}
	k := (len(text) + chunkCap - 1) / chunkCap
	size, extra := len(text)/k, len(text)%k
	for g := range k {
		m := size
		if g < extra {
			m++
		}
		dst = append(dst, newChunk(text[:m]))
		text = text[m:]
	}
	return dst
}

// Len returns the text's length in bytes.
func (r *rope) Len() int { return r.t.sum.bytes }

// Lines returns the number of lines: one more than the number of newlines.
func (r *rope) Lines() int { return r.t.sum.rows + 1 }

// Bytes returns a copy of the text.
func (r *rope) Bytes() []byte {
	return r.appendRange(make([]byte, 0, r.Len()), 0, r.Len())
}

// String returns the text.
func (r *rope) String() string {
	var b strings.Builder
	b.Grow(r.Len())
	r.t.scan(0, func(c *chunk) bool {
		b.Write(c.bytes())
		return true
	})
	return b.String()
}

// Slice returns a copy of bytes [start, end).
func (r *rope) Slice(start, end int) ([]byte, error) {
	if start < 0 || start > end || end > r.Len() {
		return nil, fmt.Errorf("%w: [%d, %d) in [0, %d]", ErrRange, start, end, r.Len())
	}
	return r.appendRange(make([]byte, 0, end-start), start, end), nil
}

func (r *rope) appendRange(dst []byte, start, end int) []byte {
	if start == end {
		return dst
	}
	i, at := r.locate(start)
	r.t.scan(i, func(c *chunk) bool {
		b := c.bytes()
		lo, hi := max(start-at, 0), min(end-at, len(b))
		dst = append(dst, b[lo:hi]...)
		at += len(b)
		return at < end
	})
	return dst
}

// locate returns the index and start offset of the chunk holding off. An offset on a
// chunk boundary belongs to the chunk it ends, so appends extend that chunk.
func (r *rope) locate(off int) (idx, start int) {
	n := r.t.root
	for n.height > 0 {
		last := len(n.kids) - 1
		for k := range n.kids {
			c := &n.kids[k]
			if off <= start+c.sum.bytes || k == last {
				n = c.node
				break
			}
			start += c.sum.bytes
			idx += c.count
		}
	}
	last := len(n.items) - 1
	for k := range n.items {
		m := int(n.items[k].n)
		if off <= start+m || k == last {
			return idx + k, start
		}
		start += m
	}
	return idx, start
}

// Point returns the line and column of byte offset off.
func (r *rope) Point(off int) (Point, error) {
	if off < 0 || off > r.Len() {
		return Point{}, fmt.Errorf("%w: offset %d in [0, %d]", ErrRange, off, r.Len())
	}
	if r.t.root == nil {
		return Point{}, nil
	}
	var acc textSum
	n := r.t.root
	for n.height > 0 {
		last := len(n.kids) - 1
		for k := range n.kids {
			c := &n.kids[k]
			if off <= acc.bytes+c.sum.bytes || k == last {
				n = c.node
				break
			}
			acc = acc.add(c.sum)
		}
	}
	last := len(n.items) - 1
	for k := range n.items {
		c := &n.items[k]
		if off <= acc.bytes+int(c.n) || k == last {
			part := c.b[:off-acc.bytes]
			if i := bytes.LastIndexByte(part, '\n'); i >= 0 {
				return Point{acc.rows + bytes.Count(part, nl), len(part) - i - 1}, nil
			}
			return Point{acc.rows, acc.col + len(part)}, nil
		}
		acc = acc.add(textSum{int(c.n), int(c.rows), int(c.col)})
	}
	return Point{}, nil
}

// Offset returns the byte offset of p. The column may equal the line's length but not
// pass it.
func (r *rope) Offset(p Point) (int, error) {
	bad := func() (int, error) {
		return 0, fmt.Errorf("%w: point %d:%d", ErrRange, p.Line, p.Column)
	}
	if p.Line < 0 || p.Column < 0 {
		return bad()
	}
	if r.t.root == nil {
		if p == (Point{}) {
			return 0, nil
		}
		return bad()
	}
	reaches := func(end textSum) bool {
		return end.rows > p.Line || end.rows == p.Line && end.col >= p.Column
	}
	if !reaches(r.t.sum) {
		return bad()
	}
	var acc textSum
	n := r.t.root
	for n.height > 0 {
		for k := range n.kids {
			c := &n.kids[k]
			end := acc.add(c.sum)
			if reaches(end) {
				n = c.node
				break
			}
			acc = end
		}
	}
	for k := range n.items {
		c := &n.items[k]
		end := acc.add(textSum{int(c.n), int(c.rows), int(c.col)})
		if !reaches(end) {
			acc = end
			continue
		}
		b := c.bytes()
		i := 0
		col := acc.col
		for row := acc.rows; row < p.Line; row++ {
			i += bytes.IndexByte(b[i:], '\n') + 1
			col = 0
		}
		want := i + p.Column - col
		if want > len(b) || bytes.IndexByte(b[i:want], '\n') >= 0 {
			return bad()
		}
		return acc.bytes + want, nil
	}
	return bad()
}

// replace swaps bytes [s, e) for text. The caller has checked the range.
func (r *rope) replace(s, e int, text string) {
	if r.replaceInChunk(s, e, text) {
		return
	}
	if r.t.root == nil {
		r.t.splice(0, 0, chunkify(nil, []byte(text)))
		return
	}
	i, is := r.locate(s)
	j, js := i, is
	if e > s {
		j, js = r.locate(e)
	}
	first, last := r.t.at(i).bytes(), r.t.at(j).bytes()
	buf := make([]byte, 0, s-is+len(text)+len(last)-(e-js)+chunkCap)
	buf = append(buf, first[:s-is]...)
	buf = append(buf, text...)
	buf = append(buf, last[e-js:]...)
	if len(buf) < chunkMin {
		switch {
		case j+1 < r.t.count:
			j++
			buf = append(buf, r.t.at(j).bytes()...)
		case i > 0:
			i--
			buf = slices.Concat(r.t.at(i).bytes(), buf)
		}
	}
	r.t.splice(i, j+1, chunkify(nil, buf))
}

// maxHeight bounds a tree's height: minItems^maxHeight items is past any text.
const maxHeight = 24

// replaceInChunk edits one chunk in place when [s, e) lies in it and the result stays
// within [chunkMin/2, chunkCap] bytes; otherwise it reports false and changes nothing.
// optimization: write the chunk in place instead of splicing new chunks.
// measured: BenchmarkInsert/64KiB -78% sec/op, 14 to 0 allocs/op (benchstat, n=10).
// trade-off: a second edit path beside splice; chunks may shrink to chunkMin/2.
func (r *rope) replaceInChunk(s, e int, text string) bool {
	n := r.t.root
	if n == nil {
		return false
	}
	var path [maxHeight]int
	depth, start := 0, 0
	for n.height > 0 {
		last := len(n.kids) - 1
		for k := range n.kids {
			c := &n.kids[k]
			if s <= start+c.sum.bytes || k == last {
				path[depth] = k
				depth++
				n = c.node
				break
			}
			start += c.sum.bytes
		}
	}
	k, last := 0, len(n.items)-1
	for ; k < last && s > start+int(n.items[k].n); k++ {
		start += int(n.items[k].n)
	}
	c := &n.items[k]
	lo, hi := s-start, e-start
	m := int(c.n) - (hi - lo) + len(text)
	if hi > int(c.n) || m > chunkCap || m == 0 || m < chunkMin/2 && r.t.count > 1 {
		return false
	}
	var parents [maxHeight]*node[chunk, textSum]
	n = r.t.mutRoot()
	for d := range depth {
		parents[d] = n
		n = r.t.mutKid(n, path[d])
	}
	c = &n.items[k]
	copy(c.b[lo+len(text):m], c.b[hi:c.n])
	copy(c.b[lo:], text)
	c.set(m)
	sum := sumChunks(n.items)
	for d := depth - 1; d >= 0; d-- {
		p := parents[d]
		p.kids[path[d]].sum = sum
		sum = sumKids(p.kids)
	}
	r.t.sum = sum
	return true
}

// sumChunks and sumKids are node.child for ropes, minus the generic dispatch.
func sumChunks(items []chunk) textSum {
	var s textSum
	for i := range items {
		s = s.add(textSum{int(items[i].n), int(items[i].rows), int(items[i].col)})
	}
	return s
}

func sumKids(kids []child[chunk, textSum]) textSum {
	var s textSum
	for i := range kids {
		s = s.add(kids[i].sum)
	}
	return s
}
