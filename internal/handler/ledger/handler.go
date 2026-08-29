package ledger

import (
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/types"
)

// ledgerSource is the narrow repository contract the ledger handler needs: read every
// declared lease row. Satisfied by *ledger.Store, so this package holds no store
// logic - it serves what the store already knows.
type ledgerSource interface {
	List() ([]types.Lease, error)
}

// Handler serves GET /api/v1/ledger: the orchestrating agent's declared plan as
// JSON ({"leases":[...],"overlaps":[...]}), in the order the rows were recorded, so the
// console's lease drawer can join them to agent activity by lease id.
//
// The overlaps are derived on every read and stored nowhere (types.NewLeaseReport),
// which is why this handler needs nothing from the store but its rows.
//
// Read-only, and the rows it serves are DECLARATIONS. Nothing magus does is gated on
// them; the console renders what an agent said it intended, never a verdict magus
// reached. See types.Lease.
type Handler struct {
	handler.Base
	src ledgerSource
}

// NewHandler returns the GET /api/v1/ledger handler reading from src.
func NewHandler(src ledgerSource, log *slog.Logger) *Handler {
	h := &Handler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	if !handler.AllowGet(w, r) {
		return
	}
	leases, err := h.src.List()
	if err != nil {
		http.Error(w, "ledger error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// An unwritten ledger serves "leases":[] rather than null - the drawer renders a list,
	// and a workspace where nobody has handed out a lease yet is empty, not broken.
	// Normalized by the constructor, so this route and the MCP tool cannot disagree about
	// the shape.
	handler.WriteJSON(w, types.NewLeaseReport(leases))
}
