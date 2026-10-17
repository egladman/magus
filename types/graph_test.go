package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestGraphDelegatesToRepository verifies every Graph wrapper forwards its
// arguments to the DepGraphRepository and returns the repo's result verbatim.
func TestGraphDelegatesToRepository(t *testing.T) {
	ctx := context.Background()
	nearCycles := []NearCycle{{From: "a", To: "b", BackPath: []string{"b", "a"}}}
	paths := []AffectedPath{{Seed: "a", Chain: []string{"a", "b"}}}

	repo := NewMockDepGraphRepository(t)
	repo.EXPECT().TopoSort().Return([]string{"a", "b"}).Once()
	repo.EXPECT().ReverseClosure([]string{"seed"}).Return([]string{"b", "c"}).Once()
	repo.EXPECT().NearCycles(ctx, 4).Return(nearCycles).Once()
	repo.EXPECT().BlastRadius().Return(map[string]int{"a": 3}).Once()
	repo.EXPECT().NCCD().Return(1.75).Once()
	repo.EXPECT().PathsFromSeeds([]string{"a"}, "b").Return(paths).Once()
	repo.EXPECT().Successors("x").Return([]string{"s1"}).Once()
	repo.EXPECT().Predecessors("y").Return([]string{"p1"}).Once()
	repo.EXPECT().Nodes().Return([]string{"a", "b", "c"}).Once()
	g := NewGraph(repo, nil)

	assert.Equal(t, []string{"a", "b"}, g.TopoSort())
	assert.Equal(t, []string{"b", "c"}, g.ReverseClosure([]string{"seed"}))
	assert.Equal(t, nearCycles, g.NearCycles(ctx, 4))
	assert.Equal(t, map[string]int{"a": 3}, g.BlastRadius())
	assert.Equal(t, 1.75, g.NCCD())
	assert.Equal(t, paths, g.PathsFromSeeds([]string{"a"}, "b"))
	assert.Equal(t, []string{"s1"}, g.Successors("x"))
	assert.Equal(t, []string{"p1"}, g.Predecessors("y"))
	assert.Equal(t, []string{"a", "b", "c"}, g.Nodes())
}

func TestNewGraph_ProjectLookup(t *testing.T) {
	projects := map[string]*Project{
		"api/":     {Path: "api/"},
		"gateway/": {Path: "gateway/"},
	}
	// No expectation: a project lookup must not reach the repository.
	g := NewGraph(NewMockDepGraphRepository(t), projects)

	p := g.Project("api/")
	require.NotNil(t, p)
	assert.Equal(t, "api/", p.Path)

	assert.Nil(t, g.Project("missing/"))
}

func TestWithGraphObserver_ComposesOntoConfig(t *testing.T) {
	cfg := NewGraphConfig(nil)
	require.NotNil(t, cfg)

	obs := NewMockObserver(t)
	obs.EXPECT().Observe(mock.Anything).Once()
	WithGraphObserver(obs)(cfg)
	cfg.Obs.Observe(GraphEvent{Op: GraphOpBuild})
}
