package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	edge := func(s, t, rel string) {
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
