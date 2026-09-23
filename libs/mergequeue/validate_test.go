package mergequeue

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func only(id string) func(*Validator) { return func(v *Validator) { v.Only = id } }

// stackOfEdits opens n changes, each editing its own file off main, all affecting "a".
func stackOfEdits(w *world, n int) []Change {
	var out []Change
	for i := 1; i <= n; i++ {
		id := string(rune('0' + i))
		out = append(out, w.open(id, w.commit(w.base, "change "+id, map[string]*string{"f" + id: str(id + "\n")})))
	}
	return out
}

func TestSpeculativeCandidatesStackAndValidateInParallel(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 3)
	p := w.plan(3)
	gate := newGate()
	got := w.validate(p, gate)
	assert.Equal(t, map[string]Decision{"1": DecisionMerge, "2": DecisionMerge, "3": DecisionMerge}, got.decisions())
	assert.EqualValues(t, 3, gate.peak.Load(), "all three candidates gate at once")
	one, two, three := got.get(t, "1"), got.get(t, "2"), got.get(t, "3")
	assert.Equal(t, Verdict{BaseCommit: w.base, Change: p.Partitions[0][0], Decision: DecisionMerge, Onto: w.base,
		Candidate: one.Candidate, Method: MethodSquash, Message: "* change 1", Depth: 1, DurationMS: one.DurationMS}, one)
	assert.Equal(t, tree{"a": "a\n", "f1": "1\n"}, w.m.files(one.Candidate))
	assert.Equal(t, []string{"1", one.Candidate}, []string{two.After, two.Onto})
	assert.Equal(t, tree{"a": "a\n", "f1": "1\n", "f2": "2\n", "f3": "3\n"}, w.m.files(three.Candidate))
	assert.Equal(t, []int{2, 3}, []int{two.Depth, three.Depth})
	assert.Equal(t, []string{"1", "2", "3"}, idsOf(got.all), "verdicts are published in queue order, each as soon as it is decided")
}

func TestAFailedCandidateRespeculatesOnlyWhatWasBehindIt(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 4)
	gate := newGate()
	gate.bad["2"] = true
	got := w.validate(w.plan(2), gate)
	assert.Equal(t, map[string]Decision{"1": DecisionMerge, "2": DecisionKick, "3": DecisionMerge, "4": DecisionMerge}, got.decisions())
	two := got.get(t, "2")
	assert.Equal(t, CodeRed, two.Code)
	assert.Contains(t, two.Report, "tests failed in 2")
	three := got.get(t, "3")
	assert.Equal(t, "1", three.After, "#3, first built onto the red #2, is rebuilt on #1 alone")
	assert.Equal(t, tree{"a": "a\n", "f1": "1\n", "f3": "3\n"}, w.m.files(three.Candidate))
	assert.ElementsMatch(t, []string{"1", "2", "3", "3", "4"}, gate.gated, "speculated #3 on #2 before it failed")
}

// A regeneration the change's own code broke is that change's verdict, not the run's
// end.
func TestARefusedCandidateIsKickedBackAndTheRestMerge(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n", generatedFile: "INDEX\n", "INDEX": ""})
	for _, id := range []string{"1", "2", "3"} {
		w.open(id, w.commit(w.base, "change "+id, map[string]*string{"f" + id: str(id + "\n"), "INDEX": str(id)}))
	}
	regen := func(ctx context.Context, dir, onto string, c Change, paths []string) error {
		if c.ID == "2" {
			return &RefusedError{Reason: "`gen` exited 1 regenerating INDEX", Paths: paths}
		}
		return w.regenerateIndex(ctx, dir, onto, c, paths)
	}
	gate := newGate()
	got := w.validate(w.plan(3), gate, func(v *Validator) { v.Regenerate = regen })
	assert.Equal(t, map[string]Decision{"1": DecisionMerge, "2": DecisionKick, "3": DecisionMerge}, got.decisions())
	two := got.get(t, "2")
	assert.Equal(t, CodeRefused, two.Code)
	assert.Equal(t, []string{"INDEX"}, two.Paths)
	assert.Contains(t, two.Report, "`gen` exited 1")
	three := got.get(t, "3")
	assert.Equal(t, "1", three.After, "#3 is built onto what did build")
	assert.Equal(t, index(tree{"a": "a\n", "f1": "1\n", "f3": "3\n"}), w.m.files(three.Candidate)["INDEX"],
		"a generated file both sides changed is regenerated, not merged")
	assert.NotContains(t, gate.gated, "2", "nothing gates a candidate that was never built")
}

func TestRegenerationWritingASourceIsRefused(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n", generatedFile: "INDEX\n", "INDEX": ""})
	w.open("1", w.commit(w.base, "one", map[string]*string{"INDEX": str("1")}))
	sneaky := func(_ context.Context, dir, _ string, _ Change, _ []string) error {
		w.m.write(dir, "a", "rewritten\n")
		return nil
	}
	got := w.validate(w.plan(1), newGate(), func(v *Validator) { v.Regenerate = sneaky })
	one := got.get(t, "1")
	assert.Equal(t, CodeRefused, one.Code)
	assert.Equal(t, "building its candidate failed: regeneration wrote files that are not generated: a", one.Reason)
	assert.Equal(t, []string{"a"}, one.Paths)
}

// Only mode builds the changes beneath without gating them, so a red on top of them
// cannot be pinned on the top change.
func TestOnlyWaitsRatherThanBlameTheTopForWhatItWasBuiltOn(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	stackOfEdits(w, 2)
	gate := newGate()
	gate.bad["2"] = true
	p := w.plan(2)
	two := w.validate(p, gate, only("2")).get(t, "2")
	assert.Equal(t, DecisionWait, two.Decision)
	assert.Equal(t, CodeBehind, two.Code)
	assert.Equal(t, "the gate failed on top of #1, which this run did not validate; retried once it is", two.Reason)

	p.Partitions[0] = p.Partitions[0][1:]
	assert.Equal(t, DecisionKick, w.validate(p, gate, only("2")).get(t, "2").Decision, "at the bottom, the red is its own")
}

// Twenty disjoint partitions at depth 3 would start sixty builds at once without a cap
// across partitions.
func TestParallelCapsCandidatesAcrossPartitions(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	for _, unit := range []string{"a", "b", "c", "d"} {
		for _, n := range []string{"1", "2", "3"} {
			id := unit + n
			w.open(id, w.commit(w.base, id, map[string]*string{id: str(id)}), func(c *Change) { c.Affected = []string{unit} })
		}
	}
	gate := newGate()
	got := w.validate(w.plan(3), gate, func(v *Validator) { v.Parallel = 2 })
	assert.Len(t, got.all, 12)
	for id, d := range got.decisions() {
		assert.Equal(t, DecisionMerge, d, id)
	}
	assert.LessOrEqual(t, gate.peak.Load(), int32(2))
}

func TestAMachineFailureStopsItsPartitionAlone(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.open("1", w.commit(w.base, "one", map[string]*string{"x": str("1")}))
	w.open("2", w.commit(w.base, "two", map[string]*string{"y": str("2")}), func(c *Change) { c.Affected = []string{"b"} })
	gate := newGate()
	gate.broken["1"] = true
	got := &collected{}
	v := NewValidator(w.m, gate, got)
	v.Scratch, v.Parallel = t.TempDir(), 2
	err := v.Run(context.Background(), w.plan(1))
	require.ErrorContains(t, err, "gate #1 (change 1): the runner ran out of memory")
	assert.Equal(t, map[string]Decision{"2": DecisionMerge}, got.decisions(), "no verdict on #1, and #2 is still decided")
}

func TestDisjointPartitionsNeverWaitForEachOther(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.open("1", w.commit(w.base, "one", map[string]*string{"x": str("1")}))
	w.open("2", w.commit(w.base, "two", map[string]*string{"y": str("2")}), func(c *Change) { c.Affected = []string{"b"} })
	gate := newGate()
	// #1's gate cannot finish until #2's has: serial partitions would deadlock here.
	gate.done = map[string]chan struct{}{"2": make(chan struct{})}
	gate.wait = map[string]chan struct{}{"1": gate.done["2"]}
	got := w.validate(w.plan(1), gate, func(v *Validator) { v.Parallel = 2 })
	assert.Equal(t, map[string]Decision{"1": DecisionMerge, "2": DecisionMerge}, got.decisions())
	assert.Equal(t, w.base, got.get(t, "2").Onto, "each partition builds on the base alone")
}

func TestAChangeConflictingWithOneAheadWaitsAndTheRestStackPastIt(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.open("1", w.commit(w.base, "one", map[string]*string{"a": str("1\n")}))
	w.open("2", w.commit(w.base, "two", map[string]*string{"a": str("2\n")}))
	w.open("3", w.commit(w.base, "three", map[string]*string{"c": str("3\n")}))
	got := w.validate(w.plan(3), newGate())
	assert.Equal(t, map[string]Decision{"1": DecisionMerge, "2": DecisionWait, "3": DecisionMerge}, got.decisions())
	two := got.get(t, "2")
	assert.Equal(t, CodeConflictAhead, two.Code)
	assert.Equal(t, "conflicts with #1 ahead of it in a; retried once it merges", two.Reason)
	assert.Equal(t, "1", got.get(t, "3").After)
}

func TestOnlyBuildsTheChainButGatesTheOneChange(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.open("1", w.commit(w.base, "one", map[string]*string{"a": str("1\n")}))
	w.open("2", w.commit(w.base, "two", map[string]*string{"a": str("2\n")}))
	w.open("3", w.commit(w.base, "three", map[string]*string{"c": str("3\n")}))
	w.open("4", w.commit(w.base, "four", map[string]*string{"d": str("4\n")}))
	gate := newGate()
	got := w.validate(w.plan(3), gate, only("3"))
	require.Len(t, got.all, 1)
	three := got.get(t, "3")
	assert.Equal(t, DecisionMerge, three.Decision)
	assert.Equal(t, "1", three.After, "the chain skips what its own run holds, as the pipeline does")
	assert.Equal(t, 2, three.Depth)
	assert.Equal(t, []string{"3"}, gate.gated, "only the top is gated")
}

func TestOnlyAChangeThePlanDidNotAdmitIsAnError(t *testing.T) {
	v := NewValidator(newModel(), newGate(), &collected{})
	v.Scratch, v.Only = t.TempDir(), "7"
	err := v.Run(context.Background(), Plan{Base: "main", BaseCommit: base, Depth: 1, Partitions: [][]Change{{change("1", "a")}}})
	require.EqualError(t, err, "7 is not an admitted change of the plan")
}

func TestAValidatorMissingAPartIsAnError(t *testing.T) {
	p := Plan{Base: "main", BaseCommit: base, Depth: 1}
	require.EqualError(t, (&Validator{}).Run(context.Background(), p),
		"a Validator needs a VCS, a Gate and a VerdictSink; build it with NewValidator")
	v := NewValidator(newModel(), newGate(), &collected{})
	require.EqualError(t, v.Run(context.Background(), p), "a Validator needs a Scratch directory to check candidates out in")
	v.Scratch, v.Parallel = t.TempDir(), -1
	require.EqualError(t, v.Run(context.Background(), p), "parallel -1 must not be negative")
}

// failingCheckout fails every checkout, the way a full disk does.
type failingCheckout struct{ *model }

func (failingCheckout) CreateCheckout(context.Context, string, string) error {
	return errors.New("no space left on device")
}

func TestAMachineFailureBuildingACandidateIsAnError(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.open("1", w.commit(w.base, "one", map[string]*string{"x": str("1")}))
	v := NewValidator(failingCheckout{w.m}, newGate(), &collected{})
	v.Scratch = t.TempDir()
	require.ErrorContains(t, v.Run(context.Background(), w.plan(1)), "no space left on device")
}
