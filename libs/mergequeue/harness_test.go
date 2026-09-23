package mergequeue

// The stack harness drives whole queue runs (plan, validate, apply) over the model with
// random stacks, merge methods, declared parents, red gates, provider refusals, pushes
// after listing and changes landed by hand, and checks after every run the invariants
// stacks add to the queue's:
//
//	I13 stack order: a change never lands before a queued, unlanded change whose head it
//	    carries.
//	I14 own delta: what a change lands is its own delta, from the change it is stacked on,
//	    on the base before it, wherever that merges cleanly: nothing lost and nothing
//	    resurrected.
//	I15 method honesty: the base's history has the shape the merge method gives (the
//	    Applier stops otherwise, so any Applier error is a violation).
//	I16 cascade without blame: only a change whose own gate is red is kicked back red,
//	    and nothing stacked on a kicked change lands in the run that kicked it.
//	I18 retarget: nothing merges into a base other than the queue's.
//	I19 atomic run: a stack landed in one call lands whole or stops applying.

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// hchange is what the harness knows of a change: the truth the queue has to find.
type hchange struct {
	Change
	from    string // the commit it was branched from: its own delta starts there
	stacked string // the change it was branched from, "" off main
	landed  bool
}

type harness struct {
	t       *rapid.T
	w       *world
	changes map[string]*hchange
	order   []string
	n       int
}

func (h *harness) open() []*hchange {
	var out []*hchange
	for _, id := range h.order {
		if c := h.changes[id]; !c.landed {
			if _, kicked := h.w.p.kicked[id]; !kicked {
				out = append(out, c)
			}
		}
	}
	return out
}

var files = []string{"f0", "f1", "f2", "f3"}

// newChange branches a change off main or off an open or landed change's head.
func (h *harness) newChange() {
	h.n++
	id := fmt.Sprintf("c%d", h.n)
	hc := &hchange{}
	open := h.open()
	from := h.w.main()
	switch kind := rapid.IntRange(0, 2).Draw(h.t, "kind"); {
	case kind == 1 && len(open) > 0:
		below := rapid.SampledFrom(open).Draw(h.t, "below")
		from, hc.stacked = below.Head, below.ID
	case kind == 2 && len(h.w.p.landed) > 0:
		l := rapid.SampledFrom(h.w.p.landed).Draw(h.t, "landed")
		from, hc.stacked = l.Head, l.ID
	}
	edits := map[string]*string{}
	for _, f := range rapid.SliceOfNDistinct(rapid.SampledFrom(files), 1, 2, rapid.ID[string]).Draw(h.t, "files") {
		switch rapid.IntRange(0, 2).Draw(h.t, "edit") {
		case 0:
			edits[f] = nil
		case 1:
			edits[f] = str("base\n") // reverts what the change beneath did
		default:
			edits[f] = str(id + "\n")
		}
	}
	edits["own/"+id] = str(id)
	hc.from = from
	head := h.w.commit(from, "change "+id, edits)
	method := MethodSquash
	if rapid.IntRange(0, 4).Draw(h.t, "merge method") == 0 {
		method = MethodMerge
	}
	parent := ""
	if hc.stacked != "" && rapid.IntRange(0, 9).Draw(h.t, "declare") < 8 {
		parent = hc.stacked
	}
	keys := rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"a", "b", "c"}), 1, 2, rapid.ID[string]).Draw(h.t, "keys")
	hc.Change = h.w.open(id, head, func(c *Change) { c.Method, c.Parent, c.Affected = method, parent, keys })
	h.changes[id] = hc
	h.order = append(h.order, id)
}

// wave runs one queue run and checks the invariants over what it landed.
func (h *harness) wave() {
	w := h.w
	gate := &fakeGate{bad: map[string]bool{}, broken: map[string]bool{}}
	for _, c := range h.open() {
		if rapid.IntRange(0, 5).Draw(h.t, "red") == 0 {
			gate.bad[c.ID] = true
		}
	}
	w.p.mu.Lock()
	w.p.refuse = map[string]bool{}
	w.p.failAfter = map[string]int{}
	for _, c := range h.open() {
		if rapid.IntRange(0, 9).Draw(h.t, "refuse") == 0 {
			w.p.refuse[c.ID] = true
		}
		if rapid.IntRange(0, 9).Draw(h.t, "fail partway") == 0 {
			w.p.failAfter[c.ID] = 1
		}
	}
	w.p.mu.Unlock()

	ctx := context.Background()
	before := len(w.p.history)
	kickedBefore := maps.Clone(w.p.kicked)
	p, err := w.planner(rapid.IntRange(1, 3).Draw(h.t, "depth")).Run(ctx, w.changes())
	if err != nil {
		h.t.Fatalf("plan: %v", err)
	}
	if open := h.open(); len(open) > 0 && rapid.IntRange(0, 5).Draw(h.t, "push after listing") == 0 {
		c := rapid.SampledFrom(open).Draw(h.t, "pushed")
		moved := w.commit(c.Head, "more "+c.ID, map[string]*string{"own/" + c.ID: str(c.ID + " more")})
		w.m.set(branchRef(c.Branch), moved)
		c.Head = moved
	}
	got := &collected{}
	v := NewValidator(w.m, gate, got)
	v.Scratch = "/scratch"
	if err := v.Run(ctx, p); err != nil {
		h.t.Fatalf("validate: %v", err)
	}
	for _, vd := range got.all {
		if vd.Code == CodeRed && !gate.bad[vd.Change.ID] {
			h.t.Fatalf("I16: #%s was kicked back red, but its own gate was green", vd.Change.ID)
		}
	}
	a := NewApplier(w.p, w.m, got)
	a.Interval = time.Millisecond
	// I19: an atomic run the provider landed partway stops applying; nothing else may.
	if err := a.Run(ctx, p); err != nil && !(len(w.p.failAfter) > 0 && strings.Contains(err.Error(), "the stack through")) {
		h.t.Fatalf("I1/I15: applying stopped: %v", err)
	}
	for _, l := range w.p.history[before:] {
		h.landed(l, kickedBefore)
	}
	for _, l := range w.p.landed {
		h.changes[l.ID].landed = true
	}
}

func (h *harness) landed(l landing, kickedBefore map[string]Kick) {
	w := h.w
	c, ok := h.changes[l.id]
	if !ok {
		h.t.Fatalf("the provider landed #%s, which nobody opened", l.id)
	}
	c.landed = true
	// I13: nothing queued that it carries is still unlanded. A change kicked back is out
	// of the queue: carrying it is carrying its content, which its reviewers saw.
	together := map[string]bool{} // what landed in the same commit, one merge of a stack
	for _, o := range w.p.landed {
		if o.Commit == l.after {
			together[o.ID] = true
		}
	}
	for _, x := range h.changes {
		if _, kicked := w.p.kicked[x.ID]; x.ID == c.ID || x.landed || kicked || together[x.ID] {
			continue
		}
		w.m.mu.Lock()
		carried := w.m.ancestors(c.Head)[x.Head] && !w.m.ancestors(l.before)[x.Head]
		w.m.mu.Unlock()
		if carried {
			h.t.Fatalf("I13: #%s landed before #%s, whose head it carries", c.ID, x.ID)
		}
	}
	// I16: nothing stacked on a change kicked in this run, at the head it was kicked at,
	// lands in it.
	if x := h.changes[c.stacked]; x != nil && x.Head == c.from {
		if _, kicked := w.p.kicked[c.stacked]; kicked {
			if _, earlier := kickedBefore[c.stacked]; !earlier {
				h.t.Fatalf("I16: #%s landed in the run that kicked #%s, which it is stacked on", c.ID, c.stacked)
			}
		}
	}
	// I14: the base gained exactly the change's own delta. A merge keeps every commit, so
	// its natural base is its own delta's start whenever what it is stacked on landed
	// the same way, which is the only way that planning admits.
	base := h.ownBase(c)
	w.m.mu.Lock()
	defer w.m.mu.Unlock()
	if base == "" || w.m.ancestors(l.before)[base] || c.Method == MethodMerge {
		base = w.m.mergeBase(l.before, c.Head)
	}
	// Where that delta conflicts with what landed since (it deletes a file the base had
	// already deleted, and another change added again), the queue lands its candidate's
	// delta since what it was built onto, which the Applier checks against the tree
	// validation predicted; I1 covers that case.
	want, conflicts, _ := merge3(w.p.treeAt(base), w.m.trees[w.m.commits[l.before].tree], w.m.trees[w.m.commits[c.Head].tree])
	if got := w.m.trees[w.m.commits[l.after].tree]; len(conflicts) == 0 && !maps.Equal(got, want) {
		h.t.Fatalf("I14: #%s landed %v on %v; its own delta from %s gives %v (conflicts %v)",
			c.ID, got, w.m.trees[w.m.commits[l.before].tree], short(base), want, conflicts)
	}
	// I18: it merged into the queue's base.
	if w.p.open[c.ID] != nil && w.p.open[c.ID].Base != "main" {
		h.t.Fatalf("I18: #%s merged while targeting %s", c.ID, w.p.open[c.ID].Base)
	}
}

// ownBase is where c's own delta starts as ancestry can see it: the commit c carries of
// the nearest change beneath it that landed, whatever head it landed at, or that is
// still queued at that commit. One kicked back, or pushed past it and still open, is
// invisible, and c's delta then starts further down; "" is where it left main.
func (h *harness) ownBase(c *hchange) string {
	from, id := c.from, c.stacked
	for id != "" {
		x := h.changes[id]
		if _, kicked := h.w.p.kicked[id]; x.landed || !kicked && x.Head == from {
			return from
		}
		from, id = x.from, x.stacked
	}
	return ""
}

// externalLand squashes an open change by hand, outside the queue.
func (h *harness) externalLand() {
	open := slices.DeleteFunc(h.open(), func(c *hchange) bool { return c.stacked != "" })
	if len(open) == 0 {
		return
	}
	c := rapid.SampledFrom(open).Draw(h.t, "landed by hand")
	h.w.p.mu.Lock()
	h.w.p.refuse = map[string]bool{}
	h.w.p.failAfter = map[string]int{}
	h.w.p.mu.Unlock()
	before := len(h.w.p.history)
	if err := h.w.p.MergeChange(context.Background(), c.Change, MergeRequest{Commit: c.Head}); err != nil {
		return // a conflict with main: the person gives up
	}
	for _, l := range h.w.p.history[before:] {
		h.changes[l.id].landed = true
	}
}

func TestStacksKeepTheirInvariants(t *testing.T) {
	// A thousand traces take a few seconds; -rapid.checks given on the command line wins.
	if f := flag.Lookup("rapid.checks"); f != nil && f.Value.String() == f.DefValue {
		require.NoError(t, f.Value.Set("1000"))
	}
	rapid.Check(t, func(rt *rapid.T) {
		w := newWorld(t, map[string]string{"f0": "base\n", "f1": "base\n", "f2": "base\n", "f3": "base\n"})
		if rapid.Bool().Draw(rt, "atomic stacks") {
			w.p.caps.StackMerge = StackMergeAtomic
		}
		h := &harness{t: rt, w: w, changes: map[string]*hchange{}}
		for range rapid.IntRange(1, 5).Draw(rt, "waves") {
			for range rapid.IntRange(0, 4).Draw(rt, "new changes") {
				h.newChange()
			}
			if rapid.IntRange(0, 4).Draw(rt, "hand landing") == 0 {
				h.externalLand()
			}
			h.wave()
		}
	})
}
