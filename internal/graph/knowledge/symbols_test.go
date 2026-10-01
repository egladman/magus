package knowledge

import (
	"testing"

	"github.com/egladman/magus/internal/json"
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
	assert.Equal(t, "pkg/foo@symbols", symbolsShardName("pkg/foo"))
	assert.True(t, isSymbolsShard("pkg/foo@symbols"))
	assert.False(t, isSymbolsShard("pkg/foo"))
	assert.False(t, isSymbolsShard(runtimeShardName))
}

// A defining file carries the size FingerprintBodies counted; a file only referenced was
// never read, so it carries none rather than a guessed zero.
func TestAssembleSymbolsSizesDefiningFiles(t *testing.T) {
	syms := []types.KnowledgeSymbol{{
		Key: "example.com/foo Bar#", Label: "Bar", Language: "go", Source: "pkg/foo/foo.go:11",
		SourceLines: 40, SourceBytes: 812,
		Defs: []string{"pkg/foo/foo.go"},
		Refs: []types.KnowledgeSymbolRef{{Path: "pkg/foo/use.go", Count: 1, Lines: []int{3}}},
	}}
	out := mergeAll([]Shard{assembleSymbols("pkg/foo", syms, nil)}).Output()

	def, ok := nodeByID(out, "file:pkg/foo/foo.go")
	require.True(t, ok)
	assert.Equal(t, map[string]string{"language": "go", AttrLines: "40", AttrBytes: "812"}, def.Attrs)
	use, ok := nodeByID(out, "file:pkg/foo/use.go")
	require.True(t, ok)
	assert.Equal(t, map[string]string{"language": "go"}, use.Attrs)
}

func TestGraphDefinitions(t *testing.T) {
	syms := []types.KnowledgeSymbol{
		{Key: "example.com/foo Bar#", Label: "Bar", Source: "pkg/foo/foo.go:11", DefEndLine: 20, BodyDigest: "abc", Defs: []string{"pkg/foo/foo.go"}},
		{Key: "example.com/foo Baz.", Label: "Baz", Source: "pkg/foo/foo.go:30", Defs: []string{"pkg/foo/foo.go"}},
	}
	g := mergeAll([]Shard{assembleSymbols("pkg/foo", syms, nil)})

	out, ok := g.Definitions("symbol:example.com/foo Bar#")
	require.True(t, ok)
	assert.Equal(t, types.KnowledgeDefinitionsOutput{
		Definition:    types.KnowledgeDefinitionsDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Symbol:        "symbol:example.com/foo Bar#",
		Label:         "Bar",
		Definitions: []types.KnowledgeDefinitionSite{{
			File: "pkg/foo/foo.go", StartLine: 11, EndLine: 20, Status: types.DefinitionUnverified,
		}},
	}, out)

	// No enclosing range was recorded: the end stays 0 rather than a guess.
	out, ok = g.Definitions("symbol:example.com/foo Baz.")
	require.True(t, ok)
	assert.Equal(t, []types.KnowledgeDefinitionSite{{File: "pkg/foo/foo.go", StartLine: 30, Status: types.DefinitionUnverified}}, out.Definitions)

	_, ok = g.Definitions("symbol:example.com/foo Nope#")
	assert.False(t, ok)
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
// state (gitignored, per-worktree, present only where the scip op has run), so anything
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
			if isSymbolsShard(sh.Name) || isCoverageShard(sh.Name) {
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

// foldFixture is three indexes the way scip-go and scip-typescript write them: a Go
// package is one namespace every file defines, a TypeScript module is one namespace per
// file, and an import is a reference to the imported namespace.
func foldFixture() map[string][]types.KnowledgeSymbol {
	const (
		nsA   = "gomod m `m/internal/a`/"
		nsB   = "gomod m `m/internal/b`/"
		nsC   = "gomod m `m/internal/c`/"
		nsApp = "gomod m `m/cmd/app`/"
		nsX   = "gomod m/libs/x `m/libs/x`/"
		nsExt = "gomod golang.org/x/sync `golang.org/x/sync/errgroup`/"
		nsTa  = "npm console src/`a.ts`/"
		nsTb  = "npm console src/lib/`b.ts`/"
		nsTc  = "npm console src/`c.ts`/"
		doX   = nsX + "Do()."
	)
	ref := func(p string) types.KnowledgeSymbolRef { return types.KnowledgeSymbolRef{Path: p, Count: 1} }
	ns := func(key, lang string, defs []string, refs ...types.KnowledgeSymbolRef) types.KnowledgeSymbol {
		return types.KnowledgeSymbol{Key: key, Namespace: key, Language: lang, Defs: defs, Refs: refs}
	}
	return map[string][]types.KnowledgeSymbol{
		".": {
			ns(nsA, "go", []string{"internal/a/a.go", "internal/a/a_test.go"}),
			// a_test.go importing b and c says nothing about the package; b2.go is b itself.
			ns(nsB, "go", []string{"internal/b/b.go", "internal/b/b2.go"},
				ref("internal/a/a.go"), ref("internal/a/a_test.go"), ref("internal/b/b2.go")),
			ns(nsC, "go", []string{"internal/c/c.go"}, ref("internal/a/a_test.go")),
			ns(nsApp, "go", []string{"cmd/app/main.go"}),
			// Defined in another project's index, and by no file at all.
			ns(nsX, "go", nil, ref("cmd/app/main.go")),
			ns(nsExt, "go", nil, ref("internal/a/a.go")),
			{Key: nsC + "Run().", Namespace: nsC, Language: "go", Source: "internal/c/c.go:3",
				Calls: []types.KnowledgeSymbolCall{{Key: doX, Count: 1}}},
			{Key: doX, Namespace: nsX, Language: "go"},
		},
		"libs/x": {
			ns(nsX, "go", []string{"libs/x/x.go"}),
			{Key: doX, Namespace: nsX, Language: "go", Source: "libs/x/x.go:5", Defs: []string{"libs/x/x.go"}},
		},
		"console": {
			ns(nsTa, "typescript", []string{"console/src/a.ts"}),
			ns(nsTb, "typescript", []string{"console/src/lib/b.ts"}, ref("console/src/a.ts")),
			ns(nsTc, "typescript", []string{"console/src/c.ts"}, ref("console/src/a.ts")),
		},
	}
}

func TestFoldImportsFoldsEveryLanguageByDirectory(t *testing.T) {
	t.Parallel()

	imports := func(from, to, lang, evidence string) types.KnowledgeEdge {
		e := extractedEdge("dir:"+from, "dir:"+to, types.RelationImports, evidence)
		e.Attrs = map[string]string{types.AttrLanguage: lang}
		return e
	}
	dir := func(d, lang string) types.KnowledgeNode {
		return types.KnowledgeNode{ID: "dir:" + d, Kind: types.KindDir, Label: d, Source: d,
			Attrs: map[string]string{types.AttrLanguage: lang}}
	}
	assert.Equal(t, map[string]foldedImports{
		".": {
			nodes: []types.KnowledgeNode{dir("cmd/app", "go"), dir("internal/a", "go"), dir("internal/b", "go"), dir("internal/c", "go"), dir("libs/x", "go")},
			edges: []types.KnowledgeEdge{
				imports("cmd/app", "libs/x", "go", "cmd/app/main.go"),
				imports("internal/a", "internal/b", "go", "internal/a/a.go"),
				imports("internal/c", "libs/x", "go", "internal/c/c.go:3"),
			},
		},
		"console": {
			nodes: []types.KnowledgeNode{dir("console/src", "typescript"), dir("console/src/lib", "typescript")},
			edges: []types.KnowledgeEdge{imports("console/src", "console/src/lib", "typescript", "console/src/a.ts")},
		},
	}, foldImports(foldFixture()))
}

// The fold is stored in the shard of the index that read each import, so a symbol load
// carries its own edges and no reader refolds.
func TestAssembleSymbolShardsStoresTheFold(t *testing.T) {
	t.Parallel()

	projects := []types.TargetGraphProject{{Path: "."}, {Path: "libs/x"}, {Path: "console"}}
	shards := assembleSymbolShards(foldFixture(), projects)

	var names []string
	for _, sh := range shards {
		names = append(names, sh.Name)
	}
	assert.Equal(t, []string{"." + symbolsShardSuffix, "console" + symbolsShardSuffix, "libs/x" + symbolsShardSuffix}, names)

	importsIn := func(sh Shard) []string {
		var out []string
		for _, e := range sh.Edges {
			if e.Relation == types.RelationImports {
				out = append(out, e.Source+" -> "+e.Target)
			}
		}
		return out
	}
	assert.Equal(t, []string{"dir:cmd/app -> dir:libs/x", "dir:internal/a -> dir:internal/b", "dir:internal/c -> dir:libs/x"}, importsIn(shards[0]))
	assert.Equal(t, []string{"dir:console/src -> dir:console/src/lib"}, importsIn(shards[1]))
	assert.Empty(t, importsIn(shards[2]))

	g := mergeAll(shards)
	for _, p := range projects {
		g.AddNode(types.KnowledgeNode{ID: projectID(p.Path), Kind: types.KindProject, Label: p.Path, Source: p.Path})
	}
	assert.Empty(t, g.UndeclaredEdges())
}

// A project's symbols split by defining directory; merging every part is the unsplit shard,
// and each symbol's part holds every edge that ends at it.
func TestSplitSymbolShardPartitionsByDefiningDirectory(t *testing.T) {
	syms := []types.KnowledgeSymbol{
		{Key: "x A().", Label: "A", Source: "a/a.go:3", Defs: []string{"a/a.go"},
			Refs: []types.KnowledgeSymbolRef{{Path: "b/b.go", Count: 1, Lines: []int{4}}}},
		{Key: "x B().", Label: "B", Source: "b/b.go:1", Defs: []string{"b/b.go"},
			Calls: []types.KnowledgeSymbolCall{{Key: "x A().", Count: 1}}},
		{Key: "x Top().", Label: "Top", Source: "top.go:1", Defs: []string{"top.go"}},
		{Key: "dep Ext().", Label: "Ext", Refs: []types.KnowledgeSymbolRef{{Path: "a/a.go", Count: 2, Lines: []int{5, 6}}}},
	}
	whole := assembleSymbols(".", syms, []types.TargetGraphProject{{Path: "."}})

	parts := splitSymbolShard(".", whole)

	var names []string
	seen := map[string]string{}
	for _, p := range parts {
		names = append(names, p.Name)
		for _, n := range p.Nodes {
			require.NotContainsf(t, seen, n.ID, "%s is in %s and %s", n.ID, seen[n.ID], p.Name)
			seen[n.ID] = p.Name
		}
	}
	assert.Equal(t, []string{".@symbols", ".@symbols:a", ".@symbols:b"}, names)
	assert.Equal(t, ".@symbols:a", seen["symbol:x A()."])
	assert.Equal(t, ".@symbols", seen["symbol:x Top()."], "a top-level definition stays in the base shard")
	assert.Equal(t, ".@symbols", seen["symbol:dep Ext()."], "a symbol defined nowhere here stays in the base shard")

	a := mergeAll([]Shard{parts[1]})
	assert.True(t, hasEdgeIn(a, "file:b/b.go", "symbol:x A()."), "the reference into A rides with A")
	assert.True(t, hasEdgeIn(a, "symbol:x B().", "symbol:x A()."), "so does the call into A")

	want, err := json.Marshal(mergeAll([]Shard{whole}).Output())
	require.NoError(t, err)
	got, err := json.Marshal(mergeAll(parts).Output())
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got), "the parts merge back into the unsplit shard")
	assert.True(t, isSymbolsShard(".@symbols:a"))
	assert.Equal(t, ".", symbolsShardProject(".@symbols:a"))
}
