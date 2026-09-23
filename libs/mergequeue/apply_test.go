package mergequeue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue/types"
	magustypes "github.com/egladman/magus/types"
)

// validated is the green verdict on c's candidate built onto onto, on top of after.
func validated(c types.Change, onto, after string) types.Verdict {
	return types.Verdict{BaseCommit: base, Change: c, Decision: types.DecisionMerge, After: after, Onto: onto,
		CandidateCommit: candidateOf(onto, c.Head), Method: c.Method, Message: "* change " + c.ID, Depth: 1}
}

// applierFor wires an Applier to d for plan, whose verdicts arrive in one final poll.
func applierFor(t *testing.T, d doubles, plan types.Plan, verdicts ...types.Verdict) *Applier {
	t.Helper()
	d.noCheckouts()
	d.src.EXPECT().Poll(mock.Anything).Return(types.VerdictBatch{Verdicts: verdicts, Done: true}, nil).Maybe()
	a, err := NewApplier(d.vcs, clone, d.provider, d.src, d.facts, t.TempDir())
	require.NoError(t, err)
	return a
}

// bases answers the base branch's fetches in order.
func (d doubles) bases(commits ...string) {
	for _, c := range commits {
		d.vcs.EXPECT().FetchRef(mock.Anything, clone.Root, clone.Remote, "refs/heads/main").Return(c, nil).Once()
	}
}

// rechecks answers applying's re-check of c at its head with a.
func (d doubles) rechecks(c types.Change, a types.Approval) {
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).Return(nil)
	d.plain(c.Head)
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(a, nil).Once()
}

func approvedAs(c types.Change) types.Approval {
	return types.Approval{Approved: true, Head: c.Head, Base: "main", Method: c.Method, Queued: true}
}

// rebuilds answers rebuilding c's candidate onto onto as the commit made, touching
// touched.
func (d doubles) rebuilds(c types.Change, onto, made string, touched ...string) {
	d.vcs.EXPECT().CreateCheckout(mock.Anything, clone.Root, mock.Anything, onto).Return(nil).Once()
	d.vcs.EXPECT().StartMerge(mock.Anything, mock.Anything, c.Head, queueIdentity).Return(nil).Once()
	d.vcs.EXPECT().Conflicts(mock.Anything, mock.Anything).Return(nil, nil).Once()
	d.vcs.EXPECT().Commit(mock.Anything, mock.Anything, magustypes.CheckoutCommit{CommitMeta: queueMeta("merge queue: candidate #" + c.ID)}).Return(made, nil).Once()
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, onto, made).Return(touched, nil).Once()
	d.vcs.EXPECT().RemoveCheckout(mock.Anything, clone.Root, mock.Anything).Return(nil).Once()
}

// status expects commit status state on c's commit, its description starting with desc.
func (d doubles) status(c types.Change, commit string, state types.CommitState, desc string) *mock.Call {
	return d.provider.EXPECT().PostStatus(mock.Anything, mock.Anything, commit, mock.MatchedBy(func(s types.CommitStatus) bool {
		return s.Context == DefaultStatusContext && s.State == state && strings.HasPrefix(s.Description, desc)
	})).Return(nil).Once()
}

// mergesAt answers the provider's merge of c at commit, after which the base is at after
// with parents, carrying tree.
func (d doubles) mergesAt(c types.Change, commit, message, after, tree string, parents ...string) *mock.Call {
	call := d.provider.EXPECT().MergeChange(mock.Anything, mock.Anything, types.MergeOptions{Commit: commit, Message: message}).Return(nil).Once()
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, after).Return(tree, nil).Once()
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, after).Return(magustypes.Commit{ID: after, Parents: parents}, nil).Maybe()
	return call
}

// cleanMerge is v's change merging at its head: rebuilt as validated onto the base, the
// provider's own merge giving exactly the validated tree.
func (d doubles) cleanMerge(v types.Verdict) (pending, merge, success *mock.Call) {
	c := v.Change
	after := oid("after", c.ID)
	d.bases(base, after)
	d.rechecks(c, approvedAs(c))
	d.rebuilds(c, base, v.CandidateCommit, "a/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil).Once()
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil).Once()
	pending = d.status(c, c.Head, types.StatePending, "applying candidate")
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
	merge = d.mergesAt(c, c.Head, v.Message, after, "validated", base)
	success = d.status(c, c.Head, types.StateSuccess, "merged as")
	return pending, merge, success
}

func TestNewApplierRefusesAMissingPart(t *testing.T) {
	d := newDoubles(t)
	for name, tc := range map[string]struct {
		build func() (*Applier, error)
		want  string
	}{
		"vcs":      {func() (*Applier, error) { return NewApplier(nil, clone, d.provider, d.src, d.facts, "/s") }, "applier needs a VCS, a provider, a verdict source and build facts"},
		"provider": {func() (*Applier, error) { return NewApplier(d.vcs, clone, nil, d.src, d.facts, "/s") }, "applier needs a VCS, a provider, a verdict source and build facts"},
		"source":   {func() (*Applier, error) { return NewApplier(d.vcs, clone, d.provider, nil, d.facts, "/s") }, "applier needs a VCS, a provider, a verdict source and build facts"},
		"facts":    {func() (*Applier, error) { return NewApplier(d.vcs, clone, d.provider, d.src, nil, "/s") }, "applier needs a VCS, a provider, a verdict source and build facts"},
		"clone":    {func() (*Applier, error) { return NewApplier(d.vcs, Clone{}, d.provider, d.src, d.facts, "/s") }, "clone needs a root and a remote"},
		"relative": {func() (*Applier, error) { return NewApplier(d.vcs, clone, d.provider, d.src, d.facts, "s") }, `scratch directory "s" is not absolute`},
	} {
		_, err := tc.build()
		require.EqualError(t, err, tc.want, name)
	}
}

// I1 and I14: applying asks the provider to merge the head, pinned, only once its own
// rebuild is the validated candidate and the provider's merge gives the validated tree,
// and it posts success only once the base carries that tree.
func TestAGreenChangeMergesAtItsHeadAndSucceedsOnlyOnceMerged(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	v := validated(c, base, "")
	d.caps()
	pending, merge, success := d.cleanMerge(v)
	merge.NotBefore(pending)
	success.NotBefore(merge)
	var out bytes.Buffer
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	a.Events = NewEvents(&out)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
	assert.Contains(t, out.String(), `"kind":"merged","change":"1"`)
}

// I1: the base must carry the tree the Applier predicted, or nothing more merges on a
// base nobody validated.
func TestABaseThatDoesNotCarryThePredictedTreeStopsApplying(t *testing.T) {
	d := newDoubles(t)
	one, two := change("1", "a"), change("2", "b")
	v := validated(one, base, "")
	after := oid("after", "1")
	d.caps()
	d.bases(base, after)
	d.rechecks(one, approvedAs(one))
	d.rebuilds(one, base, v.CandidateCommit, "a/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: one.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil)
	d.status(one, one.Head, types.StatePending, "applying")
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, one.Head).Return(approvedAs(one), nil).Once()
	d.mergesAt(one, one.Head, v.Message, after, "other", base)
	a := applierFor(t, d, planOf([]types.Change{one}, []types.Change{two}), v, validated(two, base, ""))
	err := a.Run(t.Context(), planOf([]types.Change{one}, []types.Change{two}))
	require.ErrorContains(t, err, "#1 merged, but main at "+after[:12]+" carries tree other, not the validated validated")
}

// I15: the base's history has the shape the merge method gives.
func TestEveryMergeMethodMustMergeInItsOwnShape(t *testing.T) {
	tip, head := base, head("1")
	for name, tc := range map[string]struct {
		method  types.MergeMethod
		parents []string
		line    []magustypes.Commit
		wantErr bool
	}{
		"squash: one commit on the tip":        {method: types.MethodSquash, parents: []string{tip}},
		"squash that kept the head":            {method: types.MethodSquash, parents: []string{tip, head}, wantErr: true},
		"merge: the tip and the head":          {method: types.MethodMerge, parents: []string{tip, head}},
		"merge that squashed":                  {method: types.MethodMerge, parents: []string{tip}, wantErr: true},
		"rebase: a line of commits on the tip": {method: types.MethodRebase, line: []magustypes.Commit{{ID: "r2", Parents: []string{"r1"}}, {ID: "r1", Parents: []string{tip}}}},
		"rebase that left a merge commit":      {method: types.MethodRebase, line: []magustypes.Commit{{ID: "r2", Parents: []string{"r1", head}}, {ID: "r1", Parents: []string{tip}}}, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, "after").Return("tree", nil)
			d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, "after").Return(magustypes.Commit{ID: "after", Parents: tc.parents}, nil)
			if tc.method == types.MethodRebase {
				d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, tip, "after", []string(nil)).Return(tc.line, nil)
			}
			r := &applyRun{Applier: &Applier{vcs: d.vcs, clone: clone}, plan: planOf()}
			err := r.mergedAs(t.Context(), change("1"), tip, "after", head, "tree", tc.method)
			if tc.wantErr {
				require.ErrorContains(t, err, "is not what a "+string(tc.method)+" merge onto")
				return
			}
			require.NoError(t, err)
		})
	}
}

// I14: where the provider's own merge would differ from the validated tree in declared
// outputs alone, applying pushes an update commit whose tree is the validated one on the
// head and the tip, made by the queue, under a lease on the head, and merges that.
func TestAnUpdateCommitIsTheValidatedTreeOnTheHeadAndTheTip(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	c.Branch = "feature"
	v := validated(c, base, "")
	after := oid("after", "1")
	update := head("update")
	d.caps()
	d.bases(base, after)
	d.rechecks(c, approvedAs(c))
	d.rebuilds(c, base, v.CandidateCommit, "a/x.go", "gen/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "plain"}, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, "plain", "validated").Return([]string{"gen/x.go"}, nil).Twice()
	d.facts.EXPECT().Outputs(mock.Anything, []string{"gen/x.go"}).Return(map[string]bool{"gen/x.go": true}, nil)
	d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]string{"a/x.go"}, nil)
	d.facts.EXPECT().Generation(mock.Anything, []string{"gen/x.go"}, []string{"a/x.go"}).Return(types.Generation{Units: []string{"gen"}}, nil)
	d.vcs.EXPECT().CommitTree(mock.Anything, clone.Root, magustypes.TreeCommit{
		CommitMeta: magustypes.CommitMeta{Message: "merge main into #1 and regenerate generated files", Author: queueIdentity, Committer: queueIdentity},
		Tree:       "validated", Parents: []string{c.Head, base}}).Return(update, nil)
	push := d.vcs.EXPECT().Push(mock.Anything, clone.Root, magustypes.PushLease{Remote: clone.Remote, Ref: "refs/heads/feature", To: update, Expected: c.Head}).Return(nil).Call
	d.status(c, update, types.StatePending, "applying candidate")
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, update).Return(types.Approval{Approved: true, Head: update, Base: "main", Method: c.Method, Queued: true}, nil)
	d.mergesAt(c, update, v.Message, after, "validated", base).NotBefore(push)
	d.status(c, update, types.StateSuccess, "merged as")
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	a.Regenerate = func(context.Context, types.Regeneration) error {
		t.Error("the update commit's outputs come from the validated tree the rebuild reproduced")
		return nil
	}
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
}

// kicks expects c kicked back at its head with code, after re-reading its head.
func (d doubles) kicks(c types.Change, code types.Code, report string) {
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
	d.status(c, c.Head, types.StateFailure, "kicked back")
	d.provider.EXPECT().KickBack(mock.Anything, mock.Anything, c.Head, mock.MatchedBy(func(k types.Kick) bool {
		return k.Code == code && strings.Contains(k.Report, report)
	})).Return(nil).Once()
}

// waits expects c left queued at commit, its status naming reason.
func (d doubles) waits(c types.Change, commit, reason string) {
	d.status(c, commit, types.StatePending, "waiting: "+reason)
}

// updating sets up c's merge up to the update commit it needs.
func (d doubles) updating(c types.Change, v types.Verdict) {
	d.caps()
	d.bases(base)
	d.rechecks(c, types.Approval{Approved: true, Head: c.Head, Base: "main", Method: c.Method, Queued: true, BranchSharedWith: sharedWith[c.ID]})
	d.rebuilds(c, base, v.CandidateCommit, "gen/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "plain"}, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, "plain", "validated").Return([]string{"gen/x.go"}, nil)
	d.facts.EXPECT().Outputs(mock.Anything, []string{"gen/x.go"}).Return(map[string]bool{"gen/x.go": true}, nil)
	d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]string{"a/x.go"}, nil)
	d.facts.EXPECT().Generation(mock.Anything, []string{"gen/x.go"}, []string{"a/x.go"}).Return(types.Generation{Units: []string{"gen"}}, nil)
}

var sharedWith = map[string][]string{"shared": {"9"}}

// An update commit goes only where the queue may push it, and only to the change it is
// for: a branch it cannot push to, or one another open change is headed at, sends the
// change back; a branch that moved waits; a push the branch refused sends it back.
func TestAnUpdateCommitGoesOnlyWhereItBelongs(t *testing.T) {
	for name, tc := range map[string]struct {
		branch string
		id     string
		push   error
		kick   string
		wait   string
		pushed bool
	}{
		"no branch":       {id: "1", kick: "the queue cannot push to its branch"},
		"a shared branch": {id: "shared", branch: "feature", kick: "its branch is also the head of #9, which would gain it too"},
		"a moved branch":  {id: "1", branch: "feature", push: fmt.Errorf("push: %w", magustypes.ErrStaleLease), wait: "its branch moved or was deleted since validation", pushed: true},
		"a refused push": {id: "1", branch: "feature", push: &magustypes.PushRejectedError{Ref: "refs/heads/feature", Reason: "protected branch hook declined"},
			kick: "its branch refused it: protected branch hook declined", pushed: true},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			c := change(tc.id, "a")
			c.Branch = tc.branch
			v := validated(c, base, "")
			d.updating(c, v)
			if tc.pushed {
				d.vcs.EXPECT().CommitTree(mock.Anything, clone.Root, mock.Anything).Return(head("update"), nil)
				d.vcs.EXPECT().Push(mock.Anything, clone.Root, mock.Anything).Return(tc.push)
			}
			if tc.kick != "" {
				d.kicks(c, types.CodeKickRefused, tc.kick)
			} else {
				d.waits(c, c.Head, tc.wait)
			}
			a := applierFor(t, d, planOf([]types.Change{c}), v)
			a.Regenerate = func(context.Context, types.Regeneration) error { return nil }
			require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
		})
	}
}

// Nothing validation produced is trusted: a rebuild that is not the validated candidate,
// with nothing to regenerate, is validated again rather than merged.
func TestACandidateTheRebuildDoesNotReproduceIsValidatedAgain(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	v := validated(c, base, "")
	d.caps()
	d.bases(base)
	d.rechecks(c, approvedAs(c))
	d.rebuilds(c, base, head("rebuilt"), "a/x.go")
	d.facts.EXPECT().Outputs(mock.Anything, []string{"a/x.go"}).Return(map[string]bool{}, nil)
	d.waits(c, c.Head, "rebuilding its candidate gave "+head("rebuilt")[:12]+", not the validated "+v.CandidateCommit[:12])
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
}

// The job holding the write credential never runs a change's code: a merge needing
// outputs regenerated from code the change touched goes back to its author, and nothing
// is regenerated.
func TestAMergeNeedingRegenerationOfCodeTheChangeTouchedIsKickedBack(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	v := validated(c, base, "")
	d.caps()
	d.bases(base)
	d.rechecks(c, approvedAs(c))
	d.rebuilds(c, base, head("rebuilt"), "gen/gen.go", "gen/x.go")
	d.facts.EXPECT().Outputs(mock.Anything, []string{"gen/gen.go", "gen/x.go"}).Return(map[string]bool{"gen/x.go": true}, nil)
	d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]string{"gen/gen.go"}, nil)
	d.facts.EXPECT().Generation(mock.Anything, []string{"gen/x.go"}, []string{"gen/gen.go"}).Return(types.Generation{Units: []string{"gen"}, Code: []string{"gen/gen.go"}}, nil)
	d.kicks(c, types.CodeKickRefused, "it changes code their regeneration runs (gen/gen.go), so only its author can regenerate them")
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	a.Regenerate = func(context.Context, types.Regeneration) error {
		t.Error("regenerated from code the change touched")
		return nil
	}
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
}

// Where the build tool proves the change touches none of the generator's code, applying
// runs the base's own regeneration, with the proven units, and merges only a rebuild that
// is the validated candidate.
func TestTheBasesOwnRegenerationRebuildsTheCandidate(t *testing.T) {
	for name, tc := range map[string]struct {
		regenerated string
		kick        string
	}{
		"reproduced":   {},
		"another tree": {regenerated: head("other"), kick: "the base's regeneration of gen/x.go differs from what validation produced in gen/x.go"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			c := change("1", "a")
			v := validated(c, base, "")
			d.caps()
			d.rechecks(c, approvedAs(c))
			d.rebuilds(c, base, head("rebuilt"), "gen/x.go")
			d.facts.EXPECT().Outputs(mock.Anything, []string{"gen/x.go"}).Return(map[string]bool{"gen/x.go": true}, nil)
			d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]string{"a/x.go"}, nil)
			d.facts.EXPECT().Generation(mock.Anything, []string{"gen/x.go"}, []string{"a/x.go"}).Return(types.Generation{Units: []string{"gen"}}, nil)
			regenerated := v.CandidateCommit
			if tc.regenerated != "" {
				regenerated = tc.regenerated
			}
			d.vcs.EXPECT().DirtyFiles(mock.Anything, mock.Anything, []string(nil)).Return([]string{"gen/x.go"}, nil)
			d.facts.EXPECT().Outputs(mock.Anything, []string{"gen/x.go"}).Return(map[string]bool{"gen/x.go": true}, nil)
			d.vcs.EXPECT().Commit(mock.Anything, mock.Anything, magustypes.CheckoutCommit{CommitMeta: queueMeta("regenerate generated files"), Paths: []string{"gen/x.go"}}).Return(regenerated, nil)
			var units []string
			if tc.kick != "" {
				d.bases(base)
				d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, regenerated, v.CandidateCommit).Return([]string{"gen/x.go"}, nil)
				d.kicks(c, types.CodeKickRefused, tc.kick)
			} else {
				after := oid("after", "1")
				d.bases(base, after)
				d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
				d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil)
				d.status(c, c.Head, types.StatePending, "applying")
				d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
				d.mergesAt(c, c.Head, v.Message, after, "validated", base)
				d.status(c, c.Head, types.StateSuccess, "merged")
			}
			a := applierFor(t, d, planOf([]types.Change{c}), v)
			a.Regenerate = func(_ context.Context, r types.Regeneration) error {
				units = r.Units
				assert.Equal(t, []string{"gen/x.go"}, r.Paths)
				return nil
			}
			require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
			assert.Equal(t, []string{"gen"}, units)
		})
	}
}

// Applying re-reads the change before it merges: a head pushed, an intent withdrawn, an
// approval dismissed, a method switched or a base changed since validation all leave the
// change queued, and nothing is merged.
func TestAChangeThatMovedSinceValidationIsNotMerged(t *testing.T) {
	c := change("1", "a")
	moved := head("pushed later")
	for name, tc := range map[string]struct {
		a      types.Approval
		commit string
		reason string
	}{
		"a new head":         {a: types.Approval{Approved: true, Head: moved, Base: "main", Method: types.MethodSquash, Queued: true}, commit: moved, reason: "head moved to " + moved[:12] + " after validation"},
		"withdrawn":          {a: types.Approval{Approved: true, Head: c.Head, Base: "main", Method: types.MethodSquash}, commit: c.Head, reason: "its merge intent was withdrawn after validation"},
		"approval dismissed": {a: types.Approval{Head: c.Head, Base: "main", Method: types.MethodSquash, Queued: true, Reason: "dismissed"}, commit: c.Head, reason: "approval at " + c.Head[:12] + " was withdrawn: dismissed"},
		"another method":     {a: types.Approval{Approved: true, Head: c.Head, Base: "main", Method: types.MethodMerge, Queued: true}, commit: c.Head, reason: "validated as squash, now merge"},
		"another base":       {a: types.Approval{Approved: true, Head: c.Head, Base: "release", Method: types.MethodSquash, Queued: true}, commit: c.Head, reason: "targets release, not main; skipped"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			v := validated(c, base, "")
			d.caps(types.MethodSquash, types.MethodMerge)
			d.bases(base)
			d.rechecks(c, tc.a)
			d.waits(c, tc.commit, tc.reason)
			a := applierFor(t, d, planOf([]types.Change{c}), v)
			require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
		})
	}
}

// Merge intent, base and head are read once more right before the merge.
func TestAChangeWithdrawnRightBeforeItsMergeIsNotMerged(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	v := validated(c, base, "")
	d.caps()
	d.bases(base)
	d.rechecks(c, approvedAs(c))
	d.rebuilds(c, base, v.CandidateCommit, "a/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil)
	d.status(c, c.Head, types.StatePending, "applying")
	withdrawn := approvedAs(c)
	withdrawn.Queued = false
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(withdrawn, nil).Once()
	d.waits(c, c.Head, "its merge intent was withdrawn before its merge")
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
}

func TestAProviderRefusalWaitsAndHoldsWhatIsValidatedOnIt(t *testing.T) {
	d := newDoubles(t)
	one, two := change("1", "a"), change("2", "a")
	v1 := validated(one, base, "")
	v2 := validated(two, v1.CandidateCommit, "1")
	d.caps()
	d.bases(base)
	d.rechecks(one, approvedAs(one))
	d.rebuilds(one, base, v1.CandidateCommit, "a/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v1.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: one.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil)
	d.status(one, one.Head, types.StatePending, "applying")
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, one.Head).Return(approvedAs(one), nil).Once()
	d.provider.EXPECT().MergeChange(mock.Anything, mock.Anything, types.MergeOptions{Commit: one.Head, Message: v1.Message}).Return(errors.New("required status missing"))
	d.waits(one, one.Head, "the provider refused the merge: required status missing")
	d.waits(two, two.Head, "validated on top of #1, which did not merge")
	a := applierFor(t, d, planOf([]types.Change{one, two}), v1, v2)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{one, two})))
}

// A verdict names its change, but only the plan says which head was admitted and what
// lies beneath it; one that disagrees merges nothing.
func TestAVerdictThatDisagreesWithThePlanMergesNothing(t *testing.T) {
	one := change("1", "a")
	two := stacked("2", one, "a")
	for name, tc := range map[string]struct {
		v      types.Verdict
		reason string
	}{
		"another base":      {v: func() types.Verdict { v := validated(one, base, ""); v.BaseCommit = head("old"); return v }(), reason: "validated on " + head("old")[:12] + ", not this plan's base"},
		"another head":      {v: func() types.Verdict { v := validated(one, base, ""); v.Change.Head = head("x"); return v }(), reason: "validated at " + head("x")[:12] + ", not the planned head"},
		"after nothing":     {v: validated(one, base, "7"), reason: "validated on top of #7, which is not beneath it in its partition"},
		"not onto the base": {v: validated(one, head("elsewhere"), ""), reason: "validated at the bottom of its partition, but onto " + head("elsewhere")[:12] + ", not the plan's base"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			d.caps()
			d.waits(one, one.Head, tc.reason)
			a := applierFor(t, d, planOf([]types.Change{one}), tc.v)
			require.NoError(t, a.Run(t.Context(), planOf([]types.Change{one})))
		})
	}
	t.Run("without the change beneath", func(t *testing.T) {
		d := newDoubles(t)
		d.caps()
		d.waits(one, one.Head, "conflicts")
		d.waits(two, two.Head, "validated without #1, which it is stacked on")
		held := types.Verdict{BaseCommit: base, Change: one, Decision: types.DecisionWait, Code: types.CodeWaitConflictAhead, Reason: "conflicts"}
		a := applierFor(t, d, planOf([]types.Change{one, two}), held, validated(two, base, ""))
		require.NoError(t, a.Run(t.Context(), planOf([]types.Change{one, two})))
	})
}

func TestAnUnreadableVerdictHoldsItsChangeAlone(t *testing.T) {
	d := newDoubles(t)
	one := change("1", "a")
	d.caps()
	d.noCheckouts()
	d.src.EXPECT().Poll(mock.Anything).Return(types.VerdictBatch{Rejected: []types.RejectedVerdict{{Change: "1", Reason: "not a zip"}, {Change: "8", Reason: "x"}}, Done: true}, nil)
	d.waits(one, one.Head, "its verdict could not be read: not a zip")
	a, err := NewApplier(d.vcs, clone, d.provider, d.src, d.facts, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{one})))
}

// Planning's verdicts and validation's red ones reach the provider; a kick for a head
// that moved since it was decided waits instead, since the new head gets its own
// decision.
func TestSettledVerdictsReachTheProvider(t *testing.T) {
	kicked, held, gone, red, moved := change("1", "a"), change("2", "b"), change("3", "c"), change("4", "d"), change("5", "e")
	d := newDoubles(t)
	d.caps()
	d.kicks(kicked, types.CodeKickConflict, "conflicts")
	d.waits(held, held.Head, "not approved")
	d.kicks(red, types.CodeKickRed, "the gate failed")
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, moved.Head).Return(types.Approval{Head: head("newer"), Base: "main", Method: types.MethodSquash}, nil)
	d.waits(moved, head("newer"), "head moved to "+head("newer")[:12]+" since it was decided")
	plan := planOf([]types.Change{red}, []types.Change{moved})
	plan.Verdicts = []types.Verdict{
		{Change: kicked, Decision: types.DecisionKick, Code: types.CodeKickConflict, Report: "conflicts in a.go"},
		{Change: held, Decision: types.DecisionWait, Code: types.CodeWaitNotApproved, Reason: "not approved"},
		{Change: gone, Decision: types.DecisionMerged, Reason: "its head is already on main"},
	}
	redV := types.Verdict{BaseCommit: base, Change: red, Decision: types.DecisionKick, Code: types.CodeKickRed, Report: "the gate failed: exit 1"}
	movedV := types.Verdict{BaseCommit: base, Change: moved, Decision: types.DecisionKick, Code: types.CodeKickRed, Report: "the gate failed"}
	a := applierFor(t, d, plan, redV, movedV)
	require.NoError(t, a.Run(t.Context(), plan))
}

func TestWhatNoVerdictReachedWaitsForTheNextRun(t *testing.T) {
	d := newDoubles(t)
	one := change("1", "a")
	d.caps()
	d.waits(one, one.Head, "not validated in this run")
	a := applierFor(t, d, planOf([]types.Change{one}))
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{one})))
}

// A dry run reports and calls nothing on the provider: no expectation is set on it.
func TestApplyDryRunCallsNothingOnTheProvider(t *testing.T) {
	d := newDoubles(t)
	one := change("1", "a")
	var out bytes.Buffer
	a := applierFor(t, d, planOf([]types.Change{one}), validated(one, base, ""))
	a.DryRun, a.Events = true, NewEvents(&out)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{one})))
	assert.Contains(t, out.String(), "dry run: would merge candidate "+candidateOf(base, one.Head)[:12])
}

func TestAChangeRetargetedAfterPlanningIsSkippedWithoutRetargeting(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	d.caps()
	d.bases(base)
	d.rechecks(c, types.Approval{Approved: true, Head: c.Head, Base: "release", Method: types.MethodSquash, Queued: true})
	d.waits(c, c.Head, "targets release, not main; skipped")
	a := applierFor(t, d, planOf([]types.Change{c}), validated(c, base, ""))
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
}

// I19 through the provider: an atomic provider merges a run of stacked changes in one
// call through its top, every member beneath pinned to its head, the bottom merged from
// its natural merge base and each member above from its stack base.
func TestAnAtomicProviderMergesAStackRunInOneCallWithEveryMemberPinned(t *testing.T) {
	d := newDoubles(t)
	one := change("1", "a")
	two := stacked("2", one, "a")
	v1 := validated(one, base, "")
	v2 := validated(two, v1.CandidateCommit, "1")
	after := oid("after")
	d.provider.EXPECT().Describe(mock.Anything, types.ListQuery{Base: "main"}).
		Return(types.Capabilities{StackMerge: types.StackMergeAtomic, Methods: []types.MergeMethod{types.MethodSquash}}, nil)
	d.bases(base, after)
	d.rechecks(one, approvedAs(one))
	d.rechecks(two, approvedAs(two))
	d.rebuilds(one, base, v1.CandidateCommit, "a/x.go")
	d.rebuilds(two, v1.CandidateCommit, v2.CandidateCommit, "a/y.go")
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, one.Head, v1.CandidateCommit).Return(true, nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v1.CandidateCommit).Return("tree 1", nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v2.CandidateCommit).Return("tree 2", nil)
	// The bottom from its natural merge base, even though a change beneath it may have
	// merged as a squash; the member above from its stack base.
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: one.Head}).Return(magustypes.TreeMergeResult{Tree: "tree 1"}, nil)
	expected := head("expected")
	d.vcs.EXPECT().CommitTree(mock.Anything, clone.Root, magustypes.TreeCommit{CommitMeta: queueMeta("expected"), Tree: "tree 1", Parents: []string{base}}).Return(expected, nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Base: one.Head, Ours: expected, Theirs: two.Head}).Return(magustypes.TreeMergeResult{Tree: "tree 2"}, nil)
	d.status(one, one.Head, types.StatePending, "applying candidate")
	d.status(two, two.Head, types.StatePending, "applying candidate")
	merge := d.provider.EXPECT().MergeChange(mock.Anything, mock.Anything, types.MergeOptions{Commit: two.Head, Message: v2.Message,
		Through: []types.PinnedChange{{ID: "1", Commit: one.Head}}}).Return(nil).Call
	s1, s2 := head("s1"), after
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, after, []string(nil)).Return([]magustypes.Commit{{ID: s2, Parents: []string{s1}}, {ID: s1, Parents: []string{base}}}, nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, s1).Return("tree 1", nil)
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, s1).Return(magustypes.Commit{ID: s1, Parents: []string{base}}, nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, s2).Return("tree 2", nil)
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, s2).Return(magustypes.Commit{ID: s2, Parents: []string{s1}}, nil)
	d.status(one, one.Head, types.StateSuccess, "merged in a stack").NotBefore(merge)
	d.status(two, two.Head, types.StateSuccess, "merged in a stack").NotBefore(merge)
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, one.Head).Return(approvedAs(one), nil).Once()
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, two.Head).Return(approvedAs(two), nil).Once()
	a := applierFor(t, d, planOf([]types.Change{one, two}), v1, v2)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{one, two})))
}

func TestStackRunGreenRunPinsAndBases(t *testing.T) {
	one := change("1")
	two := stacked("2", one)
	three := stacked("3", two)
	loose := change("4")
	q := []types.Change{one, two, three, loose}
	assert.Equal(t, []types.Change{one}, stackRun(q, false), "a sequential provider merges one change per call")
	assert.Equal(t, []types.Change{one, two, three}, stackRun(q, true))
	hinted := two
	hinted.Parent = ""
	assert.Equal(t, []types.Change{one}, stackRun([]types.Change{one, hinted}, true), "the provider must declare the stack too")

	v1 := validated(one, base, "")
	v2 := validated(two, v1.CandidateCommit, "1")
	v3 := validated(three, head("elsewhere"), "2")
	assert.Equal(t, 2, greenRun(q[:3], map[string]types.Verdict{"1": v1, "2": v2, "3": v3}), "the third was not built on the second's candidate")
	assert.Equal(t, 0, greenRun(q[:3], map[string]types.Verdict{"2": v2}))

	assert.Equal(t, []types.PinnedChange{{ID: "1", Commit: one.Head}, {ID: "2", Commit: two.Head}}, pins(q[:3]))
	assert.Empty(t, pins(q[:1]))
	bottom := one
	bottom.StackBase = head("squashed")
	assert.Equal(t, []string{"", two.StackBase, three.StackBase}, runBases([]types.Change{bottom, two, three}))
}

// FuzzAtomicRun holds what an atomic provider is asked to merge to I19, atomic run: a
// run is a prefix of its partition's queue, each member stacked on the one before as
// the provider declared; its green part is each member validated on the candidate
// beneath; one call pins every member beneath the top at its head, lowest first; and the
// bottom merges from its natural merge base, whatever it is stacked on, while each
// member above merges from its stack base.
func FuzzAtomicRun(f *testing.F) {
	f.Add([]byte{})
	f.Fuzz(atomicRunHolds)
}

// atomicRunHolds is FuzzAtomicRun's property over one input.
func atomicRunHolds(t *testing.T, data []byte) {
	d := &draw{data: data}
	atomic := d.n(2) == 1 // the provider merges stacks atomically
	n := 1 + d.n(6)       // changes in the partition
	q := make([]types.Change, n)
	for i := range q {
		c := change(fmt.Sprint(i + 1))
		switch {
		case i == 0 && d.n(2) == 1: // the bottom is stacked on a merged change
			c.StackBase = oid("merged head")
		case i > 0 && d.n(3) != 0: // stacked on the change before it
			c.Below, c.StackBase = q[i-1].ID, q[i-1].Head
			if d.n(4) != 0 { // and the provider says so
				c.Parent = q[i-1].ID
			}
		}
		q[i] = c
	}
	got := map[string]types.Verdict{}
	for i, c := range q {
		if d.n(4) == 0 { // no verdict yet
			continue
		}
		v := validated(c, base, "")
		if d.n(5) == 0 {
			v = types.Verdict{Change: c, Decision: types.DecisionKick, Code: types.CodeKickRed}
		}
		if i > 0 && d.n(4) != 0 { // validated on top of the change before it
			v.After = q[i-1].ID
			if d.n(4) != 0 { // onto its candidate
				v.Onto = got[q[i-1].ID].CandidateCommit
			}
		}
		got[c.ID] = v
	}

	run := stackRun(q, atomic)
	require.NotEmpty(t, run)
	require.Equal(t, q[:len(run)], run, "a run is a prefix of the queue")
	if !atomic {
		require.Len(t, run, 1)
	}
	for i := 1; i < len(run); i++ {
		require.Equal(t, run[i-1].ID, run[i].Below)
		require.Equal(t, run[i-1].ID, run[i].Parent)
	}
	if atomic && len(run) < n {
		next := q[len(run)]
		require.False(t, next.Below == run[len(run)-1].ID && next.Parent == run[len(run)-1].ID, "a run takes every declared member")
	}
	g := greenRun(run, got)
	for i := range run[:g] {
		v := got[run[i].ID]
		require.Equal(t, types.DecisionMerge, v.Decision)
		if i > 0 {
			require.Equal(t, run[i-1].ID, v.After)
			require.Equal(t, got[run[i-1].ID].CandidateCommit, v.Onto)
		}
	}
	if g < len(run) {
		v, ok := got[run[g].ID]
		require.False(t, ok && v.Decision == types.DecisionMerge && (g == 0 || v.After == run[g-1].ID && v.Onto == got[run[g-1].ID].CandidateCommit),
			"the green part takes every green member")
	}
	if g < 2 {
		return
	}
	green := run[:g]
	through := pins(green)
	require.Len(t, through, g-1, "every member beneath the top is pinned")
	for i, p := range through {
		require.Equal(t, types.PinnedChange{ID: green[i].ID, Commit: green[i].Head}, p)
	}
	bases := runBases(green)
	require.Empty(t, bases[0], "the bottom merges from its natural merge base")
	for i := 1; i < g; i++ {
		require.Equal(t, green[i].StackBase, bases[i])
	}
}

// FuzzRetarget holds retargeting to I18, retarget: the queue points a change at its
// base only when it targets the branch of the change it is stacked on and that change
// merged this run; anything else is a change retargeted after planning, and is skipped.
func FuzzRetarget(f *testing.F) {
	f.Add(true, "feature", "1", "feature", true)
	f.Add(true, "feature", "1", "release", true)
	f.Add(true, "", "1", "", true)
	f.Fuzz(func(t *testing.T, stacked bool, branch, belowID, target string, merged bool) {
		c := change("2")
		if stacked {
			c.Below = "1"
		}
		below := change(belowID)
		below.Branch = branch
		ok := retargetable(c, below, merged, target)
		want := stacked && belowID == "1" && merged && branch != "" && branch == target
		require.Equal(t, want, ok)
	})
}
