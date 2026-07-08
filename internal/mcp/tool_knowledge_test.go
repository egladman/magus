//go:build mcp

package mcp

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// The knowledge tools' graph traversal is covered by internal/knowledge and the
// CLI txtars; here we pin the MCP surface: tool names and required-param
// validation (which returns before any workspace access, so no Magus is needed).
func TestKnowledgeToolNames(t *testing.T) {
	assert.Equal(t, "magus_query", (&queryTool{}).Name())
	assert.Equal(t, "magus_explain", (&explainTool{}).Name())
	assert.Equal(t, "magus_path", (&pathTool{}).Name())
	assert.Equal(t, "magus_stats", (&statsTool{}).Name())
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
