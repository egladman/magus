package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssembleSymbols(t *testing.T) {
	syms := []types.KnowledgeSymbol{{
		Key:        "example.com/foo Bar#",
		Moniker:    "scip-go gomod example.com/foo v1 Bar#",
		Label:      "Bar",
		Language:   "go",
		SymbolKind: "Type",
		Source:     "pkg/foo/foo.go:11",
		Defs:       []string{"pkg/foo/foo.go"},
		Refs:       []types.KnowledgeSymbolRef{{Path: "pkg/baz/baz.go", Count: 2, Lines: []int{5, 8}}},
	}}
	projects := []types.TargetGraphProject{{Path: "pkg/foo"}, {Path: "pkg/baz"}}
	out := mergeAll([]Shard{assembleSymbols("pkg/foo", syms, projects)}).Output()

	n, ok := nodeByID(out, "symbol:example.com/foo Bar#")
	require.True(t, ok)
	assert.Equal(t, types.KindSymbol, n.Kind)
	assert.Equal(t, "Bar", n.Label)
	assert.Equal(t, "go", n.Attrs["language"])
	assert.Equal(t, "Type", n.Attrs["symbol_kind"])
	assert.Equal(t, "pkg/foo/foo.go:11", n.Source)

	// A defining file gets a defines edge; a using file gets a references edge whose
	// provenance carries the per-file count and capped lines.
	assert.True(t, hasEdge(out, "file:pkg/foo/foo.go", "symbol:example.com/foo Bar#", types.RelationDefines))
	e, ok := findEdge(out, "file:pkg/baz/baz.go", "symbol:example.com/foo Bar#", types.RelationReferences)
	require.True(t, ok)
	assert.Contains(t, e.Provenance, "count=2")
	assert.Contains(t, e.Provenance, "lines=5,8")

	// Each indexed file is a browsable node the edges land on, linked to its owning
	// project (the ref file to its own project, not this shard's).
	fn, ok := nodeByID(out, "file:pkg/foo/foo.go")
	require.True(t, ok, "the defining file is materialized as a node")
	assert.Equal(t, types.KindFile, fn.Kind)
	assert.True(t, hasEdge(out, "project:pkg/foo", "file:pkg/foo/foo.go", types.RelationContains))
	assert.True(t, hasEdge(out, "project:pkg/baz", "file:pkg/baz/baz.go", types.RelationContains),
		"a cross-project reference file is parented to its own project")
}

// TestAssembleShardsIngestsSymbols: a project with declared symbols yields a
// per-project @symbols shard in the assembled set, merged into the graph.
func TestAssembleShardsIngestsSymbols(t *testing.T) {
	in := sampleInputs()
	in.Symbols = map[string][]types.KnowledgeSymbol{
		"pkg/a": {{Key: "example.com/foo Bar#", Label: "Bar", Language: "go", Source: "pkg/a/a.go:1", Defs: []string{"pkg/a/a.go"}}},
	}
	shards := AssembleShards(in)

	var names []string
	for _, sh := range shards {
		names = append(names, sh.Name)
	}
	assert.Contains(t, names, "pkg/a@symbols", "a declared project gets an @symbols shard")

	out := mergeAll(shards).Output()
	_, ok := nodeByID(out, "symbol:example.com/foo Bar#")
	assert.True(t, ok, "the ingested symbol node is in the merged graph")
}

func TestRefProvenanceRoundTrip(t *testing.T) {
	prov := refProvenance(types.KnowledgeSymbolRef{Path: "a.go", Count: 3, Lines: []int{10, 20, 30}})
	assert.Equal(t, "scip count=3 lines=10,20,30", prov)
	count, lines, ok := parseRefProvenance(prov)
	require.True(t, ok)
	assert.Equal(t, 3, count)
	assert.Equal(t, []int{10, 20, 30}, lines)

	// A non-scip provenance (e.g. a defines edge's file path) is not a ref provenance.
	_, _, ok = parseRefProvenance("pkg/foo/foo.go")
	assert.False(t, ok)
}

func TestGraphRefs(t *testing.T) {
	syms := []types.KnowledgeSymbol{{
		Key: "example.com/foo Bar#", Label: "Bar", Source: "pkg/foo/foo.go:11",
		Defs: []string{"pkg/foo/foo.go"},
		Refs: []types.KnowledgeSymbolRef{
			{Path: "pkg/b/b.go", Count: 1, Lines: []int{3}},
			{Path: "pkg/a/a.go", Count: 2, Lines: []int{5, 8}},
		},
	}}
	g := mergeAll([]Shard{assembleSymbols("pkg/foo", syms, nil)})

	out, ok := g.Refs("symbol:example.com/foo Bar#")
	require.True(t, ok)
	assert.Equal(t, "Bar", out.Label)
	require.Len(t, out.Defs, 1)
	assert.Equal(t, "pkg/foo/foo.go", out.Defs[0].File)
	assert.Equal(t, 2, out.FileCount)
	assert.Equal(t, 3, out.RefCount, "1 + 2 occurrences")
	// Refs are sorted by file: pkg/a before pkg/b.
	require.Len(t, out.Refs, 2)
	assert.Equal(t, "pkg/a/a.go", out.Refs[0].File)
	assert.Equal(t, []int{5, 8}, out.Refs[0].Lines)
	assert.Equal(t, "pkg/b/b.go", out.Refs[1].File)
}

// TestGraphRefsPrefersSymbol: a fuzzy name that collides with a non-symbol node
// still resolves to the symbol, since refs is symbol-only.
func TestGraphRefsPrefersSymbol(t *testing.T) {
	g := mergeAll([]Shard{
		assembleSymbols("pkg/foo", []types.KnowledgeSymbol{{Key: "example.com/foo Bar#", Label: "Bar"}}, nil),
	})
	g.AddNode(types.KnowledgeNode{ID: "function:pkg/foo/foo.buzz:Bar", Kind: types.KindFunction, Label: "Bar"})

	out, ok := g.Refs("Bar")
	require.True(t, ok)
	assert.Equal(t, "symbol:example.com/foo Bar#", out.Symbol, "resolves to the symbol, not the function")

	// A ref carrying grammar tokens must not widen resolution to a non-symbol.
	_, ok = g.Refs("kind:function Bar")
	assert.False(t, ok, "grammar tokens in the ref cannot resolve a non-symbol node")
}

func TestSymbolsShardNaming(t *testing.T) {
	assert.Equal(t, "pkg/foo@symbols", SymbolsShardName("pkg/foo"))
	assert.True(t, IsSymbolsShard("pkg/foo@symbols"))
	assert.False(t, IsSymbolsShard("pkg/foo"))
	assert.False(t, IsSymbolsShard(RuntimeShardName))
}

// TestAssembleSymbolsRefOnly: a symbol seen only as a reference (its definition is in
// another index) still yields a node, with no def edge.
func TestAssembleSymbolsRefOnly(t *testing.T) {
	syms := []types.KnowledgeSymbol{{
		Key:   "other.com/dep Qux#",
		Label: "Qux",
		Refs:  []types.KnowledgeSymbolRef{{Path: "pkg/a/a.go", Count: 1, Lines: []int{3}}},
	}}
	out := mergeAll([]Shard{assembleSymbols("pkg/a", syms, nil)}).Output()

	_, ok := nodeByID(out, "symbol:other.com/dep Qux#")
	assert.True(t, ok, "reference-only symbol still gets a node")
	assert.True(t, hasEdge(out, "file:pkg/a/a.go", "symbol:other.com/dep Qux#", types.RelationReferences))
}
