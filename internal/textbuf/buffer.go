package textbuf

import (
	"cmp"
	"fmt"
	"slices"
)

// buffer is a text with anchors. It has one writer: a buffer is not safe for concurrent
// use, and readers on other goroutines take a snapshot.
type buffer struct {
	rope
	anchors [2]anchorTree // by bias
	version uint64
}

// snapshot is an immutable view of a buffer's text at one version, safe for concurrent
// use. Taking one costs O(1); the buffer copies the nodes it later changes.
type snapshot struct {
	rope
	version uint64
}

// newBuffer returns a buffer holding a copy of text, at version 0.
func newBuffer(text []byte) *buffer {
	return &buffer{
		rope:    newRope(text),
		anchors: [2]anchorTree{newAnchorTree(), newAnchorTree()},
	}
}

// Version counts applied changes: each Apply or ApplyDiff with at least one edit adds one.
func (b *buffer) Version() uint64 { return b.version }

// Snapshot returns the current text.
func (b *buffer) Snapshot() snapshot {
	return snapshot{rope{b.t.freeze()}, b.version}
}

// Version is the buffer version the snapshot was taken at.
func (s *snapshot) Version() uint64 { return s.version }

// Anchor creates an anchor at byte offset off. Release it once unused.
func (b *buffer) Anchor(off int, side bias) (anchor, error) {
	if off < 0 || off > b.Len() {
		return 0, fmt.Errorf("%w: offset %d in [0, %d]", errRange, off, b.Len())
	}
	if side != left && side != right {
		return 0, fmt.Errorf("invalid bias %d", side)
	}
	a := anchor(b.anchors[side].add(off) + 1)
	if side == right {
		a |= rightBit
	}
	return a, nil
}

// Resolve returns the anchor's current byte offset in O(log n). It panics when a was
// released.
func (b *buffer) Resolve(a anchor) int {
	if a == startAnchor {
		return 0
	}
	return b.anchors[a.side()].resolve(a.slot())
}

// Release frees the anchor; its handle may be reused by a later Anchor call. Releasing
// startAnchor does nothing.
func (b *buffer) Release(a anchor) {
	if a != startAnchor {
		b.anchors[a.side()].remove(a.slot())
	}
}

// edit replaces Delete bytes at Offset bytes past Anchor's position with Text.
type edit struct {
	Anchor anchor
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
func (b *buffer) Apply(edits ...edit) error {
	var small [4]span
	spans := small[:0]
	for i, e := range edits {
		s := b.Resolve(e.Anchor) + e.Offset
		if e.Delete < 0 || s < 0 || s+e.Delete > b.Len() {
			return fmt.Errorf("edit %d [%d, %d) in [0, %d]: %w", i, s, s+e.Delete, b.Len(), errRange)
		}
		spans = append(spans, span{s, s + e.Delete, i})
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

func (b *buffer) replace(s, e int, text string) {
	if s == e && text == "" {
		return
	}
	b.rope.replace(s, e, text)
	for side := range b.anchors {
		a := &b.anchors[side]
		if e > s {
			a.collapse(s, e)
		}
		if text != "" {
			a.shift(s, bias(side) == right, len(text))
		}
	}
}
