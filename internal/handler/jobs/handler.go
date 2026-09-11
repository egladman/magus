package jobs

import (
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/types"
)

// jobSource is the narrow repository contract the jobs handler needs: read every
// declared job row. Satisfied by *job.Store, so this package holds no store
// logic: it serves what the store already knows.
type jobSource interface {
	List() ([]types.Job, error)
}

// Handler serves GET /api/v1/jobs: the orchestrating agent's declared plan as
// JSON ({"jobs":[...],"overlaps":[...]}), in the order the rows were recorded, so the
// console's job drawer can join them to agent activity by job id.
//
// The overlaps are derived on every read and stored nowhere (types.NewJobList),
// which is why this handler needs nothing from the store but its rows.
//
// Read-only, and the rows it serves are DECLARATIONS. Nothing magus does is gated on
// them; the console renders what an agent said it intended, never a verdict magus
// reached. See types.Job.
type Handler struct {
	handler.Base
	src jobSource
}

// NewHandler returns the GET /api/v1/jobs handler reading from src.
func NewHandler(src jobSource, log *slog.Logger) *Handler {
	h := &Handler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	if !handler.AllowGet(w, r) {
		return
	}
	jobs, err := h.src.List()
	if err != nil {
		http.Error(w, "jobs error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// An unwritten store serves "jobs":[] rather than null: the drawer renders a list,
	// and a workspace where nobody has handed out a job yet is empty, not broken.
	// Normalized by the constructor, so this route and the MCP tool cannot disagree about
	// the shape.
	handler.WriteJSON(w, types.NewJobList(jobs))
}
