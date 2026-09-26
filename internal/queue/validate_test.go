package queue

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
)

func planOf(groups ...[]types.Change) types.Plan {
	return types.Plan{Schema: types.SchemaPlan, Base: "main", BaseCommit: base, CommitDate: when, Depth: 3, Partitions: groups}
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
	d.facts.EXPECT().AllUnits(mock.Anything).Return([]string{"/"}, nil).Maybe()
	dir := &VerdictDir{Path: t.TempDir()}
	v, err := NewValidator(d.vcs, clone, d.gate, dir, d.facts, t.TempDir())
	require.NoError(t, err)
	return v, dir
}

// gated records the gate runs as "id@commit", id naming the change whose candidate was
// gated, or "base" for the commit a red candidate was built onto, and the units each
// run was handed.
type gated struct {
	mu    sync.Mutex
	runs  []string
	units map[string][]string
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

func (g *gated) unitsOf(run string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.units[run]
}

// gates answers every gate run with result for what it gated: a change's id, or "base".
func (d doubles) gates(result func(id string) types.GateResult) *gated {
	g := &gated{units: map[string][]string{}}
	d.gate.EXPECT().Validate(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, cand types.Candidate, units []string) (types.GateResult, error) {
			id := cmp.Or(cand.Change, "base")
			run := id + "@" + cand.Commit
			g.mu.Lock()
			g.runs = append(g.runs, run)
			g.units[run] = units
			g.mu.Unlock()
			return result(id), nil
		}).Maybe()
	return g
}

func allGreen(string) types.GateResult { return types.GateResult{Green: true} }

func redFor(ids ...string) func(string) types.GateResult {
	return func(id string) types.GateResult {
		if slices.Contains(ids, id) {
			return types.GateResult{Summary: "the gate exited 1"}
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
	assert.ElementsMatch(t, []string{"1@" + c1, "2@" + c2, "3@" + c3}, g.runs, "a green candidate costs no gate of its base")
	assert.Equal(t, []string{"a"}, g.unitsOf("3@"+c3), "a gate runs its change's affected set")
}

// Every head is in the store before the first hook runs: git fetches no object the store
// already holds, so a head fetched after one candidate's hook wrote there could be the
// hook's object.
func TestEveryHeadIsFetchedBeforeAnyHookRuns(t *testing.T) {
	d := newDoubles(t)
	changes := []types.Change{change("1", "gen"), change("2", "gen"), change("3", "gen")}
	var events trail
	for _, c := range changes {
		d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).RunAndReturn(func(context.Context, string, string, string) error {
			events.add("fetch " + c.ID)
			return nil
		}).Once()
	}
	touched := []string{"gen/x.go"}
	d.builds(building{touched: touched})
	d.facts.EXPECT().Classify(mock.Anything, touched).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
	d.vcs.EXPECT().DirtyFiles(mock.Anything, mock.Anything, []string(nil)).Return(nil, nil)
	d.gate.EXPECT().Validate(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, cand types.Candidate, _ []string) (types.GateResult, error) {
			events.add("gate " + cand.Change)
			return types.GateResult{Green: true}, nil
		})
	plan := planOf(changes)
	v, _ := validating(t, d, plan)
	v.Regenerate = func(_ context.Context, r types.Regeneration) error {
		events.add("regenerate " + r.Change.ID)
		return nil
	}
	require.NoError(t, v.Run(t.Context(), plan))

	got := events.entries()
	require.Len(t, got, 9)
	assert.Equal(t, []string{"fetch 1", "fetch 2", "fetch 3"}, got[:3])
}

// A candidate auto-resolution settled is gated like any other, and its verdict names
// what was settled: the reason of a merge, and a line of a kick-back's report.
func TestAVerdictNamesWhatAutoResolutionSettled(t *testing.T) {
	for name, red := range map[string]bool{"green": false, "red": true} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			one := change("1", "a")
			d.builds(building{touched: []string{"CHANGELOG.md"}, files: map[string]string{"CHANGELOG.md": "<<<<<<< markers\n"},
				conflicts: map[string][]magustypes.Conflict{one.Head: {{Path: "CHANGELOG.md", Kind: magustypes.ConflictKindContent}}}})
			d.facts.EXPECT().Classify(mock.Anything, []string{"CHANGELOG.md"}).Return(genOutput, nil)
			d.sides(base, one.Head, "CHANGELOG.md", "a\nz\n", "a\np\nz\n", "a\nq\nz\n")
			d.allows("CHANGELOG.md", "a\nz\n", "a\np\nq\nz\n", chVerdict, true)
			d.vcs.EXPECT().MarkResolved(mock.Anything, mock.Anything, []string{"CHANGELOG.md"}).Return(nil)
			result := allGreen
			if red {
				result = redFor("1")
			}
			g := d.gates(result)
			plan := planOf([]types.Change{one})
			v, dir := validating(t, d, plan)
			var events bytes.Buffer
			v.Events = NewEvents(&events)
			require.NoError(t, v.Run(t.Context(), plan))

			cand := candidateOf(base, one.Head)
			assert.Equal(t, 1, g.count("1"), "a resolution never skips the gate")
			assert.Contains(t, events.String(), `"kind":"resolved","change":"1","reason":`+jsonString(t, chNote)+`,"commit":"`+cand+`"`)
			got := recorded(t, dir)["1"]
			if !red {
				assert.Equal(t, types.DecisionMerge, got.Decision)
				assert.Equal(t, chNote, got.Reason)
				return
			}
			assert.Equal(t, types.CodeKickRed, got.Code)
			assert.Equal(t, "The merge queue built this change at `"+short(one.Head)+"` onto `main` at `"+short(base)+"`, and the gate exited 1.\n"+
				"\nBuilding the candidate "+chNote+"; the gate ran on that merge.\n", got.Report)
		})
	}
}

// A red candidate on a green base is its change's own (everything beneath it validated),
// and what was built onto it is built again onto what did validate.
func TestARedCandidateIsKickedAndWhatWasBuiltOnItIsRebuilt(t *testing.T) {
	d := newDoubles(t)
	one, two, three := change("1", "a"), change("2", "a"), change("3", "a")
	d.builds(building{touched: []string{"a/x.go"}})
	g := d.gates(redFor("2"))
	plan := planOf([]types.Change{one, two, three})
	v, dir := validating(t, d, plan)
	v.Reproduce = types.Reproduction{Gate: `magus run ci`}
	require.NoError(t, v.Run(t.Context(), plan))
	c1 := candidateOf(base, one.Head)
	assert.Equal(t, 1, g.count("base"))
	assert.Equal(t, []string{"a"}, g.unitsOf("base@"+c1), "the base is gated on the red change's units, onto what it was built onto")

	got := recorded(t, dir)
	assert.Equal(t, types.DecisionMerge, got["1"].Decision)
	assert.Equal(t, types.DecisionKick, got["2"].Decision)
	assert.Equal(t, types.CodeKickRed, got["2"].Code)
	assert.Equal(t, "the gate exited 1", got["2"].Reason)
	assert.Equal(t, "The merge queue built this change at `"+short(two.Head)+"` onto the candidate of #1 (`"+short(c1)+"`), and the gate exited 1.\n",
		got["2"].Report, "what failed on which commits, and no hook line")
	for id, vd := range got {
		assert.Equal(t, `magus run ci`, vd.Gate, "every verdict records the gate it ran: %s", id)
		assert.Empty(t, vd.Regenerate, id)
	}
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

// A red the base carries is nobody's in the queue: every change it reddens waits, what
// is stacked on one waits beneath it, and one gate of the base answers them all.
func TestARedBaseKicksNobodyBackAndIsGatedOnce(t *testing.T) {
	d := newDoubles(t)
	one, two := change("1", "a"), change("2", "a")
	three := stacked("3", one, "a")
	d.builds(building{touched: []string{"a/x.go"}})
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, one.Head, mock.Anything).Return(true, nil).Maybe()
	g := d.gates(redFor("1", "2", "base"))
	plan := planOf([]types.Change{one, two, three})
	v, dir := validating(t, d, plan)
	require.NoError(t, v.Run(t.Context(), plan))

	got := recorded(t, dir)
	for _, id := range []string{"1", "2"} {
		assert.Equal(t, types.DecisionWait, got[id].Decision, id)
		assert.Equal(t, types.CodeWaitBaseRed, got[id].Code, id)
		assert.Equal(t, "the gate failed on it and on `main` at `"+short(base)+"` without it; retried once that is green", got[id].Reason, id)
		assert.Empty(t, got[id].Report, "nothing is reported to an author who did nothing: %s", id)
		assert.Equal(t, base, got[id].Onto, "rebuilt onto the base once the change beneath waited: %s", id)
	}
	assert.Equal(t, types.CodeWaitBelow, got["3"].Code, "stacked on a change that waits, not one kicked back")
	assert.Equal(t, 1, g.count("base"), "one gate of the base answers every change it reddens")
	assert.Equal(t, []string{"a"}, g.unitsOf("base@"+base))
}

// A set that is no proof, unknown or unbounded, reaches every hook as the build tool
// spells every unit; a proven empty set gates nothing.
func TestAChangeWhoseSetIsNoProofIsGatedOnEveryUnit(t *testing.T) {
	d := newDoubles(t)
	unknown := change("1")
	unbounded := change("2", "a")
	unbounded.UnboundedBy = "edits the declarations"
	none := change("3")
	none.Affected = []string{}
	d.builds(building{touched: []string{"a/x.go"}})
	g := d.gates(allGreen)
	plan := planOf([]types.Change{unknown}, []types.Change{unbounded}, []types.Change{none})
	v, dir := validating(t, d, plan)
	require.NoError(t, v.Run(t.Context(), plan))

	assert.Equal(t, []string{"/"}, g.unitsOf("1@"+candidateOf(base, unknown.Head)))
	assert.Equal(t, []string{"/"}, g.unitsOf("2@"+candidateOf(base, unbounded.Head)))
	assert.Zero(t, g.count("3"), "no unit, nothing to gate")
	got := recorded(t, dir)
	for _, id := range []string{"1", "2", "3"} {
		assert.Equal(t, types.DecisionMerge, got[id].Decision, id)
	}
}

// Planning proved the change merges onto the base alone, so a conflict building it onto
// the change ahead is that change's to settle first; nobody is kicked.
func TestAChangeConflictingWithOneAheadWaitsAndTheRestStackPastIt(t *testing.T) {
	d := newDoubles(t)
	one, two, three := change("1", "a"), change("2", "a"), change("3", "a")
	d.builds(building{touched: []string{"a/x.go"}, conflicts: map[string][]magustypes.Conflict{two.Head: {{Path: "a/x.go", Kind: magustypes.ConflictKindContent}}}})
	d.facts.EXPECT().Classify(mock.Anything, []string{"a/x.go"}).Return(map[string]types.Writes{}, nil)
	c1 := candidateOf(base, one.Head)
	d.sides(c1, two.Head, "a/x.go", "package a\n", "package a1\n", "package a2\n")
	d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, two.Head, c1, []string{"a/x.go"}).Return(nil, nil)
	d.gates(allGreen)
	plan := planOf([]types.Change{one, two, three})
	v, dir := validating(t, d, plan)
	require.NoError(t, v.Run(t.Context(), plan))

	got := recorded(t, dir)
	assert.Equal(t, types.CodeWaitConflictAhead, got["2"].Code)
	assert.Equal(t, "conflicts with #1 ahead of it in `a/x.go`; retried once it merges; not auto-resolved: a/x.go#(preamble): not settled",
		got["2"].Reason, "the reason names the location auto-resolution could not settle")
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
	g := d.gates(redFor("base"))
	plan := planOf([]types.Change{one}, []types.Change{two})
	v, dir := validating(t, d, plan)
	v.Regenerate = func(_ context.Context, r types.Regeneration) error {
		assert.Equal(t, r.Change.Affected, r.Units, "validation regenerates by the change's own units")
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
	assert.Equal(t, "The merge queue built this change at `"+short(one.Head)+"` onto `main` at `"+short(base)+"`, and building its candidate failed: `make gen` exited 2.\n\nRun `make gen` and push.\n",
		got["1"].Report)
	assert.Equal(t, types.DecisionMerge, got["2"].Decision)
	assert.Zero(t, g.count("base"), "a refusal is about the change, whatever the base does")
}

// No hook run on one candidate can plant anything where another's will look.
func TestEveryCandidateGetsABoxOfItsOwn(t *testing.T) {
	d := newDoubles(t)
	one, two := change("1", "a"), change("2", "a")
	d.builds(building{touched: []string{"gen/x.go"}})
	d.facts.EXPECT().Classify(mock.Anything, []string{"gen/x.go"}).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
	d.vcs.EXPECT().DirtyFiles(mock.Anything, mock.Anything, []string(nil)).Return(nil, nil)
	d.gates(allGreen)
	plan := planOf([]types.Change{one, two})
	v, _ := validating(t, d, plan)
	var mu sync.Mutex
	boxes := map[string]string{}
	v.Regenerate = func(_ context.Context, r types.Regeneration) error {
		box := filepath.Dir(r.Dir)
		assert.Equal(t, []string{filepath.Join(box, "home"), filepath.Join(box, "tmp")}, []string{r.Home, r.TempDir}, "beside its own checkout")
		assert.DirExists(t, r.Home)
		assert.DirExists(t, r.TempDir)
		mu.Lock()
		defer mu.Unlock()
		boxes[r.Change.ID] = box
		return nil
	}
	require.NoError(t, v.Run(t.Context(), plan))
	require.Len(t, boxes, 2)
	assert.NotEqual(t, boxes["1"], boxes["2"])
	for _, box := range boxes {
		assert.True(t, strings.HasPrefix(box, v.scratch), "%s is under the validator's scratch", box)
		assert.NoDirExists(t, box, "removed with its checkout")
	}
}

// Validation regenerates a candidate's stale outputs whatever code the regeneration
// runs, the change's own generator included, and gates what that commits. What it
// committed travels beside the verdict as a bundle on the merge beneath it, for an
// Applier that cannot reproduce it to check instead.
func TestValidationRegeneratesStaleOutputsAndGatesTheResult(t *testing.T) {
	touched := []string{"gen/gen.go", "gen/x.go"}
	regenerated := head("regenerated")
	for name, tc := range map[string]struct {
		written []string
	}{
		"a generator change whose outputs are fresh": {},
		"a generator change whose outputs are stale": {written: []string{"gen/x.go"}},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			c := change("1", "gen")
			merged := candidateOf(base, c.Head)
			want := merged
			if len(tc.written) > 0 {
				want = regenerated
				d.vcs.EXPECT().Commit(mock.Anything, mock.Anything, magustypes.CheckoutCommit{CommitMeta: queueMeta("regenerate generated files", when), Paths: tc.written}).
					Return(regenerated, nil).Once()
				d.facts.EXPECT().Classify(mock.Anything, tc.written).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
				d.vcs.EXPECT().Bundle(mock.Anything, clone.Root, mock.Anything, magustypes.BundleRange{Base: merged, Head: regenerated}).
					RunAndReturn(func(_ context.Context, _, file string, _ magustypes.BundleRange) error {
						return os.WriteFile(file, []byte("the bundle"), 0o644)
					}).Once()
			}
			d.builds(building{touched: touched})
			d.facts.EXPECT().Classify(mock.Anything, touched).Return(map[string]types.Writes{"gen/x.go": {Output: true}}, nil)
			d.vcs.EXPECT().DirtyFiles(mock.Anything, mock.Anything, []string(nil)).Return(tc.written, nil)
			g := d.gates(allGreen)
			plan := planOf([]types.Change{c})
			v, dir := validating(t, d, plan)
			var got types.Regeneration
			v.Regenerate = func(_ context.Context, r types.Regeneration) error {
				got = r
				return nil
			}
			require.NoError(t, v.Run(t.Context(), plan))

			batch, err := dir.Poll(t.Context())
			require.NoError(t, err)
			require.Len(t, batch.Verdicts, 1)
			verdict := batch.Verdicts[0]
			assert.Equal(t, types.DecisionMerge, verdict.Decision)
			assert.Equal(t, want, verdict.CandidateCommit, "the gate ran on what the regeneration committed")
			assert.Equal(t, 1, g.count("1"))
			assert.Equal(t, []string{"gen/x.go"}, got.Paths)
			assert.Equal(t, []string{"gen"}, got.Units, "the change's own affected set")
			if len(tc.written) == 0 {
				assert.Empty(t, batch.Bundles, "nothing regenerated, nothing to carry")
				return
			}
			bundle := filepath.Join(dir.Path, "1", CandidateBundle)
			assert.Equal(t, map[string]string{"1": bundle}, batch.Bundles)
			content, err := os.ReadFile(bundle)
			require.NoError(t, err)
			assert.Equal(t, "the bundle", string(content))
		})
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
		gate     func(string) types.GateResult
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
	d.gates(func(string) types.GateResult {
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
