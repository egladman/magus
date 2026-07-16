package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQualified(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "spell:go", Kind: types.KindSpell, Label: "go"})
	g.AddNode(types.KnowledgeNode{ID: "op:go:go-build", Kind: types.KindOp, Label: "go-build"})
	g.AddEdge(types.KnowledgeEdge{Source: "spell:go", Target: "op:go:go-build", Relation: types.RelationContains})

	q := Qualified(g, "web")
	out := q.Output()

	ids := make([]string, len(out.Nodes))
	for i, n := range out.Nodes {
		ids[i] = n.ID
	}
	assert.ElementsMatch(t, []string{"web//spell:go", "web//op:go:go-build"}, ids)
	require.Len(t, out.Links, 1)
	assert.Equal(t, "web//spell:go", out.Links[0].Source)
	assert.Equal(t, "web//op:go:go-build", out.Links[0].Target)

	// The original graph is untouched: its IDs stay unqualified.
	for _, n := range g.Nodes() {
		assert.NotContains(t, n.ID, QualifierSep, "Qualified must not mutate the input graph")
	}
}

func TestUnionIntoDistinctWorkspaces(t *testing.T) {
	a := NewGraph()
	a.AddNode(types.KnowledgeNode{ID: "spell:go", Kind: types.KindSpell})
	b := NewGraph()
	b.AddNode(types.KnowledgeNode{ID: "spell:go", Kind: types.KindSpell}) // same ID in another repo

	merged := NewGraph()
	UnionInto(merged, Qualified(a, "api"))
	UnionInto(merged, Qualified(b, "web"))

	out := merged.Output()
	// Qualification keeps the same-named spell from two repos as distinct nodes.
	assert.Equal(t, 2, out.NodeCount)
	_, okA := nodeByID(out, "api//spell:go")
	_, okB := nodeByID(out, "web//spell:go")
	assert.True(t, okA && okB, "both workspaces' nodes present and distinct")
}
