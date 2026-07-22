package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRepo satisfies types.DepGraphRepository with no-op implementations.
type stubRepo struct{}

func (stubRepo) TopoSort() []string                             { return nil }
func (stubRepo) ReverseClosure([]string) []string               { return nil }
func (stubRepo) NearCycles(context.Context, int) []NearCycle    { return nil }
func (stubRepo) BlastRadius() map[string]int                    { return nil }
func (stubRepo) NCCD() float64                                  { return 0 }
func (stubRepo) PathsFromSeeds([]string, string) []AffectedPath { return nil }
func (stubRepo) Successors(string) []string                     { return nil }
func (stubRepo) Predecessors(string) []string                   { return nil }
func (stubRepo) Nodes() []string                                { return nil }

// fakeRepo returns canned values so a delegation test can assert each Graph
// wrapper hands back exactly what the underlying DepGraphRepository produced.
type fakeRepo struct {
	topo        []string
	reverse     []string
	nearCycles  []NearCycle
	blastRadius map[string]int
	nccd        float64
	paths       []AffectedPath
	successors  []string
	predecess   []string
	nodes       []string

	gotReverseSeeds  []string
	gotNearCyclesMax int
	gotPathsSeeds    []string
	gotPathsTarget   string
	gotSuccessorArg  string
	gotPredecessArg  string
}

func (f *fakeRepo) TopoSort() []string { return f.topo }
func (f *fakeRepo) ReverseClosure(seeds []string) []string {
	f.gotReverseSeeds = seeds
	return f.reverse
}
func (f *fakeRepo) NearCycles(_ context.Context, maxDepth int) []NearCycle {
	f.gotNearCyclesMax = maxDepth
	return f.nearCycles
}
func (f *fakeRepo) BlastRadius() map[string]int { return f.blastRadius }
func (f *fakeRepo) NCCD() float64               { return f.nccd }
func (f *fakeRepo) PathsFromSeeds(seeds []string, target string) []AffectedPath {
	f.gotPathsSeeds = seeds
	f.gotPathsTarget = target
	return f.paths
}
func (f *fakeRepo) Successors(path string) []string {
	f.gotSuccessorArg = path
	return f.successors
}
func (f *fakeRepo) Predecessors(path string) []string {
	f.gotPredecessArg = path
	return f.predecess
}
func (f *fakeRepo) Nodes() []string { return f.nodes }

// TestGraphDelegatesToRepository verifies every Graph wrapper forwards its
// arguments to the DepGraphRepository and returns the repo's result verbatim.
func TestGraphDelegatesToRepository(t *testing.T) {
	f := &fakeRepo{
		topo:        []string{"a", "b"},
		reverse:     []string{"b", "c"},
		nearCycles:  []NearCycle{{From: "a", To: "b", BackPath: []string{"b", "a"}}},
		blastRadius: map[string]int{"a": 3},
		nccd:        1.75,
		paths:       []AffectedPath{{Seed: "a", Chain: []string{"a", "b"}}},
		successors:  []string{"s1"},
		predecess:   []string{"p1"},
		nodes:       []string{"a", "b", "c"},
	}
	g := NewGraph(f, nil)

	assert.Equal(t, []string{"a", "b"}, g.TopoSort())

	assert.Equal(t, []string{"b", "c"}, g.ReverseClosure([]string{"seed"}))
	assert.Equal(t, []string{"seed"}, f.gotReverseSeeds, "ReverseClosure must forward its seeds")

	assert.Equal(t, f.nearCycles, g.NearCycles(context.Background(), 4))
	assert.Equal(t, 4, f.gotNearCyclesMax, "NearCycles must forward maxDepth")

	assert.Equal(t, map[string]int{"a": 3}, g.BlastRadius())
	assert.Equal(t, 1.75, g.NCCD())

	assert.Equal(t, f.paths, g.PathsFromSeeds([]string{"a"}, "b"))
	assert.Equal(t, []string{"a"}, f.gotPathsSeeds, "PathsFromSeeds must forward seeds")
	assert.Equal(t, "b", f.gotPathsTarget, "PathsFromSeeds must forward target")

	assert.Equal(t, []string{"s1"}, g.Successors("x"))
	assert.Equal(t, "x", f.gotSuccessorArg)

	assert.Equal(t, []string{"p1"}, g.Predecessors("y"))
	assert.Equal(t, "y", f.gotPredecessArg)

	assert.Equal(t, []string{"a", "b", "c"}, g.Nodes())
}

func TestNewGraph_ProjectLookup(t *testing.T) {
	projects := map[string]*Project{
		"api/":     {Path: "api/"},
		"gateway/": {Path: "gateway/"},
	}
	g := NewGraph(stubRepo{}, projects)

	p := g.Project("api/")
	require.NotNil(t, p)
	assert.Equal(t, "api/", p.Path)

	assert.Nil(t, g.Project("missing/"))
}

func TestWithGraphObserver_ComposesOntoConfig(t *testing.T) {
	cfg := NewGraphConfig(nil)
	require.NotNil(t, cfg)

	var called int
	obs := &countObserver{count: &called}
	WithGraphObserver(obs)(cfg)
	cfg.Obs.OnError(nil) // fires the observer
	assert.Equal(t, 1, called)
}

type countObserver struct{ count *int }

func (o *countObserver) OnBuild(BuildStats) {}
func (o *countObserver) OnQuery(QueryEvent) {}
func (o *countObserver) OnError(error)      { *o.count++ }
