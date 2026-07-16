package knowledge

import (
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
	assert.Equal(t, "target:pkg/a:build", d.NodesChanged[0].ID)
	assert.Equal(t, []string{"doc"}, d.NodesChanged[0].Fields)

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
