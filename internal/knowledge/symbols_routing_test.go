package knowledge

import (
	"context"
	"os"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// symsFixture: S1 is defined in pkg/a and referenced in pkg/b; S2 only in pkg/a. So
// S1's routing spans both shards, S2's spans one - the reverse-lookup discrimination.
func symsFixture(t *testing.T) (string, Inputs) {
	cacheDir, in := buildFixture(t)
	in.Symbols = map[string][]types.KnowledgeSymbol{
		"pkg/a": {
			{Key: "S1", Label: "S1", Source: "pkg/a/a.go:1", Defs: []string{"pkg/a/a.go"}},
			{Key: "S2", Label: "S2", Source: "pkg/a/a.go:9", Defs: []string{"pkg/a/a.go"}},
		},
		"pkg/b": {
			{Key: "S1", Label: "S1", Refs: []types.KnowledgeSymbolRef{{Path: "pkg/b/b.go", Count: 1, Lines: []int{3}}}},
		},
	}
	return cacheDir, in
}

func TestBuildXref(t *testing.T) {
	in := Inputs{Symbols: map[string][]types.KnowledgeSymbol{
		"pkg/a": {{Key: "S1", Defs: []string{"pkg/a/a.go"}}, {Key: "S2", Defs: []string{"pkg/a/a.go"}}},
		"pkg/b": {{Key: "S1", Refs: []types.KnowledgeSymbolRef{{Path: "pkg/b/b.go", Count: 1, Lines: []int{3}}}}},
	}}
	xref := buildXref(AssembleShards(in))

	assert.Equal(t, []string{"pkg/a@symbols", "pkg/b@symbols"}, xref[symbolRefKey("symbol:S1")], "S1 spans both shards")
	assert.Equal(t, []string{"pkg/a@symbols"}, xref[symbolRefKey("symbol:S2")], "S2 is only in pkg/a")
}

func TestMergeSymbolShardsForTargetsRoutedShards(t *testing.T) {
	cacheDir, in := symsFixture(t)
	build(t, cacheDir, BuildOptions{}, in)
	store := NewStore(cacheDir, false, 0, nil, nil)

	// Targeting S1 loads both shards, so its cross-project reference is visible.
	g1 := NewGraph()
	require.NoError(t, store.MergeSymbolShardsFor(context.Background(), g1, []string{"symbol:S1"}))
	out, ok := g1.Refs("symbol:S1")
	require.True(t, ok)
	assert.Equal(t, 1, out.FileCount, "the pkg/b reference is loaded")

	// Targeting S2 loads only pkg/a, so pkg/b's shard (and its S1 reference) is NOT pulled in.
	g2 := NewGraph()
	require.NoError(t, store.MergeSymbolShardsFor(context.Background(), g2, []string{"symbol:S2"}))
	_, ok = g2.node("symbol:S2")
	assert.True(t, ok, "S2 is loaded")
	assert.False(t, hasEdgeIn(g2, "file:pkg/b/b.go", "symbol:S1"), "pkg/b's shard was not loaded")
}

func TestMergeSymbolShardsForFallsBackWithoutRouting(t *testing.T) {
	cacheDir, in := symsFixture(t)
	build(t, cacheDir, BuildOptions{}, in)
	store := NewStore(cacheDir, false, 0, nil, nil)
	require.NoError(t, os.Remove(store.routingPath()))

	// No routing file -> load all symbol shards, so an unknown ID still finds everything.
	g := NewGraph()
	require.NoError(t, store.MergeSymbolShardsFor(context.Background(), g, []string{"symbol:whatever"}))
	_, ok := g.node("symbol:S1")
	assert.True(t, ok, "fallback loaded all symbol shards")
}

// hasEdgeIn reports whether g has an edge from source to target (any relation).
func hasEdgeIn(g *Graph, source, target string) bool {
	for _, e := range g.Edges() {
		if e.Source == source && e.Target == target {
			return true
		}
	}
	return false
}
