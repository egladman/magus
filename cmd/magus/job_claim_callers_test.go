package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

func TestCallerClaimsExpandToOneRequestPerDeclaration(t *testing.T) {
	occ := func(line int) types.SymbolOccurrence {
		return types.SymbolOccurrence{Line: line, Status: types.SymbolOccurrenceVerified}
	}
	files := []types.SymbolOccurrenceFile{
		{File: "lib/use_test.go", Occurrences: []types.SymbolOccurrence{occ(4)}},
		{File: "lib/lib.go", Occurrences: []types.SymbolOccurrence{{Line: 3, Status: types.SymbolOccurrenceVerified, Definition: true}, occ(8), occ(9), occ(14)}},
	}
	region := func(file string, from, to int, decl string) types.RegionChange {
		return types.RegionChange{File: types.FileChange{Path: file}, Side: types.RegionNew, Lines: [2]int{from, to}, Declaration: decl, Driver: "golang"}
	}
	placed := map[string][]types.RegionChange{
		"lib/lib.go": {
			region("lib/lib.go", 1, 2, ""),
			region("lib/lib.go", 3, 5, "func Parse(s string) error {"),
			region("lib/lib.go", 6, 11, "func (c *Client) Run() error {"),
			region("lib/lib.go", 12, 16, "func (s *Server) Run() error {"),
		},
		"lib/use_test.go": {region("lib/use_test.go", 3, 6, "func TestParse(t *testing.T) {")},
	}
	got, err := callerClaims("Parse", "lib", "lib", files, func(file string) ([]types.RegionChange, error) { return placed[file], nil })
	require.NoError(t, err)
	assert.Equal(t, []types.JobClaimRequest{
		{Path: "lib/lib.go#Client) Run", Because: "refs:Parse"},
		{Path: "lib/lib.go#Server) Run", Because: "refs:Parse"},
		{Path: "lib/use_test.go#TestParse", Because: "refs:Parse"},
	}, got, "two occurrences in one declaration claim it once, and the definition claims nothing")

	files[0].Occurrences = []types.SymbolOccurrence{occ(1)}
	_, err = callerClaims("Parse", "lib", "lib", files, func(file string) ([]types.RegionChange, error) {
		return []types.RegionChange{region(file, 1, 2, "")}, nil
	})
	assert.ErrorContains(t, err, "no declaration holds the occurrence at lib/lib.go:8")
}

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
