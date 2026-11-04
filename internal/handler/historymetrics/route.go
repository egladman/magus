package historymetrics

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/service/console"
)

// route serves GET /api/v1/history-metrics: the Board as JSON.
type route struct {
	handler.Base
	src source
}

// NewHandler returns the GET /api/v1/history-metrics handler reading from src.
func NewHandler(src source, log *slog.Logger) *route {
	h := &route{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *route) serve(w http.ResponseWriter, r *http.Request) {
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
