package textbuf

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// errRange reports an offset or point outside the text.
var errRange = errors.New("position out of range")

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

// point is a zero-based line and a byte column within it.
type point struct {
	Line   int
	Column int
}

// rope is the text, a sum tree of chunks after Zed's crates/rope (GPL-3.0-or-later, see
// NOTICE). Its exported methods read; buffer and snapshot promote them.
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
		return nil, fmt.Errorf("%w: [%d, %d) in [0, %d]", errRange, start, end, r.Len())
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
	var p chunkPath
	r.pathTo(off, &p)
	return p.index, p.start
}

// Point returns the line and column of byte offset off.
func (r *rope) Point(off int) (point, error) {
	if off < 0 || off > r.Len() {
		return point{}, fmt.Errorf("%w: offset %d in [0, %d]", errRange, off, r.Len())
	}
	if r.t.root == nil {
		return point{}, nil
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
				return point{acc.rows + bytes.Count(part, nl), len(part) - i - 1}, nil
			}
			return point{acc.rows, acc.col + len(part)}, nil
		}
		acc = acc.add(textSum{int(c.n), int(c.rows), int(c.col)})
	}
	return point{}, nil
}

// Offset returns the byte offset of p. The column may equal the line's length but not
// pass it.
func (r *rope) Offset(p point) (int, error) {
	reaches := func(end textSum) bool {
		return end.rows > p.Line || end.rows == p.Line && end.col >= p.Column
	}
	if p.Line < 0 || p.Column < 0 || !reaches(r.t.sum) {
		return 0, fmt.Errorf("%w: point %d:%d", errRange, p.Line, p.Column)
	}
	if r.t.root == nil {
		return 0, nil
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
		if reaches(end) {
			if off, ok := c.offset(acc, p); ok {
				return acc.bytes + off, nil
			}
			break
		}
		acc = end
	}
	return 0, fmt.Errorf("%w: point %d:%d", errRange, p.Line, p.Column)
}

// offset returns the offset of p within the chunk, which starts at point acc, and false
// when p's column runs past the end of its line.
func (c *chunk) offset(acc textSum, p point) (int, bool) {
	b := c.bytes()
	i, col := 0, acc.col
	for row := acc.rows; row < p.Line; row++ {
		i += bytes.IndexByte(b[i:], '\n') + 1
		col = 0
	}
	want := i + p.Column - col
	return want, want <= len(b) && bytes.IndexByte(b[i:want], '\n') < 0
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
const maxHeight = 12

// replaceInChunk edits the chunk holding [s, e) in place, splitting it in two inside
// its leaf when it outgrows chunkCap; otherwise it reports false and changes nothing.
// optimization: write the chunk in place instead of splicing new chunks.
// measured: BenchmarkInsert/64KiB -78% sec/op, 14 to 0 allocs/op, split -73% B/op (n=10).
// trade-off: a second edit path beside splice; chunks may shrink to chunkMin/2.
func (r *rope) replaceInChunk(s, e int, text string) bool {
	if r.t.root == nil {
		return false
	}
	var p chunkPath
	r.pathTo(s, &p)
	leaf := p.nodes[p.depth]
	c := &leaf.items[p.slots[p.depth]]
	lo, hi := s-p.start, e-p.start
	m := int(c.n) - (hi - lo) + len(text)
	split := m > chunkCap && m <= 2*chunkCap && len(leaf.items) < maxItems
	if hi > int(c.n) || m > chunkCap && !split || m == 0 || m < chunkMin/2 && r.t.count > 1 {
		return false
	}
	leaf = r.own(&p)
	k := p.slots[p.depth]
	c = &leaf.items[k]
	grow := 0
	if split {
		var tmp [2 * chunkCap]byte
		w := copy(tmp[:], c.b[:lo])
		w += copy(tmp[w:], text)
		w += copy(tmp[w:], c.b[hi:c.n])
		leaf.items = slices.Insert(leaf.items, k+1, newChunk(tmp[w/2:w]))
		c = &leaf.items[k]
		copy(c.b[:], tmp[:w/2])
		c.set(w / 2)
		grow = 1
	} else {
		copy(c.b[lo+len(text):m], c.b[hi:c.n])
		copy(c.b[lo:], text)
		c.set(m)
	}
	r.resum(&p, grow)
	return true
}

// chunkPath is the descent to one chunk: nodes[d] and its slot slots[d] at each depth
// down to the leaf at nodes[depth]. The chunk is item index of the rope and starts at
// byte offset start.
type chunkPath struct {
	nodes               [maxHeight + 1]*node[chunk, textSum]
	slots               [maxHeight + 1]int
	depth, index, start int
}

// pathTo fills p with the descent to the chunk holding off.
func (r *rope) pathTo(off int, p *chunkPath) {
	n := r.t.root
	for n.height > 0 {
		last := len(n.kids) - 1
		for k := range n.kids {
			c := &n.kids[k]
			if off <= p.start+c.sum.bytes || k == last {
				p.nodes[p.depth], p.slots[p.depth] = n, k
				p.depth++
				n = c.node
				break
			}
			p.start += c.sum.bytes
			p.index += c.count
		}
	}
	k, last := 0, len(n.items)-1
	for ; k < last && off > p.start+int(n.items[k].n); k++ {
		p.start += int(n.items[k].n)
	}
	p.nodes[p.depth], p.slots[p.depth] = n, k
	p.index += k
}

// own makes every node on the path the writer's, cloning shared ones, and returns the leaf.
func (r *rope) own(p *chunkPath) *node[chunk, textSum] {
	n := r.t.mutRoot()
	for d := range p.depth {
		p.nodes[d] = n
		n = r.t.mutKid(n, p.slots[d])
	}
	p.nodes[p.depth] = n
	return n
}

// resum recomputes the summaries on an owned path after its leaf changed and gained
// grow chunks.
func (r *rope) resum(p *chunkPath, grow int) {
	sum := sumChunks(p.nodes[p.depth].items)
	for d := p.depth - 1; d >= 0; d-- {
		k := &p.nodes[d].kids[p.slots[d]]
		k.sum = sum
		k.count += grow
		sum = sumKids(p.nodes[d].kids)
	}
	r.t.sum = sum
	r.t.count += grow
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
