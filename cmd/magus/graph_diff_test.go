package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// TestStripVCSAttrsLeavesTheSourceGraphIntact guards the aliasing bug the helper's
// replace-never-mutate rule exists to prevent: Graph.Output() hands back the LIVE graph's
// attr maps by reference, so deleting in place would corrupt the graph being diffed.
func TestStripVCSAttrsLeavesTheSourceGraphIntact(t *testing.T) {
	shared := map[string]string{"vcs_last_commit": "abc", "lang": "go"}
	untouched := map[string]string{"lang": "ts"}
	g := types.KnowledgeGraphOutput{Nodes: []types.KnowledgeNode{
		{ID: "file:a.go", Attrs: shared},
		{ID: "file:b.ts", Attrs: untouched},
		{ID: "file:c.md", Attrs: map[string]string{"vcs_count": "3"}},
		{ID: "file:d.md"},
	}}

	stripVCSAttrs(&g)

	assert.Equal(t, map[string]string{"lang": "go"}, g.Nodes[0].Attrs)
	assert.Equal(t, map[string]string{"vcs_last_commit": "abc", "lang": "go"}, shared,
		"the caller's map must survive; Output() shares it with the live graph")

	// A node with nothing to strip keeps the very same map rather than a copy.
	assert.Equal(t, untouched, g.Nodes[1].Attrs)

	// Stripping the last attr yields nil, not an empty map: an empty map serializes as
	// `attrs: {}` and would report as a change against a base that omits the key.
	assert.Nil(t, g.Nodes[2].Attrs)
	assert.Nil(t, g.Nodes[3].Attrs)
}

func TestBaselineHasSymbols(t *testing.T) {
	assert.False(t, baselineHasSymbols(types.KnowledgeGraphOutput{}))
	assert.False(t, baselineHasSymbols(types.KnowledgeGraphOutput{
		Nodes: []types.KnowledgeNode{{ID: "target:build", Kind: types.KindTarget}},
	}))
	assert.True(t, baselineHasSymbols(types.KnowledgeGraphOutput{
		Nodes: []types.KnowledgeNode{
			{ID: "target:build", Kind: types.KindTarget},
			{ID: "sym:Open", Kind: types.KindSymbol},
		},
	}))
}

// sampleGraphDiff is one diff carrying every section a renderer can emit, so the three
// projections below are compared against the SAME data rather than each against a fixture
// shaped to flatter it.
func sampleGraphDiff() types.KnowledgeGraphDiff {
	return types.KnowledgeGraphDiff{
		Base: "HEAD~1",
		NodesAdded: []types.KnowledgeNode{
			{ID: "target:web:build", Kind: types.KindTarget, Label: "build"},
		},
		NodesRemoved: []types.KnowledgeNode{
			{ID: "target:web:lint", Kind: types.KindTarget, Label: "lint"},
		},
		NodesChanged: []types.KnowledgeNodeChange{{
			ID:     "project:web",
			Fields: []string{"label", "doc", "source", "kind", "attrs"},
			Before: types.KnowledgeNode{Kind: "project", Label: "web", Doc: "", Source: "web/magusfile.buzz"},
			After: types.KnowledgeNode{
				Kind:   "project",
				Label:  "web-app",
				Doc:    strings.Repeat("x", 60),
				Source: "web/magusfile.buzz",
				Attrs:  map[string]string{"lang": "ts", "spell": "typescript"},
			},
		}},
		EdgesAdded: []types.KnowledgeEdge{
			{Source: "project:web", Target: "target:web:build", Relation: "declares"},
		},
		EdgesRemoved: []types.KnowledgeEdge{
			{Source: "project:web", Target: "target:web:lint", Relation: "declares"},
		},
	}
}

// TestDiffNamesIsOnlyIDs pins the -o name contract: one node id per line and nothing else,
// because the projection exists to be fed to another tool.
func TestDiffNamesIsOnlyIDs(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, diffNames(sampleGraphDiff()))
	})

	assert.Equal(t, []string{"target:web:build", "target:web:lint", "project:web"},
		strings.Fields(strings.TrimSpace(out)))
}

func TestDiffTextCountsBeforeTheList(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, diffText(sampleGraphDiff()))
	})

	assert.Contains(t, out, "graph diff against HEAD~1")
	assert.Contains(t, out, "nodes: +1 -1 ~1")
	assert.Contains(t, out, "edges: +1 -1")
	assert.Contains(t, out, "+ target:web:build")
	assert.Contains(t, out, "- target:web:lint")
	assert.Contains(t, out, "~ project:web")
	assert.Less(t, strings.Index(out, "nodes: +1"), strings.Index(out, "+ target:web:build"))
}

func TestRenderDiffMarkdownCarriesEverySection(t *testing.T) {
	md := string(renderDiffMarkdown(sampleGraphDiff()))

	assert.Contains(t, md, "# Knowledge graph diff")
	assert.Contains(t, md, "Base: `HEAD~1`. Nodes +1 -1 ~1; edges +1 -1.")
	assert.Contains(t, md, "## Nodes added")
	assert.Contains(t, md, "## Nodes removed")
	assert.Contains(t, md, "## Nodes changed")
	assert.Contains(t, md, "## Edges added")
	assert.Contains(t, md, "## Edges removed")
	assert.Contains(t, md, "`project:web` --declares--> `target:web:build`")

	// An empty diff renders the headline and no section headings, so a CI comment on a
	// change that moved no graph structure says exactly that.
	empty := string(renderDiffMarkdown(types.KnowledgeGraphDiff{Base: "main"}))
	assert.Contains(t, empty, "Nodes +0 -0 ~0; edges +0 -0.")
	assert.NotContains(t, empty, "## ")
}

// TestChangeDetailNamesBothSides covers the before -> after cell: each changed field on its
// own line, empties made visible, long values clipped, and attrs summarized rather than
// dumped (the full maps live in -o json).
func TestChangeDetailNamesBothSides(t *testing.T) {
	detail := changeDetail(sampleGraphDiff().NodesChanged[0])
	lines := strings.Split(detail, "<br>")
	require.Len(t, lines, 5)

	assert.Contains(t, lines[0], "label: `web` -> `web-app`")
	assert.Contains(t, lines[1], "doc: `(empty)` ->")
	assert.Contains(t, lines[1], "...`", "a 60-char doc must be clipped for the table cell")
	assert.Contains(t, lines[2], "source: `web/magusfile.buzz` -> `web/magusfile.buzz`")
	assert.Contains(t, lines[3], "kind: `project` -> `project`")
	assert.Contains(t, lines[4], "attrs: `0 attrs` -> `2 attrs`")
}

func TestNodeFieldReadsEveryDiffableField(t *testing.T) {
	n := types.KnowledgeNode{
		Kind:   "project",
		Label:  "web",
		Doc:    "the storefront",
		Source: "web/magusfile.buzz",
		Attrs:  map[string]string{"lang": "ts"},
	}

	assert.Equal(t, "project", nodeField(n, "kind"))
	assert.Equal(t, "web", nodeField(n, "label"))
	assert.Equal(t, "the storefront", nodeField(n, "doc"))
	assert.Equal(t, "web/magusfile.buzz", nodeField(n, "source"))
	assert.Equal(t, "1 attrs", nodeField(n, "attrs"))
	// Anything unrecognized falls into the attrs arm rather than returning "".
	assert.Equal(t, "1 attrs", nodeField(n, "something-new"))
}

func TestClipMakesEmptyVisibleAndBoundsTheCell(t *testing.T) {
	assert.Equal(t, "(empty)", clip(""))
	assert.Equal(t, "short", clip("short"))

	long := clip(strings.Repeat("a", 100))
	assert.Len(t, long, 40)
	assert.True(t, strings.HasSuffix(long, "..."))
}

func TestNodeAndEdgeItemsCarryTheirKindAndRelation(t *testing.T) {
	assert.Equal(t, []string{"`a` [target]", "`b` [project]"}, nodeItems([]types.KnowledgeNode{
		{ID: "a", Kind: types.KindTarget},
		{ID: "b", Kind: "project"},
	}))

	assert.Equal(t, []string{"`a` --needs--> `b`"}, edgeItems([]types.KnowledgeEdge{
		{Source: "a", Target: "b", Relation: "needs"},
	}))

	assert.Empty(t, nodeItems(nil))
	assert.Empty(t, edgeItems(nil))
}
