package depgraph

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspace builds a *types.Workspace from a spec: each entry is
// [path, dep1, dep2, ...]. Deps that name a path not in the spec are left as-is
// (so callers can exercise the unregistered-dep path).
func workspace(entries ...[]string) *types.Workspace {
	projects := make(map[string]*types.Project, len(entries))
	for _, e := range entries {
		projects[e[0]] = &types.Project{Path: e[0], DependsOn: e[1:]}
	}
	return &types.Workspace{Root: "/repo", Projects: projects}
}

// indexOf returns the position of s in xs, or -1.
func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

// buildChain builds the diamond-free chain app -> lib -> core and returns the
// resolved graph. It is the fixture most facade tests share.
func buildChain(t *testing.T) *types.Graph {
	t.Helper()
	g, err := Build(workspace(
		[]string{"app", "lib"},
		[]string{"lib", "core"},
		[]string{"core"},
	))
	require.NoError(t, err)
	require.NotNil(t, g)
	return g
}

func TestBuild_ResolvesFacade(t *testing.T) {
	g := buildChain(t)

	// Nodes: the full node set, order-independent.
	assert.ElementsMatch(t, []string{"app", "lib", "core"}, g.Nodes())

	// Successors are direct dependencies; predecessors are direct dependents.
	assert.Equal(t, []string{"lib"}, g.Successors("app"))
	assert.Equal(t, []string{"core"}, g.Successors("lib"))
	assert.Empty(t, g.Successors("core"))
	assert.Equal(t, []string{"lib"}, g.Predecessors("core"))
	assert.Equal(t, []string{"app"}, g.Predecessors("lib"))
	assert.Empty(t, g.Predecessors("app"))
}

func TestBuild_TopoSortDepsBeforeDependents(t *testing.T) {
	g := buildChain(t)
	order := g.TopoSort()
	require.ElementsMatch(t, []string{"app", "lib", "core"}, order)

	// Kahn order starts from the in-degree-zero root (app depends on lib depends
	// on core), so a project sorts ahead of the dependencies it points at.
	assert.Less(t, indexOf(order, "app"), indexOf(order, "lib"), "app before lib")
	assert.Less(t, indexOf(order, "lib"), indexOf(order, "core"), "lib before core")
}

func TestBuild_ReverseClosure(t *testing.T) {
	g := buildChain(t)
	// Everything that transitively depends on core, seed included.
	assert.ElementsMatch(t, []string{"app", "lib", "core"}, g.ReverseClosure([]string{"core"}))
	// A leaf dependent has only itself.
	assert.ElementsMatch(t, []string{"app"}, g.ReverseClosure([]string{"app"}))
	// Unknown seeds are dropped rather than erroring.
	assert.Empty(t, g.ReverseClosure([]string{"nope"}))
}

func TestBuild_PathsFromSeeds(t *testing.T) {
	g := buildChain(t)
	paths := g.PathsFromSeeds([]string{"core"}, "app")
	require.Len(t, paths, 1)
	assert.Equal(t, types.AffectedPath{
		Seed:  "core",
		Chain: []string{"core", "lib", "app"},
	}, paths[0])

	// A target that is not in the graph yields no paths.
	assert.Empty(t, g.PathsFromSeeds([]string{"core"}, "ghost"))
}

func TestBuild_BlastRadius(t *testing.T) {
	g := buildChain(t)
	br := g.BlastRadius()
	// Every node is present.
	assert.ElementsMatch(t, []string{"app", "lib", "core"}, keysOf(br))
	// core sits deepest, so a change to it has the widest blast radius.
	assert.Greater(t, br["core"], br["app"])
	assert.GreaterOrEqual(t, br["core"], br["lib"])
}

func TestBuild_NearCyclesDetectsBackEdge(t *testing.T) {
	g := buildChain(t)
	// app -> lib -> core; adding core -> app would close a length-3 cycle.
	ncs := g.NearCycles(context.Background(), 3)
	found := false
	for _, nc := range ncs {
		if nc.From == "core" && nc.To == "app" {
			found = true
		}
	}
	assert.True(t, found, "expected a near-cycle from core to app, got %+v", ncs)
}

func TestBuild_UnregisteredDepSuggestsNearest(t *testing.T) {
	// "libx" is one edit from the real project "lib".
	_, err := Build(workspace(
		[]string{"app", "libx"},
		[]string{"lib"},
	))
	require.Error(t, err)

	var depErr *types.UnregisteredDepError
	require.ErrorAs(t, err, &depErr)
	require.Len(t, depErr.Missing, 1)
	assert.Equal(t, types.UnregisteredDep{
		Consumer:   "app",
		Dep:        "libx",
		DidYouMean: "lib",
	}, depErr.Missing[0])
}

func TestBuild_CycleFails(t *testing.T) {
	_, err := Build(workspace(
		[]string{"a", "b"},
		[]string{"b", "a"},
	))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCycle)
}

func TestBuild_EmptyWorkspace(t *testing.T) {
	g, err := Build(workspace())
	require.NoError(t, err)
	assert.Empty(t, g.Nodes())
	assert.Empty(t, g.TopoSort())
}

func keysOf(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
