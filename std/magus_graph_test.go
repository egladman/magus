package std

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/json"
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
	assert.Equal(t, types.Dir{
		Path: "internal/handler/mcp", ID: "dir:internal/handler/mcp", Layer: "handler", Language: "go",
		Imports: []string{"internal/httpx"}, ImportedBy: []string{}, ImportsIndexed: true,
		Calls: []types.DirCall{},
		CalledBy: []types.DirCall{{
			Dir: "internal/httpx", Transport: "http",
			Marker: "marker:internal/httpx/a.go:12", Source: "internal/httpx/a.go:12",
		}},
		Children: []string{},
	}, dirs[0])

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
	// The node list is the walk's own rendering of the graph and the answer is asserted by its
	// verdict below, so both are taken from out.
	assert.Equal(t, types.KnowledgeNeighborhoodOutput{
		Definition:    types.KnowledgeNeighborhoodDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Focus:         "dir:internal/httpx",
		Resolution:    types.ResolvedPath,
		Options: types.KnowledgeNeighborhoodOptions{
			Depth: 1, Relations: []types.RelationID{types.RelationCalls}, Direction: types.EdgeOut, Collapse: []string{"internal/handler"},
		},
		Nodes: out.Nodes,
		Links: []types.KnowledgeEdge{{
			Source: "dir:internal/httpx", Target: "dir:internal/handler", Relation: types.RelationCalls,
			Confidence: types.ConfidenceExtracted, Score: 1, Attrs: map[string]string{types.AttrTransport: "http"},
		}},
		Folds:  []types.KnowledgeFold{{Prefix: "internal/handler", Node: "dir:internal/handler", Folded: 1}},
		Answer: out.Answer,
	}, out)
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

// precedentGraphs is a workspace whose indexes have the given freshness and whose declared
// outputs are the given paths. Its freshen fails with freshenErr and records whether it ran
// before the freshness was read.
type precedentGraphs struct {
	*fakeGraphWorkspace
	indexes    []types.SymbolIndexStatus
	outputs    map[string]bool
	freshenErr error
	freshened  bool
	readAfter  bool
}

func (p *precedentGraphs) FreshenSymbolIndexes(context.Context) error {
	p.freshened = true
	return p.freshenErr
}

func (p *precedentGraphs) SymbolIndexStatusByStamp(context.Context) []types.SymbolIndexStatus {
	p.readAfter = p.freshened
	return p.indexes
}

func (p *precedentGraphs) ClassifyFiles(_ context.Context, paths []string) ([]types.FileEntry, error) {
	out := make([]types.FileEntry, len(paths))
	for i, path := range paths {
		out[i] = types.FileEntry{Path: path, Role: types.DiffRoleSource}
		if p.outputs[path] {
			out[i].Role = types.DiffRoleOutput
		}
	}
	return out, nil
}

// errValue declares `var name = errors.New(...)` on line of file in package internal/a, as
// scip-go indexes it.
func errValue(g *knowledge.Graph, file string, line int, name string) string {
	ns := "symbol:gomod example.com/m `example.com/m/internal/a`/"
	id := ns + name + "."
	g.AddNode(types.KnowledgeNode{ID: ns, Kind: types.KindSymbol, Label: "a", Source: "internal/a/a.go:1",
		Attrs: map[string]string{"namespace": ns, types.AttrLanguage: "go", "symbol_kind": "Package"}})
	g.AddNode(types.KnowledgeNode{ID: "file:" + file, Kind: types.KindFile, Label: file, Source: file})
	g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: name, Source: fmt.Sprintf("%s:%d", file, line),
		Attrs: map[string]string{"namespace": ns, types.AttrLanguage: "go", "symbol_kind": "Variable", knowledge.AttrSignature: "var " + name + " error"}})
	g.AddEdge(types.KnowledgeEdge{Source: "file:" + file, Target: "symbol:gomod std `errors`/New().", Relation: types.RelationReferences,
		Confidence: types.ConfidenceExtracted, Score: 1, Provenance: fmt.Sprintf("scip count=1 lines=%d", line)})
	return id
}

func TestPrecedentsReportRowsBesideIndexFreshness(t *testing.T) {
	t.Parallel()
	g := knowledge.NewGraph()
	var cited []types.Case
	for i, name := range []string{"ErrA", "ErrB", "ErrC", "ErrD", "ErrE"} {
		id := errValue(g, fmt.Sprintf("internal/a/e%d.go", i), 3, name)
		if i < 3 {
			cited = append(cited, types.Case{Node: id, Source: fmt.Sprintf("internal/a/e%d.go:3", i)})
		}
	}
	bad := errValue(g, "internal/a/z.go", 4, "badInput")
	errValue(g, "internal/a/gen.go", 2, "generatedFailure")
	stale := types.SymbolIndexStatus{Project: types.ProjectRef{Path: "libs/x"}, Op: "scip", Language: "go", Freshness: types.SymbolIndexStale}
	ws := &precedentGraphs{
		fakeGraphWorkspace: &fakeGraphWorkspace{g: g, cacheDir: t.TempDir()},
		indexes:            []types.SymbolIndexStatus{stale},
		outputs:            map[string]bool{"internal/a/gen.go": true},
	}

	got, err := MagusPrecedents(types.WithWorkspace(t.Context(), ws))
	require.NoError(t, err)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	var report types.PrecedentReport
	require.NoError(t, json.Unmarshal(b, &report))
	assert.Equal(t, types.PrecedentReport{
		Precedents: []types.Precedent{{
			Family: types.PrecedentErrSentinelName, Scope: types.PrecedentScope{Language: "go"}, Key: types.PrecedentKey{Prefix: "err"},
			Follow: 5, Cohort: 6, Share: 5.0 / 6, Established: true, Cited: cited,
			Departures: []types.Case{{Node: bad, Source: "internal/a/z.go:4"}},
		}},
		Indexes: []types.SymbolIndexStatus{stale},
	}, report, "a declared output's departure is not counted, and the index verdict rides beside the rows")

	_, err = MagusPrecedents(graphContext(t))
	assert.ErrorContains(t, err, "cannot judge its symbol indexes", "a workspace that cannot say whether its index is current gets no rows")
}

func TestPrecedentsFreshenEveryIndexBeforeJudgingIt(t *testing.T) {
	t.Parallel()
	fresh := types.SymbolIndexStatus{Project: types.ProjectRef{Path: "."}, Op: "scip", Language: "go", Freshness: types.SymbolIndexFresh}
	stale := types.SymbolIndexStatus{Project: types.ProjectRef{Path: "docs"}, Op: "scip-buzz", Language: "buzz",
		Freshness: types.SymbolIndexStale, Detail: "docs changed"}
	ws := &precedentGraphs{
		fakeGraphWorkspace: &fakeGraphWorkspace{g: knowledge.NewGraph(), cacheDir: t.TempDir()},
		indexes:            []types.SymbolIndexStatus{fresh, stale},
		freshenErr:         errors.New("scip-buzz is not on PATH"),
	}

	got, err := MagusPrecedents(types.WithWorkspace(t.Context(), ws))
	require.NoError(t, err, "a failed freshen is a verdict on its index, not an error")
	assert.True(t, ws.readAfter, "freshness is read after the freshen, so it judges the rebuilt index")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	var report types.PrecedentReport
	require.NoError(t, json.Unmarshal(b, &report))
	stale.Detail = "docs changed\nscip-buzz is not on PATH"
	assert.Equal(t, []types.SymbolIndexStatus{fresh, stale}, report.Indexes,
		"the freshen's error rides on the index it left stale, and a current index carries none")
}
