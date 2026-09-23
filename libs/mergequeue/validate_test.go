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
	all []StageResult
}

func (c *collected) add(_ context.Context, r StageResult) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.all = append(c.all, r)
	return nil
}

func (c *collected) get(t *testing.T, id string) StageResult {
	t.Helper()
	for _, r := range c.all {
		if r.Change.ID == id {
			return r
		}
	}
	require.Failf(t, "no verdict", "#%s", id)
	return StageResult{}
}

func (c *collected) decisions() map[string]Decision {
	out := map[string]Decision{}
	for _, r := range c.all {
		out[r.Change.ID] = r.Decision
	}
	return out
}

func planOf(depth int, partitions ...[]Change) Plan {
	return Plan{Schema: SchemaPlan, Base: "main", BaseSHA: "base", Depth: depth, Partitions: partitions}
}

func validateWith(t *testing.T, st *fakeStager, gate *fakeGate, p Plan, only string) *collected {
	t.Helper()
	got := &collected{}
	err := (&Validation{Stager: st, Gate: gate, Only: only, Result: got.add}).Run(context.Background(), p)
	require.NoError(t, err)
	return got
}

func TestSpeculativeStagesStackAndValidateInParallel(t *testing.T) {
	gate := newGate()
	got := validateWith(t, newStager(nil), gate, planOf(3, []Change{change("1", "a"), change("2", "a"), change("3", "a")}), "")
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionLand, "3": DecisionLand}, got.decisions())
	assert.EqualValues(t, 3, gate.peak.Load(), "all three stages gate at once")
	one := got.get(t, "1")
	assert.Equal(t, StageResult{Schema: SchemaStage, BaseSHA: "base", Change: change("1", "a"), Decision: DecisionLand,
		Stage: "base+1", Message: "* sha-1", Depth: 1, DurationMS: one.DurationMS}, one)
	two, three := got.get(t, "2"), got.get(t, "3")
	assert.Equal(t, []string{"base+1+2", "1"}, []string{two.Stage, two.After})
	assert.Equal(t, 2, two.Depth)
	assert.Equal(t, []string{"base+1+2+3", "2"}, []string{three.Stage, three.After})
	assert.Equal(t, 3, three.Depth)
	assert.Equal(t, []string{"1", "2", "3"}, []string{got.all[0].Change.ID, got.all[1].Change.ID, got.all[2].Change.ID},
		"verdicts are published in queue order, each as soon as it is decided")
}

func TestAFailedStageRespeculatesOnlyWhatWasBehindIt(t *testing.T) {
	st, gate := newStager(nil), newGate()
	gate.bad["2"] = true
	got := validateWith(t, st, gate, planOf(2, []Change{change("1", "a"), change("2", "a"), change("3", "a"), change("4", "a")}), "")
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionKick, "3": DecisionLand, "4": DecisionLand}, got.decisions())
	assert.Contains(t, got.get(t, "2").Report, "tests failed in 2")
	// #1 is never rebuilt; #3, first stacked on the red #2, is rebuilt on #1 alone.
	assert.Equal(t, "base+1+3", got.get(t, "3").Stage)
	assert.Equal(t, "1", got.get(t, "3").After)
	assert.Equal(t, "base+1+3+4", got.get(t, "4").Stage)
	assert.Equal(t, 1, strings.Count(strings.Join(st.builds, " "), "base+1 "), "#1 built once")
	assert.Contains(t, st.builds, "base+1+2+3", "speculated on #2 before it failed")
	assert.Equal(t, len(st.builds), st.discarded, "every stage directory is removed")
}

func TestDisjointPartitionsNeverWaitForEachOther(t *testing.T) {
	gate := newGate()
	// #1's gate cannot finish until #2's has: serial partitions would deadlock here.
	gate.done = map[string]chan struct{}{"2": make(chan struct{})}
	gate.wait = map[string]chan struct{}{"1": gate.done["2"]}
	got := validateWith(t, newStager(nil), gate, planOf(1, []Change{change("1", "a")}, []Change{change("2", "b")}), "")
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionLand}, got.decisions())
	assert.Equal(t, "base+2", got.get(t, "2").Stage, "each partition stages on the base alone")
}

func TestAChangeConflictingWithOneAheadWaitsAndTheRestStackPastIt(t *testing.T) {
	st := newStager(nil)
	st.conflicts[[2]string{"1", "2"}] = []string{"app/a"}
	got := validateWith(t, st, newGate(), planOf(3, []Change{change("1", "a"), change("2", "a"), change("3", "a")}), "")
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionWait, "3": DecisionLand}, got.decisions())
	assert.Contains(t, got.get(t, "2").Reason, "#1")
	assert.Equal(t, "base+1+3", got.get(t, "3").Stage)
}

func TestOnlyStagesTheChainButGatesTheOneChange(t *testing.T) {
	st, gate := newStager(nil), newGate()
	st.conflicts[[2]string{"1", "2"}] = []string{"app/a"}
	got := validateWith(t, st, gate, planOf(3, []Change{change("1", "a"), change("2", "a"), change("3", "a"), change("4", "a")}, []Change{change("9", "z")}), "3")
	require.Len(t, got.all, 1)
	three := got.get(t, "3")
	assert.Equal(t, DecisionLand, three.Decision)
	assert.Equal(t, "base+1+3", three.Stage, "the chain skips what its own run holds, as the pipeline does")
	assert.Equal(t, "1", three.After)
	assert.Equal(t, 2, three.Depth)
	assert.Equal(t, []string{"base+1+3"}, gate.gated, "only the top is gated")
	assert.Equal(t, 2, st.discarded)
}

func TestOnlyAChangeThePlanDidNotAdmitIsAnError(t *testing.T) {
	err := (&Validation{Stager: newStager(nil), Gate: newGate(), Only: "7", Result: (&collected{}).add}).Run(context.Background(), planOf(1, []Change{change("1", "a")}))
	require.EqualError(t, err, "mergequeue: 7 is not an admitted change of the plan")
}

func TestValidationMissingAPartIsAnError(t *testing.T) {
	err := (&Validation{}).Run(context.Background(), planOf(1))
	require.EqualError(t, err, "mergequeue: validation needs a Stager, a Gate and a Result sink")
}
