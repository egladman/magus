package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRepo satisfies types.GraphRepository with no-op implementations.
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
