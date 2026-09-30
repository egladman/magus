package std

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dirContext is a workspace whose graph holds two Go packages, mcp importing httpx and
// httpx declaring one http call into mcp, and whose root project declares two layers.
func dirContext(t *testing.T) context.Context {
	g := knowledge.NewGraph()
	for _, p := range []string{"internal/httpx", "internal/handler/mcp"} {
		g.AddNode(types.KnowledgeNode{ID: "dir:" + p, Kind: types.KindDir, Label: p, Source: p, Attrs: map[string]string{types.AttrLanguage: "go"}})
	}
	g.AddNode(types.KnowledgeNode{ID: "target:.:mcp-tools-generate", Kind: types.KindTarget, Label: "mcp-tools-generate"})
	g.AddNode(types.KnowledgeNode{ID: "file:internal/httpx/a.go", Kind: types.KindFile, Label: "internal/httpx/a.go", Source: "internal/httpx/a.go"})
	g.AddNode(types.KnowledgeNode{ID: "marker:internal/httpx/a.go:12", Kind: types.KindMarker, Source: "internal/httpx/a.go:12", Attrs: map[string]string{
		types.AttrMarkerFamily: string(types.MarkerCalls), types.AttrMarkerArgs: "internal/handler/mcp http", types.AttrLine: "12",
	}})
	edge := func(s, t string, rel types.RelationID, attrs map[string]string) {
		g.AddEdge(types.KnowledgeEdge{Source: s, Target: t, Relation: rel, Confidence: types.ConfidenceExtracted, Score: 1, Attrs: attrs})
	}
	edge("dir:internal/handler/mcp", "dir:internal/httpx", types.RelationImports, nil)
	edge("dir:internal/httpx", "dir:internal/handler/mcp", types.RelationCalls, map[string]string{types.AttrTransport: "http"})
	edge("marker:internal/httpx/a.go:12", "dir:internal/handler/mcp", types.RelationReferences, nil)
	ws := &fakeGraphWorkspace{g: g, cacheDir: t.TempDir(), projects: []*types.Project{
		{Path: ".", Layers: map[string]string{"internal/httpx": "transport"}},
		{Path: "internal/handler", Layers: map[string]string{"internal/handler/**": "handler"}},
	}}
	return types.WithWorkspace(t.Context(), ws)
}

func TestDirReturnsTheTypedRecord(t *testing.T) {
	t.Parallel()
	d, err := MagusDir(dirContext(t), "internal/httpx")
	require.NoError(t, err)
	assert.Equal(t, types.Dir{
		Path: "internal/httpx", ID: "dir:internal/httpx", Layer: "transport", Language: "go",
		Imports: []string{}, ImportedBy: []string{"internal/handler/mcp"}, ImportsIndexed: true,
		Calls: []types.DirCall{{
			Dir: "internal/handler/mcp", Transport: "http",
			Marker: "marker:internal/httpx/a.go:12", Source: "internal/httpx/a.go:12",
		}},
		CalledBy: []types.DirCall{}, Children: []string{}, Files: 1,
	}, d)
}

func TestDirAndLayerRaiseTheirCodes(t *testing.T) {
	t.Parallel()
	ctx := dirContext(t)

	_, err := MagusDir(ctx, "internal/htpx")
	require.ErrorIs(t, err, types.DirNotInGraph)
	assert.ErrorContains(t, err, "magus\\dir")

	_, err = MagusLayer(ctx, "service")
	require.ErrorIs(t, err, types.LayerNotDeclared)
	_, err = MagusDirs(ctx, "**", map[string]any{"layer": "service"})
	require.ErrorIs(t, err, types.LayerNotDeclared, "a DirsOptions layer is held to the declarations too")
}

func TestDirsAndLayerReadEveryProjectsDeclarations(t *testing.T) {
	t.Parallel()
	ctx := dirContext(t)

	dirs, err := MagusDirs(ctx, "internal/**", map[string]any{"layer": "handler", "language": "go", "depth": int64(0)})
	require.NoError(t, err)
	require.Len(t, dirs, 1)
	assert.Equal(t, "internal/handler/mcp", dirs[0].Path)
	assert.Equal(t, "handler", dirs[0].Layer)

	l, err := MagusLayer(ctx, "transport")
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/httpx"}, l.Declared)
	require.Len(t, l.Dirs, 1)
	assert.Equal(t, "dir:internal/httpx", l.Dirs[0].ID)
}

func TestExplainSaysHowItResolved(t *testing.T) {
	t.Parallel()
	ctx := dirContext(t)

	byPath, err := MagusExplain(ctx, "internal/handler/mcp")
	require.NoError(t, err)
	assert.Equal(t, "dir:internal/handler/mcp", byPath.Node.ID, "the path wins over the similarly named target")
	assert.Equal(t, types.ResolvedPath, byPath.Resolution)

	fuzzy, err := MagusExplain(ctx, "mcp-tools")
	require.NoError(t, err)
	assert.Equal(t, types.ResolvedFuzzy, fuzzy.Resolution)
}

func TestPathFiltersRelations(t *testing.T) {
	t.Parallel()
	ctx := dirContext(t)

	p, err := MagusPath(ctx, "internal/handler/mcp", "internal/httpx", map[string]any{"relations": []any{"imports"}})
	require.NoError(t, err)
	assert.True(t, p.Found)

	none, err := MagusPath(ctx, "internal/handler/mcp", "internal/httpx", map[string]any{"relations": []any{"depends_on"}})
	require.NoError(t, err)
	assert.False(t, none.Found, "no path of those relations is an answer")
}

func TestNeighborhoodDecodesItsOptions(t *testing.T) {
	t.Parallel()
	ctx := dirContext(t)

	out, err := MagusNeighborhood(ctx, "internal/httpx", map[string]any{
		"depth": int64(1), "relations": []any{"calls"}, "direction": "out", "collapse": []any{"internal/handler"},
	})
	require.NoError(t, err)
	assert.Equal(t, "dir:internal/httpx", out.Focus)
	assert.Equal(t, []types.KnowledgeFold{{Prefix: "internal/handler", Node: "dir:internal/handler", Folded: 1}}, out.Folds)
	require.Len(t, out.Links, 1)
	assert.Equal(t, "dir:internal/handler", out.Links[0].Target)
	assert.Equal(t, map[string]string{types.AttrTransport: "http"}, out.Links[0].Attrs)
	assert.Equal(t, types.VerdictUnknown, out.Answer.Verdict, "calls lives beside the symbol layer, and no index was loaded")

	declared, err := MagusNeighborhood(ctx, "target:.:mcp-tools-generate", map[string]any{"relations": []any{"depends_on"}})
	require.NoError(t, err)
	assert.Equal(t, types.VerdictAbsent, declared.Answer.Verdict, "a depends_on walk never needed an index")
}

// A typo in an option would otherwise answer a different question than the one asked.
func TestDirLayerAndNeighborhoodOptionsAreStrict(t *testing.T) {
	t.Parallel()
	ctx := dirContext(t)

	for name, call := range map[string]func() error{
		"unknown dirs key": func() error { _, err := MagusDirs(ctx, "**", map[string]any{"layr": "x"}); return err },
		"negative depth":   func() error { _, err := MagusDirs(ctx, "**", map[string]any{"depth": int64(-1)}); return err },
		"bad glob":         func() error { _, err := MagusDirs(ctx, "[", nil); return err },
		"unknown relation": func() error {
			_, err := MagusNeighborhood(ctx, "internal/httpx", map[string]any{"relations": []any{"import"}})
			return err
		},
		"bad direction": func() error {
			_, err := MagusNeighborhood(ctx, "internal/httpx", map[string]any{"direction": "up"})
			return err
		},
		"collapse not a list": func() error {
			_, err := MagusNeighborhood(ctx, "internal/httpx", map[string]any{"collapse": "internal"})
			return err
		},
		"unresolvable focus": func() error { _, err := MagusNeighborhood(ctx, "nonesuch-xyz", nil); return err },
		"unknown path option": func() error {
			_, err := MagusPath(ctx, "a", "b", map[string]any{"relation": []any{"imports"}})
			return err
		},
		"empty dir":          func() error { _, err := MagusDir(ctx, " "); return err },
		"empty layer name":   func() error { _, err := MagusLayer(ctx, ""); return err },
		"empty dirs glob":    func() error { _, err := MagusDirs(ctx, "", nil); return err },
		"empty neighborhood": func() error { _, err := MagusNeighborhood(ctx, "", nil); return err },
		"string depth": func() error {
			_, err := MagusNeighborhood(ctx, "internal/httpx", map[string]any{"depth": "2"})
			return err
		},
		"non-string layer": func() error { _, err := MagusDirs(ctx, "**", map[string]any{"layer": int64(1)}); return err },
		"non-string collapse": func() error {
			_, err := MagusNeighborhood(ctx, "internal/httpx", map[string]any{"collapse": []any{int64(1)}})
			return err
		},
		"unknown neighborhood": func() error {
			_, err := MagusNeighborhood(ctx, "internal/httpx", map[string]any{"dept": int64(1)})
			return err
		},
	} {
		assert.Error(t, call(), name)
	}
}

// countingGraphs counts the graph builds a sequence of members asks for.
type countingGraphs struct {
	*fakeGraphWorkspace
	builds int
}

func (c *countingGraphs) KnowledgeGraphWithSymbols(ctx context.Context) (*knowledge.Graph, error) {
	c.builds++
	return c.fakeGraphWorkspace.KnowledgeGraphWithSymbols(ctx)
}

// One evaluation builds the graph once however many members it calls; the next
// evaluation builds its own.
func TestGraphMembersShareOneBuildPerEvaluation(t *testing.T) {
	t.Parallel()
	ws := &countingGraphs{fakeGraphWorkspace: types.WorkspaceFromContext(dirContext(t)).(*fakeGraphWorkspace)}
	eval := types.WithEvalMemo(types.WithWorkspace(t.Context(), ws))
	for range 3 {
		_, err := MagusDir(eval, "internal/httpx")
		require.NoError(t, err)
	}
	_, err := MagusDirs(eval, "internal/**", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, ws.builds, "four members in one evaluation")

	_, err = MagusDir(types.WithEvalMemo(eval), "internal/httpx")
	require.NoError(t, err)
	assert.Equal(t, 2, ws.builds, "a new evaluation rebuilds")
}
