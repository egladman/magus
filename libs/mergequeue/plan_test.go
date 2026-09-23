package mergequeue

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue/types"
	magustypes "github.com/egladman/magus/types"
)

func planner(t *testing.T, d doubles) *Planner {
	t.Helper()
	p, err := NewPlanner(d.vcs, clone, d.provider, d.facts)
	require.NoError(t, err)
	p.Parallel = 1
	return p
}

func changes(cs ...types.Change) types.Changes {
	return types.Changes{Base: "main", Changes: cs}
}

func TestNewPlannerRefusesAMissingPart(t *testing.T) {
	d := newDoubles(t)
	for name, tc := range map[string]struct {
		vcs  types.ReadVCS
		p    types.Provider
		f    types.BuildFacts
		cl   Clone
		want string
	}{
		"vcs":      {nil, d.provider, d.facts, clone, "planner needs a VCS, a provider and build facts"},
		"provider": {d.vcs, nil, d.facts, clone, "planner needs a VCS, a provider and build facts"},
		"facts":    {d.vcs, d.provider, nil, clone, "planner needs a VCS, a provider and build facts"},
		"clone":    {d.vcs, d.provider, d.facts, Clone{Root: "/clone"}, "clone needs a root and a remote"},
	} {
		_, err := NewPlanner(tc.vcs, tc.cl, tc.p, tc.f)
		require.EqualError(t, err, tc.want, name)
	}
}

// Input that could reach a command line as an option is refused before anything is
// fetched: the mocks expect no call.
func TestPlanRefusesBadInputBeforeCallingAnything(t *testing.T) {
	d := newDoubles(t)
	p := planner(t, d)
	bad := change("1", "a")
	bad.Ref = "refs/heads/a b"
	_, err := p.Run(t.Context(), changes(bad))
	require.ErrorContains(t, err, "ref:")
	bad = change("1", "a")
	bad.Branch = "--force"
	_, err = p.Run(t.Context(), changes(bad))
	require.ErrorContains(t, err, "branch:")
	_, err = p.Run(t.Context(), types.Changes{Base: "-main"})
	require.ErrorContains(t, err, "base:")
	p.Depth = -1
	_, err = p.Run(t.Context(), changes(change("1", "a")))
	require.EqualError(t, err, "depth -1 and parallel 1 must not be negative")
}

func TestPlanStopsOnAProviderThatCannotSayWhatItSupports(t *testing.T) {
	for name, tc := range map[string]struct {
		caps types.Capabilities
		err  error
		want string
	}{
		"describe fails":    {err: errors.New("401"), want: "describe the provider: 401"},
		"no method allowed": {caps: types.Capabilities{StackMerge: types.StackMergeSequential}, want: "provider allows no merge method"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			d.tip(base)
			d.provider.EXPECT().Describe(mock.Anything, types.ListQuery{Base: "main"}).Return(tc.caps, tc.err)
			_, err := planner(t, d).Run(t.Context(), changes(change("1", "a")))
			require.EqualError(t, err, tc.want)
		})
	}
}

// The provider is wrong about what merged, and a stack base read from it would be too.
func TestPlanRefusesAMergedChangeTheBaseDoesNotCarry(t *testing.T) {
	d := newDoubles(t)
	d.tip(base)
	d.caps()
	m := types.MergedChange{ID: "9", Head: head("9"), Commit: head("m9"), Method: types.MethodSquash}
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, m.Commit).Return(nil)
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, m.Commit, base).Return(false, nil)
	in := changes(change("1", "a"))
	in.Merged = []types.MergedChange{m}
	_, err := planner(t, d).Run(t.Context(), in)
	require.EqualError(t, err, "provider lists #9 as merged at "+head("m9")[:12]+", which main does not carry")
}

func TestPlanOfNoChangesSaysSo(t *testing.T) {
	d := newDoubles(t)
	d.tip(base)
	d.caps()
	var out bytes.Buffer
	p := planner(t, d)
	p.Events = NewEvents(&out)
	plan, err := p.Run(t.Context(), changes())
	require.NoError(t, err)
	assert.Equal(t, types.Plan{Schema: types.SchemaPlan, Base: "main", BaseCommit: base, Depth: 1}, plan)
	assert.Contains(t, out.String(), "no change carries merge intent against main")
}

// admitting is one change's way through admission as far as want says it gets.
type admitting struct {
	onBase    bool
	approval  *types.Approval
	conflicts []magustypes.Conflict
	outputs   map[string]bool
	affected  []string
	factsErr  error
}

func (d doubles) admit(c types.Change, a admitting) {
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).Return(nil)
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, c.Head, base).Return(a.onBase, nil)
	if a.onBase {
		return
	}
	d.plain(c.Head)
	approved := types.Approval{Approved: true, Head: c.Head, Base: "main", Method: c.Method, Queued: true}
	if a.approval != nil {
		approved = *a.approval
	}
	d.provider.EXPECT().ApprovalAt(mock.Anything, c, c.Head).Return(approved, nil)
	if !approved.Approved || !approved.Queued || approved.Head != c.Head {
		return
	}
	d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]string{"a/x.go"}, nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).
		Return(magustypes.TreeMergeResult{Tree: "t", Conflicts: a.conflicts}, nil)
	if len(a.conflicts) > 0 {
		d.facts.EXPECT().Outputs(mock.Anything, conflictPaths(a.conflicts)).Return(a.outputs, nil)
	}
	if len(a.conflicts) > len(a.outputs) {
		d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, c.Head, base, mock.Anything).
			Return([]magustypes.Commit{{ID: head("x"), Subject: "change a/x.go", Parents: []string{base}}}, nil)
		return
	}
	if c.Affected == nil {
		d.facts.EXPECT().Affected(mock.Anything, c, []string{"a/x.go"}).Return(a.affected, "", a.factsErr)
	}
}

func TestPlanAdmission(t *testing.T) {
	unknown := change("1")
	for name, tc := range map[string]struct {
		c        types.Change
		admit    *admitting
		wantErr  string
		want     types.Decision
		wantCode types.Code
		wantSet  []string
	}{
		// Planning never fetches a fork's head: nothing from it reaches the queue's clones.
		"a fork is kicked back unfetched": {c: types.Change{ID: "1", Head: head("1"), Base: "main", Method: types.MethodSquash, Fork: true},
			want: types.DecisionKick, wantCode: types.CodeKickRefused},
		"a head on the base has merged": {c: change("1", "a"), admit: &admitting{onBase: true}, want: types.DecisionMerged},
		"an unapproved change waits": {c: change("1", "a"), admit: &admitting{approval: &types.Approval{Head: head("1"), Base: "main", Method: types.MethodSquash, Queued: true}},
			want: types.DecisionWait, wantCode: types.CodeWaitNotApproved},
		"a conflict in source is kicked back": {c: change("1", "a"), admit: &admitting{conflicts: []magustypes.Conflict{{Path: "a/x.go"}}, outputs: map[string]bool{}},
			want: types.DecisionKick, wantCode: types.CodeKickConflict},
		"a conflict in a declared output is regeneration's": {c: change("1", "a"), admit: &admitting{conflicts: []magustypes.Conflict{{Path: "a/gen.go"}}, outputs: map[string]bool{"a/gen.go": true}},
			wantSet: []string{"a"}},
		"the build tool is asked only for a change without a set": {c: unknown, admit: &admitting{affected: []string{"a", "b"}}, wantSet: []string{"a", "b"}},
		"a failing build tool stops planning":                     {c: unknown, admit: &admitting{factsErr: errors.New("exit 1")}, wantErr: "affected set of #1: exit 1"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			d.tip(base)
			d.caps()
			if tc.admit != nil {
				d.admit(tc.c, *tc.admit)
			}
			plan, err := planner(t, d).Run(t.Context(), changes(tc.c))
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.want == "" {
				require.Empty(t, plan.Verdicts)
				require.Len(t, plan.Partitions, 1)
				assert.Equal(t, tc.wantSet, plan.Partitions[0][0].Affected)
				return
			}
			require.Len(t, plan.Verdicts, 1)
			v := plan.Verdicts[0]
			assert.Equal(t, tc.want, v.Decision)
			assert.Equal(t, tc.wantCode, v.Code)
			if v.Code == types.CodeKickConflict {
				assert.Equal(t, []string{"a/x.go"}, v.Paths)
				assert.Equal(t, []string{head("x")[:12] + " change a/x.go"}, v.With)
				assert.Contains(t, v.Report, "Merge `main` into this branch, resolve these by hand")
			}
		})
	}
}

// A change stacked on another before a merge of the base into that one carries its top,
// not its head; planning peels the merge off to find it, reading each head's own
// commits once stacks are possible.
func TestPlanPeelsAMergeOfTheBaseOffTheChangeBeneath(t *testing.T) {
	d := newDoubles(t)
	d.tip(base)
	d.caps()
	parent, child := change("1", "a"), change("2", "b")
	top := head("top1")
	for _, c := range []types.Change{parent, child} {
		d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).Return(nil)
	}
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, parent.Head, []string(nil)).
		Return([]magustypes.Commit{{ID: parent.Head, Parents: []string{top, head("b1")}}, {ID: top, Parents: []string{base}}}, nil)
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, child.Head, []string(nil)).
		Return([]magustypes.Commit{{ID: child.Head, Parents: []string{top}}, {ID: top, Parents: []string{base}}}, nil)
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, parent.Head).Return(magustypes.Commit{ID: parent.Head, Parents: []string{top, head("b1")}}, nil)
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, head("b1"), base).Return(true, nil)
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, top).Return(magustypes.Commit{ID: top, Parents: []string{base}}, nil)
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, child.Head).Return(magustypes.Commit{ID: child.Head, Parents: []string{top}}, nil)
	// The child, stacked on an unmerged change, is approved at its head and not merged
	// onto the base alone: that would report the parent's conflicts as its own.
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, child.Head, base).Return(false, nil)
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, child.Head).Return(types.Approval{Approved: true, Head: child.Head, Base: "main", Method: types.MethodSquash, Queued: true}, nil)
	d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, child.Head, []string(nil)).Return([]string{"b/y.go"}, nil)
	// The parent's merge of the base adds nothing, so its review covers its top, where
	// nobody approved it.
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, parent.Head, base).Return(false, nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, parent.Head).Return("t", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: head("b1"), Theirs: top}).Return(magustypes.TreeMergeResult{Tree: "t"}, nil)
	d.provider.EXPECT().ApprovalAt(mock.Anything, parent, top).Return(types.Approval{Head: parent.Head, Base: "main", Method: types.MethodSquash, Queued: true, Reason: "no review"}, nil)

	plan, err := planner(t, d).Run(t.Context(), changes(child, parent))
	require.NoError(t, err)
	require.Len(t, plan.Verdicts, 2)
	byID := map[string]types.Verdict{}
	for _, v := range plan.Verdicts {
		byID[v.Change.ID] = v
	}
	assert.Equal(t, types.CodeWaitNotApproved, byID["1"].Code)
	assert.Equal(t, types.CodeWaitBelow, byID["2"].Code, "held on the change beneath, never blamed")
	assert.Equal(t, top, byID["2"].Change.StackBase)
	assert.Equal(t, "1", byID["2"].Change.Below)
}
