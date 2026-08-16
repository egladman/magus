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
		{
			ID: "unit-a", Goal: "ship the store", Checkpoint: "60dc9151",
			OwnedPaths: []string{"internal/ledger"}, State: types.StateRunning,
			Updated:  1755300000,
			Releases: []types.DelegationRelease{{Path: "types/delegation.go", Digest: "sha256:abc", ReleasedAt: 1755299000}},
		},
		{ID: "scout", ReadOnly: true, State: types.StateNoReturn},
	}}
	h := NewLedgerHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/ledger", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var out types.DelegationReport
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
	// The heartbeat: a reader watching for a unit nobody has touched needs the row's
	// own timestamp, not the moment it happened to read the route.
	if out.Units[0].Updated != 1755300000 {
		t.Errorf("want the row's updated stamp on the wire, got %d", out.Units[0].Updated)
	}
	// What the next agent inherits, and which version of it.
	if len(out.Units[0].Releases) != 1 || out.Units[0].Releases[0].Digest != "sha256:abc" {
		t.Errorf("want the released path and its digest, got %+v", out.Units[0].Releases)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("want no-store, got %q", got)
	}
}

// The overlap is derived on the read and stored nowhere, so the route reports it
// without either row saying anything about the other. A fact for the reader, not a
// verdict: nothing is blocked, reordered, or failed on account of it.
func TestLedgerHandler_ReportsOverlappingOwnedPaths(t *testing.T) {
	src := fakeLedgerSource{units: []types.DelegationUnit{
		{ID: "unit-a", OwnedPaths: []string{"internal/ledger"}, State: types.StateRunning},
		{ID: "unit-b", OwnedPaths: []string{"internal/ledger/store.go"}, State: types.StateDeclared},
		{ID: "unit-done", OwnedPaths: []string{"internal/ledger"}, State: types.StatePass},
	}}
	h := NewLedgerHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/ledger", nil))

	var out types.DelegationReport
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want valid JSON: %v; body %s", err, w.Body.String())
	}
	if len(out.Overlaps) != 1 {
		t.Fatalf("want one pair - the finished unit is not competing for anything - got %+v", out.Overlaps)
	}
	if out.Overlaps[0].UnitA != "unit-a" || out.Overlaps[0].UnitB != "unit-b" {
		t.Errorf("want the pair in ledger order, got %+v", out.Overlaps[0])
	}
	// Each side's own declaration, kept apart: they are different strings here, and a
	// reader who cannot tell which unit claimed which has nothing to act on.
	if len(out.Overlaps[0].PathsA) != 1 || out.Overlaps[0].PathsA[0] != "internal/ledger" {
		t.Errorf("want unit-a's declaration, got %v", out.Overlaps[0].PathsA)
	}
	if len(out.Overlaps[0].PathsB) != 1 || out.Overlaps[0].PathsB[0] != "internal/ledger/store.go" {
		t.Errorf("want unit-b's declaration, got %v", out.Overlaps[0].PathsB)
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
