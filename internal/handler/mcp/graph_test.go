package mcp

import (
	"context"
	"strconv"
	"testing"

	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refsGraph builds a graph with one symbol referenced from n files, enough to page
// a magus_refs response.
func refsGraph(n int) *knowledge.Graph {
	g := knowledge.NewGraph()
	const sym = "symbol:example.com/x Foo#"
	g.AddNode(types.KnowledgeNode{ID: sym, Kind: types.KindSymbol, Label: "Foo"})
	for i := 0; i < n; i++ {
		g.AddEdge(types.KnowledgeEdge{
			Source: "file:pkg/f" + strconv.Itoa(i) + ".go", Target: sym,
			Relation: types.RelationReferences, Confidence: types.ConfidenceExtracted, Score: 1,
			Provenance: "scip count=1 lines=3",
		})
	}
	return g
}

// pagedGraph builds a small graph with n target nodes under one project, enough to
// page a "kind:target" query.
func pagedGraph(n int) *knowledge.Graph {
	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "project:pkg/a", Kind: types.KindProject, Label: "pkg/a"})
	for i := 'a'; i < 'a'+rune(n); i++ {
		id := "target:pkg/a:t" + string(i)
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindTarget, Label: "t" + string(i)})
		g.AddEdge(types.KnowledgeEdge{Source: "project:pkg/a", Target: id, Relation: types.RelationContains, Confidence: types.ConfidenceExtracted, Score: 1})
	}
	return g
}

// fakeGraphResolver is a hand-built graphResolver: it hands back one canned graph
// (or an error) for every resolution path, so the graph tools are unit-testable
// through the seam without a real workspace.
type fakeGraphResolver struct {
	g   *knowledge.Graph
	err error
}

func (f fakeGraphResolver) KnowledgeGraph(context.Context, bool) (*knowledge.Graph, error) {
	return f.g, f.err
}
func (f fakeGraphResolver) KnowledgeGraphWithSymbols(context.Context) (*knowledge.Graph, error) {
	return f.g, f.err
}
func (f fakeGraphResolver) KnowledgeGraphWithSymbolsForRef(context.Context, string) (*knowledge.Graph, error) {
	return f.g, f.err
}

// TestQueryToolInvokeThroughFake drives queryTool.Invoke through the graphResolver
// seam: a non-symbol query resolves the warm graph via the fake and returns the
// matching targets, demonstrating the tool is testable with a hand-built graph.
func TestQueryToolInvokeThroughFake(t *testing.T) {
	tool := &queryTool{graph: fakeGraphResolver{g: pagedGraph(3)}}
	resp, err := tool.Invoke(context.Background(), types.InvokeRequest{Params: map[string]any{"query": "kind:target"}})
	require.NoError(t, err)
	got, ok := resp.Data.(paginatedQuery)
	require.True(t, ok, "query result is a paginatedQuery")
	assert.Equal(t, 3, got.MatchCount)
	assert.Empty(t, got.NextCursor, "an unpaged query has no next cursor")
}

// The knowledge tools' graph traversal is covered by internal/knowledge and the
// CLI txtars; here we pin the MCP surface: tool names and required-param
// validation (which returns before any workspace access, so no Magus is needed).
func TestKnowledgeToolNames(t *testing.T) {
	assert.Equal(t, "magus_query", (&queryTool{}).Name())
	assert.Equal(t, "magus_output", (&outputTool{}).Name())
	assert.Equal(t, "magus_explain", (&explainTool{}).Name())
	assert.Equal(t, "magus_path", (&pathTool{}).Name())
	assert.Equal(t, "magus_stats", (&statsTool{}).Name())
	assert.Equal(t, "magus_refs", (&refsTool{}).Name())
}

// TestRegistryHasStatsDriver pins that magus_stats is both described and wired:
// registerTools panics if a descriptor lacks a driver, so a present descriptor
// plus a present driver name is the contract.
func TestRegistryHasStatsDriver(t *testing.T) {
	var described bool
	for _, d := range Registry {
		if d.Name == "magus_stats" {
			described = true
		}
	}
	assert.True(t, described, "magus_stats missing from Registry")
}

func TestKnowledgeToolRequiredParams(t *testing.T) {
	ctx := context.Background()

	_, err := (&queryTool{}).Invoke(ctx, types.InvokeRequest{})
	assert.ErrorContains(t, err, "query is required")

	_, err = (&explainTool{}).Invoke(ctx, types.InvokeRequest{})
	assert.ErrorContains(t, err, "node is required")

	_, err = (&pathTool{}).Invoke(ctx, types.InvokeRequest{Params: map[string]any{"from": "a"}})
	assert.ErrorContains(t, err, "from and to are required")
}

func TestQueryCursorRoundTrip(t *testing.T) {
	c := queryCursor{Offset: 40, QueryHash: queryHash("kind:symbol Foo"), GraphFP: "abc123"}
	got, err := decodeCursor(encodeCursor(c))
	assert.NoError(t, err)
	assert.Equal(t, c, got)
}

func TestQueryCursorRejectsGarbage(t *testing.T) {
	_, err := decodeCursor("not-a-real-cursor!!")
	assert.Error(t, err)
}

func TestQueryHashDiffersByQuery(t *testing.T) {
	assert.Equal(t, queryHash("kind:target"), queryHash("  kind:target  "), "trimmed, so whitespace does not matter")
	assert.NotEqual(t, queryHash("kind:target"), queryHash("kind:spell"))
}

// TestPagedQueryUnpaged: no limit and no cursor returns the plain result with no
// cursor attached (backward compatible).
func TestPagedQueryUnpaged(t *testing.T) {
	resp, err := pagedQuery(pagedGraph(5), "kind:target", 50, 0, "")
	require.NoError(t, err)
	assert.Equal(t, 5, resp.MatchCount)
	assert.Empty(t, resp.NextCursor, "an unpaged query has no next cursor")
}

// TestPagedQueryWalksAllPages: limit pages the matches and the returned cursor
// advances through the whole set, ending with no next_cursor.
func TestPagedQueryWalksAllPages(t *testing.T) {
	g := pagedGraph(5)
	seen := 0
	cursor := ""
	pages := 0
	for {
		resp, err := pagedQuery(g, "kind:target", 50, 2, cursor)
		require.NoError(t, err)
		assert.Equal(t, 5, resp.MatchCount)
		seen += len(resp.Matches)
		pages++
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
		require.LessOrEqual(t, pages, 5, "must terminate")
	}
	assert.Equal(t, 5, seen, "every match returned exactly once across pages")
	assert.Equal(t, 3, pages, "5 matches at limit 2 is three pages")
}

func TestPagedQueryRejectsStaleCursor(t *testing.T) {
	g := pagedGraph(5)
	first, err := pagedQuery(g, "kind:target", 50, 2, "")
	require.NoError(t, err)
	require.NotEmpty(t, first.NextCursor)

	// A cursor from a different query is rejected.
	_, err = pagedQuery(g, "kind:spell", 50, 2, first.NextCursor)
	assert.ErrorContains(t, err, "does not match this query")

	// A cursor issued against a since-changed graph is rejected.
	g.AddNode(types.KnowledgeNode{ID: "target:pkg/a:zzz", Kind: types.KindTarget, Label: "zzz"})
	_, err = pagedQuery(g, "kind:target", 50, 2, first.NextCursor)
	assert.ErrorContains(t, err, "graph changed")
}

func TestRefsToolRequiredParam(t *testing.T) {
	_, err := (&refsTool{}).Invoke(context.Background(), types.InvokeRequest{})
	assert.ErrorContains(t, err, "symbol is required")
}

func TestPagedRefsUnpaged(t *testing.T) {
	resp, err := pagedRefs(refsGraph(5), "symbol:example.com/x Foo#", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 5, resp.FileCount)
	assert.Len(t, resp.Refs, 5)
	assert.Empty(t, resp.NextCursor)
}

func TestPagedRefsWalksAllPages(t *testing.T) {
	g := refsGraph(5)
	seen, pages, cursor := 0, 0, ""
	for {
		resp, err := pagedRefs(g, "symbol:example.com/x Foo#", 2, cursor)
		require.NoError(t, err)
		assert.Equal(t, 5, resp.FileCount, "the total is stable across pages")
		seen += len(resp.Refs)
		pages++
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
		require.LessOrEqual(t, pages, 5, "must terminate")
	}
	assert.Equal(t, 5, seen, "every referencing file returned once")
	assert.Equal(t, 3, pages, "5 files at limit 2 is three pages")
}

func TestPagedRefsRejectsStaleCursor(t *testing.T) {
	g := refsGraph(5)
	first, err := pagedRefs(g, "symbol:example.com/x Foo#", 2, "")
	require.NoError(t, err)
	require.NotEmpty(t, first.NextCursor)

	// A cursor issued for a since-changed graph is rejected.
	g.AddNode(types.KnowledgeNode{ID: "symbol:example.com/x Other#", Kind: types.KindSymbol, Label: "Other"})
	_, err = pagedRefs(g, "symbol:example.com/x Foo#", 2, first.NextCursor)
	assert.ErrorContains(t, err, "graph changed")
}

func TestPagedRefsNoSuchSymbol(t *testing.T) {
	_, err := pagedRefs(refsGraph(1), "symbol:nope Missing#", 0, "")
	assert.ErrorContains(t, err, "no symbol matches")
}
