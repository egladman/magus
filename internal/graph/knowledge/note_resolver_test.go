package knowledge

import (
	"testing"

	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func declarationGraph() *Graph {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: symbolID("m a/Store#Put()."), Kind: types.KindSymbol, Label: "Put", Source: "internal/cache/cache.go:10"})
	g.AddNode(types.KnowledgeNode{ID: symbolID("m a/Put()."), Kind: types.KindSymbol, Label: "Put", Source: "internal/cache/free.go:4"})
	g.AddNode(types.KnowledgeNode{ID: symbolID("m b/Get()."), Kind: types.KindSymbol, Label: "Get", Source: "internal/cache/two.go:1"})
	g.AddNode(types.KnowledgeNode{ID: symbolID("m b/T#Get()."), Kind: types.KindSymbol, Label: "Get", Source: "internal/cache/two.go:9"})
	g.AddNode(types.KnowledgeNode{ID: diagnosticID("MGS3009"), Kind: types.KindDiagnostic})
	return g
}

func TestDeclarationFindsTheSymbolAFileDefines(t *testing.T) {
	res := NewNoteResolver(t.TempDir(), declarationGraph())

	a, err := res.Declaration(t.Context(), "internal/cache/cache.go", "Put")
	require.NoError(t, err)
	assert.Equal(t, notes.Anchor{Kind: notes.AnchorSymbol, Target: "m a/Store#Put()."}, a)
	assert.True(t, res.Resolves(t.Context(), a))

	_, err = res.Declaration(t.Context(), "internal/cache/cache.go", "Get")
	require.Error(t, err, "a name the file does not declare")
	assert.Contains(t, err.Error(), "declares no symbol")

	_, err = res.Declaration(t.Context(), "internal/cache/two.go", "Get")
	require.Error(t, err, "two matches are never guessed between")
	assert.Contains(t, err.Error(), "declares 2 symbols")
}

func TestDiagnosticAnchorResolvesToItsNode(t *testing.T) {
	res := NewNoteResolver(t.TempDir(), declarationGraph())

	a := notes.Anchor{Kind: notes.AnchorDiagnostic, Target: "MGS3009"}
	assert.Equal(t, "diagnostic:MGS3009", res.NodeID(a))
	assert.True(t, res.Resolves(t.Context(), a))
	assert.False(t, res.Resolves(t.Context(), notes.Anchor{Kind: notes.AnchorDiagnostic, Target: "MGS9999"}))
}
