//go:build !wasm

package std

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIndexWorkspace builds its knowledge store from in on every KnowledgeGraph call, as
// the real workspace rebuilds cache-first.
type fakeIndexWorkspace struct {
	types.WorkspaceRepository
	cacheDir string
	in       knowledge.Inputs
	gaps     []types.KnowledgeSymbolGap
	probed   bool
}

func (f *fakeIndexWorkspace) KnowledgeGraph(ctx context.Context, _ bool) (*knowledge.Graph, error) {
	return knowledge.Build(ctx, f.cacheDir, knowledge.BuildOptions{}, f.in, nil)
}

func (f *fakeIndexWorkspace) SymbolGaps(context.Context) ([]types.KnowledgeSymbolGap, bool) {
	return f.gaps, f.probed
}

func (f *fakeIndexWorkspace) CacheDir() string { return f.cacheDir }

func indexWorkspace(t *testing.T) *fakeIndexWorkspace {
	return &fakeIndexWorkspace{
		cacheDir: filepath.Join(t.TempDir(), ".magus"),
		in: knowledge.Inputs{Graph: types.TargetGraphOutput{Projects: []types.TargetGraphProject{
			{Path: "pkg/a", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}}},
		}}},
		probed: true,
	}
}

func TestMagusSymbolIndexDigest(t *testing.T) {
	t.Run("no index is not indexed", func(t *testing.T) {
		got, err := MagusSymbolIndexDigest(types.WithWorkspace(t.Context(), indexWorkspace(t)))
		require.NoError(t, err)
		assert.Equal(t, types.SymbolIndexDigest{Projects: []string{}}, got)
	})

	t.Run("reads the store the graph build just wrote, with the gaps", func(t *testing.T) {
		ws := indexWorkspace(t)
		ws.in.Symbols = map[string][]types.KnowledgeSymbol{
			"pkg/a": {{Key: "example.com/a Foo#", Label: "Foo", Source: "pkg/a/a.go:1", Defs: []string{"pkg/a/a.go"}}},
		}
		ws.gaps = []types.KnowledgeSymbolGap{{Project: types.NewProjectRef("console", ""), State: types.SymbolIndexNotBuilt}}
		ctx := types.WithWorkspace(t.Context(), ws)

		got, err := MagusSymbolIndexDigest(ctx)
		require.NoError(t, err)
		want, err := knowledge.NewStore(ws.cacheDir, true, 0, nil, nil).SymbolIndexDigest()
		require.NoError(t, err)
		want.Gaps = ws.gaps
		assert.Equal(t, want, got)
		assert.True(t, got.Indexed)
		assert.Equal(t, []string{"pkg/a"}, got.Projects)

		ws.in.Symbols["pkg/a"][0].Label = "Renamed"
		moved, err := MagusSymbolIndexDigest(ctx)
		require.NoError(t, err)
		assert.NotEqual(t, got.Digest, moved.Digest, "an index change reaches the digest without a separate graph build")
	})

	t.Run("an unrunnable gap probe raises", func(t *testing.T) {
		ws := indexWorkspace(t)
		ws.probed = false
		_, err := MagusSymbolIndexDigest(types.WithWorkspace(t.Context(), ws))
		require.ErrorContains(t, err, "could not probe which declared symbol indexes are missing")
	})

	t.Run("outside a workspace raises", func(t *testing.T) {
		_, err := MagusSymbolIndexDigest(t.Context())
		require.ErrorContains(t, err, "magus\\symbolIndexDigest: no workspace on the context")
	})
}
