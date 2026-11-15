package main

import (
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// A callers claim resolves its name with the picker the claim env wires, so a name two
// symbols share reaches job.ApplyClaim as both ids and a name the index lacks as itself.
func TestJobClaimEnvPicksEverySymbolAnAmbiguousNameNames(t *testing.T) {
	const a, b = "symbol:x `a`/Parse().", "symbol:x `b`/Parse()."
	defines := func(file, id string) types.KnowledgeEdge {
		return types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1}
	}
	g := knowledge.NewGraph()
	g.Merge([]types.KnowledgeNode{{ID: a, Kind: types.KindSymbol, Label: "Parse"}, {ID: b, Kind: types.KindSymbol, Label: "Parse"}},
		[]types.KnowledgeEdge{defines("a/a.go", a), defines("b/b.go", b)})
	pick := jobClaimEnv().PickSymbol

	id, ambiguous := pick(g, "Parse")
	assert.Empty(t, id)
	assert.ElementsMatch(t, []string{a, b}, ambiguous)

	id, ambiguous = pick(g, "Missing")
	assert.Equal(t, "Missing", id)
	assert.Empty(t, ambiguous)
}
