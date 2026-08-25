package status

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/diff"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

func TestDiffSessionHandler_GetReadsAttachedSessionWithoutMutatingIt(t *testing.T) {
	root := t.TempDir()
	store := diff.NewStore("")
	h := NewDiffSessionHandler(store, fixedOrigin{branch: "feat/x"}, root, "", nil)

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

// post is one session mutation, for the publish cases below.
func post(t *testing.T, h *DiffSessionHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/diff/session", strings.NewReader(body))
	h.ServeHTTP(w, r)
	return w
}

// Publishing with no provider wired FAILS, and says which of the several "cannot publish"
// reasons applies. Every other write on this route answers with the session and lets
// bookkeeping sort itself out; this one sends sentences to colleagues, so reporting a success
// it did not have would leave a reader believing their review landed when it never left.
func TestPublishFailsLoudlyWithNoProvider(t *testing.T) {
	root := t.TempDir()
	store := diff.NewStore("")
	h := NewDiffSessionHandler(store, fixedOrigin{branch: "feat/x"}, root, "", nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")
	store.AddComment(root, types.DiffComment{Path: "a.go", Line: 4, Body: "why"}, types.DiffAuthorHuman)

	w := post(t, h, `{"op":"publish"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no review provider wired") {
		t.Fatalf("the reason must travel, got %q", w.Body.String())
	}

	// NOTHING may be marked published by a send that did not happen. A draft marked on a
	// failed publish is lost: it never reaches the host and never offers to go again.
	sess := store.Get(root)
	if len(sess.Comments) != 1 || sess.Comments[0].Published {
		t.Fatalf("a failed publish marked a draft: %#v", sess.Comments)
	}
}

// Nothing drafted is not a failure. A reader who publishes twice, or who has drafted nothing,
// made no mistake and gets the session back unchanged.
func TestPublishingNothingIsNotAnError(t *testing.T) {
	root := t.TempDir()
	store := diff.NewStore("")
	h := NewDiffSessionHandler(store, fixedOrigin{branch: "feat/x"}, root, "", nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")

	if w := post(t, h, `{"op":"publish"}`); w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}

// An AGENT's comment is never publishable. It reaches this session through MCP, and the set
// sent is derived from the session rather than named by the request precisely so no caller can
// widen it - an agent's remark going out under the human's name is the failure this prevents.
func TestAnAgentCommentIsNotPublishable(t *testing.T) {
	root := t.TempDir()
	store := diff.NewStore("")
	h := NewDiffSessionHandler(store, fixedOrigin{branch: "feat/x"}, root, "", nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")
	store.AddComment(root, types.DiffComment{Path: "a.go", Line: 2, Body: "agent"}, types.DiffAuthorAgent)

	// Only an agent comment exists, so there is nothing to send and no provider is consulted.
	if w := post(t, h, `{"op":"publish"}`); w.Code != http.StatusOK {
		t.Fatalf("an agent draft must not be publishable, got %d: %s", w.Code, w.Body.String())
	}
}
