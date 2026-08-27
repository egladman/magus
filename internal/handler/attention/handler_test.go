package attention

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
)

// plantStore points the per-repository session store at a temp directory and returns the
// repository root the handler should be built with, plus the store dir itself.
//
// XDG_STATE_HOME is set rather than the store injected, so the test exercises the REAL
// sessions.Dir resolution the handler performs. A handler that resolved the wrong directory
// would serve an empty queue forever and look perfectly healthy doing it - which is the one
// failure a fake store could not catch.
func plantStore(t *testing.T) (root, dir string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root = t.TempDir()
	dir, err := sessions.Dir(root)
	if err != nil {
		t.Fatalf("resolve store: %v", err)
	}
	return root, dir
}

// raise files one open request through the package's own producer, so the planted store holds
// records written exactly the way `magus session notify` writes them.
func raise(t *testing.T, dir, agentSession, message string) string {
	t.Helper()
	id, opened, err := sessions.OpenRequest(dir, agentSession, sessions.AttentionOpen{
		Outcome: "waiting",
		Source:  "claude/Notification",
		Where:   "/repo",
		Message: message,
	}, sessions.SessionStart{Workspace: "/repo"})
	if err != nil {
		t.Fatalf("open request: %v", err)
	}
	if !opened {
		t.Fatalf("want a freshly opened request for %q", message)
	}
	return id
}

func getQueue(t *testing.T, h *Handler) (int, attentionView) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/attention", nil))
	var out attentionView
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("want valid JSON: %v; body %s", err, w.Body.String())
		}
	}
	return w.Code, out
}

func postDispose(t *testing.T, h *Handler, payload string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/attention", strings.NewReader(payload)))
	return w
}

func TestAttentionHandler_ServesTheOpenQueue(t *testing.T) {
	root, dir := plantStore(t)
	raise(t, dir, "agent-1", "needs the deploy key")

	h := NewHandler(root, "v0.0.0-test", nil)
	code, out := getQueue(t, h)
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if out.Store != dir {
		t.Errorf("want the store path the CLI would print (%s), got %s", dir, out.Store)
	}
	if len(out.Requests) != 1 {
		t.Fatalf("want the one open request, got %+v", out.Requests)
	}
	// The fields a row cannot be acted on without: what to close, and what it is about.
	if out.Requests[0].Message != "needs the deploy key" || out.Requests[0].Outcome != "waiting" {
		t.Errorf("want the raised block verbatim, got %+v", out.Requests[0])
	}
	if out.Requests[0].OpenedMs == 0 {
		t.Error("want the open timestamp on the wire; the queue is ordered and aged by it")
	}
}

func TestAttentionHandler_EmptyQueueServesEmptyList(t *testing.T) {
	root, _ := plantStore(t)
	h := NewHandler(root, "v0.0.0-test", nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/attention", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for a repository nobody has raised a block in, got %d", w.Code)
	}
	// [] not null. An empty queue is the GOOD state, and the surface renders a list either
	// way; null would make "nothing is waiting" indistinguishable from a broken read.
	if got := w.Body.String(); !strings.HasPrefix(got, `{"requests":[]`) {
		t.Errorf(`want "requests":[], got %s`, got)
	}
}

func TestAttentionHandler_DisposeClosesTheRequest(t *testing.T) {
	root, dir := plantStore(t)
	id := raise(t, dir, "agent-1", "needs the deploy key")
	h := NewHandler(root, "v0.0.0-test", nil)

	w := postDispose(t, h, `{"id":"`+id+`","reason":"handed it over"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d; body %s", w.Code, w.Body.String())
	}
	var closed sessions.AttentionRequest
	if err := json.Unmarshal(w.Body.Bytes(), &closed); err != nil {
		t.Fatalf("want the disposed row back: %v; body %s", err, w.Body.String())
	}
	if !closed.Disposed || closed.Note != "handed it over" {
		t.Errorf("want the row reporting its own closure and reason, got %+v", closed)
	}
	// The queue is what a reader trusts, so the disposal has to be visible THERE and not only
	// in the answer to the write.
	if _, out := getQueue(t, h); len(out.Requests) != 0 {
		t.Errorf("want the queue empty after disposal, got %+v", out.Requests)
	}
}

// A prefix, the way a short revision names a commit. The id is a truncated digest, so it is
// always copied off a listing; refusing anything but the full twelve characters would be
// friction the CLI does not impose either.
func TestAttentionHandler_DisposeAcceptsAnUnambiguousPrefix(t *testing.T) {
	root, dir := plantStore(t)
	id := raise(t, dir, "agent-1", "needs the deploy key")
	h := NewHandler(root, "v0.0.0-test", nil)

	if w := postDispose(t, h, `{"id":"`+id[:8]+`"}`); w.Code != http.StatusOK {
		t.Fatalf("want 200 for an unambiguous prefix, got %d; body %s", w.Code, w.Body.String())
	}
}

// The disposing identity is stamped from the ROUTE, never from the payload: an agent reaches
// magus through MCP and cannot arrive here, so a write that landed here came from the console.
// Without this the store cannot say which surface closed a request, and "who answered this"
// becomes an inference from an empty field.
func TestAttentionHandler_DisposeStampsTheConsoleAsTheSurface(t *testing.T) {
	root, dir := plantStore(t)
	id := raise(t, dir, "agent-1", "needs the deploy key")
	h := NewHandler(root, "v0.0.0-test", nil)

	if w := postDispose(t, h, `{"id":"`+id+`"}`); w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d; body %s", w.Code, w.Body.String())
	}

	fold, err := sessions.ReadAll(dir)
	if err != nil {
		t.Fatalf("re-read store: %v", err)
	}
	var found bool
	for _, s := range sessions.Summarize(fold) {
		if !strings.HasPrefix(s.Command, "session dispose ") {
			continue
		}
		found = true
		if s.Host != consoleSessionHost {
			t.Errorf("want the disposing session stamped %q, got %q", consoleSessionHost, s.Host)
		}
		// Version is stamped on the record but Summarize does not surface it, so the
		// fold can only prove the workspace half of the CLI parity here.
		if s.Workspace != root {
			t.Errorf("want the same workspace the CLI stamps, got %+v", s)
		}
	}
	if !found {
		t.Error("want a disposing session recorded in the store")
	}
}

// Every id starts "att-", so that prefix matches the whole queue. Ambiguity is refused rather
// than resolved by any rule the handler could invent: an id addresses a person's decision to
// close a block, and picking one for them closes a request nobody chose.
func TestAttentionHandler_AmbiguousPrefixIsRefused(t *testing.T) {
	root, dir := plantStore(t)
	raise(t, dir, "agent-1", "needs the deploy key")
	raise(t, dir, "agent-2", "needs review on the migration")
	h := NewHandler(root, "v0.0.0-test", nil)

	w := postDispose(t, h, `{"id":"att-"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d; body %s", w.Code, w.Body.String())
	}
	// The candidates, because the next thing the person does is pick one and the ids differ
	// only past the prefix they typed.
	if !strings.Contains(w.Body.String(), "att-") {
		t.Errorf("want the candidates named, got %s", w.Body.String())
	}
	if _, out := getQueue(t, h); len(out.Requests) != 2 {
		t.Errorf("want both requests still open after a refused disposal, got %+v", out.Requests)
	}
}

func TestAttentionHandler_UnknownIDReturns404(t *testing.T) {
	root, _ := plantStore(t)
	h := NewHandler(root, "v0.0.0-test", nil)

	if w := postDispose(t, h, `{"id":"att-000000000000"}`); w.Code != http.StatusNotFound {
		t.Errorf("want 404 for an id that names nothing, got %d", w.Code)
	}
}

// A request closes ONCE. A second disposal is recorded by the store but changes nothing, so
// answering 200 here would credit the caller with a closure it did not perform.
func TestAttentionHandler_SecondDisposeReturns409(t *testing.T) {
	root, dir := plantStore(t)
	id := raise(t, dir, "agent-1", "needs the deploy key")
	h := NewHandler(root, "v0.0.0-test", nil)

	if w := postDispose(t, h, `{"id":"`+id+`"}`); w.Code != http.StatusOK {
		t.Fatalf("want the first disposal to succeed, got %d", w.Code)
	}
	w := postDispose(t, h, `{"id":"`+id+`"}`)
	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d; body %s", w.Code, w.Body.String())
	}
}

func TestAttentionHandler_MalformedBodyReturns400(t *testing.T) {
	root, _ := plantStore(t)
	h := NewHandler(root, "v0.0.0-test", nil)

	if w := postDispose(t, h, `{`); w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestAttentionHandler_MethodGate(t *testing.T) {
	root, _ := plantStore(t)
	h := NewHandler(root, "v0.0.0-test", nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/api/v1/attention", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("want 204 for the CORS preflight, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/attention", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405; disposing is a POST and there is no delete door, got %d", w.Code)
	}
}
