package knowledge

import (
	"maps"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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

// DiffHistorySuite diffs a base without history against a head with it, as
// `graph diff --rev` does. Only the tree's change may count as structure.
type DiffHistorySuite struct {
	suite.Suite
}

func TestDiffHistorySuite(t *testing.T) { suite.Run(t, new(DiffHistorySuite)) }

func (s *DiffHistorySuite) base() types.KnowledgeGraphOutput {
	return types.KnowledgeGraphOutput{
		Nodes: []types.KnowledgeNode{
			{ID: "dir:pkg", Kind: types.KindDir, Attrs: map[string]string{AttrDirFiles: "2"}},
			{ID: "file:pkg/a.go", Kind: types.KindFile, Attrs: map[string]string{"language": "go"}},
			{ID: "file:pkg/b.go", Kind: types.KindFile},
			{ID: "function:pkg/a.go:run", Kind: types.KindFunction},
		},
		Links: []types.KnowledgeEdge{
			{Source: "dir:pkg", Target: "file:pkg/a.go", Relation: types.RelationContains},
			{Source: "dir:pkg", Target: "file:pkg/b.go", Relation: types.RelationContains},
		},
	}
}

// head is the base plus one call edge, with history on every node that can carry it.
func (s *DiffHistorySuite) head() types.KnowledgeGraphOutput {
	return types.KnowledgeGraphOutput{
		Nodes: []types.KnowledgeNode{
			{ID: "author:Eli", Kind: types.KindAuthor},
			{ID: "dir:pkg", Kind: types.KindDir, Attrs: map[string]string{AttrDirFiles: "2", AttrDirCommits: "7"}},
			{ID: "file:pkg/a.go", Kind: types.KindFile, Attrs: map[string]string{"language": "go", attrVCSCommits: "5", attrVCSLastCommit: "abc"}},
			{ID: "file:pkg/b.go", Kind: types.KindFile, Attrs: map[string]string{attrVCSCommits: "2"}},
			{ID: "function:pkg/a.go:run", Kind: types.KindFunction},
		},
		Links: []types.KnowledgeEdge{
			{Source: "dir:pkg", Target: "file:pkg/a.go", Relation: types.RelationContains},
			{Source: "dir:pkg", Target: "file:pkg/b.go", Relation: types.RelationContains},
			{Source: "function:pkg/a.go:run", Target: "file:pkg/b.go", Relation: types.RelationCalls},
			{Source: "author:Eli", Target: "file:pkg/a.go", Relation: types.RelationAuthored},
			{Source: "file:pkg/a.go", Target: "file:pkg/b.go", Relation: types.RelationCoChanges},
		},
	}
}

func (s *DiffHistorySuite) TestOnlyTheTreeChangeIsStructural() {
	d := DiffGraphs("main", s.base(), s.head())

	s.Empty(d.NodesAdded, "an author node is history, not structure")
	s.Empty(d.NodesRemoved)
	s.Empty(d.NodesChanged, "history attrs alone are not a node change")
	s.Equal([]types.KnowledgeEdge{
		{Source: "function:pkg/a.go:run", Target: "file:pkg/b.go", Relation: types.RelationCalls},
	}, d.EdgesAdded)
	s.Empty(d.EdgesRemoved)
}

func (s *DiffHistorySuite) TestOneSidedHistoryIsNotedNotListed() {
	d := DiffGraphs("main", s.base(), s.head())

	s.Require().NotNil(d.History)
	s.Equal(types.KnowledgeGraphHistoryDiff{
		Note: "not compared: the base carries no version-control history, and the current side has it on 4 nodes and 2 edges",
	}, *d.History)

	reversed := DiffGraphs("main", s.head(), s.base())
	s.Require().NotNil(reversed.History)
	s.Contains(reversed.History.Note, "the current side carries no version-control history")
}

func (s *DiffHistorySuite) TestHistoryOnBothSidesIsDiffed() {
	before := s.head()
	before.Nodes = before.Nodes[1:] // no author yet
	before.Nodes[0].Attrs = map[string]string{AttrDirFiles: "2", AttrDirCommits: "6"}
	before.Links = append(before.Links[:2:2], types.KnowledgeEdge{Source: "file:pkg/a.go", Target: "file:pkg/b.go", Relation: types.RelationCoChanges})

	d := DiffGraphs("main", before, s.head())

	s.Require().NotNil(d.History)
	s.Empty(d.History.Note)
	s.Equal([]types.KnowledgeNode{{ID: "author:Eli", Kind: types.KindAuthor}}, d.History.NodesAdded)
	s.Require().Len(d.History.NodesChanged, 1)
	change := d.History.NodesChanged[0]
	s.Equal("dir:pkg", change.ID)
	s.Equal(map[string]string{AttrDirCommits: "6"}, change.Before.Attrs, "a history change carries only history attrs")
	s.Equal(map[string]string{AttrDirCommits: "7"}, change.After.Attrs)
	s.Equal([]types.KnowledgeEdge{
		{Source: "author:Eli", Target: "file:pkg/a.go", Relation: types.RelationAuthored},
	}, d.History.EdgesAdded)
	s.Equal([]types.KnowledgeEdge{
		{Source: "function:pkg/a.go:run", Target: "file:pkg/b.go", Relation: types.RelationCalls},
	}, d.EdgesAdded)
}

func (s *DiffHistorySuite) TestNoHistoryChangeLeavesTheSectionOut() {
	s.Nil(DiffGraphs("main", s.base(), s.base()).History)
	s.Nil(DiffGraphs("main", s.head(), s.head()).History, "identical history is no change")
}

// TestInputsSurvive guards the aliasing hazard: Graph.Output shares attr maps with the
// live graph, so a split must never delete from them.
func (s *DiffHistorySuite) TestInputsSurvive() {
	head := s.head()
	shared := head.Nodes[2].Attrs
	want := maps.Clone(shared)

	DiffGraphs("main", s.base(), head)

	s.Equal(want, shared)
	s.Len(head.Nodes, 5)
	s.Len(head.Links, 5)
}

func (s *DiffHistorySuite) TestTextPrintsEdgesAndTheHistoryLine() {
	var buf strings.Builder
	s.Require().NoError(DiffText(&buf, DiffGraphs("main", s.base(), s.head())))

	s.Equal("graph diff against main\n"+
		"  nodes: +0 -0 ~0\n"+
		"  edges: +1 -0\n"+
		"  + function:pkg/a.go:run --calls--> file:pkg/b.go\n"+
		"  History, not compared: the base carries no version-control history, and the current side has it on 4 nodes and 2 edges.\n",
		buf.String())
}

func (s *DiffHistorySuite) TestMarkdownCarriesTheHistorySection() {
	md := string(RenderDiffMarkdown(DiffGraphs("main", s.base(), s.head())))

	s.Contains(md, "Nodes +0 -0 ~0; edges +1 -0.")
	s.Contains(md, "## History")
	s.Contains(md, "History, not compared: the base carries no version-control history")
}

func (s *DiffHistorySuite) TestTextCountsMovesAndEdgesOfRemovedNodes() {
	d := types.KnowledgeGraphDiff{
		Base:         "main",
		NodesRemoved: []types.KnowledgeNode{{ID: "doc:gone.md", Kind: types.KindDoc}},
		NodesChanged: []types.KnowledgeNodeChange{
			{ID: "function:a.buzz:run", Fields: []string{"source"}, Before: types.KnowledgeNode{Source: "a.buzz:10"}, After: types.KnowledgeNode{Source: "a.buzz:14"}},
			{ID: "function:a.buzz:walk", Fields: []string{"source"}, Before: types.KnowledgeNode{Source: "a.buzz:20"}, After: types.KnowledgeNode{Source: "b.buzz:3"}},
			{ID: "function:a.buzz:stop", Fields: []string{"doc", "source"}, Before: types.KnowledgeNode{Source: "a.buzz:30"}, After: types.KnowledgeNode{Source: "a.buzz:31"}},
		},
		EdgesRemoved: []types.KnowledgeEdge{
			{Source: "dir:.", Target: "doc:gone.md", Relation: types.RelationContains},
			{Source: "function:a.buzz:run", Target: "function:a.buzz:stop", Relation: types.RelationCalls},
		},
	}
	var buf strings.Builder
	s.Require().NoError(DiffText(&buf, d))

	s.Equal("graph diff against main\n"+
		"  nodes: +0 -1 ~3\n"+
		"  edges: +0 -2\n"+
		"  - doc:gone.md\n"+
		"  ~ function:a.buzz:walk ([source])\n"+
		"  ~ function:a.buzz:stop ([doc source])\n"+
		"  ~ 1 more nodes moved within their file (source line only)\n"+
		"  - function:a.buzz:run --calls--> function:a.buzz:stop\n"+
		"  - 1 more edges of the removed nodes\n",
		buf.String())
}

func (s *DiffHistorySuite) TestComparedHistoryIsCounted() {
	h := types.KnowledgeGraphHistoryDiff{
		NodesAdded: []types.KnowledgeNode{{ID: "author:Eli", Kind: types.KindAuthor}},
		EdgesAdded: []types.KnowledgeEdge{{Source: "author:Eli", Target: "file:a", Relation: types.RelationAuthored}},
	}
	s.Equal("History, not counted above: nodes +1 -0 ~0; edges +1 -0.", historySummary(&h))
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
