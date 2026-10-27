package textbuf

import "math"

// Bias says which side of an insertion at an anchor's exact position the anchor keeps
// to, as in Zed: Left stays before the inserted text, Right moves past it.
type Bias uint8

const (
	Left Bias = iota
	Right
)

// Anchor is a stable handle to a position that moves with edits. Text deleted around an
// anchor collapses it to the deletion's edge. The zero Anchor is Start, the beginning of
// the text, which needs no Release.
type Anchor uint32

// Start is the anchor at offset 0.
const Start Anchor = 0

const rightBit Anchor = 1 << 31

func (a Anchor) bias() Bias {
	if a&rightBit != 0 {
		return Right
	}
	return Left
}

func (a Anchor) slot() uint32 { return uint32(a&^rightBit) - 1 }

// anchorEntry is one anchor in text order. gap is the distance from the previous
// anchor, so an edit shifts every later anchor by changing one gap. key is dense and
// increases in text order; it finds the entry for a handle.
type anchorEntry struct {
	key  uint64
	gap  int
	slot uint32
}

type anchorSum struct {
	pos    int
	maxKey uint64
}

func (s anchorSum) add(o anchorSum) anchorSum {
	return anchorSum{s.pos + o.pos, max(s.maxKey, o.maxKey)}
}

func (anchorSum) measure(e *anchorEntry) anchorSum { return anchorSum{e.gap, e.key} }

// keyStep spaces keys appended at the end, so appends do not halve the free key space.
const keyStep = 1 << 32

// anchorTree holds the anchors of one bias.
type anchorTree struct {
	t    tree[anchorEntry, anchorSum]
	keys []uint64 // by slot; 0 marks a free slot
	free []uint32
}

func newAnchorTree() anchorTree {
	return anchorTree{t: newTree[anchorEntry, anchorSum](nil)}
}

// seek finds the first entry whose position passes at: greater than at, or, with
// inclusive, equal to it as well. It returns the entry's index and the position and
// maximum key before it; found is false when no entry passes at.
func (a *anchorTree) seek(at int, inclusive bool) (idx, prevPos int, prevKey uint64, found bool) {
	n := a.t.root
	if n == nil {
		return 0, 0, 0, false
	}
	passes := func(p int) bool { return p > at || inclusive && p == at }
	if !passes(a.t.sum.pos) {
		return a.t.count, a.t.sum.pos, a.t.sum.maxKey, false
	}
	for n.height > 0 {
		for k := range n.kids {
			c := &n.kids[k]
			if passes(prevPos + c.sum.pos) {
				n = c.node
				break
			}
			prevPos += c.sum.pos
			prevKey = max(prevKey, c.sum.maxKey)
			idx += c.count
		}
	}
	for k := range n.items {
		e := &n.items[k]
		if passes(prevPos + e.gap) {
			return idx + k, prevPos, prevKey, true
		}
		prevPos += e.gap
		prevKey = e.key
	}
	return idx, prevPos, prevKey, false
}

// find returns the index and position of the entry with key.
func (a *anchorTree) find(key uint64) (idx, pos int) {
	n := a.t.root
	for n.height > 0 {
		for k := range n.kids {
			c := &n.kids[k]
			if c.sum.maxKey >= key {
				n = c.node
				break
			}
			pos += c.sum.pos
			idx += c.count
		}
	}
	for k := range n.items {
		e := &n.items[k]
		pos += e.gap
		if e.key == key {
			return idx + k, pos
		}
	}
	panic("textbuf: anchor key missing from its tree")
}

func (a *anchorTree) add(pos int) uint32 {
	idx, prevPos, prevKey, found := a.seek(pos, false)
	next := uint64(math.MaxUint64)
	if found {
		next = a.t.at(idx).key
	}
	key, ok := between(prevKey, next)
	if !ok {
		a.relabel()
		return a.add(pos)
	}
	var slot uint32
	if n := len(a.free); n > 0 {
		slot = a.free[n-1]
		a.free = a.free[:n-1]
		a.keys[slot] = key
	} else {
		slot = uint32(len(a.keys))
		a.keys = append(a.keys, key)
	}
	e := anchorEntry{key: key, gap: pos - prevPos, slot: slot}
	if !found {
		a.t.splice(idx, idx, []anchorEntry{e})
		return slot
	}
	succ := *a.t.at(idx)
	succ.gap -= e.gap
	a.t.splice(idx, idx+1, []anchorEntry{e, succ})
	return slot
}

// between returns a key strictly between lo and hi.
func between(lo, hi uint64) (uint64, bool) {
	if hi-lo < 2 {
		return 0, false
	}
	return lo + min(keyStep, (hi-lo)/2), true
}

func (a *anchorTree) relabel() {
	entries := a.t.collect(0, a.t.count, nil)
	stride := math.MaxUint64 / uint64(len(entries)+2)
	for i := range entries {
		entries[i].key = uint64(i+1) * stride
		a.keys[entries[i].slot] = entries[i].key
	}
	a.t = newTree[anchorEntry, anchorSum](entries)
}

func (a *anchorTree) key(slot uint32) uint64 {
	if int(slot) >= len(a.keys) || a.keys[slot] == 0 {
		panic("textbuf: anchor was released or never created")
	}
	return a.keys[slot]
}

func (a *anchorTree) resolve(slot uint32) int {
	_, pos := a.find(a.key(slot))
	return pos
}

func (a *anchorTree) remove(slot uint32) {
	idx, _ := a.find(a.key(slot))
	a.keys[slot] = 0
	a.free = append(a.free, slot)
	e := *a.t.at(idx)
	if idx+1 == a.t.count {
		a.t.splice(idx, idx+1, nil)
		return
	}
	succ := *a.t.at(idx + 1)
	succ.gap += e.gap
	a.t.splice(idx, idx+2, []anchorEntry{succ})
}

// shift adds delta to the gap of the first entry past at, which moves it and every
// entry after it. It writes the gap and the summaries above it in place.
func (a *anchorTree) shift(at int, inclusive bool, delta int) {
	passes := func(p int) bool { return p > at || inclusive && p == at }
	if a.t.root == nil || !passes(a.t.sum.pos) {
		return
	}
	n := a.t.mutRoot()
	pos := 0
	for n.height > 0 {
		for k := range n.kids {
			c := &n.kids[k]
			if passes(pos + c.sum.pos) {
				c.sum.pos += delta
				n = a.t.mutKid(n, k)
				break
			}
			pos += c.sum.pos
		}
	}
	for k := range n.items {
		if e := &n.items[k]; passes(pos + e.gap) {
			e.gap += delta
			break
		}
		pos += n.items[k].gap
	}
	a.t.sum.pos += delta
}

// collapse moves the entries in (s, e] to s and shifts the ones after e back by e-s.
func (a *anchorTree) collapse(s, e int) {
	idx, prevPos, _, found := a.seek(s, false)
	if !found {
		return
	}
	if prevPos+a.t.at(idx).gap > e {
		a.shift(s, false, s-e)
		return
	}
	var moved []anchorEntry
	pos, newPrev := prevPos, prevPos
	a.t.scan(idx, func(it *anchorEntry) bool {
		pos += it.gap
		x := *it
		newPos := s
		if pos > e {
			newPos = pos - (e - s)
		}
		x.gap = newPos - newPrev
		newPrev = newPos
		moved = append(moved, x)
		return pos <= e
	})
	a.t.splice(idx, idx+len(moved), moved)
}
