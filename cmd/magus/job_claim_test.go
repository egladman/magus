package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

func TestCallersSymbolRefusesAnAmbiguousName(t *testing.T) {
	const a, b = "symbol:x `a`/Parse().", "symbol:x `b`/Parse()."
	defines := func(file, id string) types.KnowledgeEdge {
		return types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1}
	}
	g := knowledge.NewGraph()
	g.Merge([]types.KnowledgeNode{{ID: a, Kind: types.KindSymbol, Label: "Parse"}, {ID: b, Kind: types.KindSymbol, Label: "Parse"}},
		[]types.KnowledgeEdge{defines("a/a.go", a), defines("b/b.go", b)})

	_, err := callersSymbol(g, "Parse")
	require.Error(t, err)
	assert.ErrorContains(t, err, a)
	assert.ErrorContains(t, err, b)

	_, err = callersSymbol(g, "Missing")
	assert.ErrorContains(t, err, `callers "Missing" names no symbol the index holds`)
}

func TestApplyClaimRefusesPathBesideCallers(t *testing.T) {
	rec := types.Declaration{
		Schema: types.Schema{Version: types.JobSchemaVersion},
		ID:     "fix-parser",
		Claim:  []types.JobClaimRequest{{Path: "lib/lib.go", Callers: "Parse"}},
	}
	err := applyClaim(context.Background(), t.TempDir(), []types.Declaration{rec})
	assert.ErrorContains(t, err, "names callers of Parse and also a path or a proof")
}
