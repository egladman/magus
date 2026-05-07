package types_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
)

// stubEngine satisfies types.GraphEngine with no-op implementations.
type stubEngine struct{}

func (stubEngine) TopoSort() []string                                              { return nil }
func (stubEngine) ReverseClosure([]string) []string                                { return nil }
func (stubEngine) NearCycles(context.Context, int) []types.NearCycle               { return nil }
func (stubEngine) BlastRadius() map[string]int                                     { return nil }
func (stubEngine) NCCD() float64                                                   { return 0 }
func (stubEngine) PathsFromSeeds([]string, string) []types.AffectedPath            { return nil }
func (stubEngine) Successors(string) []string                                      { return nil }
func (stubEngine) Predecessors(string) []string                                    { return nil }
func (stubEngine) Nodes() []string                                                 { return nil }

func TestNewGraph_ProjectLookup(t *testing.T) {
	projects := map[string]*types.Project{
		"api/":     {Path: "api/"},
		"gateway/": {Path: "gateway/"},
	}
	g := types.NewGraph(stubEngine{}, projects)
	if p := g.Project("api/"); p == nil || p.Path != "api/" {
		t.Errorf("Project(api/) = %v, want {Path:api/}", p)
	}
	if p := g.Project("missing/"); p != nil {
		t.Errorf("Project(missing/) = %v, want nil", p)
	}
}

func TestWithGraphObserver_ComposesOntoConfig(t *testing.T) {
	cfg := types.NewGraphConfig(nil)
	if cfg == nil {
		t.Fatal("NewGraphConfig returned nil")
	}
	var called int
	obs := &countObserver{count: &called}
	types.WithGraphObserver(obs)(cfg)
	cfg.Obs.OnError(nil) // fires the observer
	if called != 1 {
		t.Errorf("observer called %d times, want 1", called)
	}
}

type countObserver struct{ count *int }

func (o *countObserver) OnBuild(types.BuildStats) {}
func (o *countObserver) OnQuery(types.QueryEvent)  {}
func (o *countObserver) OnError(error)             { *o.count++ }
