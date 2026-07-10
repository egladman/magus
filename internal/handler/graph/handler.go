package graph

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/types"
)

// graphSource is the narrow consumer contract this handler needs from the console service:
// the knowledge-graph flavors and the target graph. Satisfied by *console.Service.
type graphSource interface {
	Graph(ctx context.Context, flavor, sel string) (types.KnowledgeGraphOutput, error)
	TargetGraph(ctx context.Context) (types.TargetGraphOutput, error)
}

// graphHandler serves GET /api/v1/graph.
//
// Query params (the frozen v1 contract the browser Graph Explorer already speaks):
//   - (none)            -> the whole knowledge graph
//   - ?level=projects   -> skeleton: project nodes + project->project edges only
//   - ?select=<terms>   -> scoped neighborhood (graph export --select semantics)
//   - ?flavor=targets   -> the describe/target graph (types.TargetGraphOutput)
//
// At most one of flavor/level/select may be set; combinations are rejected. Knowledge-graph
// flavors are written as magus.graph.v1 protojson (snake_case, wire-compatible). The targets
// flavor has no proto twin and is written as its domain JSON. ETag is sha256 of the body;
// If-None-Match yields a 304. Gzip is applied by the NewGraphHandler wrapper.
type graphHandler struct {
	src graphSource
}

// NewGraphHandler returns the GET /api/v1/graph handler reading from src, wrapped so its
// body is gzip-compressed when the client accepts it.
func NewGraphHandler(src graphSource) http.Handler {
	return httpx.Gzip(&graphHandler{src: src})
}

func (h *graphHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	flavor := q.Get("flavor")
	level := q.Get("level")
	sel := q.Get("select")

	// Reject ambiguous combinations.
	set := 0
	for _, v := range []string{flavor, level, sel} {
		if v != "" {
			set++
		}
	}
	if set > 1 {
		http.Error(w, "at most one of flavor, level, select may be specified", http.StatusBadRequest)
		return
	}
	if flavor != "" && flavor != "targets" {
		http.Error(w, "flavor must be 'targets' or empty", http.StatusBadRequest)
		return
	}
	if level != "" && level != "projects" {
		http.Error(w, "level must be 'projects' or empty", http.StatusBadRequest)
		return
	}

	body, err := h.body(r.Context(), flavor, level, sel)
	if err != nil {
		http.Error(w, "graph build error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// ETag: sha256 of the body, hex-encoded and quoted per RFC 7232.
	sum := sha256.Sum256(body)
	etag := fmt.Sprintf(`"%x"`, sum)

	if r.Header.Get("If-None-Match") == etag {
		// RFC 7232 4.1: a 304 MUST include the ETag that would have been sent in the 200 so
		// the client can update its cache entry.
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// snakeJSON writes protobuf messages as snake_case JSON, matching the domain json tags the
// explorer parses.
var snakeJSON = protojson.MarshalOptions{UseProtoNames: true}

// body serves the requested flavor as bytes. Knowledge-graph flavors ride the magus.graph.v1
// proto through protojson; the targets flavor (no proto twin) is written as its domain JSON.
func (h *graphHandler) body(ctx context.Context, flavor, level, sel string) ([]byte, error) {
	if flavor == "targets" {
		tg, err := h.src.TargetGraph(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(tg)
	}

	var kgFlavor string
	switch {
	case level == "projects":
		kgFlavor = "skeleton"
	case sel != "":
		kgFlavor = "select"
	default:
		kgFlavor = "full"
	}
	g, err := h.src.Graph(ctx, kgFlavor, sel)
	if err != nil {
		return nil, err
	}
	return snakeJSON.Marshal(graphToProto(g))
}
