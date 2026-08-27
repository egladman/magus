package status

import (
	"context"
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
	if !handler.AllowGet(w, r) {
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
	handler.WriteJSON(w, view)
}
