package mergequeue

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// polls hands out verdicts in the batches a validation run would publish them: each
// Poll returns the next batch, and the last batch comes with done.
type polls struct {
	batches [][]Verdict
	n       int
}

func (p *polls) Poll(context.Context) ([]Verdict, bool, error) {
	var fresh []Verdict
	if p.n < len(p.batches) {
		fresh = p.batches[p.n]
		p.n++
	}
	return fresh, p.n == len(p.batches), nil
}

func once(vs ...Verdict) *polls { return &polls{batches: [][]Verdict{vs}} }

// runQueue plans, validates and applies what is listed, the way one queue run does.
func (w *world) runQueue(depth int, gate *fakeGate, configure ...func(*Validator)) (Plan, []Event, error) {
	w.t.Helper()
	p := w.plan(depth)
	got := w.validate(p, gate, configure...)
	evs, err := w.apply(p, got)
	return p, evs, err
}

func TestEveryChangeMergesAsItsOwnCommitInQueueOrder(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 3)
	_, evs, err := w.runQueue(3, newGate())
	require.NoError(t, err)
	assert.Equal(t, map[string]EventKind{"1": EventMerged, "2": EventMerged, "3": EventMerged}, outcomes(evs))
	assert.Len(t, w.p.merges, 3)
	assert.Equal(t, []string{"1", "2", "3"}, []string{w.p.landed[0].ID, w.p.landed[1].ID, w.p.landed[2].ID})
	assert.Equal(t, tree{"a": "a\n", "f1": "1\n", "f2": "2\n", "f3": "3\n"}, w.m.files(w.main()))
	c, err := w.m.FindCommit(context.Background(), w.main())
	require.NoError(t, err)
	assert.Equal(t, "change 3 (#3)", c.Subject, "each change lands as its own commit")
}

// A success posted before the merge satisfies branch protection on its own: a crash in
// between would leave a change anyone could merge onto whatever the base had become.
func TestSuccessIsPostedOnlyOnceTheChangeMerged(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	cs := stackOfEdits(w, 2)
	w.p.refuse["2"] = true
	_, _, err := w.runQueue(2, newGate())
	require.NoError(t, err)
	type state struct {
		id, commit string
		state      CommitState
	}
	var got []state
	for _, s := range w.p.statuses {
		got = append(got, state{s.id, s.commit, s.state})
	}
	assert.Equal(t, []state{
		{"1", cs[0].Head, StatePending}, {"1", cs[0].Head, StateSuccess},
		{"2", cs[1].Head, StatePending}, {"2", cs[1].Head, StatePending},
	}, got, "pending while the merge is asked for; a refused merge never reads success")
	assert.Equal(t, "waiting: the host refused the merge: 405 not mergeable", w.p.statuses[3].desc)
}

// A green candidate merges while candidates above it and other partitions are still
// validating, and a verdict that arrives before the one beneath it waits for it.
func TestEachCandidateMergesTheMomentItAndEverythingBeneathItAreGreen(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 2)
	w.open("7", w.commit(w.base, "seven", map[string]*string{"g": str("7")}), func(c *Change) { c.Affected = []string{"b"} })
	p := w.plan(3)
	got := w.validate(p, newGate())
	src := &polls{batches: [][]Verdict{{got.get(t, "2")}, {got.get(t, "7")}, {got.get(t, "1")}}}
	var order []string
	w.p.afterMerge = func() { order = append(order, w.p.merges[len(w.p.merges)-1][:1]+"@poll"+string(rune('0'+src.n))) }
	var buf bytes.Buffer
	a := NewApplier(w.p, w.m, src)
	a.Interval, a.Events = 1, NewEvents(&buf)
	require.NoError(t, a.Run(context.Background(), p))
	assert.Equal(t, []string{"7@poll2", "1@poll3", "2@poll3"}, order, "#7 merges while #1 is still validating; #2, green first, waits for #1")
}

func TestAChangeValidatedOnOneThatDidNotMergeWaits(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 2)
	p := w.plan(2)
	got := w.validate(p, newGate())
	w.p.approvedAt["1"] = w.base
	evs, err := w.apply(p, got)
	require.NoError(t, err)
	assert.Empty(t, w.p.merges)
	assert.Equal(t, CodeNotApproved, last(evs, "1").Code)
	two := last(evs, "2")
	assert.Equal(t, CodeBehind, two.Code)
	assert.Equal(t, "validated on top of #1, which did not merge", two.Reason)
}

// The plan says which head was admitted and what lies beneath each change; a verdict
// file saying otherwise merges nothing.
func TestAVerdictThatDisagreesWithThePlanMergesNothing(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.open("1", w.commit(w.base, "one", map[string]*string{"x": str("1")}))
	w.open("2", w.commit(w.base, "two", map[string]*string{"y": str("2")}), func(c *Change) { c.Affected = []string{"b"} })
	w.open("3", w.commit(w.base, "three", map[string]*string{"z": str("3")}), func(c *Change) { c.Affected = []string{"c"} })
	p := w.plan(3)
	got := w.validate(p, newGate())
	tampered := got.get(t, "1")
	tampered.Change.Head = head("other")
	skipped := got.get(t, "3")
	skipped.After = "9"
	evs, err := w.apply(p, once(tampered, got.get(t, "2"), skipped))
	require.NoError(t, err)
	assert.Equal(t, []string{"2@" + p.Partitions[1][0].Head}, w.p.merges)
	assert.Equal(t, "validated at "+short(head("other"))+", not the planned head "+short(p.Partitions[0][0].Head), last(evs, "1").Reason)
	assert.Equal(t, "validated on top of #9, which is not beneath it in its partition", last(evs, "3").Reason)

	_, err = w.apply(p, once(Verdict{Change: change("5", "a"), Decision: DecisionMerge}))
	require.EqualError(t, err, "a verdict names #5, which the plan did not admit")
}

func TestPlanVerdictsAndRedVerdictsReachTheProvider(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.push(w.commit(w.base, "main", map[string]*string{"a": str("main\n")}))
	w.open("1", w.commit(w.base, "one", map[string]*string{"a": str("1\n")}))
	w.open("3", w.commit(w.base, "three", map[string]*string{"c": str("3\n")}))
	gate := newGate()
	gate.bad["3"] = true
	_, _, err := w.runQueue(1, gate)
	require.NoError(t, err)
	assert.Equal(t, Kick{Code: CodeConflict, Report: w.p.kicked["1"].Report, Paths: []string{"a"},
		With: []string{short(w.main()) + " main"}}, w.p.kicked["1"], "a kick-back carries the facts it was rendered from")
	assert.Equal(t, CodeRed, w.p.kicked["3"].Code)
	assert.Contains(t, w.p.kicked["3"].Report, "tests failed in 3")
	assert.NotEmpty(t, w.p.kicked["3"].Candidate)
}

func TestWhatNoVerdictReachedWaitsForTheNextRun(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 2)
	p := w.plan(2)
	got := w.validate(p, newGate())
	evs, err := w.apply(p, once(got.get(t, "1")))
	require.NoError(t, err)
	assert.Len(t, w.p.merges, 1)
	assert.Equal(t, Event{Kind: EventWaiting, Change: "2", Code: CodeBehind, Reason: "not validated in this run"},
		Event{Kind: last(evs, "2").Kind, Change: "2", Code: last(evs, "2").Code, Reason: last(evs, "2").Reason})
}

func TestAVerdictOnAnotherBaseWaits(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 1)
	p := w.plan(1)
	v := w.validate(p, newGate()).get(t, "1")
	v.BaseCommit = head("elsewhere")
	evs, err := w.apply(p, once(v))
	require.NoError(t, err)
	assert.Empty(t, w.p.merges)
	assert.Equal(t, CodeRevalidate, last(evs, "1").Code)
}

// Two disjoint partitions both regenerate a root index: whichever lands second was
// validated against an index nobody validated alongside it, so it waits.
func TestACandidateSharingAFileWithWhatMergedIsValidatedAgain(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n", generatedFile: "INDEX\n", "INDEX": ""})
	w.open("1", w.commit(w.base, "one", map[string]*string{"x": str("1"), "INDEX": str("1")}))
	w.open("2", w.commit(w.base, "two", map[string]*string{"y": str("2"), "INDEX": str("2")}), func(c *Change) { c.Affected = []string{"b"} })
	_, evs, err := w.runQueue(1, newGate(), func(v *Validator) { v.Regenerate = w.regenerateIndex })
	require.NoError(t, err)
	assert.Len(t, w.p.merges, 1)
	waited := last(evs, "2")
	assert.Equal(t, CodeRevalidate, waited.Code)
	assert.Equal(t, "INDEX changed both here and in what merged since validation; validated again next run", waited.Reason)
}

// Two changes in one partition both regenerate INDEX: the second's candidate holds the
// index of both, which its own merge would not, so it lands through an update commit
// on its branch, authored by its author and committed by the queue.
func TestAnUpdateCommitIsMergedAtItsOwnCommit(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n", generatedFile: "INDEX\n", "INDEX": ""})
	w.open("1", w.commit(w.base, "one", map[string]*string{"x": str("1"), "INDEX": str("1")}))
	two := w.open("2", w.commit(w.base, "two", map[string]*string{"y": str("2"), "INDEX": str("2")}))
	p, evs, err := w.runQueue(2, newGate(), func(v *Validator) { v.Regenerate = w.regenerateIndex })
	require.NoError(t, err)
	assert.Equal(t, map[string]EventKind{"1": EventMerged, "2": EventMerged}, outcomes(evs))
	update := w.m.ref(branchRef("pr2"))
	require.NotEqual(t, two.Head, update)
	c, err := w.m.FindCommit(context.Background(), update)
	require.NoError(t, err)
	assert.Equal(t, []string{two.Head, w.p.landed[0].Commit}, c.Parents, "merge-shaped, the head first")
	assert.Equal(t, "author", c.Author.Name, "the head's author")
	assert.Equal(t, "2@"+update, w.p.merges[1])
	assert.Equal(t, index(tree{"a": "a\n", "x": "1", "y": "2"}), w.m.files(w.main())["INDEX"])
	assert.Equal(t, p.Partitions[0][1].ID, "2")
}

// stalePush is a VCS whose every push finds the branch moved.
type stalePush struct{ *model }

func (stalePush) Push(context.Context, PushLease) error { return ErrStaleLease }

func TestAnUpdateWithNowhereToGoIsKickedBackAndAMovedBranchWaits(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n", generatedFile: "INDEX\n", "INDEX": ""})
	w.open("1", w.commit(w.base, "one", map[string]*string{"x": str("1")}))
	w.open("2", w.commit(w.base, "two", map[string]*string{"y": str("2"), "INDEX": str("2")}), func(c *Change) { c.Branch = "" })
	w.open("3", w.commit(w.base, "three", map[string]*string{"z": str("3"), "INDEX": str("3")}))
	p := w.plan(3)
	got := w.validate(p, newGate(), func(v *Validator) { v.Regenerate = w.regenerateIndex })
	var buf bytes.Buffer
	a := NewApplier(w.p, stalePush{w.m}, got)
	a.Interval, a.Events = 1, NewEvents(&buf)
	require.NoError(t, a.Run(context.Background(), p))
	evs := readEvents(t, &buf)
	assert.Equal(t, CodeRefused, w.p.kicked["2"].Code)
	assert.Contains(t, w.p.kicked["2"].Report, "cannot push to its branch")
	assert.Equal(t, CodeBehind, last(evs, "3").Code, "#3 was validated on #2")
}

func TestAMovedBranchWaits(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n", generatedFile: "INDEX\n", "INDEX": ""})
	w.open("1", w.commit(w.base, "one", map[string]*string{"x": str("1")}))
	w.open("2", w.commit(w.base, "two", map[string]*string{"y": str("2"), "INDEX": str("2")}))
	p := w.plan(2)
	got := w.validate(p, newGate(), func(v *Validator) { v.Regenerate = w.regenerateIndex })
	var buf bytes.Buffer
	a := NewApplier(w.p, stalePush{w.m}, got)
	a.Interval, a.Events = 1, NewEvents(&buf)
	require.NoError(t, a.Run(context.Background(), p))
	two := last(readEvents(t, &buf), "2")
	assert.Equal(t, CodeBranchMoved, two.Code)
	assert.Equal(t, "its branch moved or was deleted since validation", two.Reason)
}

func TestAHeadPushedAfterValidationIsNotMerged(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	cs := stackOfEdits(w, 1)
	p := w.plan(1)
	got := w.validate(p, newGate())
	moved := w.commit(cs[0].Head, "more", map[string]*string{"f1": str("11\n")})
	w.m.set(branchRef("pr1"), moved)
	evs, err := w.apply(p, got)
	require.NoError(t, err)
	assert.Empty(t, w.p.merges)
	assert.Equal(t, CodeHeadMoved, last(evs, "1").Code)
	assert.Equal(t, []posted{{"1", moved, StatePending, "waiting: head moved to " + short(moved) + " after validation; retried next run"}}, w.p.statuses,
		"pending on the new head; nothing on a commit that is no longer the head")
}

func TestAProviderThatReportsNoHeadStopsApplying(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 1)
	p := w.plan(1)
	got := w.validate(p, newGate())
	a := NewApplier(noHead{w.p}, w.m, got)
	require.EqualError(t, a.Run(context.Background(), p), "approval of #1 (change 1): the provider reported no head")
	assert.Empty(t, w.p.merges)
}

func TestAHostRefusalWaitsAndHoldsWhatStacksOnIt(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 2)
	w.p.refuse["1"] = true
	_, evs, err := w.runQueue(2, newGate())
	require.NoError(t, err)
	assert.Equal(t, CodeHostRefused, last(evs, "1").Code)
	assert.Equal(t, CodeBehind, last(evs, "2").Code)
}

func TestABaseThatDoesNotCarryTheValidatedTreeStopsApplying(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 2)
	w.p.afterMerge = func() {
		w.m.mu.Lock()
		tip := w.m.refs["refs/heads/main"]
		w.m.mu.Unlock()
		w.push(w.commit(tip, "intruder", map[string]*string{"z": str("z")}))
	}
	_, _, err := w.runQueue(2, newGate())
	require.ErrorContains(t, err, "not the validated")
	assert.Len(t, w.p.merges, 1, "nothing merges on an unvalidated base")
}

// liar squashes whatever method it is asked for.
type liar struct{ *modelProvider }

func (l liar) MergeChange(ctx context.Context, c Change, m MergeRequest) error {
	l.mu.Lock()
	l.open[c.ID].Method = MethodSquash
	l.mu.Unlock()
	return l.modelProvider.MergeChange(ctx, c, m)
}

// The history a merge method leaves is part of what was validated: a merge commit
// keeps the change's commits for bisect, a squash does not.
func TestAProviderThatLandsInAnotherShapeStopsApplying(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.open("1", w.commit(w.base, "one", map[string]*string{"x": str("1")}), func(c *Change) { c.Method = MethodMerge })
	p := w.plan(1)
	got := w.validate(p, newGate())
	err := NewApplier(liar{w.p}, w.m, got).Run(context.Background(), p)
	require.ErrorContains(t, err, "is not what a merge merge onto")
}

func TestAMethodChangedSinceValidationWaits(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 1)
	p := w.plan(1)
	got := w.validate(p, newGate())
	w.p.open["1"].Method = MethodRebase
	evs, err := w.apply(p, got)
	require.NoError(t, err)
	assert.Equal(t, CodeMethodChanged, last(evs, "1").Code)
	assert.Equal(t, "validated as squash, now rebase; validated again next run", last(evs, "1").Reason)
}

// The provider merges a change into its own base, so one pointed elsewhere is retargeted
// at the queue's base before it merges.
func TestAChangeTargetingAnotherBranchIsRetargeted(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 1)
	p := w.plan(1)
	got := w.validate(p, newGate())
	w.p.open["1"].Base = "feat"
	evs, err := w.apply(p, got)
	require.NoError(t, err)
	assert.Equal(t, EventMerged, last(evs, "1").Kind)
	assert.Equal(t, []string{"1->main"}, w.p.retargets)
}

func TestEveryMergeMethodLandsTheValidatedTreeInItsOwnShape(t *testing.T) {
	for _, m := range []MergeMethod{MethodMerge, MethodSquash, MethodRebase} {
		t.Run(string(m), func(t *testing.T) {
			w := newWorld(t, map[string]string{"a": "a\n"})
			first := w.commit(w.base, "one", map[string]*string{"x": str("1")})
			w.open("1", w.commit(first, "one more", map[string]*string{"y": str("1")}), func(c *Change) { c.Method = m })
			w.push(w.commit(w.base, "elsewhere", map[string]*string{"z": str("z")}))
			_, evs, err := w.runQueue(1, newGate())
			require.NoError(t, err)
			assert.Equal(t, EventMerged, last(evs, "1").Kind)
			assert.Equal(t, tree{"a": "a\n", "x": "1", "y": "1", "z": "z"}, w.m.files(w.main()))
		})
	}
}

func TestApplyDryRunCallsNothing(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 2)
	gate := newGate()
	gate.bad["2"] = true
	p := w.plan(2)
	got := w.validate(p, gate)
	var buf bytes.Buffer
	a := NewApplier(w.p, w.m, got)
	a.DryRun, a.Events = true, NewEvents(&buf)
	require.NoError(t, a.Run(context.Background(), p))
	assert.Equal(t, map[string]EventKind{"1": EventMerged, "2": EventKicked}, outcomes(readEvents(t, &buf)))
	assert.Empty(t, w.p.statuses)
	assert.Empty(t, w.p.merges)
}

func TestAnApplierMissingAPartIsAnError(t *testing.T) {
	p := Plan{Base: "main", BaseCommit: base, Depth: 1}
	require.EqualError(t, (&Applier{}).Run(context.Background(), p), "an Applier needs a Provider, a VCS and a VerdictSource; build it with NewApplier")
	a := NewApplier(newModelProvider(newModel()), newModel(), once())
	a.Committer = Person{Name: "bot"}
	require.ErrorContains(t, a.Run(context.Background(), p), "needs both a name and an email")
}
