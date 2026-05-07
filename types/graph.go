package types

import (
	"context"
)

// GraphEngine is the interface that internal/depgraph implements.
type GraphEngine interface {
	TopoSort() []string
	ReverseClosure(seeds []string) []string
	NearCycles(ctx context.Context, maxDepth int) []NearCycle
	BlastRadius() map[string]int
	NCCD() float64
	PathsFromSeeds(seeds []string, target string) []AffectedPath
	Successors(path string) []string
	Predecessors(path string) []string
	Nodes() []string
}

// Graph is the project dependency DAG; cycles are caught at construction.
type Graph struct {
	eng      GraphEngine
	projects map[string]*Project // path → project; for spell-filter render
}

// NewGraph constructs a Graph from an engine and a project map.
func NewGraph(eng GraphEngine, projects map[string]*Project) *Graph {
	return &Graph{eng: eng, projects: projects}
}

// GraphOption configures a call to depgraph.Build (or (*Magus).Graph).
type GraphOption func(*GraphConfig)

// GraphConfig carries configuration for a graph build.
type GraphConfig struct {
	Obs Observer
}

// NewGraphConfig returns a GraphConfig seeded with an initial observer.
func NewGraphConfig(initial Observer) *GraphConfig {
	return &GraphConfig{Obs: initial}
}

// WithGraphObserver attaches an observer; multiple calls compose via FanOut.
func WithGraphObserver(o Observer) GraphOption {
	return func(c *GraphConfig) {
		c.Obs = FanOut(c.Obs, o)
	}
}

// TopoSort returns project paths in topological order (dependencies before dependents).
func (g *Graph) TopoSort() []string {
	return g.eng.TopoSort()
}

// ReverseClosure returns every project that transitively depends on any seed (seeds included).
func (g *Graph) ReverseClosure(seeds []string) []string {
	return g.eng.ReverseClosure(seeds)
}

// NearCycle describes a pair where adding From→To would close a cycle.
type NearCycle struct {
	From, To string
	BackPath []string
}

// NearCycles returns pairs where adding From→To would create a cycle of length ≤ maxDepth.
// depth=0 disables the check. Partial results on ctx cancellation.
func (g *Graph) NearCycles(ctx context.Context, maxDepth int) []NearCycle {
	return g.eng.NearCycles(ctx, maxDepth)
}

// BlastRadius returns a map from project path to the count of projects affected by a change.
func (g *Graph) BlastRadius() map[string]int {
	return g.eng.BlastRadius()
}

// NCCD returns the Normalized Cumulative Component Dependency: the graph's CCD
// over that of a balanced binary tree of the same size (>1 means more coupling
// than a balanced tree). Named to match GraphEngine.NCCD() and the internal
// engines rather than spelling the acronym out only on this wrapper.
func (g *Graph) NCCD() float64 {
	return g.eng.NCCD()
}

// AffectedPath is one dependency chain from a seed to a target project.
type AffectedPath struct {
	Seed  string   // project that contained a changed file
	Chain []string // [seed, ..., target]
}

// PathsFromSeeds returns the shortest chain from each seed to target.
func (g *Graph) PathsFromSeeds(seeds []string, target string) []AffectedPath {
	return g.eng.PathsFromSeeds(seeds, target)
}

func (g *Graph) Successors(path string) []string   { return g.eng.Successors(path) }
func (g *Graph) Predecessors(path string) []string { return g.eng.Predecessors(path) }
func (g *Graph) Nodes() []string                   { return g.eng.Nodes() }
func (g *Graph) Project(path string) *Project      { return g.projects[path] }
