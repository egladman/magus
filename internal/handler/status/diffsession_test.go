package status

import (
	"context"
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

// fixedOrigin stands in for the workspace, which this package deliberately does not hold.
type fixedOrigin struct {
	branch string
	remote string
}

func (f fixedOrigin) ReviewOrigin(context.Context) types.ReviewOrigin {
	return types.ReviewOrigin{Branch: f.branch, Remote: f.remote}
}

// With no provider wired there is no review, and that is a 200 with a reason rather than an
// error. Every "cannot review here" - no provider, no pull request, an unreachable host -
// leaves the reader with the same options, and rendering any of them as a failure would
// accuse them of something they did not do.
func TestReviewLookupWithNoProviderIsNotAnError(t *testing.T) {
	h := NewDiffReviewHandler(fixedOrigin{branch: "feat/x"}, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/review", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got diffReviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Number != 0 || got.Reason == "" {
		t.Fatalf("want a closed target carrying its reason, got %#v", got)
	}
	// Never null: a client iterating threads must not have to guard a state that means the
	// same thing an empty list does.
	if got.Threads == nil {
		t.Fatal("threads must be an empty array, not null")
	}
}

// A daemon with no workspace has no branch to look a review up for, and says so instead of
// panicking on a nil source.
func TestReviewLookupWithoutAWorkspace(t *testing.T) {
	h := NewDiffReviewHandler(nil, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/review", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got diffReviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Reason != "no workspace" {
		t.Fatalf("want the workspace-less reason, got %#v", got)
	}
}

// A reply is outward-facing, so it fails the way publish does rather than the way a cursor
// sync does. A caller told the reply was sent when it never left believes the conversation is
// finished, and the colleague waiting on it hears nothing.
func TestReplyFailsLoudlyWithNoProvider(t *testing.T) {
	root := t.TempDir()
	store := diff.NewStore("")
	h := NewDiffSessionHandler(store, fixedOrigin{branch: "feat/x"}, root, "", nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")

	w := post(t, h, `{"op":"reply","id":"th1","body":"agreed"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no review provider wired") {
		t.Fatalf("the reason must travel, got %q", w.Body.String())
	}
}

// An empty reply is caught before a provider is consulted. "Nothing to say" is the caller's
// mistake, not the host's, and reporting it as a bad gateway would send the reader looking at
// their network.
func TestAnEmptyReplyIsRefusedBeforeTheHostIsAsked(t *testing.T) {
	root := t.TempDir()
	store := diff.NewStore("")
	h := NewDiffSessionHandler(store, fixedOrigin{branch: "feat/x"}, root, "", nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")

	w := post(t, h, `{"op":"reply","id":"th1"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "something to say") {
		t.Fatalf("want the empty-body reason, got %q", w.Body.String())
	}
}
