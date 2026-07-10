package graph

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSource is a graphSource returning canned knowledge/target graphs.
type fakeSource struct{}

func (fakeSource) Graph(_ context.Context, flavor, _ string) (types.KnowledgeGraphOutput, error) {
	return types.KnowledgeGraphOutput{
		Definition:    "test graph (" + flavor + ")",
		SchemaVersion: types.KnowledgeSchemaVersion,
		Directed:      true,
		NodeCount:     2,
		EdgeCount:     1,
		SourceBaseURL: "https://github.com/o/r/blob/main",
		Nodes: []types.KnowledgeNode{
			{ID: "project/foo", Kind: "file", Label: "foo"},
			{ID: "project/bar", Kind: "file", Label: "bar"},
		},
		Links: []types.KnowledgeEdge{
			{Source: "project/foo", Target: "project/bar", Relation: "imports"},
		},
	}, nil
}

func (fakeSource) TargetGraph(context.Context) (types.TargetGraphOutput, error) {
	return types.TargetGraphOutput{
		Definition: "magusfile.buzz",
		Projects: []types.TargetGraphProject{
			{Path: "project/foo", DependsOn: []string{"project/bar"}},
			{Path: "project/bar"},
		},
	}, nil
}

func newHandler() http.Handler { return NewGraphHandler(fakeSource{}, nil) }

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// --- 200 flavor paths ---

func TestGraph_Flavors_Return200(t *testing.T) {
	h := newHandler()
	for _, target := range []string{"/api/v1/graph", "/api/v1/graph?level=projects", "/api/v1/graph?select=foo", "/api/v1/graph?flavor=targets"} {
		w := get(t, h, target)
		require.Equalf(t, http.StatusOK, w.Code, "target %s body %s", target, w.Body.String())
		var out map[string]any
		require.NoErrorf(t, json.Unmarshal(w.Body.Bytes(), &out), "target %s", target)
	}
}

// TestGraph_SnakeCaseFieldNames asserts the knowledge-graph protojson output uses the
// snake_case field names the explorer parses (matching the domain json tags), not the proto
// camelCase defaults.
func TestGraph_SnakeCaseFieldNames(t *testing.T) {
	w := get(t, newHandler(), "/api/v1/graph")
	require.Equal(t, http.StatusOK, w.Code)

	var out map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	for _, key := range []string{"definition", "schema_version", "directed", "node_count", "edge_count", "source_base", "nodes", "links"} {
		assert.Containsf(t, out, key, "want snake_case key %q in %v", key, keys(out))
	}
	// Reject the camelCase spellings protojson would emit without UseProtoNames.
	for _, bad := range []string{"schemaVersion", "nodeCount", "edgeCount", "sourceBase"} {
		assert.NotContainsf(t, out, bad, "unexpected camelCase key %q", bad)
	}

	var nodes []map[string]any
	require.NoError(t, json.Unmarshal(out["nodes"], &nodes))
	require.NotEmpty(t, nodes)
	for _, k := range []string{"id", "kind", "label"} {
		assert.Contains(t, nodes[0], k)
	}

	var links []map[string]any
	require.NoError(t, json.Unmarshal(out["links"], &links))
	require.NotEmpty(t, links)
	for _, k := range []string{"source", "target", "relation"} {
		assert.Contains(t, links[0], k)
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestGraph_TargetsFlavorShape confirms the targets flavor keeps its domain JSON (projects +
// definition), which is what the explorer's detectFlavor keys on.
func TestGraph_TargetsFlavorShape(t *testing.T) {
	w := get(t, newHandler(), "/api/v1/graph?flavor=targets")
	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Contains(t, out, "projects")
	assert.Contains(t, out, "definition")
}

// --- param validation ---

func TestGraph_BadParams_Return400(t *testing.T) {
	h := newHandler()
	for _, target := range []string{"/api/v1/graph?flavor=bogus", "/api/v1/graph?level=all", "/api/v1/graph?flavor=targets&level=projects"} {
		w := get(t, h, target)
		assert.Equalf(t, http.StatusBadRequest, w.Code, "target %s", target)
	}
}

func TestGraph_MethodNotAllowed(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/graph", nil)
	w := httptest.NewRecorder()
	newHandler().ServeHTTP(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- ETag / 304 ---

func TestGraph_ETagStableAnd304(t *testing.T) {
	h := newHandler()
	w1 := get(t, h, "/api/v1/graph")
	etag := w1.Header().Get("ETag")
	require.NotEmpty(t, etag)
	assert.Equal(t, etag, get(t, h, "/api/v1/graph").Header().Get("ETag"), "ETag stable")

	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil)
	r.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Equal(t, http.StatusNotModified, w.Code)
	assert.Equal(t, etag, w.Header().Get("ETag"), "304 echoes the ETag")
}

// TestGraph_DistinctVariantsDistinctETags checks the flavor branches produce distinct bodies.
func TestGraph_DistinctVariantsDistinctETags(t *testing.T) {
	h := newHandler()
	seen := map[string]string{}
	for _, target := range []string{"/api/v1/graph", "/api/v1/graph?level=projects", "/api/v1/graph?flavor=targets"} {
		etag := get(t, h, target).Header().Get("ETag")
		require.NotEmpty(t, etag)
		for prev, prevTarget := range seen {
			assert.NotEqualf(t, prev, etag, "%s and %s share an ETag", target, prevTarget)
		}
		seen[etag] = target
	}
}

// --- gzip ---

func TestGraph_Gzip(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	newHandler().ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	assert.Contains(t, w.Header().Get("Vary"), "Accept-Encoding")
	assert.NotEmpty(t, w.Header().Get("ETag"))

	gr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	defer gr.Close()
	raw, err := io.ReadAll(gr)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
}

// TestGraph_SelectDoesNotIncludeAmbiguousMatch is a smoke check that ?select= routes to the
// select flavor (its body differs from full for a non-matching term is not asserted here; we
// only confirm a 200 + JSON, since Select semantics are covered in the knowledge package).
func TestGraph_SelectSmoke(t *testing.T) {
	w := get(t, newHandler(), "/api/v1/graph?select=imports")
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), "application/json"))
}
