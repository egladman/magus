package status

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

// insightSource is the narrow consumer contract the insight and flake handlers need from the
// console service: assemble the VCS-history lenses and the runtime-history flake read. It is
// satisfied by *console.Service; the handler package holds no concrete service.
type insightSource interface {
	Insight(ctx context.Context) (types.InsightView, error)
	Flake(ctx context.Context) (types.FlakeReport, error)
}

// InsightHandler serves GET /api/v1/insight: the four VCS-history lenses (hotspots, affinity,
// ownership, trend) as JSON (types.InsightView), computed in-daemon from one bounded git-log
// scan and cached by the service. A service with no workspace yields 503, not 500.
type InsightHandler struct {
	handler.Base
	src insightSource
}

// NewInsightHandler returns the GET /api/v1/insight handler reading from src.
func NewInsightHandler(src insightSource, log *slog.Logger) *InsightHandler {
	h := &InsightHandler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *InsightHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	view, err := h.src.Insight(r.Context())
	if err != nil {
		if errors.Is(err, console.ErrNoWorkspace) {
			http.Error(w, "workspace unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "insight error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, view)
}

// FlakeHandler serves GET /api/v1/flake: per-(project, target) flakiness as JSON
// (types.FlakeReport), read from the shared runtime-history file and scored in-daemon. It is
// a pure file read, so it does not require a workspace.
type FlakeHandler struct {
	handler.Base
	src insightSource
}

// NewFlakeHandler returns the GET /api/v1/flake handler reading from src.
func NewFlakeHandler(src insightSource, log *slog.Logger) *FlakeHandler {
	h := &FlakeHandler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *FlakeHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	report, err := h.src.Flake(r.Context())
	if err != nil {
		http.Error(w, "flake error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, report)
}

// allowGet answers a CORS preflight (204) and rejects non-GET methods (405), returning false
// when the caller should stop. It mirrors the method gate on the status handler.
func allowGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// writeJSON marshals v and writes it as an uncached JSON body, matching the status handler's
// no-store posture (these reads reflect live daemon state).
func writeJSON(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "marshal error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
