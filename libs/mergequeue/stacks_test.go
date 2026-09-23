package mergequeue

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	withoutL = "one\ntwo\nthree\n"
	withL    = "one\nL\ntwo\nthree\n"
)

// stacked opens #1 adding line L and #2 branched from #1's head deleting it again, both
// squashed: the stack in which a squash of #1 makes #2's natural merge base the fork
// point, from which #2's deletion reads as never having happened.
func stacked(t *testing.T) (w *world, a, b Change) {
	w = newWorld(t, map[string]string{"f": withoutL})
	ha := w.commit(w.base, "add L", map[string]*string{"f": str(withL)})
	hb := w.commit(ha, "drop L, add g", map[string]*string{"f": str(withoutL), "g": str("g\n")})
	a = w.open("1", ha)
	b = w.open("2", hb, func(c *Change) { c.Affected = []string{"b"} })
	return w, a, b
}

// B1: the prediction of a stacked child merged from any base older than its candidate's
// onto brings back the line it deleted from the change beneath it, lands that tree,
// and passes the check after the merge, since the check compares against the same
// prediction. Predicted from onto, the child lands exactly its own delta.
func TestAStackedChildsDeletionOfItsParentsLineStaysDeleted(t *testing.T) {
	w, _, _ := stacked(t)
	p := w.plan(2)
	require.Equal(t, [][]string{{"1", "2"}}, ids(p.Partitions), "one partition whatever the keys say")
	assert.Equal(t, "1", p.Partitions[0][1].Below)
	got := w.validate(p, newGate())
	two := got.get(t, "2")
	assert.Equal(t, tree{"f": withoutL, "g": "g\n"}, w.m.files(two.Candidate), "validated without L")

	evs, err := w.apply(p, got)
	require.NoError(t, err)
	assert.Equal(t, map[string]EventKind{"1": EventMerged, "2": EventMerged}, outcomes(evs))
	assert.Equal(t, tree{"f": withoutL, "g": "g\n"}, w.m.files(w.main()), "main carries the validated tree")

	// What the old prediction, from the plan's base, would have landed.
	one := w.p.landed[0].Commit
	old, err := w.m.MergeTrees(context.Background(), TreeMerge{Base: p.BaseCommit, Ours: one, Theirs: two.Candidate})
	require.NoError(t, err)
	assert.Equal(t, withL, w.m.files(old.Tree)["f"], "the resurrected line")

	// #2 landed through an update commit whose own delta is exactly what its reviewers
	// approved: tip plus #2's changes since #1's head.
	update := w.m.ref(branchRef("pr2"))
	c, err := w.m.FindCommit(context.Background(), update)
	require.NoError(t, err)
	assert.Equal(t, []string{p.Partitions[0][1].Head, one}, c.Parents)
}

// The same stack across runs: #1 landed as a squash in an earlier run, so #2 is built
// and landed against #1's head, which the provider lists as landed, not against main's.
func TestAChildOfAParentSquashedInAnEarlierRunLandsItsOwnDelta(t *testing.T) {
	w, _, _ := stacked(t)
	p := w.plan(1)
	one := p.Partitions[0][0]
	p.Partitions[0] = p.Partitions[0][:1]
	_, err := w.apply(p, once(w.validate(p, newGate()).get(t, "1")))
	require.NoError(t, err)
	require.Len(t, w.p.landed, 1)

	p = w.plan(1)
	two := p.Partitions[0][0]
	assert.Equal(t, one.Head, two.StackBase, "stacked on #1's landed head")
	assert.Empty(t, two.Below, "nothing left to land first")
	got := w.validate(p, newGate())
	assert.Equal(t, tree{"f": withoutL, "g": "g\n"}, w.m.files(got.get(t, "2").Candidate))
	evs, err := w.apply(p, got)
	require.NoError(t, err)
	assert.Equal(t, EventMerged, last(evs, "2").Kind)
	assert.Equal(t, tree{"f": withoutL, "g": "g\n"}, w.m.files(w.main()))
}

func TestAChildListedBeforeItsParentIsPlannedAfterIt(t *testing.T) {
	w := newWorld(t, map[string]string{"f": "f\n"})
	ha := w.commit(w.base, "a", map[string]*string{"a": str("a")})
	hb := w.commit(ha, "b", map[string]*string{"b": str("b")})
	w.open("2", hb, func(c *Change) { c.Affected = []string{"b"} })
	w.open("1", ha)
	w.open("3", w.commit(w.base, "c", map[string]*string{"c": str("c")}), func(c *Change) { c.Affected = []string{"c"} })
	p := w.plan(2)
	assert.Equal(t, [][]string{{"1", "2"}, {"3"}}, ids(p.Partitions))
	assert.Equal(t, ha, p.Partitions[0][1].StackBase)
}

// A merge of the base into a change, GitHub's "Update branch" or an update commit the
// queue pushed, moves its head past the commit a change stacked on it carries; the stack
// is still there, open or landed. Found by TestStacksKeepTheirInvariants.
func TestAStackSurvivesAMergeOfTheBaseIntoTheChangeBeneath(t *testing.T) {
	w := newWorld(t, map[string]string{"f": "f\n"})
	a := w.commit(w.base, "a", map[string]*string{"a": str("a")})
	w.push(w.commit(w.base, "main", map[string]*string{"m": str("m")}))
	updated := w.m.mergeCommit(a, w.main(), tree{"f": "f\n", "a": "a", "m": "m"}, "update branch")
	w.open("1", updated)
	w.p.approvedAt["1"] = a // a review of a covers a plain merge of the base into it
	w.open("2", w.commit(a, "b", map[string]*string{"b": str("b")}), func(c *Change) { c.Affected = []string{"b"} })
	p := w.plan(2)
	require.Equal(t, [][]string{{"1", "2"}}, ids(p.Partitions))
	assert.Equal(t, a, p.Partitions[0][1].StackBase, "open, at the head beneath its update")

	landed := newWorld(t, map[string]string{"f": "f\n"})
	a = landed.commit(landed.base, "a", map[string]*string{"a": str("a")})
	landed.push(landed.commit(landed.base, "main", map[string]*string{"m": str("m")}))
	updated = landed.m.mergeCommit(a, landed.main(), tree{"f": "f\n", "a": "a", "m": "m"}, "update branch")
	squashed := landed.commit(landed.main(), "a (#1)", map[string]*string{"a": str("a")})
	landed.push(squashed)
	landed.p.landed = []Landed{{ID: "1", Head: updated, Commit: squashed, Method: MethodSquash}}
	landed.open("2", landed.commit(a, "b", map[string]*string{"b": str("b")}))
	two := landed.plan(1).Partitions[0][0]
	assert.Equal(t, a, two.StackBase, "landed through its update commit")
}

func TestAStackThatCannotLandIsRefusedOrHeld(t *testing.T) {
	w := newWorld(t, map[string]string{"f": "f\n"})
	ha := w.commit(w.base, "a", map[string]*string{"a": str("a")})
	hx := w.commit(w.base, "x", map[string]*string{"x": str("x")})
	w.open("1", ha)
	w.open("9", hx)
	w.open("2", w.commit(ha, "b", map[string]*string{"b": str("b")}), func(c *Change) { c.Method = MethodMerge })
	w.open("3", w.commit(ha, "c", map[string]*string{"c": str("c")}), func(c *Change) { c.Method, c.Parent = MethodRebase, "" })
	w.open("4", w.commit(ha, "d", map[string]*string{"d": str("d")}), func(c *Change) { c.Parent = "9" })
	w.open("5", w.commit(ha, "e", map[string]*string{"e": str("e")}), func(c *Change) { c.Parent = "77" })
	both := w.m.mergeCommit(ha, hx, tree{"f": "f\n", "a": "a", "x": "x"}, "both")
	w.open("6", w.commit(both, "g", map[string]*string{"g": str("g")}))
	w.open("7", w.commit(w.base, "h", map[string]*string{"h": str("h")}), func(c *Change) { c.Parent = "9" })
	v := verdictsByID(w.plan(2))
	assert.Equal(t, CodeRefused, v["2"].Code)
	assert.Equal(t, "the stack mixes squash (#1) and merge (#2); a stack lands with one merge method", v["2"].Reason)
	assert.Equal(t, CodeRefused, v["3"].Code)
	assert.Contains(t, v["3"].Reason, "cannot land with the rebase method yet")
	assert.Equal(t, CodeRestack, v["4"].Code, "declared on #9, built on #1")
	assert.Equal(t, CodeParent, v["5"].Code)
	assert.Equal(t, "stacked on #77, which is not queued", v["5"].Reason)
	assert.Equal(t, CodeRefused, v["6"].Code)
	assert.Contains(t, v["6"].Reason, "which are not stacked on each other")
	assert.Equal(t, CodeRestack, v["7"].Code, "declared on #9, built on nothing queued")
}

func TestAChangeStackedOnOnePlanningHeldWaitsWithoutBlame(t *testing.T) {
	w := newWorld(t, map[string]string{"f": "f\n"})
	w.push(w.commit(w.base, "main", map[string]*string{"f": str("main\n")}))
	kicked := w.commit(w.base, "a", map[string]*string{"f": str("a\n")})
	unapproved := w.commit(w.base, "x", map[string]*string{"x": str("x")})
	w.open("1", kicked)
	w.open("2", w.commit(kicked, "b", map[string]*string{"b": str("b")}))
	w.open("3", w.commit(w.m.ref("refs/pull/2/head"), "c", map[string]*string{"c": str("c")}))
	w.open("4", unapproved)
	w.open("5", w.commit(unapproved, "y", map[string]*string{"y": str("y")}))
	w.p.approvedAt["4"] = w.base
	v := verdictsByID(w.plan(2))
	assert.Equal(t, CodeConflict, v["1"].Code)
	assert.Equal(t, CodeParentKicked, v["2"].Code)
	assert.Equal(t, CodeParentKicked, v["3"].Code, "and what is stacked on that")
	assert.Equal(t, CodeNotApproved, v["4"].Code)
	assert.Equal(t, CodeParent, v["5"].Code)
	assert.Contains(t, v["5"].Reason, "stacked on #4, which is waiting: not approved")
}

func TestAChangeStackedOnARedOneWaitsWithoutBlame(t *testing.T) {
	w := newWorld(t, map[string]string{"f": "f\n"})
	ha := w.commit(w.base, "a", map[string]*string{"a": str("a")})
	hb := w.commit(ha, "b", map[string]*string{"b": str("b")})
	w.open("1", ha)
	w.open("2", hb)
	w.open("3", w.commit(hb, "c", map[string]*string{"c": str("c")}))
	w.open("4", w.commit(w.base, "d", map[string]*string{"d": str("d")}))
	gate := newGate()
	gate.bad["1"] = true
	got := w.validate(w.plan(3), gate)
	assert.Equal(t, map[string]Decision{"1": DecisionKick, "2": DecisionWait, "3": DecisionWait, "4": DecisionMerge}, got.decisions())
	assert.Equal(t, CodeParentKicked, got.get(t, "2").Code)
	assert.Equal(t, CodeParentKicked, got.get(t, "3").Code)
	assert.Equal(t, "#1 was kicked back; this stays queued and is validated again once it returns", got.get(t, "3").Reason,
		"the kick-back further down stays the reason")
	assert.Equal(t, "", got.get(t, "4").After, "the rest restack onto what validated")
}

func TestALandedChangeTheBaseDoesNotCarryIsAnError(t *testing.T) {
	w := newWorld(t, map[string]string{"f": "f\n"})
	elsewhere := w.commit(w.base, "elsewhere", map[string]*string{"x": str("x")})
	w.p.landed = []Landed{{ID: "9", Head: elsewhere, Commit: elsewhere, Method: MethodSquash}}
	_, err := w.planner(1).Run(context.Background(), w.changes())
	require.ErrorContains(t, err, "the provider lists #9 as landed at "+short(elsewhere)+", which main does not carry")
}

// A provider that lands stacks atomically lands a validated run of them in one call
// through its top, and the queue checks every member's tree after.
func TestAnAtomicProviderLandsAStackRunInOneCall(t *testing.T) {
	w := newWorld(t, map[string]string{"f": "f\n"})
	w.p.caps.StackMerge = StackMergeAtomic
	ha := w.commit(w.base, "a", map[string]*string{"a": str("a")})
	hb := w.commit(ha, "b", map[string]*string{"b": str("b")})
	w.open("1", ha)
	w.open("2", hb, func(c *Change) { c.Parent = "1" })
	var calls int
	w.p.afterMerge = func() { calls++ }
	_, evs, err := w.runQueue(2, newGate())
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "one provider call")
	assert.Equal(t, map[string]EventKind{"1": EventMerged, "2": EventMerged}, outcomes(evs))
	assert.Equal(t, tree{"f": "f\n", "a": "a", "b": "b"}, w.m.files(w.main()))
}

// The provider lands the bottom of a stack run from its natural merge base. When the
// change beneath the bottom landed as a squash, that brings back what the bottom
// deleted from it, so the run lands one change at a time, the bottom through an update
// commit. Found by TestStacksKeepTheirInvariants.
func TestAStackRunWhoseBottomSitsOnASquashedChangeLandsOneAtATime(t *testing.T) {
	w, _, b := stacked(t)
	w.p.caps.StackMerge = StackMergeAtomic
	p := w.plan(1)
	p.Partitions[0] = p.Partitions[0][:1]
	_, err := w.apply(p, once(w.validate(p, newGate()).get(t, "1")))
	require.NoError(t, err)
	w.p.mu.Lock()
	w.p.open["2"].Parent = ""
	w.p.mu.Unlock()
	w.open("3", w.commit(b.Head, "h", map[string]*string{"h": str("h\n")}), func(c *Change) { c.Parent = "2" })
	var calls int
	w.p.afterMerge = func() { calls++ }
	_, evs, err := w.runQueue(2, newGate())
	require.NoError(t, err)
	assert.Equal(t, map[string]EventKind{"2": EventMerged, "3": EventMerged}, outcomes(evs))
	assert.Equal(t, 2, calls, "one at a time")
	assert.Equal(t, tree{"f": withoutL, "g": "g\n", "h": "h\n"}, w.m.files(w.main()))
}

func TestAnAtomicStackThatLandsPartwayStopsApplying(t *testing.T) {
	w := newWorld(t, map[string]string{"f": "f\n"})
	w.p.caps.StackMerge = StackMergeAtomic
	ha := w.commit(w.base, "a", map[string]*string{"a": str("a")})
	w.open("1", ha)
	w.open("2", w.commit(ha, "b", map[string]*string{"b": str("b")}), func(c *Change) { c.Parent = "1" })
	w.p.failAfter["2"] = 1
	_, evs, err := w.runQueue(2, newGate())
	require.ErrorContains(t, err, "landed 1 of 2 changes; stopping")
	assert.Equal(t, EventMerged, last(evs, "1").Kind)
	assert.Equal(t, tree{"f": "f\n", "a": "a"}, w.m.files(w.main()))
}

// An approval carries over a rebase that only moved the change: its whole diff,
// replayed onto the new base, is exactly the new head.
func TestAnApprovalCarriesOverATrivialRebaseAndNothingElse(t *testing.T) {
	w := newWorld(t, map[string]string{"f": "f\n"})
	old := w.commit(w.base, "a", map[string]*string{"a": str("a")})
	moved := w.commit(w.base, "main", map[string]*string{"m": str("m")})
	w.push(moved)
	rebased := w.commit(moved, "a", map[string]*string{"a": str("a")})
	edited := w.commit(moved, "a", map[string]*string{"a": str("a, edited")})
	w.open("1", rebased)
	w.open("2", edited, func(c *Change) { c.Affected = []string{"b"} })
	w.p.approvedAt["1"], w.p.approvedAt["2"] = old, old
	var buf bytes.Buffer
	pl := w.planner(1)
	pl.Events = NewEvents(&buf)
	p, err := pl.Run(context.Background(), w.changes())
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"1"}}, ids(p.Partitions))
	assert.Equal(t, CodeNotApproved, verdictsByID(p)["2"].Code, "a rebase that changed the diff needs a new approval")
	assert.Contains(t, buf.String(), "is "+short(old)+" rebased with its diff unchanged; its approval carried over")
}

// generatedWorld is main with a source, an unrelated file and an INDEX generated from
// both, and a change to the source whose author then merged a moved main in.
func generatedWorld(t *testing.T, mergedIndex func(tree) string) (w *world, own, head string) {
	w = newWorld(t, map[string]string{"s": "s0", "o": "o0", generatedFile: "INDEX\n"})
	start := tree{"s": "s0", "o": "o0"}
	w.push(w.commit(w.base, "index", map[string]*string{"INDEX": str(index(start))}))
	from := w.main()
	own = w.commit(from, "change s", map[string]*string{"s": str("s1"), "INDEX": str(index(tree{"s": "s1", "o": "o0"}))})
	w.push(w.commit(from, "change o", map[string]*string{"o": str("o1"), "INDEX": str(index(tree{"s": "s0", "o": "o1"}))}))
	merged := tree{"s": "s1", "o": "o1", generatedFile: "INDEX\n"}
	merged["INDEX"] = mergedIndex(merged)
	head = w.m.mergeCommit(own, w.main(), merged, "merge main")
	return w, own, head
}

// A merge of the base into a change that differs from the plain merge only in generated
// files adds nothing a reviewer did not see only when regeneration reproduces them:
// the review of the commit beneath it covers the merge then, and only then.
func TestAReviewCoversAMergeOfTheBaseOnlyWhenRegenerationReproducesIt(t *testing.T) {
	w, own, h := generatedWorld(t, index)
	w.open("1", h)
	w.p.approvedAt["1"] = own
	regen := func(v *Validator) { v.Regenerate = w.regenerateIndex }
	p := w.plan(1)
	require.Equal(t, [][]string{{"1"}}, ids(p.Partitions), "approved at the commit beneath the merge")
	got := w.validate(p, newGate(), regen)
	assert.Equal(t, own, got.get(t, "1").Reviewed)
	evs, err := w.apply(p, got)
	require.NoError(t, err)
	assert.Equal(t, EventMerged, last(evs, "1").Kind)

	// Without a hook to prove it, the merge is unreviewed.
	w, own, h = generatedWorld(t, index)
	w.open("1", h)
	w.p.approvedAt["1"] = own
	unproven := w.validate(w.plan(1), newGate()).get(t, "1")
	assert.Equal(t, CodeNotApproved, unproven.Code)
	assert.Contains(t, unproven.Reason, "needs a regenerate hook")
}

// A file marked generated that regeneration does not produce, such as a vendored tree,
// gets no exemption: the mark alone would let it ride in unreviewed.
func TestAGeneratedFileRegenerationDoesNotReproduceNeedsItsOwnReview(t *testing.T) {
	w, own, h := generatedWorld(t, func(tree) string { return "vendored, edited by hand" })
	w.open("1", h)
	w.p.approvedAt["1"] = own
	got := w.validate(w.plan(1), newGate(), func(v *Validator) { v.Regenerate = w.regenerateIndex })
	one := got.get(t, "1")
	assert.Equal(t, CodeNotApproved, one.Code)
	assert.Equal(t, "not approved at "+short(h)+": INDEX are marked generated, but regeneration does not reproduce them, so no review covers them", one.Reason)
}
