package queue

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readProvider answers the two read calls validation makes; any write fails the test.
type readProvider struct {
	changes  []Change
	approval func(c Change) Approval
}

func (p *readProvider) Name() string { return "fake" }
func (p *readProvider) List(context.Context, ListQuery) ([]Change, error) {
	return p.changes, nil
}

func (p *readProvider) ApprovalAt(_ context.Context, c Change, _ string) (Approval, error) {
	if p.approval != nil {
		return p.approval(c), nil
	}
	return Approval{Approved: true, Head: c.Head}, nil
}

func (p *readProvider) PostStatus(context.Context, Change, string, Status) error {
	panic("validation must not post")
}

func (p *readProvider) Merge(context.Context, Change, string, string) error {
	panic("validation must not merge")
}

func (p *readProvider) KickBack(context.Context, Change, string, string) error {
	panic("validation must not kick back")
}

// fakeStager names a stage commit by what it stacks: "base+1+2" is base, then #1, then #2.
type fakeStager struct {
	paths     map[string][]string
	conflicts map[[2]string][]string // {"base" or an earlier id, id} -> conflicting paths
	mu        sync.Mutex
	builds    []string
	discarded int
}

func (s *fakeStager) Tip(context.Context, string) (string, error) { return "base", nil }
func (s *fakeStager) Fetch(context.Context, Change) error           { return nil }
func (s *fakeStager) Changed(_ context.Context, _, head string) ([]string, error) {
	return s.paths[strings.TrimPrefix(head, "sha-")], nil
}

func (s *fakeStager) Overlap(_ context.Context, _ string, c Change) error {
	if p, ok := s.conflicts[[2]string{"base", c.ID}]; ok {
		return &ConflictError{Conflict: Conflict{Change: c, Paths: p, With: []string{"abc123 an earlier change"}}}
	}
	return nil
}

func (s *fakeStager) Build(_ context.Context, on string, c Change) (Stage, error) {
	for _, below := range strings.Split(on, "+")[1:] {
		if p, ok := s.conflicts[[2]string{below, c.ID}]; ok {
			return Stage{}, &ConflictError{Conflict: Conflict{Change: c, Paths: p}}
		}
	}
	commit := on + "+" + c.ID
	s.mu.Lock()
	s.builds = append(s.builds, commit)
	s.mu.Unlock()
	return Stage{Commit: commit, Dir: "/stages/" + commit}, nil
}

func (s *fakeStager) Discard(context.Context, Stage) error {
	s.mu.Lock()
	s.discarded++
	s.mu.Unlock()
	return nil
}

func (s *fakeStager) Message(_ context.Context, _, head string) (string, error) {
	return "* " + head, nil
}

// fakeGate turns a stage red when the change it adds is bad, holding each gate long
// enough for concurrent ones to overlap.
type fakeGate struct {
	bad     map[string]bool
	hold    time.Duration
	wait    map[string]chan struct{} // a change's gate blocks until its channel closes
	done    map[string]chan struct{} // closed when a change's gate returns
	running atomic.Int32
	peak    atomic.Int32
	mu      sync.Mutex
	gated   []string
}

func (g *fakeGate) Validate(ctx context.Context, s Stage, below string) (GateResult, error) {
	top := strings.TrimPrefix(s.Commit, below+"+")
	n := g.running.Add(1)
	defer g.running.Add(-1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	g.mu.Lock()
	g.gated = append(g.gated, s.Commit)
	g.mu.Unlock()
	if ch, ok := g.wait[top]; ok {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			return GateResult{}, context.DeadlineExceeded
		}
	}
	if ch, ok := g.done[top]; ok {
		defer close(ch)
	}
	select {
	case <-time.After(g.hold):
	case <-ctx.Done():
		return GateResult{}, ctx.Err()
	}
	if g.bad[top] {
		return GateResult{Summary: "tests failed in " + top}, nil
	}
	return GateResult{Green: true}, nil
}

func newValidation(paths map[string][]string, ids ...string) (*Validation, *fakeStager, *fakeGate, *readProvider) {
	st := &fakeStager{paths: paths, conflicts: map[[2]string][]string{}}
	gate := &fakeGate{bad: map[string]bool{}, hold: 20 * time.Millisecond}
	prov := &readProvider{}
	for _, id := range ids {
		prov.changes = append(prov.changes, Change{ID: id, Head: "sha-" + id, Base: "main"})
	}
	return &Validation{Provider: prov, Stager: st, Graph: fakeGraph{}, Gate: gate, Base: "main", Depth: 3}, st, gate, prov
}

// fakeGraph maps a path's first segment to its project; a magusfile edit is unproven.
type fakeGraph struct{}

func (fakeGraph) Closure(_ context.Context, paths []string) (Closure, error) {
	c := Closure{Proven: true}
	for _, p := range paths {
		if p == "magusfile.buzz" {
			return Closure{Why: "declarations changed"}, nil
		}
		proj, _, _ := strings.Cut(p, "/")
		if !slices.Contains(c.Projects, proj) {
			c.Projects = append(c.Projects, proj)
		}
	}
	return c, nil
}

func decisions(m Manifest) map[string]Decision {
	out := map[string]Decision{}
	for _, p := range m.Changes {
		out[p.Change.ID] = p.Decision
	}
	return out
}

func planned(t *testing.T, m Manifest, id string) Planned {
	t.Helper()
	i := slices.IndexFunc(m.Changes, func(p Planned) bool { return p.Change.ID == id })
	require.GreaterOrEqual(t, i, 0, "no decision for #%s", id)
	return m.Changes[i]
}

func TestSpeculativeStagesStackAndValidateInParallel(t *testing.T) {
	v, _, gate, _ := newValidation(map[string][]string{"1": {"app/a"}, "2": {"app/b"}, "3": {"app/c"}}, "1", "2", "3")
	m, err := v.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionLand, "3": DecisionLand}, decisions(m))
	assert.EqualValues(t, 3, gate.peak.Load(), "all three stages gate at once")
	assert.Equal(t, 3, m.Validations)
	one, two, three := planned(t, m, "1"), planned(t, m, "2"), planned(t, m, "3")
	assert.Equal(t, Planned{Change: one.Change, Order: 0, Decision: DecisionLand, Group: 0,
		Stage: "base+1", Message: "* sha-1", Depth: 1, Duration: one.Duration}, one)
	assert.Equal(t, "base+1+2", two.Stage)
	assert.Equal(t, "1", two.After)
	assert.Equal(t, 2, two.Depth)
	assert.Equal(t, "base+1+2+3", three.Stage)
	assert.Equal(t, "2", three.After)
	assert.Equal(t, 3, three.Depth)
}

func TestAFailedStageRespeculatesOnlyWhatWasBehindIt(t *testing.T) {
	paths := map[string][]string{"1": {"app/a"}, "2": {"app/b"}, "3": {"app/c"}, "4": {"app/d"}}
	v, st, _, _ := newValidation(paths, "1", "2", "3", "4")
	v.Depth = 2
	_, gate := v.Gate.(*fakeGate)
	require.True(t, gate)
	v.Gate.(*fakeGate).bad["2"] = true
	m, err := v.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionKick, "3": DecisionLand, "4": DecisionLand}, decisions(m))
	assert.Contains(t, planned(t, m, "2").Report, "tests failed in 2")
	// #1 is never rebuilt; #3, first stacked on the red #2, is rebuilt on #1 alone.
	assert.Equal(t, "base+1+3", planned(t, m, "3").Stage)
	assert.Equal(t, "1", planned(t, m, "3").After)
	assert.Equal(t, "base+1+3+4", planned(t, m, "4").Stage)
	assert.Equal(t, 1, strings.Count(strings.Join(st.builds, " "), "base+1 "), "#1 built once")
	assert.Contains(t, st.builds, "base+1+2+3", "speculated on #2 before it failed")
}

func TestDisjointPartitionsNeverWaitForEachOther(t *testing.T) {
	v, _, gate, _ := newValidation(map[string][]string{"1": {"app/a"}, "2": {"lib/b"}}, "1", "2")
	v.Depth = 1
	// #1's gate cannot finish until #2's has: serial partitions would deadlock here.
	gate.done = map[string]chan struct{}{"2": make(chan struct{})}
	gate.wait = map[string]chan struct{}{"1": gate.done["2"]}
	m, err := v.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"1"}, {"2"}}, m.Groups)
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionLand}, decisions(m))
	assert.Equal(t, "base+2", planned(t, m, "2").Stage, "each partition stages on the base alone")
}

func TestAdmissionHoldsTheUnapprovedAndKicksBackBaseConflicts(t *testing.T) {
	v, st, _, prov := newValidation(map[string][]string{"1": {"app/a"}, "2": {"lib/b"}, "3": {"web/c"}, "4": {"api/d"}}, "1", "2", "3", "4")
	st.conflicts[[2]string{"base", "2"}] = []string{"lib/b"}
	prov.approval = func(c Change) Approval {
		switch c.ID {
		case "1":
			return Approval{Head: c.Head, Required: 1, Reason: "0 of 1 approvals at this commit"}
		case "3":
			return Approval{Approved: true, Head: "sha-new"}
		}
		return Approval{Approved: true, Head: c.Head}
	}
	m, err := v.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]Decision{"1": DecisionWait, "2": DecisionKick, "3": DecisionWait, "4": DecisionLand}, decisions(m))
	assert.Equal(t, "not approved at sha-1: 0 of 1 approvals at this commit", planned(t, m, "1").Reason)
	assert.Contains(t, planned(t, m, "2").Report, "`lib/b`")
	assert.Contains(t, planned(t, m, "2").Report, "abc123 an earlier change")
	assert.Equal(t, "sha-new", planned(t, m, "3").Change.Head, "the wait is reported on the new head")
	assert.Equal(t, []int{0, 1, 2, 3}, []int{planned(t, m, "1").Order, planned(t, m, "2").Order, planned(t, m, "3").Order, planned(t, m, "4").Order})
}

func TestAChangeConflictingWithOneAheadWaitsAndTheRestStackPastIt(t *testing.T) {
	v, st, _, _ := newValidation(map[string][]string{"1": {"app/a"}, "2": {"app/a"}, "3": {"app/c"}}, "1", "2", "3")
	st.conflicts[[2]string{"1", "2"}] = []string{"app/a"}
	m, err := v.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]Decision{"1": DecisionLand, "2": DecisionWait, "3": DecisionLand}, decisions(m))
	assert.Contains(t, planned(t, m, "2").Reason, "#1")
	assert.Equal(t, "base+1+3", planned(t, m, "3").Stage)
}

func TestValidationDryRunPartitionsWithoutBuilding(t *testing.T) {
	v, st, _, _ := newValidation(map[string][]string{"1": {"app/a"}, "2": {"lib/b"}}, "1", "2")
	v.DryRun = true
	m, err := v.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"1"}, {"2"}}, m.Groups)
	assert.Empty(t, st.builds)
	assert.Empty(t, m.Changes)
}

func TestValidationMissingAPartIsAnError(t *testing.T) {
	_, err := (&Validation{Base: "main"}).Run(context.Background())
	require.EqualError(t, err, "queue: validation is missing Gate, Graph, Provider, Stager")
}

func TestManifestRoundTripsThroughItsFile(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{Version: ManifestVersion, Base: "main", BaseSHA: "abc", Groups: [][]string{{"1"}},
		Changes: []Planned{{Change: Change{ID: "1", Head: "h"}, Decision: DecisionLand, Stage: "s", Duration: time.Second}}}
	require.NoError(t, m.Write(dir))
	got, err := ReadManifest(dir)
	require.NoError(t, err)
	assert.Equal(t, m, got)
}
