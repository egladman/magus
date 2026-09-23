package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func green(id string) Verdict {
	return Verdict{BaseCommit: base, Change: change(id), Decision: DecisionMerge, Onto: base, CandidateCommit: head("c" + id), Method: MethodSquash}
}

// Two changes at one head each read as stacked on the other, which sent planning into
// unbounded recursion.
func TestChangesCheckRefusesTwoChangesAtOneHeadAndAnIDTwice(t *testing.T) {
	twin := change("2")
	twin.Head = head("1")
	require.EqualError(t, Changes{Base: "main", Changes: []Change{change("1"), twin}}.Check(), "changes[1]: #1 and #2 share head "+head("1"))

	merged := MergedChange{ID: "1", Head: head("m"), Commit: head("mc"), Method: MethodSquash}
	require.EqualError(t, Changes{Base: "main", Changes: []Change{change("1")}, Merged: []MergedChange{merged}}.Check(), `merged[0]: id "1" appears twice`)
	require.EqualError(t, Changes{Base: "main", Unqueued: []UnqueuedChange{{ID: "1", Head: "x"}}}.Check(), `unqueued[0]: #1: head "x" is not a full commit id`)
	require.ErrorContains(t, Changes{Base: "-main"}.Check(), "base:")
	require.NoError(t, Changes{Base: "main", Changes: []Change{change("1"), change("2")}}.Check())
}

// A code is how a provider and a workflow act on a verdict, so one that contradicts the
// decision it rides on is refused.
func TestVerdictCheckRefusesACodeThatContradictsItsDecision(t *testing.T) {
	merge := green("1")
	merge.Code = CodeKickRed
	for _, v := range []Verdict{
		{Change: change("1"), Decision: DecisionKick, Code: CodeWaitBehind},
		{Change: change("1"), Decision: DecisionWait, Code: CodeKickRed},
		{Change: change("1"), Decision: DecisionWait},
		{Change: change("1"), Decision: DecisionMerged, Code: CodeWaitBehind},
		merge,
	} {
		require.ErrorContains(t, v.Check(), "carries code", "%s %s", v.Decision, v.Code)
	}
	for _, v := range []Verdict{
		{Change: change("1"), Decision: DecisionKick, Code: CodeKickConflict},
		{Change: change("1"), Decision: DecisionWait, Code: CodeWaitUnqueuedBelow},
		{Change: change("1"), Decision: DecisionMerged},
		green("1"),
	} {
		require.NoError(t, v.Check(), "%s %s", v.Decision, v.Code)
	}
}

// Applying rebuilds a merge verdict's candidate onto what it names, so a merge verdict
// naming none of them is refused, as is a commit that is no commit id.
func TestVerdictCheckRefusesAMergeThatNamesNoCandidate(t *testing.T) {
	for name, edit := range map[string]func(*Verdict){
		"no candidate": func(v *Verdict) { v.CandidateCommit = "" },
		"no onto":      func(v *Verdict) { v.Onto = "" },
		"no base":      func(v *Verdict) { v.BaseCommit = "" },
	} {
		v := green("1")
		edit(&v)
		require.ErrorContains(t, v.Check(), "names no base commit, onto or candidate commit", name)
	}
	v := green("1")
	v.Onto = "HEAD"
	require.ErrorContains(t, v.Check(), `onto "HEAD" is not a commit id`)
	v = green("1")
	v.After = "../1"
	require.ErrorContains(t, v.Check(), "after:")
	v = green("1")
	v.Method = ""
	require.ErrorContains(t, v.Check(), `merge method ""`)
	require.ErrorContains(t, Verdict{Change: change("1"), Decision: "ship"}.Check(), `unknown decision "ship"`)
}

func TestPlanCheckRefusesAChangeAheadOfWhatItIsStackedOnADuplicateAndAPlanningMerge(t *testing.T) {
	child := change("2")
	child.Below = "1"
	for _, p := range []Plan{
		{Base: "main", BaseCommit: base, Depth: 1, Partitions: [][]Change{{child, change("1")}}},
		{Base: "main", BaseCommit: base, Depth: 1, Partitions: [][]Change{{change("1")}, {child}}},
	} {
		require.ErrorContains(t, p.Check(), "is stacked on #1, which is not ahead of it in its partition")
	}
	dup := Plan{Base: "main", BaseCommit: base, Depth: 1, Partitions: [][]Change{{change("1")}, {change("1")}}}
	require.ErrorContains(t, dup.Check(), `change "1" appears twice`)
	merge := Plan{Base: "main", BaseCommit: base, Depth: 1, Verdicts: []Verdict{green("1")}}
	require.ErrorContains(t, merge.Check(), "planning decides merge for #1, which only validation decides")
	require.ErrorContains(t, Plan{Base: "main", BaseCommit: base}.Check(), "depth 0 is below 1")
	require.ErrorContains(t, Plan{Base: "main", BaseCommit: "tip", Depth: 1}.Check(), `base commit "tip" is not a commit id`)
	require.NoError(t, Plan{Base: "main", BaseCommit: base, Depth: 1, Partitions: [][]Change{{change("1"), child}}}.Check())
}

func TestCapabilitiesCheckRefusesAnUnknownStackMergeAndNoMethod(t *testing.T) {
	require.ErrorContains(t, Capabilities{StackMerge: "batch", Methods: []MergeMethod{MethodSquash}}.Check(), `provider describes stack merging as "batch"`)
	require.EqualError(t, Capabilities{StackMerge: StackMergeAtomic}.Check(), "provider allows no merge method")
	require.ErrorContains(t, Capabilities{StackMerge: StackMergeAtomic, Methods: []MergeMethod{"ff"}}.Check(), `merge method "ff"`)
	caps := Capabilities{StackMerge: StackMergeSequential, Methods: []MergeMethod{MethodSquash}}
	require.NoError(t, caps.Check())
	require.True(t, caps.Allows(MethodSquash))
	require.False(t, caps.Allows(MethodMerge))
}
