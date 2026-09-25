package queue

import (
	"fmt"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
)

func TestAdmissionDecidesTheCodeOfEveryWaitAndKick(t *testing.T) {
	c := change("1", "a")
	caps := types.Capabilities{StackMerge: types.StackMergeSequential, Methods: []types.MergeMethod{types.MethodSquash}}
	approved := approvalResult{Approval: types.Approval{Approved: true, Head: c.Head, Base: "main", Method: types.MethodSquash, Queued: true}, reviewed: c.Head}
	for name, tc := range map[string]struct {
		edit     func(*approvalResult, *types.Capabilities)
		wantCode types.Code
		wantWhy  string
		wantHead string
	}{
		"admitted": {edit: func(*approvalResult, *types.Capabilities) {}},
		"head moved": {edit: func(a *approvalResult, _ *types.Capabilities) { a.Head = head("2") },
			wantCode: types.CodeWaitHeadMoved, wantWhy: "head moved to " + head("2")[:12] + " while listing; retried next run", wantHead: head("2")},
		"withdrawn": {edit: func(a *approvalResult, _ *types.Capabilities) { a.Queued = false },
			wantCode: types.CodeWaitWithdrawn, wantWhy: "its merge intent was withdrawn while listing"},
		"not approved": {edit: func(a *approvalResult, _ *types.Capabilities) { a.Approved, a.Reason = false, "changes requested" },
			wantCode: types.CodeWaitNotApproved, wantWhy: "not approved at " + c.Head[:12] + ": changes requested"},
		"method not allowed": {edit: func(_ *approvalResult, caps *types.Capabilities) {
			caps.Methods = []types.MergeMethod{types.MethodMerge}
		},
			wantCode: types.CodeKickRefused, wantWhy: "the repository does not allow the squash merge method; pick one it does"},
	} {
		t.Run(name, func(t *testing.T) {
			a, cp, got := approved, caps, c
			tc.edit(&a, &cp)
			v := admission(&got, a, cp)
			if tc.wantCode == "" {
				assert.Nil(t, v)
				return
			}
			require.NotNil(t, v)
			assert.Equal(t, tc.wantCode, v.Code)
			assert.Equal(t, tc.wantWhy, v.Reason)
			require.NoError(t, v.Check())
			if tc.wantHead != "" {
				assert.Equal(t, tc.wantHead, v.Change.Head, "the wait names the head it is retried at")
			}
		})
	}
}

// A provider that leaves out what the queue merges by is broken, and the run stops
// rather than guess.
func TestApprovalRefusesAProviderReportingNoHeadBaseOrMethod(t *testing.T) {
	c := change("1", "a")
	for name, tc := range map[string]struct {
		a    types.Approval
		want string
	}{
		"no head":   {types.Approval{Approved: true, Base: "main", Method: types.MethodSquash}, "approval of #1: provider reported no head"},
		"no base":   {types.Approval{Approved: true, Head: c.Head, Method: types.MethodSquash}, `approval of #1: provider reported base "" and merge method "squash", both required`},
		"no method": {types.Approval{Approved: true, Head: c.Head, Base: "main"}, `provider reported base "main" and merge method "", both required`},
		"not a commit": {types.Approval{Head: c.Head, Base: "main", Method: types.MethodSquash, ApprovedCommit: "v1.2"},
			`approval of #1: provider reported approvals at "v1.2", not a commit id`},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			d.plain(c.Head)
			d.provider.EXPECT().ApprovalAt(mock.Anything, c, c.Head).Return(tc.a, nil)
			_, err := approval(t.Context(), d.provider, d.vcs, d.facts, clone, base, c, "", nil)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// An approval at an older commit carries over to a rebase of it that changed nothing
// else: its delta replayed onto the new base merges into exactly the new head's tree.
func TestAnApprovalCarriesOverATrivialRebaseAndNothingElse(t *testing.T) {
	c := change("1", "a")
	old, oldBase, newBase := head("old"), head("oldbase"), head("newbase")
	for name, tc := range map[string]struct {
		rebased  string
		wantOK   bool
		conflict bool
	}{
		"the same delta":   {rebased: "tree", wantOK: true},
		"another delta":    {rebased: "other tree"},
		"a conflicted one": {rebased: "tree", conflict: true},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			d.plain(c.Head)
			d.provider.EXPECT().ApprovalAt(mock.Anything, c, c.Head).
				Return(types.Approval{Head: c.Head, Base: "main", Method: types.MethodSquash, Queued: true, Reason: "no review", ApprovedCommit: old}, nil)
			d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, old).Return(nil)
			d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, old, []string(nil)).Return([]magustypes.Commit{{ID: old, Parents: []string{oldBase}}}, nil)
			d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]magustypes.Commit{{ID: c.Head, Parents: []string{newBase}}}, nil)
			var conflicts []magustypes.Conflict
			if tc.conflict {
				conflicts = []magustypes.Conflict{{Path: "a.go"}}
			}
			d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Base: oldBase, Ours: newBase, Theirs: old}).
				Return(magustypes.TreeMergeResult{Tree: "tree", Conflicts: conflicts}, nil)
			d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, c.Head).Return(tc.rebased, nil).Maybe()

			a, err := approval(t.Context(), d.provider, d.vcs, d.facts, clone, base, c, "", nil)
			require.NoError(t, err)
			assert.Equal(t, tc.wantOK, a.Approved)
			if tc.wantOK {
				assert.Equal(t, old, a.carried)
				assert.Empty(t, a.Reason)
			}
		})
	}
}

func TestCarryBase(t *testing.T) {
	commit := func(id string, parents ...string) magustypes.Commit {
		return magustypes.Commit{ID: id, Parents: parents}
	}
	ref := func(id, head string, own ...string) carryRef {
		return carryRef{stackRef: stackRef{id: id, head: head}, own: set(own...)}
	}
	for name, tc := range map[string]struct {
		f        carryFacts
		wantBase string
		wantOK   bool
	}{
		"off the base, its delta starts where it left it": {
			f:        carryFacts{old: "o2", commits: []magustypes.Commit{commit("o2", "o1"), commit("o1", "B")}},
			wantBase: "B", wantOK: true,
		},
		"on a change, its delta starts at that change's head": {
			f:        carryFacts{old: "o1", commits: []magustypes.Commit{commit("o1", "p1"), commit("p1", "B")}, refs: []carryRef{ref("2", "p1", "p1")}},
			wantBase: "p1", wantOK: true,
		},
		// The approval was given on a diff that could not show these commits apart.
		"carrying an unqueued change's head": {
			f: carryFacts{old: "o1", commits: []magustypes.Commit{commit("o1", "u1"), commit("u1", "B")},
				refs: []carryRef{{stackRef: stackRef{id: "3", head: "u1", unqueued: true}}}},
		},
		"carrying two changes stacked on neither": {
			f: carryFacts{old: "o1", commits: []magustypes.Commit{commit("o1", "p1", "q1"), commit("p1", "B"), commit("q1", "B")},
				refs: []carryRef{ref("2", "p1", "p1"), ref("3", "q1", "q1")}},
		},
		"sharing a commit with a change it does not carry": {
			f: carryFacts{old: "o1", commits: []magustypes.Commit{commit("o1", "s1"), commit("s1", "B")},
				refs: []carryRef{ref("4", "s2", "s1", "s2")}},
		},
		// Its delta forked from inside the change beneath, and merged that one's head in
		// afterwards.
		"forked from beneath its stack base's head": {
			f: carryFacts{old: "o2", commits: []magustypes.Commit{commit("o2", "o1", "p2"), commit("o1", "p1"), commit("p2", "p1"), commit("p1", "B")},
				refs: []carryRef{ref("2", "p2", "p1", "p2")}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := carryBase(tc.f)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantBase, got)
			}
		})
	}
}

// carryWorld is changes branched from each other, then old, an approved commit built on
// one of them, as FuzzCarryBase draws them.
type carryWorld struct {
	f       carryFacts
	own     map[string]map[string]bool // each ref's own commits
	oldOwn  map[string]bool
	clean   bool   // old is its own commits on a queued change's head, or off the base
	wantFor string // the base a clean old's delta starts at
}

func drawCarry(d *draw) carryWorld {
	w := carryWorld{own: map[string]map[string]bool{}}
	type node struct {
		id, head, top string
		own, atTop    map[string]bool
		unqueued      bool
	}
	parents := map[string][]string{}
	var nodes []node
	branch := func(from int, fromHead bool, prefix string, k int) (string, map[string]bool) {
		tip, carried := "B0", set()
		if from > 0 {
			p := nodes[from-1]
			tip, carried = p.top, maps.Clone(p.atTop)
			if fromHead {
				tip, carried = p.head, maps.Clone(p.own)
			}
		}
		for x := 1; x <= k; x++ {
			id := oid(prefix, fmt.Sprint(x))
			parents[id] = []string{tip}
			carried[id] = true
			tip = id
		}
		return tip, carried
	}
	n := 1 + d.n(5) // changes
	for i := range n {
		id := fmt.Sprint(i + 1)
		from := d.n(i + 1)         // 0 is the base, else the change it is branched from
		k := 1 + d.n(2)            // its own commits
		fromHead := d.n(2) == 1    // branched from the head of one with a merge of the base on top
		mergeOfBase := d.n(3) == 0 // a merge of the base on top of its own commits
		unqueued := d.n(4) == 0    // it carries no merge intent
		top, own := branch(from, fromHead, id, k)
		nd := node{id: id, head: top, top: top, own: own, atTop: maps.Clone(own), unqueued: unqueued}
		if mergeOfBase {
			nd.head = oid(id, "merge of base")
			parents[nd.head] = []string{top, "B1"}
			nd.own = maps.Clone(own)
			nd.own[nd.head] = true
		}
		nodes = append(nodes, nd)
		w.own[id] = nd.own
		cr := carryRef{stackRef: stackRef{id: id, head: nd.head, unqueued: unqueued}}
		if !unqueued {
			cr.own = nd.own
		}
		w.f.refs = append(w.f.refs, cr)
	}
	from := d.n(n + 1)      // what old is built on: 0 is the base
	k := 1 + d.n(2)         // old's own commits
	fromHead := d.n(2) == 1 // built on that change's head, not its top
	extra := d.n(4) == 0    // old also merges another change's head in
	other := d.n(n)         // which one
	tip, own := branch(from, fromHead, "old", k)
	if extra {
		merge := oid("old", "merge")
		parents[merge] = []string{tip, nodes[other].head}
		maps.Copy(own, nodes[other].own)
		own[merge] = true
		tip = merge
	}
	w.f.old, w.oldOwn = tip, own
	for id := range own {
		w.f.commits = append(w.f.commits, magustypes.Commit{ID: id, Parents: parents[id]})
	}
	carriesUnqueued := false
	for _, nd := range nodes {
		carriesUnqueued = carriesUnqueued || nd.unqueued && own[nd.head]
	}
	switch {
	case extra || carriesUnqueued:
	case from == 0:
		w.clean, w.wantFor = true, "B0"
	case fromHead || nodes[from-1].head == nodes[from-1].top:
		w.clean, w.wantFor = true, nodes[from-1].head
	}
	return w
}

// FuzzCarryBase holds carrying an approval over to what the queue restacked to I17,
// restack approval: an approval given at old carries only when old's own delta is told
// apart from every other change's. Carrying one means old carries no unqueued change's
// head, and its delta, measured from the base returned, shares no commit with a change
// it does not carry and forked from that base alone. A commit that is a change's own
// commits on a queued change's head, or off the base, always carries, from that head.
func FuzzCarryBase(f *testing.F) {
	f.Add([]byte{})
	f.Fuzz(carryHolds)
}

// carryHolds is FuzzCarryBase's property over one input.
func carryHolds(t *testing.T, data []byte) {
	w := drawCarry(&draw{data: data})
	got, ok := carryBase(w.f)
	if w.clean {
		require.True(t, ok, "I17: a clean restack of old carries its approval")
		require.Equal(t, w.wantFor, got, "I17: its delta starts at what it was built on")
	}
	if !ok {
		return
	}
	delta := maps.Clone(w.oldOwn)
	for _, r := range w.f.refs {
		if r.head != w.f.old && w.oldOwn[r.head] {
			require.False(t, r.unqueued, "I17: old carries the unqueued #%s", r.id)
			if r.head == got {
				for id := range w.own[r.id] {
					delete(delta, id)
				}
			}
		}
	}
	delete(delta, got)
	for _, r := range w.f.refs {
		if r.unqueued || w.oldOwn[r.head] {
			continue
		}
		for id := range delta {
			require.False(t, w.own[r.id][id], "I17: old's delta holds %s, a commit of #%s it does not carry", id, r.id)
		}
	}
	for _, c := range w.f.commits {
		if !delta[c.ID] {
			continue
		}
		for _, p := range c.Parents {
			require.True(t, delta[p] || p == got || !w.oldOwn[p], "I17: old's delta forks from %s, not from %s alone", p, got)
		}
	}
}
