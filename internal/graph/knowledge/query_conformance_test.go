package knowledge

import (
	"slices"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

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
