package mergequeue

import (
	"bytes"
	"errors"
	"testing"
	"time"

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
	assert.Equal(t, types.Plan{Schema: types.SchemaPlan, Base: "main", BaseCommit: base, CommitDate: when, Depth: 1}, plan)
	assert.Contains(t, out.String(), "no change carries merge intent against main")
}

// An applier clears the queued mark from what left the queue, reading the marks the
// listing reported out of the plan, even one that admits nothing.
func TestPlanCarriesTheMarkEachUnqueuedChangeShows(t *testing.T) {
	d := newDoubles(t)
	d.tip(base)
	d.caps()
	in := changes()
	in.Unqueued = []types.UnqueuedChange{
		{ID: "4", Repo: "acme/acme", Head: head("4"), Mark: types.MarkQueued},
		{ID: "5", Repo: "acme/acme", Head: head("5"), Mark: types.MarkRejected},
		{ID: "6", Repo: "acme/acme", Head: head("6")},
	}
	plan, err := planner(t, d).Run(t.Context(), in)
	require.NoError(t, err)
	assert.Equal(t, types.Plan{Schema: types.SchemaPlan, Base: "main", BaseCommit: base, CommitDate: when, Depth: 1, Unqueued: in.Unqueued}, plan)
	require.NoError(t, plan.Check())
}

// admitting is one change's way through admission as far as want says it gets.
type admitting struct {
	onBase    bool
	approval  *types.Approval
	conflicts []magustypes.Conflict
	outputs   map[string]types.Writes
	affected  []string
	factsErr  error
	// changed are the files the change touches, "a/x.go" when empty; generated are how
	// the build tool writes them, and generation its account of regenerating those it
	// declares as outputs.
	changed    []string
	generated  map[string]types.Writes
	generation types.Generation
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
	changed := a.changed
	if len(changed) == 0 {
		changed = []string{"a/x.go"}
	}
	d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return(changed, nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).
		Return(magustypes.TreeMergeResult{Tree: "t", Conflicts: a.conflicts}, nil)
	if len(a.conflicts) > 0 {
		d.facts.EXPECT().Classify(mock.Anything, conflictPaths(a.conflicts)).Return(a.outputs, nil)
	}
	if len(a.conflicts) > len(a.outputs) {
		d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, c.Head, base, mock.Anything).
			Return([]magustypes.Commit{{ID: head("x"), Subject: "change a/x.go", Parents: []string{base}}}, nil)
		return
	}
	if c.Affected == nil {
		d.facts.EXPECT().Affected(mock.Anything, c, changed).Return(a.affected, "", a.factsErr)
		if a.factsErr != nil {
			return
		}
	}
	d.facts.EXPECT().Classify(mock.Anything, changed).Return(a.generated, nil)
	var outputs []string
	for _, p := range changed {
		if a.generated[p].Output {
			outputs = append(outputs, p)
		}
	}
	if len(outputs) > 0 {
		d.facts.EXPECT().Generation(mock.Anything, outputs, changed).Return(a.generation, nil)
	}
}

// The date comes from the commits the plan names, never a clock, so planning the same
// queue again builds the same candidates.
func TestPlanDatesItsCommitsAsTheNewestAdmittedCommit(t *testing.T) {
	later := when.Add(time.Hour)
	c := change("1", "a")
	d := newDoubles(t)
	d.tip(base)
	d.caps()
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, c.Head).
		Return(magustypes.Commit{ID: c.Head, Parents: []string{base}, Author: author, Date: later.In(time.FixedZone("CET", 3600))}, nil)
	d.admit(c, admitting{})
	plan, err := planner(t, d).Run(t.Context(), changes(c))
	require.NoError(t, err)
	assert.Equal(t, later, plan.CommitDate)
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
		// wantRegen is what the admitted change records only its author can regenerate.
		wantRegen []string
	}{
		// Alone in the listing, nothing can be built on a fork, so its head is never fetched.
		"a fork is kicked back unfetched": {c: types.Change{ID: "1", Head: head("1"), Base: "main", Method: types.MethodSquash, Fork: true},
			want: types.DecisionKick, wantCode: types.CodeKickRefused},
		"a head on the base has merged": {c: change("1", "a"), admit: &admitting{onBase: true}, want: types.DecisionMerged},
		"an unapproved change waits": {c: change("1", "a"), admit: &admitting{approval: &types.Approval{Head: head("1"), Base: "main", Method: types.MethodSquash, Queued: true}},
			want: types.DecisionWait, wantCode: types.CodeWaitNotApproved},
		"a conflict in source is kicked back": {c: change("1", "a"), admit: &admitting{conflicts: []magustypes.Conflict{{Path: "a/x.go"}}, outputs: map[string]types.Writes{}},
			want: types.DecisionKick, wantCode: types.CodeKickConflict},
		"a conflict in a declared output is regeneration's": {c: change("1", "a"), admit: &admitting{conflicts: []magustypes.Conflict{{Path: "a/gen.go"}}, outputs: map[string]types.Writes{"a/gen.go": {Output: true}}},
			wantSet: []string{"a"}},
		"the build tool is asked only for a change without a set": {c: unknown, admit: &admitting{affected: []string{"a", "b"}}, wantSet: []string{"a", "b"}},
		"a failing build tool stops planning":                     {c: unknown, admit: &admitting{factsErr: errors.New("exit 1")}, wantErr: "affected set of #1: exit 1"},
		"generated files regenerated from the change's own code are its author's": {c: change("1", "a"), admit: &admitting{
			changed: []string{"gen/gen.go", "gen/x.go"}, generated: map[string]types.Writes{"gen/x.go": {Output: true}},
			generation: types.Generation{Units: []string{"gen"}, Code: []string{"gen/gen.go"}},
		}, wantSet: []string{"a"}, wantRegen: []string{"gen/x.go"}},
		"generated files the base's regeneration provably makes are not": {c: change("1", "a"), admit: &admitting{
			changed: []string{"a/x.go", "gen/x.go"}, generated: map[string]types.Writes{"gen/x.go": {Output: true}},
			generation: types.Generation{Units: []string{"gen"}},
		}, wantSet: []string{"a"}},
		"generated files no regeneration can be bounded for are the author's": {c: change("1", "a"), admit: &admitting{
			changed: []string{"gen/x.go", "magusfile.buzz"}, generated: map[string]types.Writes{"gen/x.go": {Output: true}},
			generation: types.Generation{Units: []string{"gen"}, Unbounded: "magusfile.buzz edits the declarations"},
		}, wantSet: []string{"a"}, wantRegen: []string{"gen/x.go"}},
		// Every hook takes the units as arguments after its own, with no "--" between.
		"a project a hook would read as an option is kicked back": {c: unknown, admit: &admitting{affected: []string{"a", "--gate=sh"}},
			want: types.DecisionKick, wantCode: types.CodeKickRefused},
		"so is one given with the change": {c: change("1", "-x"), admit: &admitting{},
			want: types.DecisionKick, wantCode: types.CodeKickRefused},
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
				assert.Equal(t, tc.wantRegen, plan.Partitions[0][0].AuthorRegenerates)
				return
			}
			require.Len(t, plan.Verdicts, 1)
			v := plan.Verdicts[0]
			assert.Equal(t, tc.want, v.Decision)
			assert.Equal(t, tc.wantCode, v.Code)
			if v.Code == types.CodeKickConflict {
				assert.Equal(t, []string{"a/x.go"}, v.Paths)
				assert.Equal(t, []string{head("x")[:12] + " change a/x.go"}, v.With)
				assert.Equal(t, "The merge queue could not merge this change at `"+short(v.Change.Head)+"`: it conflicts with `main` outside the generated files.\n\n"+
					"Merge `main` into this branch and resolve the conflict by hand.\n", v.Report, "the files travel in paths and with, not in the prose")
				assert.Equal(t, "The merge queue could not merge this change at `"+short(v.Change.Head)+"`: it conflicts with `main` outside the generated files.", v.Reason)
				assert.Empty(t, v.Gate, "planning runs no hook")
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
	d.facts.EXPECT().Classify(mock.Anything, []string{"b/y.go"}).Return(nil, nil)
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

// mergeOfBaseOnto says head is a merge of the base, at onBase, into top, and top sits on
// the base.
func (d doubles) mergeOfBaseOnto(head, top, onBase string) {
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, head).Return(magustypes.Commit{ID: head, Parents: []string{top, onBase}}, nil)
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, onBase, base).Return(true, nil)
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, top).Return(magustypes.Commit{ID: top, Parents: []string{base}}, nil)
}

// Before, a change built on an unqueued one, which then had main merged into it, carried
// that change's commits but not its head, and was admitted.
func TestPlanHoldsAChangeBuiltOnAnUnqueuedChangeBeneathAMergeOfTheBase(t *testing.T) {
	d := newDoubles(t)
	d.tip(base)
	d.caps()
	c := change("2", "a")
	u := types.UnqueuedChange{ID: "7", Head: head("u merge")}
	top := head("u top")
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).Return(nil)
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, c.Head, []string(nil)).
		Return([]magustypes.Commit{{ID: c.Head, Parents: []string{top}}, {ID: top, Parents: []string{base}}}, nil)
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, c.Head).Return(magustypes.Commit{ID: c.Head, Parents: []string{top}}, nil)
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, u.Head).Return(nil)
	d.mergeOfBaseOnto(u.Head, top, head("b1"))

	in := changes(c)
	in.Unqueued = []types.UnqueuedChange{u}
	plan, err := planner(t, d).Run(t.Context(), in)
	require.NoError(t, err)
	require.Len(t, plan.Verdicts, 1)
	assert.Equal(t, types.CodeWaitUnqueuedBelow, plan.Verdicts[0].Code)
	assert.Equal(t, "carries the commits of #7, which is open but not queued", plan.Verdicts[0].Reason)
	assert.Empty(t, plan.Partitions)
}

// The same for a fork: its head is fetched for its ancestry, and a change built on its
// top waits on its kick-back rather than merging its commits.
func TestPlanHoldsAChangeBuiltOnAForkBeneathAMergeOfTheBase(t *testing.T) {
	d := newDoubles(t)
	d.tip(base)
	d.caps()
	fork := types.Change{ID: "1", Head: head("f merge"), Base: "main", Method: types.MethodSquash, Fork: true}
	c := change("2", "a")
	top := head("f top")
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, fork.Head).Return(nil)
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, fork.Head, []string(nil)).
		Return([]magustypes.Commit{{ID: fork.Head, Parents: []string{top, head("b1")}}, {ID: top, Parents: []string{base}}}, nil)
	d.mergeOfBaseOnto(fork.Head, top, head("b1"))
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).Return(nil)
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, c.Head, []string(nil)).
		Return([]magustypes.Commit{{ID: c.Head, Parents: []string{top}}, {ID: top, Parents: []string{base}}}, nil)
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, c.Head).Return(magustypes.Commit{ID: c.Head, Parents: []string{top}}, nil)
	// Admitted on its own, then held for the fork beneath it.
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, c.Head, base).Return(false, nil)
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil)
	d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]string{"a/x.go"}, nil)
	d.facts.EXPECT().Classify(mock.Anything, []string{"a/x.go"}).Return(nil, nil)

	plan, err := planner(t, d).Run(t.Context(), changes(fork, c))
	require.NoError(t, err)
	byID := map[string]types.Verdict{}
	for _, v := range plan.Verdicts {
		byID[v.Change.ID] = v
	}
	assert.Equal(t, types.CodeKickRefused, byID["1"].Code)
	assert.Equal(t, types.CodeWaitBelowKicked, byID["2"].Code)
	assert.Equal(t, top, byID["2"].Change.StackBase)
	assert.Empty(t, plan.Partitions)
}
