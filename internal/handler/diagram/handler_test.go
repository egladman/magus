package diagram

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	json "github.com/egladman/magus/internal/json"
	figuresrc "github.com/egladman/magus/libs/figure"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWorkspace answers with canned graphs, the shape the graph handler tests use.
type fakeWorkspace struct {
	targets types.TargetGraphOutput
	imports types.ImportGraph
	remote  string
	head    string
}

func (f fakeWorkspace) TargetGraph(context.Context) (types.TargetGraphOutput, error) {
	return f.targets, nil
}

func (f fakeWorkspace) ImportGraph(context.Context) (types.ImportGraph, error) {
	return f.imports, nil
}

func (f fakeWorkspace) ReviewOrigin(context.Context) types.ReviewOrigin {
	return types.ReviewOrigin{Remote: f.remote}
}

func (f fakeWorkspace) RevisionCheckpoint(_ context.Context, rev string) (types.VCSCheckpoint, error) {
	if f.head == "" {
		return types.VCSCheckpoint{}, errors.New("no revision")
	}
	return types.VCSCheckpoint{Revision: f.head, Branch: rev}, nil
}

// chainWorkspace is app -> lib -> core -> base, plus a tool project depending on nothing.
func chainWorkspace() fakeWorkspace {
	return fakeWorkspace{
		targets: types.TargetGraphOutput{
			Definition: "magusfile.buzz",
			Projects: []types.TargetGraphProject{
				{Path: "app", Name: "app", DependsOn: []string{"libs/lib"}, Nodes: []types.TargetGraphNode{
					{Name: "build", Dependencies: []string{"test"}},
					{Name: "test"},
				}},
				{Path: "libs/lib", Name: "lib", DependsOn: []string{"libs/core"}},
				{Path: "libs/core", Name: "core", DependsOn: []string{"libs/base"}},
				{Path: "libs/base", Name: `base "{x}"`},
				{Path: "tools", Name: "tools"},
			},
		},
		remote: "git@github.com:acme/widgets.git",
		head:   "0123abcd",
	}
}

func get(t *testing.T, ws fakeWorkspace, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	NewHandler(ws, nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func TestDiagramsListing(t *testing.T) {
	w := get(t, chainWorkspace(), "/api/v1/diagrams")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var out struct {
		Diagrams []map[string]any `json:"diagrams"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, []map[string]any{
		{"id": "projects", "kind": "projects", "title": "Workspace projects"},
		{"id": "targets:app", "kind": "targets", "title": "Targets in app", "project": "app"},
		{"id": "targets:libs/lib", "kind": "targets", "title": "Targets in lib", "project": "libs/lib"},
		{"id": "targets:libs/core", "kind": "targets", "title": "Targets in core", "project": "libs/core"},
		{"id": "targets:libs/base", "kind": "targets", "title": `Targets in base "{x}"`, "project": "libs/base"},
		{"id": "targets:tools", "kind": "targets", "title": "Targets in tools", "project": "tools"},
		{"id": "imports", "kind": "imports", "title": "Package imports", "indexed": false},
	}, out.Diagrams)
}

func TestDiagramRendersProjects(t *testing.T) {
	w := get(t, chainWorkspace(), "/api/v1/diagrams/projects")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var out Rendered
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "projects", out.ID)
	assert.Equal(t, "Workspace projects", out.Title)
	assert.Equal(t, []Node{
		{ID: "external:app", Anchor: "app", Label: "app"},
		{ID: "external:lib", Anchor: "libs/lib", Label: "lib"},
		{ID: "external:core", Anchor: "libs/core", Label: "core"},
		{ID: `external:base "{x}"`, Anchor: "libs/base", Label: `base "{x}"`},
		{ID: "external:tools", Anchor: "tools", Label: "tools"},
	}, out.Nodes)
	assert.Equal(t, "https://github.com/acme/widgets/blob/0123abcd/{path}#L{line}", out.SourceURL)
	// Each row names the node the SVG draws; the quoted, braced label proves the driver
	// escaped it into a Buzz literal intact.
	assert.Contains(t, out.SVG, `data-node="external:core"`)
	assert.Contains(t, out.SVG, "<svg")
	assert.Contains(t, out.SVG, ">core</text>")
	assert.Contains(t, out.SVG, "base &quot;{x}&quot;")
}

func TestDiagramRendersTargets(t *testing.T) {
	w := get(t, chainWorkspace(), "/api/v1/diagrams/targets:app")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var out Rendered
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, []Node{
		{ID: "external:build", Anchor: "app", Label: "build"},
		{ID: "external:test", Anchor: "app", Label: "test"},
	}, out.Nodes)

	assert.Equal(t, http.StatusNotFound, get(t, chainWorkspace(), "/api/v1/diagrams/targets:nope").Code)
	assert.Equal(t, http.StatusNotFound, get(t, chainWorkspace(), "/api/v1/diagrams/nope").Code)
}

func TestDiagramFocusAndDepthCutTheFigure(t *testing.T) {
	w := get(t, chainWorkspace(), "/api/v1/diagrams/projects?focus=libs/core&depth=1")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out Rendered
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, []string{"libs/lib", "libs/core", "libs/base"}, anchors(out.Nodes))

	w = get(t, chainWorkspace(), "/api/v1/diagrams/projects?scope=libs&focus=libs-lib&depth=2")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, []string{"libs/lib", "libs/core", "libs/base"}, anchors(out.Nodes), "scope drops app before the walk")

	for _, target := range []string{
		"/api/v1/diagrams/projects?focus=app&depth=-1",
		"/api/v1/diagrams/projects?depth=2",
		"/api/v1/diagrams/projects?focus=nowhere",
		"/api/v1/diagrams/projects?scope=tools&focus=app",
	} {
		assert.Equal(t, http.StatusBadRequest, get(t, chainWorkspace(), target).Code, target)
	}
}

func TestDiagramOverBudgetIs422NamingTheFix(t *testing.T) {
	ws := chainWorkspace()
	ws.targets.Projects = nil
	for i := range 10 {
		p := "p" + strconv.Itoa(i)
		ws.targets.Projects = append(ws.targets.Projects, types.TargetGraphProject{Path: p, Name: p})
	}
	w := get(t, ws, "/api/v1/diagrams/projects")
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "10 nodes exceeds the budget of 9")
	assert.Contains(t, w.Body.String(), "split into overview plus detail")

	w = get(t, ws, "/api/v1/diagrams/projects?scope=p1")
	assert.Equal(t, http.StatusOK, w.Code, "narrowing the lens is the fix: %s", w.Body.String())
}

func TestDiagramImportsRefuseWithoutAnIndex(t *testing.T) {
	w := get(t, chainWorkspace(), "/api/v1/diagrams/imports")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "magus graph build")

	ws := chainWorkspace()
	ws.imports = types.ImportGraph{Indexed: true, Packages: map[string][]string{
		"internal/server":        {"internal/handler/graph", "types"},
		"internal/handler/graph": {"types"},
	}}
	w = get(t, ws, "/api/v1/diagrams/imports")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out Rendered
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, []string{"internal/handler/graph", "internal/server", "types"}, anchors(out.Nodes))
	// Each package is a box built from a Dir record, so its group carries the directory.
	assert.Contains(t, out.SVG, `data-anchor="internal/handler/graph"`)
}

func TestDiagramSourceURL(t *testing.T) {
	const linked = "https://github.com/acme/widgets/blob/abc/{path}#L{line}"
	for _, tc := range []struct{ remote, want string }{
		{"https://github.com/acme/widgets.git", linked},
		{"https://github.com/acme/widgets", linked},
		{"git@github.com:acme/widgets.git", linked},
		{"ssh://git@github.com/acme/widgets.git", linked},
		{"https://gitlab.com/acme/widgets.git", ""},
		{"https://github.com/acme", ""},
		{"https://github.com/acme/widgets/extra", ""},
		{"", ""},
	} {
		assert.Equal(t, tc.want, githubBlobTemplate(tc.remote, "abc"), tc.remote)
	}
	assert.Empty(t, githubBlobTemplate("https://github.com/acme/widgets", ""), "no revision, no link")

	ws := chainWorkspace()
	ws.remote = ""
	w := get(t, ws, "/api/v1/diagrams/projects?focus=tools&depth=0")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out Rendered
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Empty(t, out.SourceURL)
}

// The console lays a figure out itself only if it runs the code the server runs.
func TestDiagramSourceServesTheEmbeddedLibrary(t *testing.T) {
	w := get(t, chainWorkspace(), SourcePath)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out Sources
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, map[string]string{"libs/figure/figure.buzz": figuresrc.Source}, out.Files)
}

func anchors(nodes []Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Anchor)
	}
	return out
}
