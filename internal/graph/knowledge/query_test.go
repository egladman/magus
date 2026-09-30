package knowledge

import (
	"maps"
	"path"
	"slices"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleGraph() *Graph { return mergeAll(AssembleShards(sampleInputs())) }

func matchIDs(ms []types.KnowledgeMatch) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func TestSeedsLazyLayer(t *testing.T) {
	// A wildcard that reaches symbols must seed too, or the query loads no symbol
	// shards and silently returns empty (the match side and the load side must agree).
	for _, in := range []string{
		"kind:symbol Foo", "symbol:example.com/a Foo#", "relation:defines", "relation:references", "id:symbol:x",
		"kind:sym*", "kind:symbol*", "kind:*", "id:sym*",
		"relation:calls",                         // symbol->symbol calls live only in the lazy shards
		"file:internal/a/a.go", "dir:internal/a", // a Go file node exists only in the lazy shards
	} {
		assert.Truef(t, SeedsLazyLayer(in), "%q should seed symbols", in)
	}
	for _, in := range []string{
		"kind:target build", "build", "project:pkg/a", "relation:uses",
		"kind:tar*", "id:target:*", // wildcards that cannot reach symbols
		"profile:x",
	} {
		assert.Falsef(t, SeedsLazyLayer(in), "%q should NOT seed symbols", in)
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
	assert.Equal(t, "go build ./...", op.Node.Attrs[attrArgv], "the op carries the base argv")

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

func TestParseQueryOperators(t *testing.T) {
	q := parseQuery(`kind=spell project=web kind!=op id=~build`)
	assert.Equal(t, []string{"spell"}, q.fields["kind"])
	assert.Equal(t, []string{"web"}, q.fields["project"])
	assert.Equal(t, []string{"op"}, q.negFields["kind"], "!= routes to the same exclusion as -kind:op")
	require.Len(t, q.reFields["id"], 1)
	assert.True(t, q.reFields["id"][0].MatchString("target:pkg/a:build"), "=~ compiles a regex over the id target")
}

func TestResolveEqualsIsColonAlias(t *testing.T) {
	g := sampleGraph()
	assert.Equal(t, matchIDs(g.Resolve("kind:spell", 0)), matchIDs(g.Resolve("kind=spell", 0)),
		"= is the canonical spelling of the : field grammar; both resolve identically")
}

func TestResolveBangEqualsExcludesKind(t *testing.T) {
	// kind!=op is the canonical negation and must behave exactly like the compat -kind:op.
	ids := matchIDs(sampleGraph().Resolve("go kind!=op", 0))
	assert.Contains(t, ids, "spell:go")
	for _, id := range ids {
		assert.NotContains(t, id, "op:")
	}
}

func TestResolveRegexFieldMatchesID(t *testing.T) {
	ids := matchIDs(sampleGraph().Resolve(`id=~build$`, 0))
	assert.Contains(t, ids, "target:pkg/a:build")
	assert.Contains(t, ids, "target:pkg/b:build")
	assert.NotContains(t, ids, "target:pkg/a:gen")
}

func TestResolveRegexKind(t *testing.T) {
	assert.Equal(t, []string{"spell:go"}, matchIDs(sampleGraph().Resolve(`kind=~^spell$`, 0)),
		"a kind regex ranges over node kinds, here anchored to spell alone")
}

func TestResolveLimit(t *testing.T) {
	assert.Len(t, sampleGraph().Resolve("kind:target", 2), 2)
}

func TestExplainByID(t *testing.T) {
	out, ok := sampleGraph().Explain("target:pkg/a:build")
	require.True(t, ok)
	assert.Equal(t, types.KindTarget, out.Node.Kind)

	outRel := func(edges []types.KnowledgeEdgeRef, rel types.RelationID, other string) bool {
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

// TestExplainCarriesAPackagesDocsURL pins the docs URL on the card: derived for a
// package at its version attr, absent for a replaced package (its name and version
// describe different modules) and for every other kind.
func TestExplainCarriesAPackagesDocsURL(t *testing.T) {
	g := mergeAll([]Shard{assemblePackages(map[string][]types.KnowledgePackage{
		"web": {{Manager: "npm", Name: "@connectrpc/connect", Version: "2.1.2"}},
		".":   {{Manager: "gomod", Name: "example.com/fork", Version: "v1.2.0", Replaced: true}},
		"a":   {{Manager: "gomod", Name: "golang.org/x/mod", Version: "v0.38.0"}},
		"b":   {{Manager: "gomod", Name: "golang.org/x/mod", Version: "v0.37.0"}},
	})})

	for ref, want := range map[string]string{
		"package:npm @connectrpc/connect": "https://www.npmjs.com/package/@connectrpc/connect/v/2.1.2",
		"package:gomod golang.org/x/mod":  "https://pkg.go.dev/golang.org/x/mod@v0.37.0",
		"package:gomod example.com/fork":  "",
	} {
		out, ok := g.Explain(ref)
		require.True(t, ok, ref)
		assert.Equal(t, want, out.DocsURL, ref)
	}

	out, ok := sampleGraph().Explain("target:pkg/a:build")
	require.True(t, ok)
	assert.Empty(t, out.DocsURL, "only a package has a docs URL")
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
// the project node and its targets, the trap where `project:web kind:function`
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

// conformanceGraph is a fixed set of representative nodes for the grammar
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

// TestGrammarConformance is the fixture that pins the deterministic grammar (fields,
// wildcards, negation) so the Go query engine cannot silently drift. It is intended to
// become the shared cross-language fixture the docs-site search (search.js) also
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

// CouldMatchLazyLayer is the weaker "was the symbol layer relevant" question. It exists so
// an empty result does not point its reader at a layer that could never have held the
// answer: `kind:author` returning nothing has nothing to do with code symbols.
func TestCouldMatchLazyLayer(t *testing.T) {
	for _, in := range []string{
		"kind:symbol Foo", "relation:calls", "someBareName", "project:pkg/a", "",
	} {
		assert.Truef(t, CouldMatchLazyLayer(in), "%q leaves the symbol layer in scope", in)
	}
	for _, in := range []string{"kind:author", "kind:target build", "kind:spell"} {
		assert.Falsef(t, CouldMatchLazyLayer(in), "%q rules the symbol layer out itself", in)
	}
}

// dependentsFixture is a build DAG (ci -> test -> build) plus the non-depends_on edges that make
// Dependents and blastRadius disagree: a spell the targets USE, and a doc that DOCUMENTS it.
func dependentsFixture() *Graph {
	g := NewGraph()
	for _, n := range []types.KnowledgeNode{
		{ID: "target:.:build", Kind: types.KindTarget, Label: "build"},
		{ID: "target:.:test", Kind: types.KindTarget, Label: "test"},
		{ID: "target:.:ci", Kind: types.KindTarget, Label: "ci"},
		{ID: "spell:go", Kind: types.KindSpell, Label: "go"},
		{ID: "doc:go.md", Kind: types.KindDoc, Label: "go.md"},
	} {
		g.AddNode(n)
	}
	edge := func(s, t string, rel types.RelationID) {
		g.AddEdge(types.KnowledgeEdge{
			Source: s, Target: t, Relation: rel,
			Confidence: types.ConfidenceExtracted, Score: 1,
		})
	}
	edge("target:.:test", "target:.:build", types.RelationDependsOn)
	edge("target:.:ci", "target:.:test", types.RelationDependsOn)
	edge("target:.:build", "spell:go", types.RelationUses)
	edge("doc:go.md", "spell:go", types.RelationDocuments)
	return g
}

func TestDependentsWalksTransitively(t *testing.T) {
	got := dependentsFixture().Dependents("target:.:build")
	assert.ElementsMatch(t, []string{"target:.:test", "target:.:ci"}, got,
		"ci depends on test depends on build, so both rebuild")
}

// The case the browser and the engine disagreed on, and the reason Dependents exists: a spell is
// USED, never depended on, so nothing rebuilds when it changes even though a great deal of the
// graph reaches it. Reporting blastRadius as though it answered this question reads as an
// undercount in the UI and is simply a different measure.
func TestDependentsIsNotBlastRadius(t *testing.T) {
	g := dependentsFixture()
	assert.Empty(t, g.Dependents("spell:go"), "nothing depends_on a spell")
	// 4, not 2: blastRadius is transitive over every relation, so it picks up the doc and the
	// target that USE the spell and then everything behind that target as well. Empty against 4
	// on the same node, in a five-node fixture, is the whole reason these are separate methods.
	assert.Equal(t, 4, g.blastRadius("spell:go"))
}

func TestDependentsExcludesTheNodeItself(t *testing.T) {
	got := dependentsFixture().Dependents("target:.:ci")
	assert.Empty(t, got, "nothing depends on the root of the DAG")
}

func TestDependentsOnAnUnknownNodeIsNil(t *testing.T) {
	require.Nil(t, dependentsFixture().Dependents("target:.:nope"))
}

// A cycle must terminate rather than revisit. depends_on cycles are a configuration error magus
// reports, not something it refuses to walk. The seed stays OUT of its own result even when the
// cycle leads back to it: the question is what rebuilds when you change this node, and the node
// is the thing being changed.
func TestDependentsTerminatesOnACycle(t *testing.T) {
	g := dependentsFixture()
	g.AddEdge(types.KnowledgeEdge{
		Source: "target:.:build", Target: "target:.:ci", Relation: types.RelationDependsOn,
		Confidence: types.ConfidenceExtracted, Score: 1,
	})
	assert.ElementsMatch(t, []string{"target:.:test", "target:.:ci"},
		g.Dependents("target:.:build"))
}

// dirFixture is the shape the dirs, symbols and markers shards emit for three Go packages:
// mcp imports httpx, and httpx declares two calls into mcp from two files, which the graph
// holds as one dir -calls-> dir edge. A target whose name fuzzy-matches the mcp path sits
// beside them.
func dirFixture() *Graph {
	g := NewGraph()
	goDir := func(p string, attrs map[string]string) {
		all := map[string]string{types.AttrLanguage: "go"}
		maps.Copy(all, attrs)
		g.AddNode(types.KnowledgeNode{ID: "dir:" + p, Kind: types.KindDir, Label: p, Source: p, Attrs: all})
	}
	g.AddNode(types.KnowledgeNode{ID: "dir:internal", Kind: types.KindDir, Label: "internal", Source: "internal"})
	goDir("internal/httpx", map[string]string{types.AttrLayer: "transport"})
	goDir("internal/handler", nil)
	goDir("internal/handler/mcp", nil)
	g.AddNode(types.KnowledgeNode{ID: "target:.:mcp-tools-generate", Kind: types.KindTarget, Label: "mcp-tools-generate"})
	edge := func(s, t string, rel types.RelationID, attrs map[string]string) {
		g.AddEdge(types.KnowledgeEdge{Source: s, Target: t, Relation: rel, Confidence: types.ConfidenceExtracted, Score: 1, Attrs: attrs})
	}
	for _, f := range []string{"internal/httpx/a.go", "internal/httpx/b.go", "internal/handler/mcp/t.go"} {
		g.AddNode(types.KnowledgeNode{ID: "file:" + f, Kind: types.KindFile, Label: f, Source: f})
		edge("dir:"+path.Dir(f), "file:"+f, types.RelationContains, nil)
	}
	edge("dir:internal", "dir:internal/httpx", types.RelationContains, nil)
	edge("dir:internal", "dir:internal/handler", types.RelationContains, nil)
	edge("dir:internal/handler", "dir:internal/handler/mcp", types.RelationContains, nil)
	edge("dir:internal/handler/mcp", "dir:internal/httpx", types.RelationImports, map[string]string{types.AttrLanguage: "go"})
	edge("dir:internal/httpx", "dir:internal/handler/mcp", types.RelationCalls, map[string]string{types.AttrTransport: "http"})
	for _, site := range []struct{ file, line, args string }{
		{"internal/httpx/a.go", "12", "internal/handler/mcp http"},
		{"internal/httpx/b.go", "40", "internal/handler/mcp grpc"},
	} {
		id := "marker:" + site.file + ":" + site.line
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindMarker, Label: "magus:calls", Source: site.file + ":" + site.line, Attrs: map[string]string{
			types.AttrMarkerFamily: string(types.MarkerCalls), types.AttrMarkerVerb: string(types.MarkerPoint),
			types.AttrMarkerArgs: site.args, types.AttrLine: site.line,
		}})
		edge("file:"+site.file, id, types.RelationContains, nil)
		edge(id, "dir:internal/handler/mcp", types.RelationReferences, nil)
	}
	return g
}

var fixtureLayers = map[string]string{"internal/httpx": "transport", "internal/handler/**": "handler"}

func TestResolveTriesThePathBeforeRanking(t *testing.T) {
	g := dirFixture()
	for ref, want := range map[string]struct {
		id  string
		how types.KnowledgeResolution
	}{
		"dir:internal/handler/mcp":    {"dir:internal/handler/mcp", types.ResolvedID},
		"internal/handler/mcp":        {"dir:internal/handler/mcp", types.ResolvedPath},
		"./internal/handler/mcp":      {"dir:internal/handler/mcp", types.ResolvedPath},
		"internal/httpx/a.go":         {"file:internal/httpx/a.go", types.ResolvedPath},
		"mcp-tools-generate":          {"target:.:mcp-tools-generate", types.ResolvedFuzzy},
		"target:.:mcp-tools-generate": {"target:.:mcp-tools-generate", types.ResolvedID},
	} {
		out, ok := g.Explain(ref)
		require.True(t, ok, ref)
		assert.Equal(t, want.id, out.Node.ID, ref)
		assert.Equal(t, want.how, out.Resolution, ref)
	}
}

// A path with no node falls to ranking, and says so.
func TestResolveLabelsAFuzzyAnswerForAPath(t *testing.T) {
	out, ok := dirFixture().Explain("internal/handler/mc")
	require.True(t, ok)
	assert.Equal(t, types.ResolvedFuzzy, out.Resolution)
}

func TestPathShaped(t *testing.T) {
	for _, in := range []string{"internal/httpx", "./a/b", "a\\b", "/abs/p", "C:\\x\\y"} {
		assert.Truef(t, pathShaped(in), "%q", in)
	}
	for _, in := range []string{"build", "kind=dir a/b", "project=pkg/a", "target:pkg/a:build", "a/*", "https://x.y/z", ""} {
		assert.Falsef(t, pathShaped(in), "%q", in)
	}
}

func TestSeedsLazyLayerOnPathsAndLayers(t *testing.T) {
	for _, in := range []string{"internal/httpx", "layer=handler", "layer=~hand"} {
		assert.Truef(t, SeedsLazyLayer(in), "%q", in)
	}
	for _, in := range []string{"family=calls", "kind=marker stamp=schema=16"} {
		assert.Falsef(t, SeedsLazyLayer(in), "%q", in)
	}
}

func TestExplainCarriesEdgeAttrs(t *testing.T) {
	out, ok := dirFixture().Explain("internal/httpx")
	require.True(t, ok)
	var calls []types.KnowledgeEdgeRef
	for _, e := range out.Out {
		if e.Relation == types.RelationCalls {
			calls = append(calls, e)
		}
	}
	require.Len(t, calls, 1)
	assert.Equal(t, map[string]string{types.AttrTransport: "http"}, calls[0].Attrs)
}

func TestDirRecord(t *testing.T) {
	g := dirFixture()
	d, err := g.Dir("internal/httpx", fixtureLayers)
	require.NoError(t, err)
	assert.Equal(t, types.Dir{
		Path: "internal/httpx", ID: "dir:internal/httpx", Layer: "transport", Language: "go",
		Imports: []string{}, ImportedBy: []string{"internal/handler/mcp"}, ImportsIndexed: true,
		Calls: []types.DirCall{
			{Dir: "internal/handler/mcp", Transport: "http", Marker: "marker:internal/httpx/a.go:12", Source: "internal/httpx/a.go:12"},
			{Dir: "internal/handler/mcp", Transport: "grpc", Marker: "marker:internal/httpx/b.go:40", Source: "internal/httpx/b.go:40"},
		},
		CalledBy: []types.DirCall{}, Children: []string{}, Files: 2,
	}, d)

	mcp, err := g.Dir("dir:internal/handler/mcp", fixtureLayers)
	require.NoError(t, err)
	assert.Equal(t, "handler", mcp.Layer, "an unstamped dir takes the declared layer")
	assert.Equal(t, []string{"internal/httpx"}, mcp.Imports)
	assert.Len(t, mcp.CalledBy, 2, "one DirCall per declaring marker")

	root, err := g.Dir("internal", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/handler", "internal/httpx"}, root.Children)
	assert.False(t, root.ImportsIndexed, "no package was indexed here")
}

func TestDirRaisesOnATypo(t *testing.T) {
	_, err := dirFixture().Dir("internal/htpx", nil)
	require.ErrorIs(t, err, types.DirNotInGraph)
	assert.ErrorContains(t, err, `did you mean "internal/httpx"`)
}

// An edge no marker explains still reads back as one call, from the edge alone.
func TestDirCallWithoutAMarker(t *testing.T) {
	g := dirFixture()
	g.AddEdge(types.KnowledgeEdge{
		Source: "dir:internal/handler", Target: "dir:internal/httpx", Relation: types.RelationCalls,
		Confidence: types.ConfidenceDeclared, Score: 1, Attrs: map[string]string{types.AttrTransport: "exec"},
	})
	d, err := g.Dir("internal/handler", nil)
	require.NoError(t, err)
	assert.Equal(t, []types.DirCall{{Dir: "internal/httpx", Transport: "exec"}}, d.Calls)
}

func TestDirsFilters(t *testing.T) {
	g := dirFixture()
	paths := func(ds []types.Dir) []string {
		out := make([]string, len(ds))
		for i, d := range ds {
			out[i] = d.Path
		}
		return out
	}
	all, err := g.Dirs("internal/*", types.DirsOptions{}, fixtureLayers)
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/handler", "internal/httpx"}, paths(all))

	shallow, err := g.Dirs("internal/**", types.DirsOptions{Depth: 1}, fixtureLayers)
	require.NoError(t, err)
	assert.NotContains(t, paths(shallow), "internal/handler/mcp")
	assert.Contains(t, paths(shallow), "internal/httpx")

	handler, err := g.Dirs("**", types.DirsOptions{Layer: "handler"}, fixtureLayers)
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/handler", "internal/handler/mcp"}, paths(handler))

	golang, err := g.Dirs("**", types.DirsOptions{Language: "go"}, fixtureLayers)
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/handler", "internal/handler/mcp", "internal/httpx"}, paths(golang))

	_, err = g.Dirs("**", types.DirsOptions{Layer: "service"}, fixtureLayers)
	require.ErrorIs(t, err, types.LayerNotDeclared)
	_, err = g.Dirs("[", types.DirsOptions{}, nil)
	require.Error(t, err)
}

func TestLayer(t *testing.T) {
	g := dirFixture()
	l, err := g.Layer("handler", fixtureLayers)
	require.NoError(t, err)
	assert.Equal(t, "handler", l.Name)
	assert.Equal(t, []string{"internal/handler/**"}, l.Declared)
	require.Len(t, l.Dirs, 2)
	assert.Equal(t, "internal/handler/mcp", l.Dirs[1].Path)

	_, err = g.Layer("service", fixtureLayers)
	require.ErrorIs(t, err, types.LayerNotDeclared)
	assert.ErrorContains(t, err, "declared layers: handler, transport")
	_, err = g.Layer("service", nil)
	require.ErrorIs(t, err, types.LayerNotDeclared)
}

func TestQueryFiltersOnLayerFamilyAndStamp(t *testing.T) {
	g := dirFixture()
	for _, stamp := range []string{"16", "15"} {
		id := "marker:AGENTS.md:" + stamp
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindMarker, Label: "magus:skills", Attrs: map[string]string{
			types.AttrMarkerFamily: string(types.MarkerSkills), types.AttrMarkerVerb: string(types.MarkerBlock),
			types.AttrLine: stamp, "schema": stamp,
		}})
	}
	for q, want := range map[string][]string{
		"layer=transport":                       {"dir:internal/httpx"},
		"kind=dir layer!=transport language=go": {"dir:internal/handler", "dir:internal/handler/mcp"},
		"family=skills":                         {"marker:AGENTS.md:15", "marker:AGENTS.md:16"},
		"family=skills stamp!=schema=16":        {"marker:AGENTS.md:15"},
		"family=skills stamp=schema=16":         {"marker:AGENTS.md:16"},
		"stamp=15":                              {"marker:AGENTS.md:15"},
		"stamp=~^schema=1[56]$":                 {"marker:AGENTS.md:15", "marker:AGENTS.md:16"},
		"family=~^cal":                          {"marker:internal/httpx/a.go:12", "marker:internal/httpx/b.go:40"},
		"stamp=line=12":                         {}, // line is the marker's own attr, not a stamp pair
	} {
		got := matchIDs(g.Resolve(q, 0))
		slices.Sort(got)
		assert.Equal(t, want, got, q)
	}
}

func TestPathWithRelations(t *testing.T) {
	g := dirFixture()
	out, ok := g.PathWith("internal/handler/mcp", "internal/httpx", types.KnowledgePathOptions{Relations: []types.RelationID{types.RelationImports}})
	require.True(t, ok)
	assert.True(t, out.Found)
	assert.Equal(t, []types.KnowledgePathStep{{From: "dir:internal/handler/mcp", To: "dir:internal/httpx", Relation: types.RelationImports, Forward: true}}, out.Steps)

	none, ok := g.PathWith("internal/handler/mcp", "internal/httpx", types.KnowledgePathOptions{Relations: []types.RelationID{types.RelationDependsOn}})
	require.True(t, ok)
	assert.False(t, none.Found)
}

func TestNeighborhoodOfWalksDepthRelationsAndDirection(t *testing.T) {
	g := dirFixture()
	nodeIDs := func(out types.KnowledgeNeighborhoodOutput) []string {
		ids := make([]string, len(out.Nodes))
		for i, n := range out.Nodes {
			ids[i] = n.ID
		}
		return ids
	}
	imports := []types.RelationID{types.RelationImports}

	out, ok := g.NeighborhoodOf("internal/handler/mcp", types.KnowledgeNeighborhoodOptions{Relations: imports})
	require.True(t, ok)
	assert.Equal(t, "dir:internal/handler/mcp", out.Focus)
	assert.Equal(t, types.ResolvedPath, out.Resolution)
	assert.Equal(t, 1, out.Options.Depth, "0 means 1")
	assert.Equal(t, []string{"dir:internal/handler/mcp", "dir:internal/httpx"}, nodeIDs(out))
	require.Len(t, out.Links, 1)
	assert.Equal(t, types.RelationImports, out.Links[0].Relation)

	in, ok := g.NeighborhoodOf("internal/handler/mcp", types.KnowledgeNeighborhoodOptions{Relations: imports, Direction: types.EdgeIn})
	require.True(t, ok)
	assert.Equal(t, []string{"dir:internal/handler/mcp"}, nodeIDs(in), "nothing imports mcp")

	deep, ok := g.NeighborhoodOf("dir:internal", types.KnowledgeNeighborhoodOptions{Depth: 2, Relations: []types.RelationID{types.RelationContains}, Direction: types.EdgeOut})
	require.True(t, ok)
	assert.Contains(t, nodeIDs(deep), "dir:internal/handler/mcp")
	assert.NotContains(t, nodeIDs(deep), "file:internal/handler/mcp/t.go", "three hops out")

	_, ok = g.NeighborhoodOf("nonesuch-xyz", types.KnowledgeNeighborhoodOptions{})
	assert.False(t, ok)
}

func TestNeighborhoodOfCollapsesByPrefix(t *testing.T) {
	g := dirFixture()
	out, ok := g.NeighborhoodOf("internal/httpx", types.KnowledgeNeighborhoodOptions{
		Depth: 2, Collapse: []string{"internal/handler", "internal/httpx/"},
	})
	require.True(t, ok)
	for _, n := range out.Nodes {
		assert.NotContains(t, []string{"dir:internal/handler/mcp", "file:internal/httpx/a.go", "marker:internal/httpx/a.go:12"}, n.ID, "folded away")
	}
	assert.Equal(t, []types.KnowledgeFold{
		{Prefix: "internal/handler", Node: "dir:internal/handler", Folded: 2},
		{Prefix: "internal/httpx", Node: "dir:internal/httpx", Folded: 4},
	}, out.Folds)
	var calls, self int
	for _, e := range out.Links {
		if e.Source == e.Target {
			self++
		}
		if e.Relation == types.RelationCalls {
			calls++
			assert.Equal(t, "dir:internal/httpx", e.Source)
			assert.Equal(t, "dir:internal/handler", e.Target)
		}
	}
	assert.Zero(t, self, "an edge folded onto itself is dropped")
	assert.Equal(t, 1, calls, "re-pointed duplicates merge")
}

func TestDirsGlobBaseAndSegments(t *testing.T) {
	assert.Equal(t, "internal", globBase("internal/**"))
	assert.Equal(t, "", globBase("**/gen"))
	assert.Equal(t, "a/b", globBase("a/b"))
	assert.Equal(t, 2, segmentsBelow("internal", "internal/a/b"))
	assert.Equal(t, 0, segmentsBelow("internal", "internal"))
	assert.Equal(t, 1, segmentsBelow("", "a"))
}
