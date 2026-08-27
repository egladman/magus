package insight

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/service/console"
)

// Handler serves GET /api/v1/insight: every insight lens as JSON (types.InsightView) -
// the four VCS-history lenses (hotspots, affinity, ownership, trend) from one bounded git-log
// scan cached by the service, plus the run-outcome volatility lens folded in fresh. A service
// with no workspace yields 503, not 500.
type Handler struct {
	handler.Base
	src Source
}

// NewHandler returns the GET /api/v1/insight handler reading from src.
func NewHandler(src Source, log *slog.Logger) *Handler {
	h := &Handler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
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
