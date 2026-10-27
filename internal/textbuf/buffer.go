package textbuf

import (
	"cmp"
	"fmt"
	"slices"
)

// Buffer is a text with anchors. It has one writer: a Buffer is not safe for concurrent
// use, and readers on other goroutines take a Snapshot.
type Buffer struct {
	rope
	anchors [2]anchorTree // by Bias
	version uint64
}

// Snapshot is an immutable view of a Buffer's text at one version, safe for concurrent
// use. Taking one costs O(1); the Buffer copies the nodes it later changes.
type Snapshot struct {
	rope
	version uint64
}

// New returns a Buffer holding a copy of text, at version 0.
func New(text []byte) *Buffer {
	return &Buffer{
		rope:    newRope(text),
		anchors: [2]anchorTree{newAnchorTree(), newAnchorTree()},
	}
}

// Version counts the calls to Apply and ApplyDiff that changed the text.
func (b *Buffer) Version() uint64 { return b.version }

// Snapshot returns the current text.
func (b *Buffer) Snapshot() Snapshot {
	return Snapshot{rope{b.t.freeze()}, b.version}
}

// Version is the Buffer version the snapshot was taken at.
func (s *Snapshot) Version() uint64 { return s.version }

// Anchor creates an anchor at byte offset off. Release it once unused.
func (b *Buffer) Anchor(off int, bias Bias) (Anchor, error) {
	if off < 0 || off > b.Len() {
		return 0, fmt.Errorf("%w: offset %d in [0, %d]", ErrRange, off, b.Len())
	}
	if bias != Left && bias != Right {
		return 0, fmt.Errorf("invalid bias %d", bias)
	}
	a := Anchor(b.anchors[bias].add(off) + 1)
	if bias == Right {
		a |= rightBit
	}
	return a, nil
}

// Resolve returns the anchor's current byte offset in O(log n). It panics when a was
// released.
func (b *Buffer) Resolve(a Anchor) int {
	if a == Start {
		return 0
	}
	return b.anchors[a.bias()].resolve(a.slot())
}

// Release frees the anchor; its handle may be reused by a later Anchor call. Releasing
// Start does nothing.
func (b *Buffer) Release(a Anchor) {
	if a != Start {
		b.anchors[a.bias()].remove(a.slot())
	}
}

// Edit replaces Delete bytes at Offset bytes past Anchor's position with Text.
type Edit struct {
	Anchor Anchor
	Offset int
	Delete int
	Text   string
}

type span struct {
	start, end int
	edit       int
}

// Apply resolves every edit against the current text, then applies them as one change.
// Ranges must not overlap; inserts at one position land in argument order. On error
// nothing is applied.
func (b *Buffer) Apply(edits ...Edit) error {
	spans := make([]span, len(edits))
	for i, e := range edits {
		s := b.Resolve(e.Anchor) + e.Offset
		if e.Delete < 0 || s < 0 || s+e.Delete > b.Len() {
			return fmt.Errorf("edit %d: %w: [%d, %d) in [0, %d]", i, ErrRange, s, s+e.Delete, b.Len())
		}
		spans[i] = span{s, s + e.Delete, i}
	}
	slices.SortStableFunc(spans, func(x, y span) int { return cmp.Or(x.start-y.start, x.end-y.end) })
	for i := 1; i < len(spans); i++ {
		if spans[i-1].end > spans[i].start {
			return fmt.Errorf("edits %d and %d overlap", spans[i-1].edit, spans[i].edit)
		}
	}
	for i := len(spans) - 1; i >= 0; i-- {
		sp := spans[i]
		b.replace(sp.start, sp.end, edits[sp.edit].Text)
	}
	if len(spans) > 0 {
		b.version++
	}
	return nil
}

func (b *Buffer) replace(s, e int, text string) {
	if s == e && text == "" {
		return
	}
	b.rope.replace(s, e, text)
	for bias := range b.anchors {
		a := &b.anchors[bias]
		if e > s {
			a.collapse(s, e)
		}
		if text != "" {
			a.shift(s, Bias(bias) == Right, len(text))
		}
	}
}
