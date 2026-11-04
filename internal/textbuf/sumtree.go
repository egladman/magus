package textbuf

import (
	"slices"
	"sync/atomic"
)

// maxItems 8 beat 16 only on BenchmarkInsertSnapshot (-18%), and lost on edits (+5-17%)
// and on resolving 16k anchors (+31%).
const (
	maxItems = 16
	minItems = maxItems / 2
)

// summary is the monoid a tree folds over its items: add combines two adjacent spans,
// left then right, and the zero value is the identity.
type summary[S, T any] interface {
	add(S) S
	measure(*T) S
}

// child is one slot of an inner node. Its summary and item count sit inline so a
// descent reads them without touching the child node.
type child[T any, S summary[S, T]] struct {
	sum   S
	count int
	node  *node[T, S]
}

// node is a leaf (height 0, items) or an inner node (kids). A node belongs to the tree
// whose owner it carries; any other tree clones it before writing.
type node[T any, S summary[S, T]] struct {
	owner  uint64
	height int
	items  []T
	kids   []child[T, S]
}

// leafNode and innerNode put a node and its fixed-capacity backing array in one
// allocation.
type leafNode[T any, S summary[S, T]] struct {
	node[T, S]
	buf [maxItems]T
}

type innerNode[T any, S summary[S, T]] struct {
	node[T, S]
	buf [maxItems]child[T, S]
}

func newLeaf[T any, S summary[S, T]](owner uint64) *node[T, S] {
	l := &leafNode[T, S]{}
	l.owner = owner
	l.items = l.buf[:0]
	return &l.node
}

func newInner[T any, S summary[S, T]](owner uint64, height int) *node[T, S] {
	n := &innerNode[T, S]{}
	n.owner = owner
	n.height = height
	n.kids = n.buf[:0]
	return &n.node
}

func (n *node[T, S]) len() int {
	if n.height == 0 {
		return len(n.items)
	}
	return len(n.kids)
}

func (n *node[T, S]) clone(owner uint64) *node[T, S] {
	if n.height == 0 {
		c := newLeaf[T, S](owner)
		c.items = append(c.items, n.items...)
		return c
	}
	c := newInner[T, S](owner, n.height)
	c.kids = append(c.kids, n.kids...)
	return c
}

func (n *node[T, S]) child() child[T, S] {
	var s S
	if n.height == 0 {
		for i := range n.items {
			s = s.add(s.measure(&n.items[i]))
		}
		return child[T, S]{sum: s, count: len(n.items), node: n}
	}
	count := 0
	for i := range n.kids {
		s = s.add(n.kids[i].sum)
		count += n.kids[i].count
	}
	return child[T, S]{sum: s, count: count, node: n}
}

var owners atomic.Uint64

// tree is a copy-on-write B+ tree after Zed's sum_tree (GPL-3.0-or-later, see NOTICE).
// Writes go through one tree value; freeze hands out a read-only copy in O(1), after
// which the writer clones each shared node it touches.
type tree[T any, S summary[S, T]] struct {
	root  *node[T, S]
	sum   S
	count int
	owner uint64
}

func newTree[T any, S summary[S, T]](items []T) tree[T, S] {
	t := tree[T, S]{owner: owners.Add(1)}
	t.setRoot(t.packLeaves(items))
	return t
}

// freeze returns a read-only copy. Owner 0 matches no node, so a write through the copy
// would clone rather than reach the writer's nodes.
func (t *tree[T, S]) freeze() tree[T, S] {
	s := *t
	s.owner = 0
	t.owner = owners.Add(1)
	return s
}

func (t *tree[T, S]) mut(n *node[T, S]) *node[T, S] {
	if n.owner == t.owner {
		return n
	}
	return n.clone(t.owner)
}

func (t *tree[T, S]) mutRoot() *node[T, S] {
	t.root = t.mut(t.root)
	return t.root
}

func (t *tree[T, S]) mutKid(n *node[T, S], i int) *node[T, S] {
	k := t.mut(n.kids[i].node)
	n.kids[i].node = k
	return k
}

func (t *tree[T, S]) setRoot(kids []child[T, S]) {
	for len(kids) > 1 {
		kids = t.packInner(kids, kids[0].node.height+1)
	}
	if len(kids) == 0 {
		var zero S
		t.root, t.sum, t.count = nil, zero, 0
		return
	}
	root := kids[0]
	for root.node.height > 0 && len(root.node.kids) == 1 {
		root = root.node.kids[0]
	}
	t.root, t.sum, t.count = root.node, root.sum, root.count
}

// groups splits n entries into the fewest groups of at most maxItems, as even as
// possible, so every group holds at least minItems once n exceeds maxItems.
func groups(n int) (k, size, extra int) {
	k = (n + maxItems - 1) / maxItems
	return k, n / k, n % k
}

func (t *tree[T, S]) packLeaves(items []T) []child[T, S] {
	if len(items) == 0 {
		return nil
	}
	k, size, extra := groups(len(items))
	out := make([]child[T, S], 0, k)
	for g := range k {
		m := size
		if g < extra {
			m++
		}
		l := newLeaf[T, S](t.owner)
		l.items = append(l.items, items[:m]...)
		items = items[m:]
		out = append(out, l.child())
	}
	return out
}

func (t *tree[T, S]) packInner(kids []child[T, S], height int) []child[T, S] {
	if len(kids) == 0 {
		return nil
	}
	k, size, extra := groups(len(kids))
	out := make([]child[T, S], 0, k)
	for g := range k {
		m := size
		if g < extra {
			m++
		}
		n := newInner[T, S](t.owner, height)
		n.kids = append(n.kids, kids[:m]...)
		kids = kids[m:]
		out = append(out, n.child())
	}
	return out
}

// splice replaces items [lo, hi) with repl.
func (t *tree[T, S]) splice(lo, hi int, repl []T) {
	if t.root == nil {
		t.setRoot(t.packLeaves(repl))
		return
	}
	t.setRoot(t.spliceNode(t.root, lo, hi, repl))
}

// spliceNode returns the nodes, all at n's height, that replace n. Each holds at least
// minItems entries except when it is the only one.
func (t *tree[T, S]) spliceNode(n *node[T, S], lo, hi int, repl []T) []child[T, S] {
	if n.height == 0 {
		m := len(n.items) - (hi - lo) + len(repl)
		if m == 0 {
			return nil
		}
		if m <= maxItems {
			n = t.mut(n)
			n.items = slices.Replace(n.items, lo, hi, repl...)
			return []child[T, S]{n.child()}
		}
		all := make([]T, 0, m)
		all = append(all, n.items[:lo]...)
		all = append(all, repl...)
		all = append(all, n.items[hi:]...)
		return t.packLeaves(all)
	}
	last := len(n.kids) - 1
	i, si := 0, 0
	for i < last && lo >= si+n.kids[i].count {
		si += n.kids[i].count
		i++
	}
	j, sj := i, si
	for j < last && hi > sj+n.kids[j].count {
		sj += n.kids[j].count
		j++
	}
	var mid []child[T, S]
	if i == j {
		mid = t.spliceNode(n.kids[i].node, lo-si, hi-si, repl)
	} else {
		mid = t.spliceNode(n.kids[i].node, lo-si, n.kids[i].count, repl)
		mid = append(mid, t.spliceNode(n.kids[j].node, 0, hi-sj, nil)...)
	}
	kids := make([]child[T, S], 0, len(n.kids)-(j+1-i)+len(mid))
	kids = append(kids, n.kids[:i]...)
	kids = append(kids, mid...)
	kids = append(kids, n.kids[j+1:]...)
	kids = t.rebalance(kids)
	switch {
	case len(kids) == 0:
		return nil
	case len(kids) <= maxItems:
		n = t.mut(n)
		n.kids = append(n.kids[:0], kids...)
		return []child[T, S]{n.child()}
	default:
		return t.packInner(kids, n.height)
	}
}

// rebalance merges each underfull node in kids into a neighbor.
func (t *tree[T, S]) rebalance(kids []child[T, S]) []child[T, S] {
	for k := 0; k < len(kids) && len(kids) > 1; k++ {
		if kids[k].node.len() >= minItems {
			continue
		}
		l := k
		if k+1 == len(kids) {
			l = k - 1
		}
		merged := t.merge(kids[l].node, kids[l+1].node)
		kids = slices.Replace(kids, l, l+2, merged...)
		k = l - 1
	}
	return kids
}

func (t *tree[T, S]) merge(a, b *node[T, S]) []child[T, S] {
	if a.height == 0 {
		all := make([]T, 0, len(a.items)+len(b.items))
		all = append(append(all, a.items...), b.items...)
		return t.packLeaves(all)
	}
	all := make([]child[T, S], 0, len(a.kids)+len(b.kids))
	all = append(append(all, a.kids...), b.kids...)
	// An underfull node with no sibling may sit at the seam.
	return t.packInner(t.rebalance(all), a.height)
}

// at returns the item at index i. The pointer is read-only.
func (t *tree[T, S]) at(i int) *T {
	n := t.root
	for n.height > 0 {
		k := 0
		for i >= n.kids[k].count {
			i -= n.kids[k].count
			k++
		}
		n = n.kids[k].node
	}
	return &n.items[i]
}

// scan calls fn on each item from index lo on, in order, until fn returns false.
func (t *tree[T, S]) scan(lo int, fn func(*T) bool) {
	if t.root != nil {
		scanNode(t.root, lo, fn)
	}
}

func scanNode[T any, S summary[S, T]](n *node[T, S], lo int, fn func(*T) bool) bool {
	if n.height == 0 {
		for i := lo; i < len(n.items); i++ {
			if !fn(&n.items[i]) {
				return false
			}
		}
		return true
	}
	for k := range n.kids {
		c := n.kids[k].count
		if lo >= c {
			lo -= c
			continue
		}
		if !scanNode(n.kids[k].node, lo, fn) {
			return false
		}
		lo = 0
	}
	return true
}

// collect appends items [lo, hi) to dst.
func (t *tree[T, S]) collect(lo, hi int, dst []T) []T {
	if lo >= hi {
		return dst
	}
	n := hi - lo
	t.scan(lo, func(it *T) bool {
		dst = append(dst, *it)
		n--
		return n > 0
	})
	return dst
}
