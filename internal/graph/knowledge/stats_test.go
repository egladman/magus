package knowledge

import (
	"strconv"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statsFixture: spell go is declared and used (a target uses its op); cosign is declared
// with an op nothing uses (a dead op-provider -> orphan); rust is an undeclared builtin
// with an unused op (available, not neglect -> not an orphan); magusfile is declared but
// provides no ops (a structural dispatch spell -> not an orphan). Plus one documented and
// one undocumented diagnostic, and one isolated doc (orphan).
func statsFixture() *Graph {
	g := NewGraph()
	node := func(id, kind, label string) {
		g.AddNode(types.KnowledgeNode{ID: id, Kind: kind, Label: label})
	}
	spell := func(id, label string, declared bool) {
		var attrs map[string]string
		if declared {
			attrs = map[string]string{AttrDeclared: "true"}
		}
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindSpell, Label: label, Attrs: attrs})
	}
	edge := func(src, tgt, rel string) {
		g.AddEdge(types.KnowledgeEdge{Source: src, Target: tgt, Relation: rel, Confidence: types.ConfidenceExtracted, Score: 1})
	}
	spell("spell:go", "go", true)
	node("op:go:build", types.KindOp, "go-build")
	edge("spell:go", "op:go:build", types.RelationContains)
	node("target:.:build", types.KindTarget, "build")
	edge("target:.:build", "op:go:build", types.RelationUses) // go is used

	spell("spell:cosign", "cosign", true)
	node("op:cosign:sign", types.KindOp, "sign")
	edge("spell:cosign", "op:cosign:sign", types.RelationContains) // declared, unused -> orphan

	spell("spell:rust", "rust", false) // undeclared builtin
	node("op:rust:cargo-build", types.KindOp, "cargo-build")
	edge("spell:rust", "op:rust:cargo-build", types.RelationContains) // unused but available -> not an orphan

	spell("spell:magusfile", "magusfile", true) // declared but provides no ops -> not an orphan

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
	assert.Contains(t, ids, "spell:cosign", "a declared op-providing spell nothing uses is an orphan")
	assert.Equal(t, "declared but no target uses it", ids["spell:cosign"])
	assert.Contains(t, ids, "doc:docs/orphan.md", "isolated doc is an orphan")
	assert.NotContains(t, ids, "spell:go", "a used spell is not an orphan")
	assert.NotContains(t, ids, "spell:rust", "an undeclared builtin (available, unused) is not an orphan")
	assert.NotContains(t, ids, "spell:magusfile", "a declared spell with no ops (structural) is not an orphan")
}

func TestStatsConnectivity(t *testing.T) {
	// The fixture has one connected component around spell:go/target:.:build/op:go:build, another around
	// spell:cosign/op, another around spell:rust/op, the documented MGS1001<->doc pair, the isolated
	// spell:magusfile, the isolated diagnostic:MGS2001, and the isolated doc:docs/orphan.md.
	s := statsFixture().Stats("")
	// Two non-spell isolated nodes: diagnostic:MGS2001 and doc:docs/orphan.md (spell:magusfile is excluded).
	assert.Equal(t, 2, s.IsolatedCount, "isolated counts every 0-degree non-spell node")
	// Every node reachable in some component; a single-node isolated is its own component. The largest is
	// the go build cluster (spell:go + op:go:build + target:.:build = 3).
	assert.Equal(t, 3, s.LargestComponentSize)
	assert.Greater(t, s.ComponentCount, 1, "an unlinked graph splits into several components")
	assert.LessOrEqual(t, s.ComponentCount, s.NodeCount)
}

// TestStatsOrphanCapExemptsSpells builds more than maxOrphans isolated nodes plus a semantic spell orphan
// whose ID sorts last, and pins that the isolated list is capped to a SAMPLE while the spell orphan is
// still reported (never truncated) and IsolatedCount reflects the true total.
func TestStatsOrphanCapExemptsSpells(t *testing.T) {
	g := NewGraph()
	total := maxOrphans + 10
	for i := 0; i < total; i++ {
		// Zero-padded so "diagnostic:MGS0000".. all sort before the "spell:zzz" orphan below.
		id := "diagnostic:MGS" + pad(i)
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindDiagnostic, Label: id})
	}
	// A declared, op-providing, unused spell: a semantic orphan (has edges), ID sorts after every diagnostic.
	g.AddNode(types.KnowledgeNode{ID: "spell:zzz", Kind: types.KindSpell, Label: "zzz", Attrs: map[string]string{AttrDeclared: "true"}})
	g.AddNode(types.KnowledgeNode{ID: "op:zzz:x", Kind: types.KindOp, Label: "x"})
	g.AddEdge(types.KnowledgeEdge{Source: "spell:zzz", Target: "op:zzz:x", Relation: types.RelationContains, Confidence: types.ConfidenceExtracted, Score: 1})

	s := g.Stats("")
	assert.Equal(t, total, s.IsolatedCount, "IsolatedCount is the true total of 0-degree non-spell nodes")
	assert.Len(t, s.Orphans, maxOrphans+1, "the isolated SAMPLE is capped, plus the one exempt spell orphan")
	// The spell orphan survives the cap even though its ID sorts last.
	var sawSpell bool
	for _, o := range s.Orphans {
		if o.ID == "spell:zzz" {
			sawSpell = true
		}
	}
	assert.True(t, sawSpell, "the semantic spell orphan is never truncated by the isolated-sample cap")
}

// pad renders i as a 5-digit zero-padded string so generated node IDs sort lexically by number.
func pad(i int) string {
	s := "00000" + strconv.Itoa(i)
	return s[len(s)-5:]
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
