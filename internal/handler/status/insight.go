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

// insightSource is the narrow consumer contract the insight handler needs from the console
// service: assemble every insight lens (the four VCS-history lenses plus the folded-in
// runtime-history volatility lens). It is satisfied by *console.Service; the handler package
// holds no concrete service.
type insightSource interface {
	Insight(ctx context.Context) (types.InsightView, error)
}

// InsightHandler serves GET /api/v1/insight: every insight lens as JSON (types.InsightView) -
// the four VCS-history lenses (hotspots, affinity, ownership, trend) from one bounded git-log
// scan cached by the service, plus the run-outcome volatility lens folded in fresh. A service
// with no workspace yields 503, not 500.
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

// allowGet answers a CORS preflight (204) and rejects non-GET methods (405), returning false
// when the caller should stop. It mirrors the method gate the other read handlers use.
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

// writeJSON marshals v and writes it as an uncached JSON body, matching the read handlers'
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
