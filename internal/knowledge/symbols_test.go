package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssembleSymbols(t *testing.T) {
	syms := []types.KnowledgeSymbol{{
		Key:        "example.com/foo Bar#",
		Moniker:    "scip-go gomod example.com/foo v1 Bar#",
		Label:      "Bar",
		Language:   "go",
		SymbolKind: "Type",
		Source:     "pkg/foo/foo.go:11",
		Defs:       []string{"pkg/foo/foo.go"},
		Refs:       []types.KnowledgeSymbolRef{{Path: "pkg/baz/baz.go", Count: 2, Lines: []int{5, 8}}},
	}}
	out := mergeAll([]Shard{assembleSymbols("pkg/foo", syms)}).Output()

	n, ok := nodeByID(out, "symbol:example.com/foo Bar#")
	require.True(t, ok)
	assert.Equal(t, types.KindSymbol, n.Kind)
	assert.Equal(t, "Bar", n.Label)
	assert.Equal(t, "go", n.Attrs["language"])
	assert.Equal(t, "Type", n.Attrs["symbol_kind"])
	assert.Equal(t, "pkg/foo/foo.go:11", n.Source)

	// A defining file gets a defines edge; a using file gets a references edge whose
	// provenance carries the per-file count and capped lines.
	assert.True(t, hasEdge(out, "file:pkg/foo/foo.go", "symbol:example.com/foo Bar#", types.RelationDefines))
	e, ok := findEdge(out, "file:pkg/baz/baz.go", "symbol:example.com/foo Bar#", types.RelationReferences)
	require.True(t, ok)
	assert.Contains(t, e.Provenance, "count=2")
	assert.Contains(t, e.Provenance, "lines=5,8")
}

func TestSymbolsShardNaming(t *testing.T) {
	assert.Equal(t, "pkg/foo@symbols", SymbolsShardName("pkg/foo"))
	assert.True(t, IsSymbolsShard("pkg/foo@symbols"))
	assert.False(t, IsSymbolsShard("pkg/foo"))
	assert.False(t, IsSymbolsShard(RuntimeShardName))
}

// TestAssembleSymbolsRefOnly: a symbol seen only as a reference (its definition is in
// another index) still yields a node, with no def edge.
func TestAssembleSymbolsRefOnly(t *testing.T) {
	syms := []types.KnowledgeSymbol{{
		Key:   "other.com/dep Qux#",
		Label: "Qux",
		Refs:  []types.KnowledgeSymbolRef{{Path: "pkg/a/a.go", Count: 1, Lines: []int{3}}},
	}}
	out := mergeAll([]Shard{assembleSymbols("pkg/a", syms)}).Output()

	_, ok := nodeByID(out, "symbol:other.com/dep Qux#")
	assert.True(t, ok, "reference-only symbol still gets a node")
	assert.True(t, hasEdge(out, "file:pkg/a/a.go", "symbol:other.com/dep Qux#", types.RelationReferences))
}
