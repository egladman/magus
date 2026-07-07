package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statsFixture: spell go is used (a target uses its op), spell cosign is not;
// one diagnostic is documented, one is not; one doc is isolated (orphan).
func statsFixture() *Graph {
	g := NewGraph()
	node := func(id, kind, label string) {
		g.AddNode(types.KnowledgeNode{ID: id, Kind: kind, Label: label})
	}
	edge := func(src, tgt, rel string) {
		g.AddEdge(types.KnowledgeEdge{Source: src, Target: tgt, Relation: rel, Confidence: types.ConfidenceExtracted, Score: 1})
	}
	node("spell:go", types.KindSpell, "go")
	node("op:go:build", types.KindOp, "go-build")
	edge("spell:go", "op:go:build", types.RelationContains)
	node("target:.:build", types.KindTarget, "build")
	edge("target:.:build", "op:go:build", types.RelationUses) // go is used

	node("spell:cosign", types.KindSpell, "cosign")
	node("op:cosign:sign", types.KindOp, "sign")
	edge("spell:cosign", "op:cosign:sign", types.RelationContains) // nothing uses it -> orphan

	node("diagnostic:MGS1001", types.KindDiagnostic, "MGS1001")
	node("diagnostic:MGS2001", types.KindDiagnostic, "MGS2001")
	node("doc:docs/codes/MGS1001.md", types.KindDoc, "docs/codes/MGS1001.md")
	edge("doc:docs/codes/MGS1001.md", "diagnostic:MGS1001", types.RelationDocuments)
	node("doc:docs/orphan.md", types.KindDoc, "docs/orphan.md") // isolated -> orphan
	return g
}

func TestStatsOrphans(t *testing.T) {
	s := statsFixture().Stats("")
	ids := map[string]string{}
	for _, o := range s.Orphans {
		ids[o.ID] = o.Reason
	}
	assert.Contains(t, ids, "spell:cosign", "unused spell is an orphan")
	assert.Equal(t, "no target uses it", ids["spell:cosign"])
	assert.Contains(t, ids, "doc:docs/orphan.md", "isolated doc is an orphan")
	assert.NotContains(t, ids, "spell:go", "a used spell is not an orphan")
}

func TestStatsCoverage(t *testing.T) {
	s := statsFixture().Stats("")
	var diag types.KnowledgeDocCoverage
	for _, c := range s.Coverage {
		if c.Kind == types.KindDiagnostic {
			diag = c
		}
	}
	require.Equal(t, types.KindDiagnostic, diag.Kind)
	assert.Equal(t, 2, diag.Total)
	assert.Equal(t, 1, diag.Documented)
	assert.Equal(t, 50, diag.Percent)
	assert.Equal(t, []string{"MGS2001"}, diag.Undocumented)
}

func TestStatsGodsSortedByDegree(t *testing.T) {
	s := statsFixture().Stats("")
	require.NotEmpty(t, s.Gods)
	for i := 1; i < len(s.Gods); i++ {
		assert.GreaterOrEqual(t, s.Gods[i-1].Degree, s.Gods[i].Degree, "gods sorted by degree desc")
	}
}

func TestStatsKindScope(t *testing.T) {
	s := statsFixture().Stats(types.KindSpell)
	for _, g := range s.Gods {
		assert.Equal(t, types.KindSpell, g.Kind, "kind-scoped gods are all spells")
	}
	// Coverage scoped to a non-documentable... spell is documentable, so it appears.
	for _, c := range s.Coverage {
		assert.Equal(t, types.KindSpell, c.Kind)
	}
}
