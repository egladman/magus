package status

import (
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/types"
)

// ledgerSource is the narrow repository contract the ledger handler needs: read every
// declared delegation row. Satisfied by *ledger.Store, so this package holds no store
// logic - it serves what the store already knows.
type ledgerSource interface {
	List() ([]types.DelegationUnit, error)
}

// LedgerHandler serves GET /api/v1/ledger: the orchestrating agent's declared plan as
// JSON ({"units":[...],"overlaps":[...]}), in the order the rows were recorded, so the
// console's delegation drawer can join them to agent activity by unit id.
//
// The overlaps are derived on every read and stored nowhere (types.NewDelegationReport),
// which is why this handler needs nothing from the store but its rows.
//
// Read-only, and the rows it serves are DECLARATIONS. Nothing magus does is gated on
// them; the console renders what an agent said it intended, never a verdict magus
// reached. See types.DelegationUnit.
type LedgerHandler struct {
	handler.Base
	src ledgerSource
}

// NewLedgerHandler returns the GET /api/v1/ledger handler reading from src.
func NewLedgerHandler(src ledgerSource, log *slog.Logger) *LedgerHandler {
	h := &LedgerHandler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *LedgerHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	units, err := h.src.List()
	if err != nil {
		http.Error(w, "ledger error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// An unwritten ledger is [] rather than null: the drawer renders a list, and a
	// workspace where nobody has delegated yet is empty, not broken.
	if units == nil {
		units = []types.DelegationUnit{}
	}
	writeJSON(w, types.NewDelegationReport(units))
}
