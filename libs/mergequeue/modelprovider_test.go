package mergequeue

// modelProvider is the model's provider: it lists changes, approves them, and lands them
// on the model's main with each change's own merge method, the way GitHub does: a
// squash is the plain merge of the head as one commit, a merge a merge commit, a rebase
// each own commit replayed. It lands a stack atomically through its top, the bottom
// from its natural merge base and each member above as its own diff against the member
// below it.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
)

type posted struct {
	id, commit string
	state      CommitState
	desc       string
}

type modelProvider struct {
	m    *model
	caps Capabilities

	mu         sync.Mutex
	open       map[string]*Change // what is listed, as the provider sees it now
	order      []string           // listing order
	landed     []Landed
	approvedAt map[string]string // change -> the commit its approvals stand at; default its head
	refuse     map[string]bool   // changes whose merge the provider refuses
	failAfter  map[string]int    // an atomic run through this change lands this many, then fails
	afterMerge func()            // runs after each landing: another writer on main

	statuses  []posted
	merges    []string // "id@commit"
	kicked    map[string]Kick
	retargets []string
	history   []landing
}

// landing is one change landed: main before and after it.
type landing struct {
	id, head, before, after string
	method                  MergeMethod
}

var _ Provider = (*modelProvider)(nil)

func newModelProvider(m *model) *modelProvider {
	return &modelProvider{m: m, caps: Capabilities{StackMerge: StackMergeSequential, Methods: []MergeMethod{MethodMerge, MethodSquash, MethodRebase}},
		open: map[string]*Change{}, approvedAt: map[string]string{}, refuse: map[string]bool{}, failAfter: map[string]int{},
		kicked: map[string]Kick{}}
}

// list opens c on the provider; its branch, when it has one, holds its head.
func (f *modelProvider) list(c Change) Change {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c.Base == "" {
		c.Base = "main"
	}
	cp := c
	f.open[c.ID] = &cp
	f.order = append(f.order, c.ID)
	if c.Branch != "" {
		f.m.set(branchRef(c.Branch), c.Head)
	}
	f.m.set("refs/pull/"+c.ID+"/head", c.Head)
	return c
}

// headOf is c's head as the provider sees it: its branch's commit, which an update commit
// the queue pushed moves. Callers hold mu.
func (f *modelProvider) headOf(c *Change) string {
	if c.Branch != "" {
		if h := f.m.ref(branchRef(c.Branch)); h != "" {
			return h
		}
	}
	return c.Head
}

func (f *modelProvider) Describe(context.Context, ListQuery) (Capabilities, error) {
	return f.caps, nil
}

func (f *modelProvider) ListChanges(_ context.Context, q ListQuery) (Changes, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := Changes{Schema: SchemaChanges, Base: q.Base, Landed: slices.Clone(f.landed)}
	for _, id := range f.order {
		if c, ok := f.open[id]; ok {
			cp := *c
			cp.Head = f.headOf(c)
			out.Changes = append(out.Changes, cp)
		}
	}
	return out, nil
}

func (f *modelProvider) ApprovalAt(_ context.Context, c Change, commit string) (Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.open[c.ID]
	if !ok {
		return Approval{}, fmt.Errorf("no open change #%s", c.ID)
	}
	h := f.headOf(ch)
	at, ok := f.approvedAt[c.ID]
	if !ok {
		at = h
	}
	a := Approval{Approved: commit == at, Head: h, Base: ch.Base, Method: ch.Method}
	if !a.Approved {
		a.Reason, a.ApprovedAt = "0 of 1 approvals at this commit", at
	}
	return a, nil
}

func (f *modelProvider) PostStatus(_ context.Context, c Change, commit string, s CommitStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append(f.statuses, posted{c.ID, commit, s.State, s.Description})
	return nil
}

func (f *modelProvider) Retarget(_ context.Context, c Change, base string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch, ok := f.open[c.ID]; ok {
		ch.Base = base
	}
	f.retargets = append(f.retargets, c.ID+"->"+base)
	return nil
}

func (f *modelProvider) KickBack(_ context.Context, c Change, _ string, k Kick) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kicked[c.ID] = k
	delete(f.open, c.ID)
	return nil
}

func (f *modelProvider) MergeChange(_ context.Context, c Change, req MergeRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.open[c.ID]
	switch {
	case !ok:
		return fmt.Errorf("no open change #%s", c.ID)
	case f.headOf(ch) != req.Commit:
		return fmt.Errorf("head of #%s is %s, not %s", c.ID, short(f.headOf(ch)), short(req.Commit))
	case f.refuse[c.ID]:
		return errors.New("405 not mergeable")
	case ch.Base != "main":
		return fmt.Errorf("#%s targets %s", c.ID, ch.Base)
	}
	run := []*Change{ch}
	if req.Through != "" {
		for run[0].ID != req.Through {
			below, ok := f.open[run[0].Parent]
			if !ok {
				return fmt.Errorf("#%s is not stacked down to #%s", c.ID, req.Through)
			}
			run = append([]*Change{below}, run...)
		}
	}
	if err := f.land(run, ch.Method); err != nil {
		return err
	}
	if f.afterMerge != nil {
		f.afterMerge()
	}
	return nil
}

// land lands run bottom first. Callers hold mu.
func (f *modelProvider) land(run []*Change, method MergeMethod) error {
	heads := make([]string, len(run))
	for i, c := range run {
		heads[i] = f.headOf(c)
	}
	m := f.m
	m.mu.Lock()
	defer m.mu.Unlock()
	tip := m.refs["refs/heads/main"]
	top := run[len(run)-1]
	limit := len(run)
	if n, ok := f.failAfter[top.ID]; ok {
		limit = n
	}
	var landed []Landed
	cur := tip
	fail := func(err error) error {
		m.refs["refs/heads/main"] = cur
		f.record(landed)
		return err
	}
	author := func(c *Change) Person { return Person{Name: c.Author + "x", Email: c.Author + "@example.invalid"} }
	if method == MethodMerge {
		h := heads[len(heads)-1]
		t, conflicts, _ := merge3(f.treeAt(m.mergeBase(cur, h)), m.trees[m.commits[cur].tree], m.trees[m.commits[h].tree])
		if len(conflicts) > 0 {
			return fmt.Errorf("merge conflict in %v", conflicts)
		}
		cur = m.putCommit(m.putTree(t), []string{cur, h}, author(top), "Merge #"+top.ID)
		for i, c := range run {
			landed = append(landed, Landed{ID: c.ID, Head: heads[i], Commit: cur, Method: method})
		}
		f.history = append(f.history, landing{id: top.ID, head: h, before: tip, after: cur, method: method})
		return fail(nil)
	}
	for i, c := range run {
		if i == limit {
			return fail(fmt.Errorf("the stack merge stopped at #%s", c.ID))
		}
		h := heads[i]
		before := cur
		switch method {
		case MethodSquash:
			b := m.mergeBase(cur, h)
			if i > 0 {
				b = heads[i-1]
			}
			t, conflicts, _ := merge3(f.treeAt(b), m.trees[m.commits[cur].tree], m.trees[m.commits[h].tree])
			if len(conflicts) > 0 {
				return fail(fmt.Errorf("merge conflict in %v", conflicts))
			}
			cur = m.putCommit(m.putTree(t), []string{cur}, author(c), c.Title+" (#"+c.ID+")")
		case MethodRebase:
			var own []string
			skip := m.ancestors(cur)
			for id := range m.ancestors(h) {
				if !skip[id] && len(m.commits[id].parents) == 1 {
					own = append(own, id)
				}
			}
			slices.SortFunc(own, func(a, b string) int { return m.commits[a].seq - m.commits[b].seq })
			for _, id := range own {
				cm := m.commits[id]
				t, conflicts, _ := merge3(m.trees[m.commits[cm.parents[0]].tree], m.trees[m.commits[cur].tree], m.trees[cm.tree])
				if len(conflicts) > 0 {
					return fail(fmt.Errorf("rebase conflict in %v", conflicts))
				}
				cur = m.putCommit(m.putTree(t), []string{cur}, cm.author, cm.subject)
			}
		}
		landed = append(landed, Landed{ID: c.ID, Head: h, Commit: cur, Method: method})
		f.history = append(f.history, landing{id: c.ID, head: h, before: before, after: cur, method: method})
	}
	return fail(nil)
}

// record marks landed as merged. Callers hold mu and the model's.
func (f *modelProvider) record(landed []Landed) {
	for _, l := range landed {
		f.merges = append(f.merges, l.ID+"@"+l.Head)
		f.landed = append(f.landed, l)
		delete(f.open, l.ID)
	}
}

// treeAt is rev's tree, empty for "". Callers hold the model's mu.
func (f *modelProvider) treeAt(rev string) tree {
	if rev == "" {
		return tree{}
	}
	t, _ := f.m.treeOf(rev)
	return maps.Clone(t)
}
