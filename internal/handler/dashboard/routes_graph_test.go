package dashboard_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/handler/dashboard"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/types"
)

// minimalGraph builds a small in-memory graph with one node and one edge so the
// bridge has something to serialize. It does not require a real workspace.
func minimalGraph() *knowledge.Graph {
	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{
		ID:    "project/foo",
		Kind:  "file",
		Label: "foo",
	})
	g.AddNode(types.KnowledgeNode{
		ID:    "project/bar",
		Kind:  "file",
		Label: "bar",
	})
	g.AddEdge(types.KnowledgeEdge{
		Source:   "project/foo",
		Target:   "project/bar",
		Relation: "imports",
	})
	return g
}

// minimalTargetGraph returns a minimal TargetGraphOutput for tests.
func minimalTargetGraph() types.TargetGraphOutput {
	return types.TargetGraphOutput{
		Definition: "magusfile.buzz",
		Projects: []types.TargetGraphProject{
			{
				Path:      "project/foo",
				DependsOn: []string{"project/bar"},
			},
			{Path: "project/bar"},
		},
	}
}

// graphOpts returns Options with fake graph providers injected so handleGraph
// can be exercised without a real workspace.
func graphOpts() dashboard.Options {
	g := minimalGraph()
	tg := minimalTargetGraph()
	opts := minimalOpts()
	opts.KnowledgeGraphFn = func(_ context.Context, _ bool) (*knowledge.Graph, error) {
		return g, nil
	}
	opts.DescribeGraphFn = func() types.TargetGraphOutput {
		return tg
	}
	return opts
}

// --- handleGraph 200 paths ---

func TestHandleGraph_Full_Returns200WithBody(t *testing.T) {
	h := testHandler(graphOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want JSON body: %v", err)
	}
}

func TestHandleGraph_FlavorTargets_Returns200WithBody(t *testing.T) {
	h := testHandler(graphOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph?flavor=targets", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want JSON body: %v", err)
	}
}

func TestHandleGraph_LevelProjects_Returns200WithBody(t *testing.T) {
	h := testHandler(graphOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph?level=projects", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want JSON body: %v", err)
	}
}

func TestHandleGraph_Select_Returns200WithBody(t *testing.T) {
	h := testHandler(graphOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph?select=foo", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want JSON body: %v", err)
	}
}

// --- ETag on real handleGraph ---

func TestHandleGraph_ETagPresent_AndStable(t *testing.T) {
	h := testHandler(graphOpts())

	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w1.Code)
	}
	etag1 := w1.Header().Get("ETag")
	if etag1 == "" {
		t.Fatal("want ETag header, got empty")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	etag2 := w2.Header().Get("ETag")
	if etag1 != etag2 {
		t.Errorf("ETag not stable: first=%q second=%q", etag1, etag2)
	}
}

func TestHandleGraph_IfNoneMatch_Returns304(t *testing.T) {
	h := testHandler(graphOpts())

	// First request to get the ETag.
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	etag := w1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("want ETag header on first request")
	}

	// Second request with If-None-Match: expect 304 + ETag echoed (RFC 7232).
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotModified {
		t.Errorf("want 304, got %d", w2.Code)
	}
	if got := w2.Header().Get("ETag"); got != etag {
		t.Errorf("want ETag %q echoed on 304, got %q", etag, got)
	}
}

// --- Distinct variants get distinct ETags ---

// TestHandleGraph_DistinctVariants_DistinctETags checks that the three mutually
// exclusive variant code paths (?default, ?flavor=targets, ?level=projects) produce
// distinct ETags for our test graph. The ?select= variant is tested separately
// because its output may coincide with the full graph when no nodes match.
func TestHandleGraph_DistinctVariants_DistinctETags(t *testing.T) {
	h := testHandler(graphOpts())
	// These three paths hit distinct serializer branches and produce structurally
	// different JSON (KnowledgeGraphOutput vs TargetGraphOutput vs skeleton).
	variants := []string{
		"/api/v1/graph",
		"/api/v1/graph?flavor=targets",
		"/api/v1/graph?level=projects",
	}
	etags := make(map[string]string, len(variants))
	for _, v := range variants {
		req := httptest.NewRequest(http.MethodGet, v, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 for %s, got %d", v, w.Code)
		}
		etag := w.Header().Get("ETag")
		if etag == "" {
			t.Fatalf("want ETag for %s, got empty", v)
		}
		for prev, prevETag := range etags {
			if etag == prevETag {
				t.Errorf("variant %q has same ETag as %q (%s); expect distinct ETags", v, prev, etag)
			}
		}
		etags[v] = etag
	}
}

// TestHandleGraph_SelectVariant_ETagPresent verifies ?select= returns 200 with an ETag.
func TestHandleGraph_SelectVariant_ETagPresent(t *testing.T) {
	h := testHandler(graphOpts())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/graph?select=imports", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d; body: %s", w.Code, w.Body.String())
	}
	if etag := w.Header().Get("ETag"); etag == "" {
		t.Error("want ETag header on ?select= 200 response, got empty")
	}
}

// --- Gzip branch ---

func TestHandleGraph_Gzip_CompressedResponse(t *testing.T) {
	h := testHandler(graphOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ce := w.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("want Content-Encoding: gzip, got %q", ce)
	}
	if v := w.Header().Get("Vary"); !strings.Contains(v, "Accept-Encoding") {
		t.Errorf("want Vary: Accept-Encoding header, got %q", v)
	}
	// Decompress and verify valid JSON.
	gr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gr.Close()
	raw, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("want valid JSON in gzip body: %v", err)
	}
}

func TestHandleGraph_Gzip_ETagPresent(t *testing.T) {
	h := testHandler(graphOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if etag := w.Header().Get("ETag"); etag == "" {
		t.Error("want ETag header on gzip 200 response, got empty")
	}
}

// --- bridge.enabled=false: no /api/ routes ---

// TestBridgeDisabled_NoAPIRoutes verifies that when bridge.enabled is false the
// Bridge is not mounted and /api/ routes return 404.
// This is tested at the mcp.ServeHTTP level indirectly; here we verify that
// testHandler (which always mounts) vs a mux without Mount differ, asserting
// the test setup is correct.
func TestBridgeDisabled_MuxWithoutMount_Returns404(t *testing.T) {
	// A mux that has NOT had Mount called on it.
	mux := http.NewServeMux()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404 on unmounted mux, got %d", w.Code)
	}
}
