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

// A symbol's Calls become symbol->symbol edges, carrying the attributed count in the same
// provenance format the reference edges use so one decoder serves both.
func TestAssembleSymbolsEmitsCallEdges(t *testing.T) {
	syms := []types.KnowledgeSymbol{
		{
			Key:    "example.com/foo Caller().",
			Label:  "Caller",
			Source: "pkg/foo/foo.go:11",
			Defs:   []string{"pkg/foo/foo.go"},
			Calls:  []types.KnowledgeSymbolCall{{Key: "example.com/foo Callee().", Count: 3}},
		},
		{
			Key:    "example.com/foo Callee().",
			Label:  "Callee",
			Source: "pkg/foo/foo.go:30",
			Defs:   []string{"pkg/foo/foo.go"},
		},
	}
	out := mergeAll([]Shard{assembleSymbols("pkg/foo", syms, []types.TargetGraphProject{{Path: "pkg/foo"}})}).Output()

	e, ok := findEdge(out, "symbol:example.com/foo Caller().", "symbol:example.com/foo Callee().", types.RelationCalls)
	require.True(t, ok, "the caller reaches the callee directly, not only through their shared file")
	assert.Contains(t, e.Provenance, "count=3")

	// One decoder, one format: a call edge's provenance must read back through the same
	// parser the reference edges use, or a consumer would need to know which it holds.
	count, lines, ok := parseRefProvenance(e.Provenance)
	require.True(t, ok)
	assert.Equal(t, 3, count)
	assert.Empty(t, lines, "call sites live on the file's references edge, not repeated per pair")
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
	// The definition line (from the symbol's Source "pkg/foo/foo.go:11") is surfaced
	// so an agent can edit at the exact line without reading the whole file.
	assert.Equal(t, []int{11}, out.Defs[0].Lines)
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

func TestGraphHasSymbols(t *testing.T) {
	// A domain-only graph (a project node, no symbols) reports no index: refs uses
	// this to tell "no index built" apart from "index built, symbol absent".
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "project:pkg/a", Kind: types.KindProject, Label: "pkg/a"})
	assert.False(t, g.HasSymbols())

	g.AddNode(types.KnowledgeNode{ID: "symbol:example.com/foo Bar#", Kind: types.KindSymbol, Label: "Bar"})
	assert.True(t, g.HasSymbols())
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

// The default graph must not change when a SCIP index exists. A symbol index is CACHE
// state - gitignored, per-worktree, present only where the scip op has run - so anything
// it contributes has to stay in the lazily-loaded @symbols shards. When it did not, the
// aggregate @dirs shard minted dir nodes and @io minted produces/consumes edges for
// symbol paths, both merged into the default graph: MAGUS.md and gen/knowledge-graph.json
// then differed between a developer who had run `magus graph build` and CI, which never
// does, and the drift gate fired on the difference.
//
// The @io half was worse than nondeterministic. Those edges landed in the default graph
// while their target file nodes did not, so the committed graph carried 138 references to
// nodes it does not contain.
func TestSymbolsDoNotChangeTheDefaultGraph(t *testing.T) {
	base := sampleInputs()
	base.Root = ""

	withSyms := sampleInputs()
	withSyms.Root = ""
	withSyms.Symbols = map[string][]types.KnowledgeSymbol{
		"pkg/a": {{
			Key:    "example.com/foo Bar#",
			Label:  "Bar",
			Source: "pkg/a/deep/nested/a.go:1",
			Defs:   []string{"pkg/a/deep/nested/a.go"},
			Refs:   []types.KnowledgeSymbolRef{{Path: "pkg/a/other/b.go", Count: 1, Lines: []int{4}}},
		}},
	}

	// merge exactly as Store.Sync does: every shard except the lazily-loaded ones.
	defaultGraph := func(in Inputs) types.KnowledgeGraphOutput {
		g := NewGraph()
		for _, sh := range AssembleShards(in) {
			if IsSymbolsShard(sh.Name) || IsCoverageShard(sh.Name) {
				continue
			}
			g.Merge(sh.Nodes, sh.Edges)
		}
		return g.Output()
	}

	got, want := defaultGraph(withSyms), defaultGraph(base)
	assert.Equal(t, want.NodeCount, got.NodeCount, "a symbol index must not add default-graph nodes")
	assert.Equal(t, want.EdgeCount, got.EdgeCount, "a symbol index must not add default-graph edges")

	// And every edge in the default graph must land on a node it actually contains.
	ids := make(map[string]bool, len(got.Nodes))
	for _, n := range got.Nodes {
		ids[n.ID] = true
	}
	for _, e := range got.Links {
		require.Truef(t, ids[e.Target], "edge %s -%s-> %s targets a node the default graph does not hold", e.Source, e.Relation, e.Target)
	}
}
