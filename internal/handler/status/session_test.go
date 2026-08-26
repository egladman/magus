package status

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/diff"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

type fakePatchSource struct {
	patch string
	err   error
	// gotPaths records what the handler passed through, so the scoping tests assert on the
	// call rather than on a response that would look identical either way.
	gotPaths []string
}

func (f *fakePatchSource) WorkingDiff(_ context.Context, paths []string) (string, error) {
	f.gotPaths = paths
	return f.patch, f.err
}

func TestDiffHandler_ReturnsPatch(t *testing.T) {
	src := &fakePatchSource{patch: "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n"}
	h := NewPatchHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/patch", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var out diffResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want valid JSON: %v; body %s", err, w.Body.String())
	}
	if out.Patch != src.patch {
		t.Errorf("patch not passed through verbatim:\ngot  %q\nwant %q", out.Patch, src.patch)
	}
	if out.Clean {
		t.Error("a non-empty patch must not report clean")
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("want no-store, got %q", got)
	}
}

// A clean tree is a STATE, not an absence, and the flag is what lets the console tell it
// apart from a patch it failed to read. Whitespace-only counts as clean: some backends emit
// a trailing newline for an empty diff.
func TestDiffHandler_CleanTreeIsFlaggedNotGuessed(t *testing.T) {
	for _, patch := range []string{"", "\n", "  \n\t"} {
		h := NewPatchHandler(&fakePatchSource{patch: patch}, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/patch", nil))
		var out diffResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("want valid JSON for %q: %v", patch, err)
		}
		if !out.Clean {
			t.Errorf("patch %q must report clean", patch)
		}
	}
}

func TestDiffHandler_ScopesToRepeatedPathParams(t *testing.T) {
	src := &fakePatchSource{}
	h := NewPatchHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/patch?path=a.go&path=b.go", nil))
	if len(src.gotPaths) != 2 || src.gotPaths[0] != "a.go" || src.gotPaths[1] != "b.go" {
		t.Errorf("want [a.go b.go], got %q", src.gotPaths)
	}
}

// An empty `path=` must scope to NOTHING, not to the empty pathspec - every backend reads
// that as the whole repository, which is the opposite of what the caller asked for.
func TestDiffHandler_EmptyPathParamDoesNotWidenScope(t *testing.T) {
	src := &fakePatchSource{}
	h := NewPatchHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/patch?path=&path=+&path=real.go", nil))
	if len(src.gotPaths) != 1 || src.gotPaths[0] != "real.go" {
		t.Errorf("want only [real.go], got %q", src.gotPaths)
	}
}

func TestDiffHandler_NoWorkspaceReturns503(t *testing.T) {
	h := NewPatchHandler(&fakePatchSource{err: console.ErrNoWorkspace}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/patch", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", w.Code)
	}
}

func TestDiffHandler_ErrorReturns500(t *testing.T) {
	h := NewPatchHandler(&fakePatchSource{err: errors.New("git boom")}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/patch", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", w.Code)
	}
}

func TestDiffHandler_RejectsNonGet(t *testing.T) {
	h := NewPatchHandler(&fakePatchSource{}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/diff/patch", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

func TestContextHandler_ReturnsBoundedWorkingTreeLines(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("one\ntwo\nthree\nfour\nfive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := &fakePatchSource{patch: "diff --git a/source.go b/source.go\n@@ -3 +3 @@\n-three\n+three\n"}
	h := NewContextHandler(root, src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/context?path=source.go&as_of="+diff.PatchDigest(src.patch)+"&start=3&end=3&radius=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var out contextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.AsOf != diff.PatchDigest(src.patch) || out.Start != 2 || len(out.Lines) != 3 || out.Lines[0] != "two" || out.Lines[2] != "four" {
		t.Fatalf("unexpected context: %#v", out)
	}
}

func TestContextHandler_RejectsPathEscape(t *testing.T) {
	h := NewContextHandler(t.TempDir(), &fakePatchSource{}, nil)
	for _, path := range []string{"../secret", ".."} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/context?path="+path+"&start=1&end=1", nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("path %q: want 400, got %d", path, w.Code)
		}
	}
}

func TestContextHandler_RejectsPathsOutsideTheReviewedSnapshot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"reviewed.go", "private.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	src := &fakePatchSource{patch: "diff --git a/reviewed.go b/reviewed.go\n@@ -1 +1 @@\n-old\n+new\n"}
	h := NewContextHandler(root, src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/context?path=private.go&as_of="+diff.PatchDigest(src.patch)+"&start=1&end=1", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestContextHandler_RejectsStaleSnapshotAndNonRegularOrOversizedFiles(t *testing.T) {
	root := t.TempDir()
	patch := "diff --git a/reviewed.go b/reviewed.go\n@@ -1 +1 @@\n-old\n+new\n"
	src := &fakePatchSource{patch: patch}
	h := NewContextHandler(root, src, nil)
	for name, body := range map[string]string{
		"reviewed.go": "package test\n",
		"large.go":    strings.Repeat("x", maxContextFileBytes+1),
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "dir.go"), 0o700); err != nil {
		t.Fatal(err)
	}

	stale := httptest.NewRecorder()
	h.ServeHTTP(stale, httptest.NewRequest(http.MethodGet, "/api/v1/diff/context?path=reviewed.go&as_of=wrong&start=1&end=1", nil))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale snapshot: want 409, got %d", stale.Code)
	}

	for _, tc := range []struct {
		name string
		want int
	}{
		{name: "dir.go", want: http.StatusBadRequest},
		{name: "large.go", want: http.StatusRequestEntityTooLarge},
	} {
		// Add each target to the patch before testing the filesystem contract; a file outside the
		// snapshot must be rejected before it reveals whether it is a directory or a large file.
		src.patch = "diff --git a/" + tc.name + " b/" + tc.name + "\n@@ -1 +1 @@\n-old\n+new\n"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/context?path="+tc.name+"&as_of="+diff.PatchDigest(src.patch)+"&start=1&end=1", nil))
		if w.Code != tc.want {
			t.Fatalf("%s: want %d, got %d: %s", tc.name, tc.want, w.Code, w.Body.String())
		}
	}
}

func TestDiffSessionHandler_GetReadsAttachedSessionWithoutMutatingIt(t *testing.T) {
	root := t.TempDir()
	store := diff.NewStore("")
	h := NewDiffSessionHandler(DiffSessionOptions{Sessions: store, Workspace: fakeReview{}, Root: root}, nil)

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
	h := NewDiffSessionHandler(DiffSessionOptions{Sessions: store, Workspace: fakeReview{}, Root: root}, nil)
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
	h := NewDiffSessionHandler(DiffSessionOptions{Sessions: store, Workspace: fakeReview{}, Root: root}, nil)
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
	h := NewDiffSessionHandler(DiffSessionOptions{Sessions: store, Workspace: fakeReview{}, Root: root}, nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")
	store.AddComment(root, types.DiffComment{Path: "a.go", Line: 2, Body: "agent"}, types.DiffAuthorAgent)

	// Only an agent comment exists, so there is nothing to send and no provider is consulted.
	if w := post(t, h, `{"op":"publish"}`); w.Code != http.StatusOK {
		t.Fatalf("an agent draft must not be publishable, got %d: %s", w.Code, w.Body.String())
	}
}

// fakeReview stands in for the workspace, which this package deliberately does not hold. It
// reports a branch and a clean tree, so no provider is consulted and no threads are placed.
type fakeReview struct{}

func (fakeReview) ReviewOrigin(context.Context) types.ReviewOrigin {
	return types.ReviewOrigin{Branch: "feat/x"}
}

func (fakeReview) WorkingDiff(context.Context, []string) (string, error) { return "", nil }

// With no provider wired there is no review, and that is a 200 with a reason rather than an
// error. Every "cannot review here" - no provider, no pull request, an unreachable host -
// leaves the reader with the same options, and rendering any of them as a failure would
// accuse them of something they did not do.
func TestReviewLookupWithNoProviderIsNotAnError(t *testing.T) {
	h := NewDiffReviewHandler(fakeReview{}, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/review", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got diffReviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "" || got.Reason == "" {
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
	h := NewDiffSessionHandler(DiffSessionOptions{Sessions: store, Workspace: fakeReview{}, Root: root}, nil)
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
	h := NewDiffSessionHandler(DiffSessionOptions{Sessions: store, Workspace: fakeReview{}, Root: root}, nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")

	w := post(t, h, `{"op":"reply","id":"th1"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "something to say") {
		t.Fatalf("want the empty-body reason, got %q", w.Body.String())
	}
}

// A malformed thread does NOT hide the readable ones, and does not pass unmentioned. Both
// failure modes are the same mistake in different directions: 502 would hide a whole
// conversation behind one bad remark, and silence would say a colleague said nothing.
//
// With no provider wired there is nothing to decode, so what this pins is the shape - threads
// and reason travel together on one 200 - rather than the decode itself, which is pinned in
// internal/interp/bindings.
func TestAReviewReadCarriesThreadsAndItsReasonTogether(t *testing.T) {
	h := NewDiffReviewHandler(fakeReview{}, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/review", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got diffReviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Threads == nil {
		t.Fatal("threads must be an empty array even when a reason is set")
	}
}
