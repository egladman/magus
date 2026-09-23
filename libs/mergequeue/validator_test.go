package mergequeue

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collected gathers verdicts in the order validation decided them.
type collected struct {
	mu  sync.Mutex
	all []Verdict
}

func (c *collected) Record(_ context.Context, v Verdict) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.all = append(c.all, v)
	return nil
}

func (c *collected) get(t *testing.T, id string) Verdict {
	t.Helper()
	for _, v := range c.all {
		if v.Change.ID == id {
			return v
		}
	}
	require.Failf(t, "no verdict", "#%s", id)
	return Verdict{}
}

func (c *collected) decisions() map[string]Decision {
	out := map[string]Decision{}
	for _, v := range c.all {
		out[v.Change.ID] = v.Decision
	}
	return out
}

func planOf(depth int, partitions ...[]Change) Plan {
	return Plan{Schema: SchemaPlan, Base: "main", BaseCommit: base, Depth: depth, Partitions: partitions}
}

func validateWith(t *testing.T, st *fakeStager, gate *fakeGate, p Plan, configure func(*Validator)) (*collected, error) {
	t.Helper()
	got := &collected{}
	v := NewValidator(st, gate, got)
	if configure != nil {
		configure(v)
	}
	return got, v.Run(context.Background(), p)
}

func only(id string) func(*Validator) { return func(v *Validator) { v.Only = id } }

func TestSpeculativeStagesStackAndValidateInParallel(t *testing.T) {
	gate := newGate()
	got, err := validateWith(t, newStager(nil), gate, planOf(3, []Change{change("1", "a"), change("2", "a"), change("3", "a")}), nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionLand, "3": DecisionLand}, got.decisions())
	assert.EqualValues(t, 3, gate.peak.Load(), "all three stages gate at once")
	one := got.get(t, "1")
	assert.Equal(t, Verdict{BaseCommit: base, Change: change("1", "a"), Decision: DecisionLand,
		Onto: base, Stage: base + "+1", Message: "* 1", Depth: 1, DurationMS: one.DurationMS}, one)
	two, three := got.get(t, "2"), got.get(t, "3")
	assert.Equal(t, []string{base + "+1+2", "1", base + "+1"}, []string{two.Stage, two.After, two.Onto})
	assert.Equal(t, 2, two.Depth)
	assert.Equal(t, []string{base + "+1+2+3", "2"}, []string{three.Stage, three.After})
	assert.Equal(t, 3, three.Depth)
	assert.Equal(t, []string{"1", "2", "3"}, []string{got.all[0].Change.ID, got.all[1].Change.ID, got.all[2].Change.ID},
		"verdicts are published in queue order, each as soon as it is decided")
}

func TestAFailedStageRespeculatesOnlyWhatWasBehindIt(t *testing.T) {
	st, gate := newStager(nil), newGate()
	gate.bad["2"] = true
	got, err := validateWith(t, st, gate, planOf(2, []Change{change("1", "a"), change("2", "a"), change("3", "a"), change("4", "a")}), nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionKick, "3": DecisionLand, "4": DecisionLand}, got.decisions())
	assert.Contains(t, got.get(t, "2").Report, "tests failed in 2")
	// #1 is never rebuilt; #3, first stacked on the red #2, is rebuilt on #1 alone.
	assert.Equal(t, base+"+1+3", got.get(t, "3").Stage)
	assert.Equal(t, "1", got.get(t, "3").After)
	assert.Equal(t, base+"+1+3+4", got.get(t, "4").Stage)
	assert.Equal(t, 1, strings.Count(strings.Join(st.builds, " "), base+"+1 "), "#1 built once")
	assert.Contains(t, st.builds, base+"+1+2+3", "speculated on #2 before it failed")
	assert.Equal(t, len(st.builds), st.discarded, "every stage directory is removed")
}

// A regeneration the change's own code broke is that change's verdict, not the run's
// end: before, it aborted validation with no verdict at all, and the change sat at the
// bottom of its partition forever.
func TestARefusedStagingIsKickedBackAndTheRestLand(t *testing.T) {
	st, gate := newStager(nil), newGate()
	st.refused["2"] = "`gen` exited 1 regenerating app/gen/out.txt"
	got, err := validateWith(t, st, gate, planOf(3, []Change{change("1", "a"), change("2", "a"), change("3", "a")}), nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionKick, "3": DecisionLand}, got.decisions())
	assert.Contains(t, got.get(t, "2").Report, "`gen` exited 1")
	assert.Equal(t, base+"+1+3", got.get(t, "3").Stage, "#3 is built onto what staged")
	assert.Equal(t, "1", got.get(t, "3").After)
	assert.NotContains(t, gate.gated, "", "nothing gates a stage that was never built")
}

// Only mode stages the changes beneath without gating them, so a red on top of them
// cannot be pinned on the top change: before, #2's author was kicked back for #1's bug.
func TestOnlyWaitsRatherThanBlameTheTopForWhatItWasStagedOn(t *testing.T) {
	st, gate := newStager(nil), newGate()
	gate.bad["2"] = true
	got, err := validateWith(t, st, gate, planOf(2, []Change{change("1", "a"), change("2", "a")}), only("2"))
	require.NoError(t, err)
	two := got.get(t, "2")
	assert.Equal(t, DecisionWait, two.Decision)
	assert.Equal(t, "the gate failed on top of #1, which this run did not validate; retried once it is", two.Reason)

	got, err = validateWith(t, newStager(nil), gate, planOf(2, []Change{change("2", "a")}), only("2"))
	require.NoError(t, err)
	assert.Equal(t, DecisionKick, got.get(t, "2").Decision, "at the bottom, the red is its own")
}

// Twenty disjoint partitions at depth 3 would start sixty builds at once without a cap
// across partitions; before, Parallel did not exist and the peak here was twelve.
func TestParallelCapsStagesAcrossPartitions(t *testing.T) {
	gate := newGate()
	var parts [][]Change
	for _, unit := range []string{"a", "b", "c", "d"} {
		parts = append(parts, []Change{change(unit+"1", unit), change(unit+"2", unit), change(unit+"3", unit)})
	}
	got, err := validateWith(t, newStager(nil), gate, planOf(3, parts...), func(v *Validator) { v.Parallel = 2 })
	require.NoError(t, err)
	assert.Len(t, got.all, 12)
	for id, d := range got.decisions() {
		assert.Equal(t, DecisionLand, d, id)
	}
	assert.LessOrEqual(t, gate.peak.Load(), int32(2))
}

func TestAMachineFailureStopsItsPartitionAlone(t *testing.T) {
	gate := newGate()
	gate.broken["1"] = true
	got, err := validateWith(t, newStager(nil), gate, planOf(1, []Change{change("1", "a")}, []Change{change("2", "b")}),
		func(v *Validator) { v.Parallel = 2 })
	require.ErrorContains(t, err, "gate #1: the runner ran out of memory")
	assert.Equal(t, map[string]Decision{"2": DecisionLand}, got.decisions(), "no verdict on #1, and #2 is still decided")
}

func TestDisjointPartitionsNeverWaitForEachOther(t *testing.T) {
	gate := newGate()
	// #1's gate cannot finish until #2's has: serial partitions would deadlock here.
	gate.done = map[string]chan struct{}{"2": make(chan struct{})}
	gate.wait = map[string]chan struct{}{"1": gate.done["2"]}
	got, err := validateWith(t, newStager(nil), gate, planOf(1, []Change{change("1", "a")}, []Change{change("2", "b")}),
		func(v *Validator) { v.Parallel = 2 })
	require.NoError(t, err)
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionLand}, got.decisions())
	assert.Equal(t, base+"+2", got.get(t, "2").Stage, "each partition stages on the base alone")
}

func TestAChangeConflictingWithOneAheadWaitsAndTheRestStackPastIt(t *testing.T) {
	st := newStager(nil)
	st.conflicts[[2]string{"1", "2"}] = []string{"app/a"}
	got, err := validateWith(t, st, newGate(), planOf(3, []Change{change("1", "a"), change("2", "a"), change("3", "a")}), nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionWait, "3": DecisionLand}, got.decisions())
	assert.Equal(t, "conflicts with #1 ahead of it in app/a; retried once it lands", got.get(t, "2").Reason)
	assert.Equal(t, base+"+1+3", got.get(t, "3").Stage)
}

func TestOnlyStagesTheChainButGatesTheOneChange(t *testing.T) {
	st, gate := newStager(nil), newGate()
	st.conflicts[[2]string{"1", "2"}] = []string{"app/a"}
	got, err := validateWith(t, st, gate, planOf(3, []Change{change("1", "a"), change("2", "a"), change("3", "a"), change("4", "a")}, []Change{change("9", "z")}), only("3"))
	require.NoError(t, err)
	require.Len(t, got.all, 1)
	three := got.get(t, "3")
	assert.Equal(t, DecisionLand, three.Decision)
	assert.Equal(t, base+"+1+3", three.Stage, "the chain skips what its own run holds, as the pipeline does")
	assert.Equal(t, "1", three.After)
	assert.Equal(t, base+"+1", three.Onto)
	assert.Equal(t, 2, three.Depth)
	assert.Equal(t, []string{base + "+1+3"}, gate.gated, "only the top is gated")
	assert.Equal(t, 2, st.discarded)
}

func TestOnlyAChangeThePlanDidNotAdmitIsAnError(t *testing.T) {
	_, err := validateWith(t, newStager(nil), newGate(), planOf(1, []Change{change("1", "a")}), only("7"))
	require.EqualError(t, err, "7 is not an admitted change of the plan")
}

func TestAValidatorMissingAPartIsAnError(t *testing.T) {
	err := (&Validator{}).Run(context.Background(), planOf(1))
	require.EqualError(t, err, "a Validator needs a StagingRepo, a Gate and a VerdictSink; build it with NewValidator")
	_, err = validateWith(t, newStager(nil), newGate(), planOf(1), func(v *Validator) { v.Parallel = -1 })
	require.EqualError(t, err, "parallel -1 must not be negative")
}
