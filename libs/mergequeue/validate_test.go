package mergequeue

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue/types"
	magustypes "github.com/egladman/magus/types"
)

func planOf(groups ...[]types.Change) types.Plan {
	return types.Plan{Schema: types.SchemaPlan, Base: "main", BaseCommit: base, Depth: 3, Partitions: groups}
}

// validating wires a Validator to d, answering each change's fetch.
func validating(t *testing.T, d doubles, plan types.Plan) (*Validator, *VerdictDir) {
	t.Helper()
	d.noCheckouts()
	for _, g := range plan.Partitions {
		for _, c := range g {
			d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).Return(nil).Maybe()
		}
	}
	dir := &VerdictDir{Path: t.TempDir()}
	v, err := NewValidator(d.vcs, clone, d.gate, dir, d.facts, t.TempDir())
	require.NoError(t, err)
	return v, dir
}

// gated records the gate runs: which change, onto what.
type gated struct {
	mu   sync.Mutex
	runs []string // "id@onto"
}

func (g *gated) count(id string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, r := range g.runs {
		if strings.HasPrefix(r, id+"@") {
			n++
		}
	}
	return n
}

// gates answers every gate run with result for its change.
func (d doubles) gates(result func(c types.Change) types.GateResult) *gated {
	g := &gated{}
	d.gate.EXPECT().Validate(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, cand types.Candidate, onto string, c types.Change) (types.GateResult, error) {
			g.mu.Lock()
			g.runs = append(g.runs, c.ID+"@"+onto)
			g.mu.Unlock()
			return result(c), nil
		}).Maybe()
	return g
}

func allGreen(types.Change) types.GateResult { return types.GateResult{Green: true} }

func redFor(ids ...string) func(types.Change) types.GateResult {
	return func(c types.Change) types.GateResult {
		for _, id := range ids {
			if c.ID == id {
				return types.GateResult{Summary: "`make test` exited 1"}
			}
		}
		return types.GateResult{Green: true}
	}
}

func recorded(t *testing.T, dir *VerdictDir) map[string]types.Verdict {
	t.Helper()
	batch, err := dir.Poll(t.Context())
	require.NoError(t, err)
	require.Empty(t, batch.Rejected)
	out := map[string]types.Verdict{}
	for _, v := range batch.Verdicts {
		out[v.Change.ID] = v
	}
	return out
}

func TestNewValidatorRefusesAMissingPart(t *testing.T) {
	d := newDoubles(t)
	dir := &VerdictDir{Path: t.TempDir()}
	for name, tc := range map[string]struct {
		build func() (*Validator, error)
		want  string
	}{
		"vcs":      {func() (*Validator, error) { return NewValidator(nil, clone, d.gate, dir, d.facts, "/s") }, "validator needs a VCS, a gate, a verdict directory and build facts"},
		"gate":     {func() (*Validator, error) { return NewValidator(d.vcs, clone, nil, dir, d.facts, "/s") }, "validator needs a VCS, a gate, a verdict directory and build facts"},
		"dir":      {func() (*Validator, error) { return NewValidator(d.vcs, clone, d.gate, nil, d.facts, "/s") }, "validator needs a VCS, a gate, a verdict directory and build facts"},
		"facts":    {func() (*Validator, error) { return NewValidator(d.vcs, clone, d.gate, dir, nil, "/s") }, "validator needs a VCS, a gate, a verdict directory and build facts"},
		"clone":    {func() (*Validator, error) { return NewValidator(d.vcs, Clone{}, d.gate, dir, d.facts, "/s") }, "clone needs a root and a remote"},
		"relative": {func() (*Validator, error) { return NewValidator(d.vcs, clone, d.gate, dir, d.facts, "scratch") }, `scratch directory "scratch" is not absolute`},
	} {
		_, err := tc.build()
		require.EqualError(t, err, tc.want, name)
	}
}

// Candidates build onto each other, base+1, base+1+2, base+1+2+3, and gate side by side.
func TestSpeculativeCandidatesStackOntoEachOther(t *testing.T) {
	d := newDoubles(t)
	one, two, three := change("1", "a"), change("2", "a"), change("3", "a")
	d.builds(building{touched: []string{"a/x.go"}})
	g := d.gates(allGreen)
	v, dir := validating(t, d, planOf([]types.Change{one, two, three}))
	require.NoError(t, v.Run(t.Context(), planOf([]types.Change{one, two, three})))

	c1 := candidateOf(base, one.Head)
	c2 := candidateOf(c1, two.Head)
	c3 := candidateOf(c2, three.Head)
	got := recorded(t, dir)
	for id, want := range map[string]types.Verdict{
		"1": {After: "", Onto: base, CandidateCommit: c1, Depth: 1},
		"2": {After: "1", Onto: c1, CandidateCommit: c2, Depth: 2},
		"3": {After: "2", Onto: c2, CandidateCommit: c3, Depth: 3},
	} {
		v := got[id]
		assert.Equal(t, types.DecisionMerge, v.Decision, id)
		assert.Equal(t, base, v.BaseCommit, id)
		assert.Equal(t, want.After, v.After, id)
		assert.Equal(t, want.Onto, v.Onto, id)
		assert.Equal(t, want.CandidateCommit, v.CandidateCommit, id)
		assert.Equal(t, want.Depth, v.Depth, id)
	}
	assert.ElementsMatch(t, []string{"1@" + base, "2@" + c1, "3@" + c2}, g.runs, "each gate runs on what its candidate was built onto")
}

// A red candidate is its change's own (everything beneath it validated), and what was
// built onto it is built again onto what did validate.
func TestARedCandidateIsKickedAndWhatWasBuiltOnItIsRebuilt(t *testing.T) {
	d := newDoubles(t)
	one, two, three := change("1", "a"), change("2", "a"), change("3", "a")
	d.builds(building{touched: []string{"a/x.go"}})
	d.gates(redFor("2"))
	plan := planOf([]types.Change{one, two, three})
	v, dir := validating(t, d, plan)
	require.NoError(t, v.Run(t.Context(), plan))

	got := recorded(t, dir)
	assert.Equal(t, types.DecisionMerge, got["1"].Decision)
	assert.Equal(t, types.DecisionKick, got["2"].Decision)
	assert.Equal(t, types.CodeKickRed, got["2"].Code)
	assert.Equal(t, "the gate failed: `make test` exited 1", got["2"].Reason)
	assert.Contains(t, got["2"].Report, "Push a fix and queue the change again.")
	c1 := candidateOf(base, one.Head)
	assert.Equal(t, types.DecisionMerge, got["3"].Decision)
	assert.Equal(t, "1", got["3"].After, "rebuilt onto what validated")
	assert.Equal(t, c1, got["3"].Onto)
	assert.Equal(t, candidateOf(c1, three.Head), got["3"].CandidateCommit)
}

// What is stacked on a red change waits for it and is never gated as though it could
// merge without it.
func TestAChangeStackedOnARedOneWaitsWithoutBlame(t *testing.T) {
	d := newDoubles(t)
	one := change("1", "a")
	two := stacked("2", one, "a")
	d.builds(building{touched: []string{"a/x.go"}})
	// The candidate beneath carries the stack base, so the natural merge base serves.
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, one.Head, mock.Anything).Return(true, nil).Maybe()
	d.gates(redFor("1"))
	plan := planOf([]types.Change{one, two})
	v, dir := validating(t, d, plan)
	require.NoError(t, v.Run(t.Context(), plan))

	got := recorded(t, dir)
	assert.Equal(t, types.CodeKickRed, got["1"].Code)
	assert.Equal(t, types.DecisionWait, got["2"].Decision)
	assert.Equal(t, types.CodeWaitBelowKicked, got["2"].Code)
	assert.Equal(t, "#1 was kicked back; this stays queued and is validated again once it returns", got["2"].Reason)
}

// Planning proved the change merges onto the base alone, so a conflict building it onto
// the change ahead is that change's to settle first; nobody is kicked.
func TestAChangeConflictingWithOneAheadWaitsAndTheRestStackPastIt(t *testing.T) {
	d := newDoubles(t)
	one, two, three := change("1", "a"), change("2", "a"), change("3", "a")
	d.builds(building{touched: []string{"a/x.go"}, conflicts: map[string][]magustypes.Conflict{two.Head: {{Path: "a/x.go", Kind: magustypes.ConflictKindContent}}}})
	d.facts.EXPECT().Classify(mock.Anything, []string{"a/x.go"}).Return(map[string]types.Writes{}, nil)
	c1 := candidateOf(base, one.Head)
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, two.Head, c1, []string{"a/x.go"}).Return(nil, nil)
	d.gates(allGreen)
	plan := planOf([]types.Change{one, two, three})
	v, dir := validating(t, d, plan)
	require.NoError(t, v.Run(t.Context(), plan))

	got := recorded(t, dir)
	assert.Equal(t, types.CodeWaitConflictAhead, got["2"].Code)
	assert.Equal(t, "conflicts with #1 ahead of it in a/x.go; retried once it merges", got["2"].Reason)
	assert.Equal(t, []string{"a/x.go"}, got["2"].Paths)
	assert.Equal(t, "1", got["3"].After, "stacked past the change that waits")
	assert.Equal(t, types.DecisionMerge, got["3"].Decision)
}

// A regeneration the change's own code broke refuses the candidate, which is that
// change's red verdict, with the paths at issue and what to do.
func TestARefusedCandidateIsKickedBackAndTheRestValidate(t *testing.T) {
	d := newDoubles(t)
	one, two := change("1", "a"), change("2", "b")
	d.builds(building{touched: []string{"gen/x.go"}})
	d.facts.EXPECT().Classify(mock.Anything, []string{"gen/x.go"}).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
	d.vcs.EXPECT().DirtyFiles(mock.Anything, mock.Anything, []string(nil)).Return(nil, nil).Maybe()
	d.gates(allGreen)
	plan := planOf([]types.Change{one}, []types.Change{two})
	v, dir := validating(t, d, plan)
	v.Regenerate = func(_ context.Context, r types.Regeneration) error {
		if r.Change.ID == "1" {
			return &types.RefusedError{Reason: "`make gen` exited 2", Paths: []string{"gen/x.go"}, Remedy: "Run `make gen` and push."}
		}
		return nil
	}
	require.NoError(t, v.Run(t.Context(), plan))

	got := recorded(t, dir)
	assert.Equal(t, types.CodeKickRefused, got["1"].Code)
	assert.Equal(t, "building its candidate failed: `make gen` exited 2", got["1"].Reason)
	assert.Equal(t, []string{"gen/x.go"}, got["1"].Paths)
	assert.Contains(t, got["1"].Report, "`make gen` exited 2. Run `make gen` and push.")
	assert.Equal(t, types.DecisionMerge, got["2"].Decision)
}

// No hook run on one candidate can plant anything where another's will look.
func TestEveryCandidateGetsAPrivateScratchDirectory(t *testing.T) {
	d := newDoubles(t)
	one, two := change("1", "a"), change("2", "a")
	d.builds(building{touched: []string{"gen/x.go"}})
	d.facts.EXPECT().Classify(mock.Anything, []string{"gen/x.go"}).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
	d.vcs.EXPECT().DirtyFiles(mock.Anything, mock.Anything, []string(nil)).Return(nil, nil)
	d.gates(allGreen)
	plan := planOf([]types.Change{one, two})
	v, _ := validating(t, d, plan)
	var mu sync.Mutex
	scratches := map[string]string{}
	v.Regenerate = func(_ context.Context, r types.Regeneration) error {
		assert.DirExists(t, r.Scratch)
		assert.Equal(t, filepath.Dir(r.Dir), filepath.Dir(r.Scratch), "beside its own checkout")
		mu.Lock()
		defer mu.Unlock()
		scratches[r.Change.ID] = r.Scratch
		return nil
	}
	require.NoError(t, v.Run(t.Context(), plan))
	require.Len(t, scratches, 2)
	assert.NotEqual(t, scratches["1"], scratches["2"])
	for _, s := range scratches {
		assert.True(t, strings.HasPrefix(s, v.scratch), "%s is under the validator's scratch", s)
		assert.NoDirExists(t, s, "removed with its checkout")
	}
}

// A failure the queue can prove is the machine's stops its partition; the others'
// verdicts stand.
func TestAMachineFailureStopsItsPartitionAlone(t *testing.T) {
	d := newDoubles(t)
	one, two := change("1", "a"), change("2", "b")
	d.builds(building{touched: []string{"a/x.go"}, fail: map[string]error{one.Head: errors.New("disk full")}})
	d.gates(allGreen)
	plan := planOf([]types.Change{one}, []types.Change{two})
	v, dir := validating(t, d, plan)
	err := v.Run(t.Context(), plan)
	require.ErrorContains(t, err, "build the candidate of #1 onto "+base[:12]+": disk full")
	got := recorded(t, dir)
	assert.NotContains(t, got, "1")
	assert.Equal(t, types.DecisionMerge, got["2"].Decision)
}

// Only builds the chain beneath its change without gating it, since the chain's own runs
// gate those, and gates the one change.
func TestOnlyBuildsTheChainButGatesTheOneChange(t *testing.T) {
	for name, tc := range map[string]struct {
		gate     func(types.Change) types.GateResult
		want     types.Decision
		wantCode types.Code
	}{
		"green": {gate: allGreen, want: types.DecisionMerge},
		// Red on top of changes this run did not gate cannot be pinned on the top change.
		"red": {gate: redFor("2"), want: types.DecisionWait, wantCode: types.CodeWaitBehind},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			one, two := change("1", "a"), change("2", "a")
			d.builds(building{touched: []string{"a/x.go"}})
			g := d.gates(tc.gate)
			plan := planOf([]types.Change{one, two})
			v, dir := validating(t, d, plan)
			v.Only = "2"
			require.NoError(t, v.Run(t.Context(), plan))
			got := recorded(t, dir)
			require.Len(t, got, 1)
			assert.Equal(t, tc.want, got["2"].Decision)
			assert.Equal(t, tc.wantCode, got["2"].Code)
			assert.Equal(t, "1", got["2"].After)
			assert.Equal(t, 0, g.count("1"))
			assert.Equal(t, 1, g.count("2"))
		})
	}
}

func TestOnlyAChangeThePlanDidNotAdmitIsAnError(t *testing.T) {
	d := newDoubles(t)
	plan := planOf([]types.Change{change("1", "a")})
	v, _ := validating(t, d, plan)
	v.Only = "9"
	require.EqualError(t, v.Run(t.Context(), plan), "9 is not an admitted change of the plan")
}

// Parallel caps the gates across every partition, not per partition.
func TestParallelCapsCandidatesAcrossPartitions(t *testing.T) {
	d := newDoubles(t)
	var groups [][]types.Change
	for _, id := range []string{"1", "2", "3", "4"} {
		groups = append(groups, []types.Change{change(id, id)})
	}
	d.builds(building{touched: []string{"x"}})
	var running, peak atomic.Int32
	d.gates(func(types.Change) types.GateResult {
		n := running.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return types.GateResult{Green: true}
	})
	plan := planOf(groups...)
	v, dir := validating(t, d, plan)
	v.Parallel = 2
	require.NoError(t, v.Run(t.Context(), plan))
	assert.LessOrEqual(t, peak.Load(), int32(2))
	assert.Len(t, recorded(t, dir), 4)
}
