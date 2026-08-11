package knowledge

import (
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"slices"
	"testing"
)

func sampleGraph() *Graph { return mergeAll(AssembleShards(sampleInputs())) }

func matchIDs(ms []types.KnowledgeMatch) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func TestSeedsSymbols(t *testing.T) {
	// A wildcard that reaches symbols must seed too, or the query loads no symbol
	// shards and silently returns empty (the match side and the load side must agree).
	for _, in := range []string{
		"kind:symbol Foo", "symbol:example.com/a Foo#", "relation:defines", "relation:references", "id:symbol:x",
		"kind:sym*", "kind:symbol*", "kind:*", "id:sym*",
		"relation:calls", // symbol->symbol calls live only in the lazy shards
	} {
		assert.Truef(t, SeedsSymbols(in), "%q should seed symbols", in)
	}
	for _, in := range []string{
		"kind:target build", "build", "project:pkg/a", "relation:uses",
		"kind:tar*", "id:target:*", // wildcards that cannot reach symbols
	} {
		assert.Falsef(t, SeedsSymbols(in), "%q should NOT seed symbols", in)
	}
}

func TestParseQuery(t *testing.T) {
	q := parseQuery(`kind:spell project:pkg/foo build -kind:op -legacy`)
	assert.Equal(t, []string{"build"}, q.terms)
	assert.Equal(t, []string{"legacy"}, q.negTerms)
	assert.Equal(t, []string{"spell"}, q.fields["kind"])
	assert.Equal(t, []string{"pkg/foo"}, q.fields["project"])
	assert.Equal(t, []string{"op"}, q.negFields["kind"])
}

func TestParseQueryPhrase(t *testing.T) {
	q := parseQuery(`"two words" solo`)
	assert.Equal(t, []string{"two words", "solo"}, q.terms)
}

func TestResolveByKind(t *testing.T) {
	ids := matchIDs(sampleGraph().Resolve("kind:spell", 0))
	assert.Equal(t, []string{"spell:go"}, ids)
}

// TestQueryAndExplainTool: kind:tool resolves the tool node, and explaining it shows the
// incoming op->tool and spell->tool uses edges (everything that runs the program).
func TestQueryAndExplainTool(t *testing.T) {
	in := sampleInputs()
	in.Spells[0].OpCommands = map[string][]string{
		"go-build": {"go", "build", "./..."},
	}
	g := mergeAll(AssembleShards(in))

	// The op carries the argv; the tool is its own kind.
	assert.ElementsMatch(t, []string{"tool:go"}, matchIDs(g.Resolve("kind:tool", 0)))
	op, ok := g.Explain("op:go:go-build")
	require.True(t, ok)
	assert.Equal(t, "go build ./...", op.Node.Attrs[AttrArgv], "the op carries the base argv")

	out, ok := g.Explain("tool:go")
	require.True(t, ok)
	assert.Equal(t, types.KindTool, out.Node.Kind)

	var fromOp, fromSpell bool
	for _, e := range out.In {
		if e.Relation != types.RelationUses {
			continue
		}
		switch e.Other {
		case "op:go:go-build":
			fromOp = true
		case "spell:go":
			fromSpell = true
		}
	}
	assert.True(t, fromOp, "explain tool shows the op->tool uses edge")
	assert.True(t, fromSpell, "explain tool shows the spell->tool uses edge")
}

func TestResolveByTerm(t *testing.T) {
	ids := matchIDs(sampleGraph().Resolve("build", 0))
	assert.Contains(t, ids, "target:pkg/a:build")
	assert.Contains(t, ids, "target:pkg/b:build")
	assert.NotContains(t, ids, "target:pkg/a:gen")
}

func TestResolveByProject(t *testing.T) {
	ids := matchIDs(sampleGraph().Resolve("project:pkg/a", 0))
	assert.ElementsMatch(t, []string{"project:pkg/a", "target:pkg/a:build", "target:pkg/a:gen"}, ids)
}

func TestResolveNegationExcludesKind(t *testing.T) {
	// "go" matches both spell:go and op:go:* nodes; -kind:op drops the ops.
	ids := matchIDs(sampleGraph().Resolve("go -kind:op", 0))
	assert.Contains(t, ids, "spell:go")
	for _, id := range ids {
		assert.NotContains(t, id, "op:")
	}
}

func TestResolveLimit(t *testing.T) {
	assert.Len(t, sampleGraph().Resolve("kind:target", 2), 2)
}

func TestExplainByID(t *testing.T) {
	out, ok := sampleGraph().Explain("target:pkg/a:build")
	require.True(t, ok)
	assert.Equal(t, types.KindTarget, out.Node.Kind)

	outRel := func(edges []types.KnowledgeEdgeRef, rel, other string) bool {
		for _, e := range edges {
			if e.Relation == rel && e.Other == other {
				return true
			}
		}
		return false
	}
	assert.True(t, outRel(out.Out, types.RelationDependsOn, "target:pkg/a:gen"), "out depends_on gen")
	assert.True(t, outRel(out.Out, types.RelationUses, "op:go:go-build"), "out uses op")
	assert.True(t, outRel(out.In, types.RelationContains, "project:pkg/a"), "in contains from project")
	assert.True(t, outRel(out.In, types.RelationReferences, "charm:rw"), "in referenced by charm")
	assert.GreaterOrEqual(t, out.BlastRadius, 2)
}

func TestExplainResolvesByName(t *testing.T) {
	out, ok := sampleGraph().Explain("gen")
	require.True(t, ok)
	assert.Equal(t, "target:pkg/a:gen", out.Node.ID)
}

func TestExplainUnknown(t *testing.T) {
	_, ok := sampleGraph().Explain("nonesuch-xyz")
	assert.False(t, ok)
}

func TestPathConnects(t *testing.T) {
	out, ok := sampleGraph().Path("charm:rw", "target:pkg/a:gen")
	require.True(t, ok)
	require.True(t, out.Found)
	// charm:rw --references--> build --depends_on--> gen
	require.Len(t, out.Steps, 2)
	assert.Equal(t, "charm:rw", out.Steps[0].From)
	assert.Equal(t, "target:pkg/a:gen", out.Steps[len(out.Steps)-1].To)
}

func TestPathSameNode(t *testing.T) {
	out, ok := sampleGraph().Path("target:pkg/a:build", "target:pkg/a:build")
	require.True(t, ok)
	assert.True(t, out.Found)
	assert.Empty(t, out.Steps)
}

func TestPathUnresolved(t *testing.T) {
	_, ok := sampleGraph().Path("target:pkg/a:build", "nonesuch-xyz")
	assert.False(t, ok)
}

func TestQueryNeighborhoodRespectsBudget(t *testing.T) {
	out := sampleGraph().Query("build", 3)
	assert.LessOrEqual(t, len(out.Nodes), 3)
	assert.Positive(t, out.MatchCount)
}

func TestQueryDeterministic(t *testing.T) {
	a, _ := json.Marshal(sampleGraph().Query("kind:target", 50))
	b, _ := json.Marshal(sampleGraph().Query("kind:target", 50))
	assert.Equal(t, string(a), string(b))
}

func TestQueryPagePaginatesMatches(t *testing.T) {
	g := sampleGraph()
	all := g.Query("kind:target", 50)
	require.GreaterOrEqual(t, all.MatchCount, 3, "fixture has several targets")

	var paged []string
	limit := 2
	for offset := 0; offset < all.MatchCount; offset += limit {
		page := g.QueryPage("kind:target", 50, offset, limit)
		assert.Equal(t, all.MatchCount, page.MatchCount, "total is stable across pages")
		assert.Equal(t, offset, page.Offset)
		assert.LessOrEqual(t, len(page.Matches), limit)
		paged = append(paged, matchIDs(page.Matches)...)
	}
	// Paging covers exactly the full ranked match set, in order, no dup or gap.
	assert.Equal(t, matchIDs(all.Matches), paged)
}

func TestQueryPageOffsetPastEnd(t *testing.T) {
	g := sampleGraph()
	total := g.Query("kind:target", 50).MatchCount
	page := g.QueryPage("kind:target", 50, total+10, 5)
	assert.Empty(t, page.Matches, "offset past the end yields no matches")
	assert.Equal(t, total, page.MatchCount, "but still reports the true total so a caller can stop")
}

func TestFingerprintStableAndSensitive(t *testing.T) {
	a := sampleGraph().Fingerprint()
	b := sampleGraph().Fingerprint()
	assert.Equal(t, a, b, "identical graphs fingerprint identically")

	g := sampleGraph()
	g.AddNode(types.KnowledgeNode{ID: "target:pkg/z:new", Kind: types.KindTarget, Label: "new"})
	assert.NotEqual(t, a, g.Fingerprint(), "a new node changes the fingerprint")
}

func TestSelectNeighborhoodExport(t *testing.T) {
	g := sampleGraph()
	full := g.Output()
	sub := g.Select("build", 5)
	// The selected subgraph is a real neighborhood: non-empty but smaller than
	// the whole graph, and its counts agree with the node/link slices.
	assert.Positive(t, sub.NodeCount)
	assert.Less(t, sub.NodeCount, full.NodeCount)
	assert.Equal(t, sub.NodeCount, len(sub.Nodes))
	assert.Equal(t, sub.EdgeCount, len(sub.Links))
}

func TestSelectRespectsBudget(t *testing.T) {
	assert.LessOrEqual(t, sampleGraph().Select("build", 3).NodeCount, 3)
}

func TestSelectNoMatchEmpty(t *testing.T) {
	sub := sampleGraph().Select("zzznotarealnode", 50)
	assert.Zero(t, sub.NodeCount)
	assert.Empty(t, sub.Nodes)
	assert.Empty(t, sub.Links)
}

// TestResolveProjectOwnsContainedNodes guards the project: filter reaching the
// entities a project contains (files, functions via their source path), not just
// the project node and its targets - the trap where `project:web kind:function`
// silently returned nothing.
func TestResolveProjectOwnsContainedNodes(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "project:.", Kind: types.KindProject, Label: "root", Source: "."})
	g.AddNode(types.KnowledgeNode{ID: "project:web", Kind: types.KindProject, Label: "web", Source: "web"})
	g.AddNode(types.KnowledgeNode{ID: "file:web/site.buzz", Kind: types.KindFile, Label: "site.buzz", Source: "web/site.buzz"})
	g.AddNode(types.KnowledgeNode{ID: "function:web/site.buzz:render", Kind: types.KindFunction, Label: "render", Source: "web/site.buzz:10"})
	g.AddNode(types.KnowledgeNode{ID: "file:magusfile.buzz", Kind: types.KindFile, Label: "magusfile.buzz", Source: "magusfile.buzz"})

	ids := matchIDs(g.Resolve("project:web", 0))
	assert.ElementsMatch(t, []string{"project:web", "file:web/site.buzz", "function:web/site.buzz:render"}, ids)

	// Nested ownership: the root project owns only what no nested project claims.
	ids = matchIDs(g.Resolve("project:. kind:file", 0))
	assert.Equal(t, []string{"file:magusfile.buzz"}, ids)

	// The combination that used to return zero.
	ids = matchIDs(g.Resolve("project:web kind:function render", 0))
	assert.Equal(t, []string{"function:web/site.buzz:render"}, ids)

	// Negation excludes the contained nodes too.
	ids = matchIDs(g.Resolve("kind:file -project:web", 0))
	assert.Equal(t, []string{"file:magusfile.buzz"}, ids)
}

// TestResolveFreeTextReachesFullID guards the documented "free text over IDs"
// promise: a term matching a non-leaf path segment of a slash-heavy ID (where
// LeafScore goes zero or negative) still matches, ranked below leaf hits.
func TestResolveFreeTextReachesFullID(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "function:web/deep/dir/site.buzz:render", Kind: types.KindFunction, Label: "render", Source: "web/deep/dir/site.buzz:1"})
	g.AddNode(types.KnowledgeNode{ID: "function:web/deep/dir/site.buzz:parse", Kind: types.KindFunction, Label: "parse", Source: "web/deep/dir/site.buzz:2"})

	ids := matchIDs(g.Resolve("kind:function web render", 0))
	assert.Equal(t, []string{"function:web/deep/dir/site.buzz:render"}, ids)
}

func TestGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, s string
		want       bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"build", "build", true},
		{"build", "rebuild", false}, // no wildcard = exact
		{"*build", "target:pkg/a:build", true},
		{"*build", "builder", false},
		{"target:*", "target:pkg/a:build", true},
		{"target:*:build", "target:pkg/a:build", true},
		{"pkg/*/build", "pkg/a/build", true},
		{"pkg/*/build", "pkg/a/b/build", true}, // * crosses separators
		{"Ren*", "renderPage", true},           // case-insensitive
		{"*x*y*", "axbyc", true},
		{"*x*y*", "aybxc", false}, // order matters
	} {
		assert.Equalf(t, tc.want, globMatch(tc.pattern, tc.s), "globMatch(%q, %q)", tc.pattern, tc.s)
	}
}

// conformanceGraph is a fixed corpus of representative nodes for the grammar
// conformance table. Deterministic node IDs and labels so the expected match sets are
// exact.
func conformanceGraph() *Graph {
	g := NewGraph()
	nodes := []types.KnowledgeNode{
		{ID: "project:pkg/foo", Kind: types.KindProject, Label: "pkg/foo"},
		{ID: "project:pkg/bar", Kind: types.KindProject, Label: "pkg/bar"},
		{ID: "target:pkg/foo:build", Kind: types.KindTarget, Label: "build"},
		{ID: "target:pkg/foo:gen", Kind: types.KindTarget, Label: "gen"},
		{ID: "target:pkg/bar:build", Kind: types.KindTarget, Label: "build"},
		{ID: "spell:go", Kind: types.KindSpell, Label: "go"},
		{ID: "symbol:example.com/x Render#", Kind: types.KindSymbol, Label: "Render"},
		{ID: "symbol:example.com/x Parse#", Kind: types.KindSymbol, Label: "Parse"},
	}
	for _, n := range nodes {
		g.AddNode(n)
	}
	return g
}

// TestGrammarConformance is the corpus that pins the deterministic grammar (fields,
// wildcards, negation) so the Go query engine cannot silently drift. It is intended to
// become the shared cross-language corpus the docs-site search (search.js) also
// validates against; the JS side is not wired to it yet, so for now it is a Go-only
// regression gate. Free-text fuzzy ranking is intentionally excluded; this table is
// about which nodes a query MATCHES, not their order.
func TestGrammarConformance(t *testing.T) {
	g := conformanceGraph()
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"kind:spell", []string{"spell:go"}},
		{"kind:target", []string{"target:pkg/bar:build", "target:pkg/foo:build", "target:pkg/foo:gen"}},
		{"kind:sym*", []string{"symbol:example.com/x Parse#", "symbol:example.com/x Render#"}},
		{"id:target:pkg/foo:*", []string{"target:pkg/foo:build", "target:pkg/foo:gen"}},
		{"id:*build", []string{"target:pkg/bar:build", "target:pkg/foo:build"}},
		{"project:pkg/foo", []string{"project:pkg/foo", "target:pkg/foo:build", "target:pkg/foo:gen"}},
		{"project:pkg/*", []string{"project:pkg/bar", "project:pkg/foo", "target:pkg/bar:build", "target:pkg/foo:build", "target:pkg/foo:gen"}},
		{"kind:target -id:*gen", []string{"target:pkg/bar:build", "target:pkg/foo:build"}},
		{"Render*", []string{"symbol:example.com/x Render#"}},
		{"kind:target -build", []string{"target:pkg/foo:gen"}},
	} {
		got := matchIDs(g.Resolve(tc.query, 0))
		slices.Sort(got)
		assert.Equalf(t, tc.want, got, "query %q", tc.query)
	}
}

// CouldMatchSymbol is the weaker "was the symbol layer relevant" question. It exists so
// an empty result does not point its reader at a layer that could never have held the
// answer: `kind:author` returning nothing has nothing to do with code symbols.
func TestCouldMatchSymbol(t *testing.T) {
	for _, in := range []string{
		"kind:symbol Foo", "relation:calls", "someBareName", "project:pkg/a", "",
	} {
		assert.Truef(t, CouldMatchSymbol(in), "%q leaves the symbol layer in scope", in)
	}
	for _, in := range []string{"kind:author", "kind:target build", "kind:spell"} {
		assert.Falsef(t, CouldMatchSymbol(in), "%q rules the symbol layer out itself", in)
	}
}
