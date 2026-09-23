package mergequeue

// This file holds planning's stack decisions as pure functions over what planning read
// from the version control and the provider: which change is stacked on which, which
// stacks cannot merge, what waits on a change planning settled, and the order admitted
// changes validate and merge in. Nothing here reads a repository.

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/libs/mergequeue/types"
)

// maxStackDepth bounds how many unmerged changes one change may be stacked on.
const maxStackDepth = 16

// stackInput is what stack detection reads, index for index with changes.
type stackInput struct {
	changes []types.Change
	own     []map[string]bool // the commits each change carries that the base does not; nil for a fork
	// tops are each change's head with the merges of the base into it peeled off, which
	// is what a change stacked on it before such a merge carries.
	tops      []string
	merged    []types.MergedChange
	mergedOwn [][]string // each merged change's own commits the base does not carry, newest first
	unqueued  []types.UnqueuedChange
}

// stackNode is a change another may be stacked on.
type stackNode struct {
	id     string
	head   string
	method types.MergeMethod
	open   bool
	own    map[string]bool
}

// detect records, on every change verdicts has not settled, what it is stacked on, and
// settles those that cannot be admitted as stacked, then refuses cycles.
func (in stackInput) detect(verdicts []*types.Verdict) {
	for i := range in.changes {
		if verdicts[i] == nil {
			verdicts[i] = in.stack(i)
		}
	}
	refuseCycles(in.changes, verdicts)
}

// stack finds what change i is stacked on, from ancestry: a listed or merged change whose
// head it carries and the base does not. It records the nearest one's head as the
// change's stack base and, while that one is unmerged, its id as Below; the provider's
// declared parent must agree. It returns a verdict when the change cannot be admitted as
// stacked, or carries an unqueued change.
func (in stackInput) stack(i int) *types.Verdict {
	c := &in.changes[i]
	c.StackBase, c.Below = "", ""
	mine := in.own[i]
	var nodes []stackNode
	for j, o := range in.changes {
		if j != i {
			nodes = append(nodes, stackNode{id: o.ID, head: in.tops[j], method: o.Method, open: true, own: in.own[j]})
		}
	}
	for _, u := range in.unqueued {
		if mine[u.Head] {
			return decided(*c, types.DecisionWait, types.CodeWaitUnqueuedBelow, "carries the head of #"+u.ID+", which is open but not queued", "")
		}
	}
	for j, m := range in.merged {
		// A change merged through an update commit is reported at that commit, which a
		// change stacked on it does not carry: its stack base is the newest of the
		// merged change's own commits it does carry.
		n := stackNode{id: m.ID, head: m.Head, method: m.Method, own: map[string]bool{}}
		var own []string
		if j < len(in.mergedOwn) {
			own = in.mergedOwn[j]
		}
		for _, id := range own {
			n.own[id] = true
		}
		if k := slices.IndexFunc(own, func(id string) bool { return mine[id] }); k >= 0 {
			n.head = own[k]
		}
		nodes = append(nodes, n)
	}
	var below []stackNode
	for _, n := range nodes {
		if mine[n.head] {
			below = append(below, n)
		}
	}
	declared := func() *types.Verdict {
		if c.Parent == "" {
			return nil
		}
		if !slices.ContainsFunc(nodes, func(n stackNode) bool { return n.id == c.Parent }) {
			return decided(*c, types.DecisionWait, types.CodeWaitBelow, "stacked on #"+c.Parent+", which is not queued", "")
		}
		return decided(*c, types.DecisionWait, types.CodeWaitRestack, "not built on #"+c.Parent+"'s head; restack it (gh stack rebase, gt restack or av sync)", "")
	}
	if len(below) == 0 {
		return declared()
	}
	var nearest *stackNode
	for k, n := range below {
		if !slices.ContainsFunc(below, func(o stackNode) bool { return o.id != n.id && !n.own[o.head] }) {
			nearest = &below[k]
			break
		}
	}
	if nearest == nil {
		for _, a := range below {
			for _, b := range below {
				if a.id != b.id && !a.own[b.head] && !b.own[a.head] {
					return refused(*c, "built on #"+a.id+" and #"+b.id+", which are not stacked on each other")
				}
			}
		}
		return refused(*c, "built on changes that are stacked on each other in a cycle")
	}
	if c.Parent != "" && c.Parent != nearest.id {
		return declared()
	}
	unmerged := 0
	for _, n := range below {
		if n.open {
			unmerged++
		}
	}
	switch {
	case unmerged > maxStackDepth:
		return refused(*c, fmt.Sprintf("stacked on %d unmerged changes, and the queue merges stacks up to %d deep", unmerged, maxStackDepth))
	case c.Method == types.MethodRebase:
		return refused(*c, "a stacked change cannot merge with the rebase method yet: predicting it needs git replay, which git still marks experimental")
	case nearest.method != c.Method:
		return refused(*c, fmt.Sprintf("the stack mixes %s (#%s) and %s (#%s), and a stack merges with one merge method", nearest.method, nearest.id, c.Method, c.ID))
	}
	c.StackBase = nearest.head
	if nearest.open {
		c.Below = nearest.id
	}
	return nil
}

// refuseCycles refuses every change whose Below chain comes back to it: two changes at
// one top each read as stacked on the other, and neither can merge first.
func refuseCycles(changes []types.Change, verdicts []*types.Verdict) {
	index := make(map[string]int, len(changes))
	for i, c := range changes {
		index[c.ID] = i
	}
	var cyclic []int
	for i := range changes {
		j, ok := index[changes[i].Below]
		for steps := 0; ok && steps < len(changes); steps++ {
			if j == i {
				cyclic = append(cyclic, i)
				break
			}
			j, ok = index[changes[j].Below]
		}
	}
	for _, i := range cyclic {
		verdicts[i] = refused(changes[i], "stacked in a cycle through #"+changes[i].Below)
	}
}

// arrange holds what is stacked on a change planning settled, then returns the settled
// changes' verdicts in queue order and partitions the rest, each after the change it is
// stacked on.
func arrange(changes []types.Change, verdicts []*types.Verdict) ([]types.Verdict, [][]types.Change) {
	cascade(changes, verdicts)
	var settled []types.Verdict
	var admitted []types.Change
	for i, c := range changes {
		if v := verdicts[i]; v != nil {
			settled = append(settled, *v)
			continue
		}
		admitted = append(admitted, c)
	}
	return settled, partition(stackOrder(admitted))
}

// cascade holds every change stacked on one planning settled: it waits, and is never
// blamed for what the change beneath it did.
func cascade(changes []types.Change, verdicts []*types.Verdict) {
	index := make(map[string]int, len(changes))
	for i, c := range changes {
		index[c.ID] = i
	}
	var hold func(i int) *types.Verdict
	hold = func(i int) *types.Verdict {
		if verdicts[i] != nil || changes[i].Below == "" {
			return verdicts[i]
		}
		j, ok := index[changes[i].Below]
		if !ok {
			return nil
		}
		below := hold(j)
		switch {
		case below == nil:
			return nil
		case below.Code == types.CodeWaitBelowKicked:
			verdicts[i] = decided(changes[i], types.DecisionWait, types.CodeWaitBelowKicked, below.Reason, "")
		case below.Decision == types.DecisionKick:
			verdicts[i] = decided(changes[i], types.DecisionWait, types.CodeWaitBelowKicked, kickedBelow(changes[j].ID), "")
		case below.Decision == types.DecisionMerged:
			// Its head is on the base, so what is stacked on it is stacked on the base.
			changes[i].Below = ""
			return nil
		default:
			verdicts[i] = decided(changes[i], types.DecisionWait, types.CodeWaitBelow, "stacked on #"+changes[j].ID+", which is waiting: "+below.Reason, "")
		}
		return verdicts[i]
	}
	for i := range changes {
		hold(i)
	}
}

// kickedBelow is the wait of a change stacked, directly or not, on kicked.
func kickedBelow(kicked string) string {
	return "#" + kicked + " was kicked back; this stays queued and is validated again once it returns"
}

// stackOrder keeps queue order but moves each change after the one it is stacked on.
func stackOrder(changes []types.Change) []types.Change {
	index := make(map[string]int, len(changes))
	for i, c := range changes {
		index[c.ID] = i
	}
	out := make([]types.Change, 0, len(changes))
	done := make([]bool, len(changes))
	var emit func(i int)
	emit = func(i int) {
		if done[i] {
			return
		}
		done[i] = true
		if j, ok := index[changes[i].Below]; ok {
			emit(j)
		}
		out = append(out, changes[i])
	}
	for i := range changes {
		emit(i)
	}
	return out
}
