package historymetrics

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/service/console"
)

// Handler serves GET /api/v1/history-metrics: the Board as JSON.
type Handler struct {
	handler.Base
	src source
}

// NewHandler returns the GET /api/v1/history-metrics handler reading from src.
func NewHandler(src source, log *slog.Logger) *Handler {
	h := &Handler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	if !handler.AllowGet(w, r) {
		return
	}
	board, err := h.src.HistoryMetrics(r.Context())
	if err != nil {
		if errors.Is(err, console.ErrNoWorkspace) {
			handler.Refuse(w, r, rpcerr.WorkspaceNotWired())
			return
		}
		h.Fail(w, r, "history-metrics", err)
		return
	}
	handler.WriteJSON(w, r, board)
}
