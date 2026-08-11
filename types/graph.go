package types

import "context"

// Graph is the project dependency DAG; cycles are caught at construction. The
// DepGraphRepository it wraps (the query engine) lives in repository.go.
type Graph struct {
	repo     DepGraphRepository
	projects map[string]*Project // path → project; for spell-filter render
}

// NewGraph constructs a Graph from a repository and a project map.
func NewGraph(repo DepGraphRepository, projects map[string]*Project) *Graph {
	return &Graph{repo: repo, projects: projects}
}

// GraphOption configures a call to dependency.Build (or (*Magus).Graph).
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

// TopoSort returns project paths in topological order (dependents before dependencies -
// see View's doc comment for the measured example).
func (g *Graph) TopoSort() []string {
	return g.repo.TopoSort()
}

// ReverseClosure returns every project that transitively depends on any seed (seeds included).
func (g *Graph) ReverseClosure(seeds []string) []string {
	return g.repo.ReverseClosure(seeds)
}

// NearCycle describes a pair where adding From→To would close a cycle.
type NearCycle struct {
	From, To string
	BackPath []string
}

// NearCycles returns pairs where adding From→To would create a cycle of length ≤ maxDepth.
// depth=0 disables the check. Partial results on ctx cancellation.
func (g *Graph) NearCycles(ctx context.Context, maxDepth int) []NearCycle {
	return g.repo.NearCycles(ctx, maxDepth)
}

// BlastRadius returns a map from project path to the count of projects affected by a change.
func (g *Graph) BlastRadius() map[string]int {
	return g.repo.BlastRadius()
}

// NCCD returns the Normalized Cumulative Component Dependency: the graph's CCD
// over that of a balanced binary tree of the same size (>1 means more coupling
// than a balanced tree). Named to match DepGraphRepository.NCCD() and the internal
// engines rather than spelling the acronym out only on this wrapper.
func (g *Graph) NCCD() float64 {
	return g.repo.NCCD()
}

// AffectedPath is one dependency chain from a seed to a target project.
type AffectedPath struct {
	Seed  string   // project that contained a changed file
	Chain []string // [seed, ..., target]
}

// PathsFromSeeds returns the shortest chain from each seed to target.
func (g *Graph) PathsFromSeeds(seeds []string, target string) []AffectedPath {
	return g.repo.PathsFromSeeds(seeds, target)
}

func (g *Graph) Successors(path string) []string   { return g.repo.Successors(path) }
func (g *Graph) Predecessors(path string) []string { return g.repo.Predecessors(path) }
func (g *Graph) Nodes() []string                   { return g.repo.Nodes() }
func (g *Graph) Project(path string) *Project      { return g.projects[path] }

// GraphView is the boundary object magus.graph returns: the project dependency
// DAG flattened to plain data a magusfile can walk.
//
// Nodes are in topological order, so a caller that just iterates gets a valid
// build order without sorting anything itself - which is the question a magusfile
// asks the graph most often. dependsOn is the direct-predecessor set per node, so
// the caller can still reconstruct the edges.
type GraphView struct {
	Nodes     []string
	DependsOn map[string][]string
	// BlastRadius is how many projects each node can affect, transitively. It is
	// the number a magusfile branches on to decide whether a change is safe to
	// batch, and it is already computed by the graph.
	BlastRadius map[string]int
}

// View flattens the graph into the boundary object above.
//
// Edges in this graph run DEPENDENT -> DEPENDENCY, so Successors(web) is what web
// depends on and TopoSort yields dependents before dependencies. A caller wanting a
// build order needs the reverse, which is what Nodes gives. (Measured, not assumed:
// for web depending on api, TopoSort returns [. web api] and Successors(web) is
// [api].)
func (g *Graph) View() GraphView {
	topo := g.TopoSort()
	nodes := make([]string, len(topo))
	for i, n := range topo {
		nodes[len(topo)-1-i] = n
	}
	dependsOn := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		dependsOn[n] = g.Successors(n)
	}
	return GraphView{Nodes: nodes, DependsOn: dependsOn, BlastRadius: g.BlastRadius()}
}
