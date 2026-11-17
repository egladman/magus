package knowledge

import (
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffGraphs(t *testing.T) {
	before := types.KnowledgeGraphOutput{
		Nodes: []types.KnowledgeNode{
			{ID: "project:pkg/a", Kind: types.KindProject, Label: "pkg/a"},
			{ID: "target:pkg/a:build", Kind: types.KindTarget, Label: "build", Doc: "old doc"},
			{ID: "target:pkg/a:gone", Kind: types.KindTarget, Label: "gone"},
		},
		Links: []types.KnowledgeEdge{
			{Source: "project:pkg/a", Target: "target:pkg/a:build", Relation: types.RelationContains},
			{Source: "project:pkg/a", Target: "target:pkg/a:gone", Relation: types.RelationContains},
		},
	}
	after := types.KnowledgeGraphOutput{
		Nodes: []types.KnowledgeNode{
			{ID: "project:pkg/a", Kind: types.KindProject, Label: "pkg/a"},
			{ID: "target:pkg/a:build", Kind: types.KindTarget, Label: "build", Doc: "new doc"}, // doc changed
			{ID: "target:pkg/a:test", Kind: types.KindTarget, Label: "test"},                   // added
		},
		Links: []types.KnowledgeEdge{
			{Source: "project:pkg/a", Target: "target:pkg/a:build", Relation: types.RelationContains},
			{Source: "project:pkg/a", Target: "target:pkg/a:test", Relation: types.RelationContains}, // added
		},
	}

	d := DiffGraphs("HEAD~1", before, after)
	assert.Equal(t, "HEAD~1", d.Base)

	require.Len(t, d.NodesAdded, 1)
	assert.Equal(t, "target:pkg/a:test", d.NodesAdded[0].ID)

	require.Len(t, d.NodesRemoved, 1)
	assert.Equal(t, "target:pkg/a:gone", d.NodesRemoved[0].ID)

	require.Len(t, d.NodesChanged, 1)
	wantChange := types.KnowledgeNodeChange{
		ID:     "target:pkg/a:build",
		Fields: []string{"doc"},
		Before: before.Nodes[1],
		After:  after.Nodes[1],
	}
	assert.Equal(t, wantChange, d.NodesChanged[0])

	require.Len(t, d.EdgesAdded, 1)
	assert.Equal(t, "target:pkg/a:test", d.EdgesAdded[0].Target)
	require.Len(t, d.EdgesRemoved, 1)
	assert.Equal(t, "target:pkg/a:gone", d.EdgesRemoved[0].Target)
}

func TestDiffGraphsIdentical(t *testing.T) {
	g := types.KnowledgeGraphOutput{
		Nodes: []types.KnowledgeNode{{ID: "spell:go", Kind: types.KindSpell, Label: "go", Attrs: map[string]string{"x": "1"}}},
		Links: []types.KnowledgeEdge{{Source: "spell:go", Target: "op:go:build", Relation: types.RelationContains}},
	}
	d := DiffGraphs("HEAD", g, g)
	assert.Empty(t, d.NodesAdded)
	assert.Empty(t, d.NodesRemoved)
	assert.Empty(t, d.NodesChanged, "identical attrs are not a change")
	assert.Empty(t, d.EdgesAdded)
	assert.Empty(t, d.EdgesRemoved)
}

// TestDiffGraphsEdgeAttrChangeIgnored locks in the stated contract: edges are identified
// by (source, target, relation), so an edge whose score/confidence/provenance changed but
// kept that triple is reported as neither added, removed, nor changed.
func TestDiffGraphsEdgeAttrChangeIgnored(t *testing.T) {
	before := types.KnowledgeGraphOutput{Links: []types.KnowledgeEdge{
		{Source: "a", Target: "b", Relation: types.RelationReferences, Score: 0.9, Confidence: "extracted", Provenance: "a.go:1"},
	}}
	after := types.KnowledgeGraphOutput{Links: []types.KnowledgeEdge{
		{Source: "a", Target: "b", Relation: types.RelationReferences, Score: 0.4, Confidence: "inferred", Provenance: "a.go:2 a.go:9"},
	}}
	d := DiffGraphs("HEAD", before, after)
	assert.Empty(t, d.EdgesAdded, "same triple is not an addition")
	assert.Empty(t, d.EdgesRemoved, "same triple is not a removal")
}

func TestDiffGraphsAttrChange(t *testing.T) {
	before := types.KnowledgeGraphOutput{Nodes: []types.KnowledgeNode{
		{ID: "project:pkg/a", Kind: types.KindProject, Attrs: map[string]string{"engine": "buzz", "target_count": "2"}},
	}}
	after := types.KnowledgeGraphOutput{Nodes: []types.KnowledgeNode{
		{ID: "project:pkg/a", Kind: types.KindProject, Attrs: map[string]string{"engine": "buzz", "target_count": "3"}},
	}}
	d := DiffGraphs("HEAD", before, after)
	require.Len(t, d.NodesChanged, 1)
	assert.Equal(t, []string{"attrs"}, d.NodesChanged[0].Fields)
}

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

	StripVCSAttrs(&g)

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
	assert.False(t, BaselineHasSymbols(types.KnowledgeGraphOutput{}))
	assert.False(t, BaselineHasSymbols(types.KnowledgeGraphOutput{
		Nodes: []types.KnowledgeNode{{ID: "target:build", Kind: types.KindTarget}},
	}))
	assert.True(t, BaselineHasSymbols(types.KnowledgeGraphOutput{
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
	assert.Equal(t, []string{"target:web:build", "target:web:lint", "project:web"}, DiffNames(sampleGraphDiff()))
}

func TestDiffTextCountsBeforeTheList(t *testing.T) {
	var buf strings.Builder
	require.NoError(t, DiffText(&buf, sampleGraphDiff()))
	out := buf.String()

	assert.Contains(t, out, "graph diff against HEAD~1")
	assert.Contains(t, out, "nodes: +1 -1 ~1")
	assert.Contains(t, out, "edges: +1 -1")
	assert.Contains(t, out, "+ target:web:build")
	assert.Contains(t, out, "- target:web:lint")
	assert.Contains(t, out, "~ project:web")
	assert.Less(t, strings.Index(out, "nodes: +1"), strings.Index(out, "+ target:web:build"))
}

func TestRenderDiffMarkdownCarriesEverySection(t *testing.T) {
	md := string(RenderDiffMarkdown(sampleGraphDiff()))

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
	empty := string(RenderDiffMarkdown(types.KnowledgeGraphDiff{Base: "main"}))
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
