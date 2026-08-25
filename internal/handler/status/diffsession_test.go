package status

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egladman/magus/internal/diff"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

func TestDiffSessionHandler_GetReadsAttachedSessionWithoutMutatingIt(t *testing.T) {
	root := t.TempDir()
	store := diff.NewStore("")
	h := NewDiffSessionHandler(store, root, "", nil)

	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/v1/diff/session", nil))
	if missing.Code != http.StatusConflict {
		t.Fatalf("missing session: want 409, got %d", missing.Code)
	}

	want := store.Attach(root, "main", types.Diff{Base: "main"}, "snapshot-a")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/session", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got types.DiffSession
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.AsOf != "snapshot-a" || got.Base != "main" {
		t.Fatalf("unexpected session: %#v", got)
	}
}
