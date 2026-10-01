package queue

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/internal/risk"
	"github.com/egladman/magus/spells"
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
			_, err := approval(t.Context(), d.provider, d.vcs, d.facts, clone, base, c, "", nil, nil)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// carryEdit is one path's content in the approved delta replayed onto the new base and
// at the head; nil is absent.
type carryEdit struct{ replayed, head []byte }

// carryCase is the version control's side of carrying an approval at old over to c's
// head: old forked from oldBase, the head from newBase, and old's delta replayed onto
// newBase conflicts in conflicts or else differs from the head in edits.
type carryCase struct {
	edits     map[string]carryEdit
	conflicts []string
}

const replayedTree, headTree = "replayed-tree", "head-tree"

var carryOld, carryOldBase, carryNewBase = head("old"), head("oldbase"), head("newbase")

func (d doubles) carry(c types.Change, cc carryCase, generated ...string) {
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, carryOld).Return(nil)
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, carryOld, []string(nil)).Return([]magustypes.Commit{{ID: carryOld, Parents: []string{carryOldBase}}}, nil)
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]magustypes.Commit{{ID: c.Head, Parents: []string{carryNewBase}}}, nil)
	var conflicts []magustypes.Conflict
	for _, p := range cc.conflicts {
		conflicts = append(conflicts, magustypes.Conflict{Path: p, Kind: magustypes.ConflictKindContent})
	}
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Base: carryOldBase, Ours: carryNewBase, Theirs: carryOld}).
		Return(magustypes.TreeMergeResult{Tree: replayedTree, Conflicts: conflicts}, nil)
	if len(conflicts) > 0 {
		return
	}
	if len(cc.edits) == 0 {
		d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, c.Head).Return(replayedTree, nil)
		return
	}
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, c.Head).Return(headTree, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, replayedTree, headTree).Return(slices.Sorted(maps.Keys(cc.edits)), nil)
	read := func(rev string) {
		for p, e := range cc.edits {
			side := e.replayed
			if rev == c.Head {
				side = e.head
			}
			if side == nil {
				d.vcs.EXPECT().ReadFileAt(mock.Anything, clone.Root, rev, p).Return("", errors.New("absent"))
			} else {
				d.vcs.EXPECT().ReadFileAt(mock.Anything, clone.Root, rev, p).Return(string(side), nil)
			}
		}
	}
	read(replayedTree)
	read(c.Head)
	d.facts.EXPECT().ClassifyEdit(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(classifyWith(generated...))
}

// classifyWith is the risk classifier magus's workspace answers ClassifyEdit with, over
// literal declarations: generated outputs, the built-in prose globs, and Go's comments.
func classifyWith(generated ...string) func(context.Context, string, []byte, []byte) (types.ClassifiedPath, error) {
	rc := risk.Classifier{
		Role: func(_ context.Context, paths []string) (map[string]string, error) {
			roles := map[string]string{}
			for _, p := range paths {
				if slices.Contains(generated, p) {
					roles[p] = "output"
				}
			}
			return roles, nil
		},
		Prose:  risk.ProseScopes(nil),
		Syntax: map[string]spells.CommentSyntax{".go": {LineComments: []string{"//"}, Quotes: []spells.Quote{{Open: `"`, Close: `"`}}}},
	}
	return func(ctx context.Context, path string, old, cur []byte) (types.ClassifiedPath, error) {
		c := rc.Edit(ctx, path, old, cur)
		return types.ClassifiedPath{Path: path, Tier: types.CarryTier(c.Class.String()), Why: c.Why}, nil
	}
}

// An approval at an older commit carries over to the head when old's delta, replayed onto
// the head's base, leaves only changes whose tier the policy allows; otherwise the change
// waits, the reason naming the files that kept the approval from carrying.
func TestCarryApprovalByTier(t *testing.T) {
	c := change("1", "a")
	h, o := c.Head[:12], carryOld[:12]
	goFile := []byte("package a\n\nfunc A() int { return 1 }\n")
	commented := []byte("package a\n\n// A is one.\nfunc A() int { return 1 }\n")
	changed := []byte("package a\n\nfunc A() int { return 2 }\n")
	doc := []byte("# a\n")
	const proseWhy = `matches "**/*.md" (built-in default)`
	const codeWhy = "differs beyond comments from the revision compared against"
	for name, tc := range map[string]struct {
		cc        carryCase
		generated []string
		policy    types.CarryPolicy
		want      types.CarryVerdict
		// wantReason is the approval's, which a wait reports; "" when it carried.
		wantReason string
	}{
		"a rebase": {
			want: types.CarryVerdict{Carry: true, Tier: types.CarryRebase, Reason: "head " + h + " is " + o + " rebased with its diff unchanged"},
		},
		"a regenerated output": {
			cc:        carryCase{edits: map[string]carryEdit{"gen/api.go": {goFile, changed}}},
			generated: []string{"gen/api.go"},
			want: types.CarryVerdict{Carry: true, Tier: types.CarryGenerated,
				Changed: []types.ClassifiedPath{{Path: "gen/api.go", Tier: types.CarryGenerated, Why: "a declared output glob claims it"}},
				Reason:  "head " + h + " is " + o + " rebased; changed since the approval: gen/api.go (generated: a declared output glob claims it)"},
		},
		"a changelog fragment added": {
			cc: carryCase{edits: map[string]carryEdit{"changes/unreleased/a.md": {nil, doc}}},
			want: types.CarryVerdict{Carry: true, Tier: types.CarryProse,
				Changed: []types.ClassifiedPath{{Path: "changes/unreleased/a.md", Tier: types.CarryProse, Why: proseWhy}},
				Reason:  "head " + h + " is " + o + " rebased; changed since the approval: changes/unreleased/a.md (prose: " + proseWhy + ")"},
		},
		"a comment": {
			cc: carryCase{edits: map[string]carryEdit{"a.go": {goFile, commented}}},
			want: types.CarryVerdict{Carry: true, Tier: types.CarryCommentOnly,
				Changed: []types.ClassifiedPath{{Path: "a.go", Tier: types.CarryCommentOnly, Why: "only comments differ from the revision compared against"}},
				Reason:  "head " + h + " is " + o + " rebased; changed since the approval: a.go (comment-only: only comments differ from the revision compared against)"},
		},
		"code": {
			cc: carryCase{edits: map[string]carryEdit{"a.go": {goFile, changed}}},
			want: types.CarryVerdict{Tier: types.CarryCode,
				Changed: []types.ClassifiedPath{{Path: "a.go", Tier: types.CarryCode, Why: codeWhy}},
				Refused: []string{"a.go"},
				Reason:  "changed since the approval at " + o + ": a.go (code: " + codeWhy + ")"},
			wantReason: "no review; changed since the approval at " + o + ": a.go (code: " + codeWhy + ")",
		},
		"a file of nothing but comments added": {
			cc: carryCase{edits: map[string]carryEdit{"b.go": {nil, []byte("// b\n")}}},
			want: types.CarryVerdict{Tier: types.CarryCode,
				Changed: []types.ClassifiedPath{{Path: "b.go", Tier: types.CarryCode, Why: "absent at the revision compared against"}},
				Refused: []string{"b.go"},
				Reason:  "changed since the approval at " + o + ": b.go (code: absent at the revision compared against)"},
			wantReason: "no review; changed since the approval at " + o + ": b.go (code: absent at the revision compared against)",
		},
		"prose beside code": {
			cc: carryCase{edits: map[string]carryEdit{"docs/a.md": {doc, []byte("# b\n")}, "a.go": {goFile, changed}}},
			want: types.CarryVerdict{Tier: types.CarryCode,
				Changed: []types.ClassifiedPath{{Path: "a.go", Tier: types.CarryCode, Why: codeWhy}, {Path: "docs/a.md", Tier: types.CarryProse, Why: proseWhy}},
				Refused: []string{"a.go"},
				Reason:  "changed since the approval at " + o + ": a.go (code: " + codeWhy + ")"},
			wantReason: "no review; changed since the approval at " + o + ": a.go (code: " + codeWhy + ")",
		},
		"prose under a rebase-only policy": {
			cc:     carryCase{edits: map[string]carryEdit{"docs/a.md": {doc, []byte("# b\n")}}},
			policy: types.CarryPolicy{types.CarryRebase},
			want: types.CarryVerdict{Tier: types.CarryProse,
				Changed: []types.ClassifiedPath{{Path: "docs/a.md", Tier: types.CarryProse, Why: proseWhy}},
				Refused: []string{"docs/a.md"},
				Reason:  "changed since the approval at " + o + ": docs/a.md (prose: " + proseWhy + ")"},
			wantReason: "no review; changed since the approval at " + o + ": docs/a.md (prose: " + proseWhy + ")",
		},
		"a replay that conflicts": {
			cc: carryCase{conflicts: []string{"a.go", "b.go"}},
			want: types.CarryVerdict{Tier: types.CarryCode, Refused: []string{"a.go", "b.go"},
				Reason: "the approved " + o + "'s diff conflicts with " + carryNewBase[:12] + " in a.go, b.go"},
			wantReason: "no review; the approved " + o + "'s diff conflicts with " + carryNewBase[:12] + " in a.go, b.go",
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			d.plain(c.Head)
			d.provider.EXPECT().ApprovalAt(mock.Anything, c, c.Head).
				Return(types.Approval{Head: c.Head, Base: "main", Method: types.MethodSquash, Queued: true, Reason: "no review", ApprovedCommit: carryOld}, nil)
			d.carry(c, tc.cc, tc.generated...)
			policy := tc.policy
			if policy == nil {
				policy = types.DefaultCarryPolicy()
			}

			a, err := approval(t.Context(), d.provider, d.vcs, d.facts, clone, base, c, "", nil, policy)
			require.NoError(t, err)
			tc.want.From, tc.want.Head = carryOld, c.Head
			require.NotNil(t, a.carry)
			assert.Equal(t, tc.want, *a.carry)
			assert.Equal(t, tc.want.Carry, a.Approved)
			assert.Equal(t, tc.wantReason, a.Reason)
			if tc.want.Carry {
				assert.Equal(t, tc.want.Reason+", so its approval carried over", carriedNotice(a))
				return
			}
			assert.Empty(t, carriedNotice(a))
			held := c
			v := admission(&held, a, types.Capabilities{StackMerge: types.StackMergeSequential, Methods: []types.MergeMethod{types.MethodSquash}})
			require.NotNil(t, v)
			assert.Equal(t, types.CodeWaitNotApproved, v.Code)
			assert.Equal(t, "not approved at "+h+": "+tc.wantReason, v.Reason)
		})
	}
}

// A change that took main in after its approval, and added a commit, is classified from
// the approved commit to the commit beneath the merge. Its own head, listed beside it,
// carries the approved commit, which is no reason to refuse: that is the change itself,
// not another change sharing its commits. Reviews compares the same way.
func TestCarryApprovalIgnoresTheChangesOwnListedHead(t *testing.T) {
	c := change("1", "a")
	approved, reviewed, onMain, fork := head("x"), head("y"), head("main"), head("fork")
	d := newDoubles(t)
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, c.Head).Return(magustypes.Commit{ID: c.Head, Parents: []string{reviewed, onMain}}, nil)
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, onMain, base).Return(true, nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, c.Head).Return("merge-tree", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: onMain, Theirs: reviewed}).Return(magustypes.TreeMergeResult{Tree: "merge-tree"}, nil)
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, reviewed).Return(magustypes.Commit{ID: reviewed, Parents: []string{approved}}, nil)
	d.provider.EXPECT().ApprovalAt(mock.Anything, c, reviewed).
		Return(types.Approval{Head: c.Head, Base: "main", Method: types.MethodSquash, Queued: true, Reason: "no review", ApprovedCommit: approved}, nil)
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, approved).Return(nil)
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, approved, []string(nil)).Return([]magustypes.Commit{{ID: approved, Parents: []string{fork}}}, nil)
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, reviewed, []string(nil)).
		Return([]magustypes.Commit{{ID: reviewed, Parents: []string{approved}}, {ID: approved, Parents: []string{fork}}}, nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Base: fork, Ours: fork, Theirs: approved}).Return(magustypes.TreeMergeResult{Tree: replayedTree}, nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, reviewed).Return(headTree, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, replayedTree, headTree).Return([]string{"docs/a.md"}, nil)
	d.vcs.EXPECT().ReadFileAt(mock.Anything, clone.Root, replayedTree, "docs/a.md").Return("# a\n", nil)
	d.vcs.EXPECT().ReadFileAt(mock.Anything, clone.Root, reviewed, "docs/a.md").Return("# b\n", nil)
	d.facts.EXPECT().ClassifyEdit(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(classifyWith())
	refs := planRefs(types.Changes{Base: "main", Changes: []types.Change{c}})

	a, err := approval(t.Context(), d.provider, d.vcs, d.facts, clone, base, c, "", refs, types.DefaultCarryPolicy())
	require.NoError(t, err)
	require.NotNil(t, a.carry)
	assert.True(t, a.Approved, a.carry.Reason)
	assert.Equal(t, types.CarryProse, a.carry.Tier)
}

// CarryApproval is the classifier a caller outside planning asks: it fetches the base
// itself and decides where each delta starts from the listing.
func TestCarryApprovalFetchesTheBaseAndRefusesWhatIsNoCommit(t *testing.T) {
	c := change("1", "a")
	d := newDoubles(t)
	d.tip(base)
	d.carry(c, carryCase{edits: map[string]carryEdit{"docs/a.md": {[]byte("# a\n"), []byte("# b\n")}}})
	q := CarryQuery{Approved: carryOld, Now: c.Head, Listing: types.Changes{Base: "main", Changes: []types.Change{c}}}

	got, err := CarryApproval(t.Context(), d.vcs, d.facts, clone, q, nil)
	require.NoError(t, err)
	assert.Equal(t, types.CarryVerdict{From: carryOld, Head: c.Head, Tier: types.CarryProse,
		Changed: []types.ClassifiedPath{{Path: "docs/a.md", Tier: types.CarryProse, Why: `matches "**/*.md" (built-in default)`}},
		Refused: []string{"docs/a.md"},
		Reason:  "changed since the approval at " + carryOld[:12] + `: docs/a.md (prose: matches "**/*.md" (built-in default))`}, got,
		"the zero policy carries a rebase alone")

	q.Approved = "v1.2"
	_, err = CarryApproval(t.Context(), d.vcs, d.facts, clone, q, nil)
	require.EqualError(t, err, `carry an approval from "v1.2" to "`+c.Head+`": both must be commit ids`)
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
