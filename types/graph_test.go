package types

import (
	"context"
	"testing"
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
	if p := g.Project("api/"); p == nil || p.Path != "api/" {
		t.Errorf("Project(api/) = %v, want {Path:api/}", p)
	}
	if p := g.Project("missing/"); p != nil {
		t.Errorf("Project(missing/) = %v, want nil", p)
	}
}

func TestWithGraphObserver_ComposesOntoConfig(t *testing.T) {
	cfg := NewGraphConfig(nil)
	if cfg == nil {
		t.Fatal("NewGraphConfig returned nil")
	}
	var called int
	obs := &countObserver{count: &called}
	WithGraphObserver(obs)(cfg)
	cfg.Obs.OnError(nil) // fires the observer
	if called != 1 {
		t.Errorf("observer called %d times, want 1", called)
	}
}

type countObserver struct{ count *int }

func (o *countObserver) OnBuild(BuildStats) {}
func (o *countObserver) OnQuery(QueryEvent) {}
func (o *countObserver) OnError(error)      { *o.count++ }
