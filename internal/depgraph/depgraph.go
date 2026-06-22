// Package depgraph constructs the project dependency DAG, translating path strings to node IDs.
package depgraph

import (
	"context"
	"fmt"

	"github.com/egladman/magus/types"
)

type engine struct{ g *Graph }

func (e *engine) TopoSort() []string {
	ids := e.g.TopoOrder()
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i], _ = e.g.Path(id)
	}
	return out
}

func (e *engine) ReverseClosure(seeds []string) []string {
	seedIDs := make([]ID, 0, len(seeds))
	for _, s := range seeds {
		if id, ok := e.g.ID(s); ok {
			seedIDs = append(seedIDs, id)
		}
	}
	resultIDs := e.g.ReverseClosure(nil, seedIDs)
	out := make([]string, len(resultIDs))
	for i, id := range resultIDs {
		out[i], _ = e.g.Path(id)
	}
	return out
}

func (e *engine) NearCycles(ctx context.Context, maxDepth int) []types.NearCycle {
	rawNCs := e.g.NearCycles(ctx, maxDepth)
	if rawNCs == nil {
		return nil
	}
	out := make([]types.NearCycle, len(rawNCs))
	for i, nc := range rawNCs {
		bp := make([]string, len(nc.BackPath))
		for j, id := range nc.BackPath {
			bp[j], _ = e.g.Path(id)
		}
		from, _ := e.g.Path(nc.From)
		to, _ := e.g.Path(nc.To)
		out[i] = types.NearCycle{From: from, To: to, BackPath: bp}
	}
	return out
}

func (e *engine) BlastRadius() map[string]int {
	rawBR := e.g.BlastRadius()
	out := make(map[string]int, len(rawBR))
	for id, count := range rawBR {
		if path, ok := e.g.Path(ID(id)); ok {
			out[path] = int(count)
		}
	}
	return out
}

func (e *engine) NCCD() float64 {
	return e.g.NCCD()
}

func (e *engine) PathsFromSeeds(seeds []string, target string) []types.AffectedPath {
	targetID, ok := e.g.ID(target)
	if !ok {
		return nil
	}
	seedIDs := make([]ID, 0, len(seeds))
	for _, s := range seeds {
		if id, ok := e.g.ID(s); ok {
			seedIDs = append(seedIDs, id)
		}
	}
	rawPaths := e.g.PathsFromSeeds(targetID, seedIDs, nil)
	out := make([]types.AffectedPath, len(rawPaths))
	for i, ap := range rawPaths {
		chain := make([]string, len(ap.Chain))
		for j, id := range ap.Chain {
			chain[j], _ = e.g.Path(id)
		}
		seed, _ := e.g.Path(ap.Seed)
		out[i] = types.AffectedPath{Seed: seed, Chain: chain}
	}
	return out
}

func (e *engine) Successors(path string) []string {
	id, ok := e.g.ID(path)
	if !ok {
		return nil
	}
	var out []string
	for sid := range e.g.Successors(id) {
		if p, ok := e.g.Path(sid); ok {
			out = append(out, p)
		}
	}
	return out
}

func (e *engine) Predecessors(path string) []string {
	id, ok := e.g.ID(path)
	if !ok {
		return nil
	}
	var out []string
	for pid := range e.g.Predecessors(id) {
		if p, ok := e.g.Path(pid); ok {
			out = append(out, p)
		}
	}
	return out
}

func (e *engine) Nodes() []string {
	var out []string
	for id := range e.g.Nodes() {
		if p, ok := e.g.Path(id); ok {
			out = append(out, p)
		}
	}
	return out
}

// graphObsAdapter bridges types.Observer to Observer.
type graphObsAdapter struct{ o types.Observer }

func (a graphObsAdapter) OnBuild(s BuildStats) {
	a.o.OnBuild(types.BuildStats{Nodes: s.Nodes, Edges: s.Edges, Duration: s.Duration})
}

func (a graphObsAdapter) OnQuery(e QueryEvent) {
	a.o.OnQuery(types.QueryEvent{Op: e.Op, Nodes: e.Nodes, Seeds: e.Seeds, Strategy: e.Strategy, ResultCount: e.ResultCount, Duration: e.Duration})
}

func (a graphObsAdapter) OnError(err error) { a.o.OnError(err) }

// Build constructs the dependency graph for the workspace.
//
// The observer from w.GraphObserver() is composed with any WithGraphObserver
// options. Cycles fail with ErrCycle. An unregistered dependency fails the
// build: every missing dep is collected and returned as *UnregisteredDepError.
func Build(w *types.Workspace, opts ...types.GraphOption) (*types.Graph, error) {
	var initial types.Observer = types.NoopObserver{}
	if obs := w.GraphObserver(); obs != nil {
		initial = obs
	}
	cfg := types.NewGraphConfig(initial)
	for _, o := range opts {
		o(cfg)
	}

	b := New()
	var missing []types.UnregisteredDep
	for _, p := range w.All() {
		fromID := b.AddNode(p.Path)
		for _, dep := range p.DependsOn {
			if w.Get(dep) == nil {
				missing = append(missing, types.UnregisteredDep{
					Consumer:   p.Path,
					Dep:        dep,
					DidYouMean: nearestProjectPath(dep, w),
				})
				continue
			}
			toID := b.AddNode(dep)
			if err := b.AddEdge(fromID, toID); err != nil {
				return nil, fmt.Errorf("magus/depgraph: add edge %s -> %s: %w", p.Path, dep, err)
			}
		}
	}

	if len(missing) > 0 {
		return nil, &types.UnregisteredDepError{Missing: missing}
	}

	g, err := b.Build(WithObserver(graphObsAdapter{cfg.Obs}))
	if err != nil {
		return nil, fmt.Errorf("magus/depgraph: build graph: %w", err)
	}
	projects := make(map[string]*types.Project, len(w.Projects))
	for k, v := range w.Projects {
		projects[k] = v
	}
	return types.NewGraph(&engine{g}, projects), nil
}
