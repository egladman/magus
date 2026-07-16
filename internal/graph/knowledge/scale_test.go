package knowledge

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The scale tenet ("a match on a high-degree node cannot pull in the whole
// graph"; steady state is a fingerprint check, not a rebuild) is guarded here by
// invariants that hold regardless of machine speed. Wall-clock and allocation
// budgets live in the benchmarks (bench_test.go) with benchstat evidence, per the
// repo's go-ultra-optimize discipline - a timing assertion in a unit test only
// buys CI flakes.

// largeGraph builds the 16k-target synthetic fixture once per test. It is the
// same corpus the benchmarks use, so a scale regression trips here too.
func largeGraph(tb testing.TB) *Graph {
	tb.Helper()
	if testing.Short() {
		tb.Skip("skipping large-graph scale test under -short")
	}
	return mergeAll(AssembleShards(syntheticInputs(benchProjects, benchTargets)))
}

func TestLargeGraphQueryRespectsBudget(t *testing.T) {
	g := largeGraph(t)
	// Sanity: the fixture really is large (>16k targets alone), so the budget
	// bound below is meaningful rather than trivially satisfied.
	require.Greater(t, len(g.Nodes()), benchProjects*benchTargets)

	// A query seeded on a common term must never exceed its node budget, even
	// though "t003" matches thousands of nodes across the graph.
	for _, budget := range []int{10, 50, 200} {
		out := g.Query("t003", budget)
		assert.LessOrEqualf(t, len(out.Nodes), budget,
			"query budget %d exceeded: got %d nodes", budget, len(out.Nodes))
		assert.Positive(t, out.MatchCount)
	}
}

func TestLargeGraphBlastRadiusTerminates(t *testing.T) {
	g := largeGraph(t)
	// blastRadius walks the whole reachable component; on the dependency chain the
	// deepest project reaches many others. The invariant is that it terminates and
	// stays within the graph, not a specific count.
	id, ok := g.resolveOne("project:pkg/p00000")
	require.True(t, ok)
	br := g.blastRadius(id)
	assert.GreaterOrEqual(t, br, 0)
	assert.Less(t, br, len(g.Nodes()))
}

func TestWarmLoadMatchesColdBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cold-build scale test under -short")
	}
	in := syntheticInputs(benchProjects, benchTargets)
	dir := t.TempDir()
	ctx := context.Background()

	cold, err := Build(ctx, dir, BuildOptions{}, in, nil)
	require.NoError(t, err)

	// A warm Load reads only the persisted shards (no assembly) and must reproduce
	// the same graph the cold build merged in memory - the cache-first contract.
	warm, err := NewStore(dir, false, 0, nil, nil).Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, len(cold.Nodes()), len(warm.Nodes()))
	assert.Equal(t, len(cold.Edges()), len(warm.Edges()))
}
