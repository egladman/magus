package status

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// fakeLedgerSource is a ledgerSource returning canned rows or a fixed error.
type fakeLedgerSource struct {
	units []types.DelegationUnit
	err   error
}

func (f fakeLedgerSource) List() ([]types.DelegationUnit, error) { return f.units, f.err }

func TestLedgerHandler_Returns200WithUnits(t *testing.T) {
	src := fakeLedgerSource{units: []types.DelegationUnit{
		{ID: "unit-a", Goal: "ship the store", Checkpoint: "60dc9151", OwnedPaths: []string{"internal/ledger"}, State: types.StateRunning},
		{ID: "scout", ReadOnly: true, State: types.StateNoReturn},
	}}
	h := NewLedgerHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/ledger", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var out struct {
		Units []types.DelegationUnit `json:"units"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want valid JSON: %v; body %s", err, w.Body.String())
	}
	if len(out.Units) != 2 {
		t.Fatalf("want 2 rows, got %d", len(out.Units))
	}
	// The join key the console drawer needs, and the two fields a row cannot be read
	// without: which state it ended in, and what tree it was handed.
	if out.Units[0].ID != "unit-a" || out.Units[0].Checkpoint != "60dc9151" {
		t.Errorf("want the declared row verbatim, got %+v", out.Units[0])
	}
	if out.Units[1].State != types.StateNoReturn || !out.Units[1].ReadOnly {
		t.Errorf("want the abbreviated no_return row, got %+v", out.Units[1])
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("want no-store, got %q", got)
	}
}

func TestLedgerHandler_EmptyLedgerServesEmptyList(t *testing.T) {
	h := NewLedgerHandler(fakeLedgerSource{}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/ledger", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	// [] not null: a workspace where nobody has delegated yet is empty, not broken.
	if got := w.Body.String(); got != `{"units":[]}` {
		t.Errorf(`want {"units":[]}, got %s`, got)
	}
}

func TestLedgerHandler_ErrorReturns500(t *testing.T) {
	h := NewLedgerHandler(fakeLedgerSource{err: errors.New("corrupt ledger")}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/ledger", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", w.Code)
	}
}

func TestLedgerHandler_MethodGate(t *testing.T) {
	h := NewLedgerHandler(fakeLedgerSource{}, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/api/v1/ledger", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("want 204 for the CORS preflight, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/ledger", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405; the write door is the MCP tool, not this endpoint, got %d", w.Code)
	}
}
