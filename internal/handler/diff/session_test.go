package diff

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// answeringSource mocks a patchSource that answers any WorkingDiff with patch and err. A
// test that asserts on the paths the handler passes through sets its own expectation
// instead, so the scoping tests assert on the call rather than on a response that would
// look identical either way.
func answeringSource(t *testing.T, patch string, err error) *mockPatchSource {
	t.Helper()
	src := newMockPatchSource(t)
	src.EXPECT().WorkingDiff(mock.Anything, mock.Anything).Return(patch, err).Maybe()
	return src
}

func TestDiffHandler_ReturnsPatch(t *testing.T) {
	const patch = "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n"
	h := NewPatchHandler(answeringSource(t, patch, nil), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/patch", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var out diffResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want valid JSON: %v; body %s", err, w.Body.String())
	}
	if out.Patch != patch {
		t.Errorf("patch not passed through verbatim:\ngot  %q\nwant %q", out.Patch, patch)
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
		h := NewPatchHandler(answeringSource(t, patch, nil), nil)
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
	src := newMockPatchSource(t)
	src.EXPECT().WorkingDiff(mock.Anything, []string{"a.go", "b.go"}).Return("", nil).Once()
	h := NewPatchHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/patch?path=a.go&path=b.go", nil))
}

// An empty `path=` must scope to NOTHING, not to the empty pathspec; every backend reads
// that as the whole repository, which is the opposite of what the caller asked for.
func TestDiffHandler_EmptyPathParamDoesNotWidenScope(t *testing.T) {
	src := newMockPatchSource(t)
	src.EXPECT().WorkingDiff(mock.Anything, []string{"real.go"}).Return("", nil).Once()
	h := NewPatchHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/patch?path=&path=+&path=real.go", nil))
}

// The error names the server's workspace path, so it stays in the log.
func TestDiffHandler_ErrorReturns500(t *testing.T) {
	h := NewPatchHandler(answeringSource(t, "", errors.New("git -C /Users/dev/repo diff: boom")), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/patch", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"reason":"MGS9027"`) || !strings.Contains(body, "diff failed") {
		t.Errorf("want the internal-failure reason naming what failed, got %q", body)
	}
	if strings.Contains(body, "/Users/dev") {
		t.Errorf("the body names the server's path: %q", body)
	}
}

func TestDiffHandler_NoWorkspaceReturns503(t *testing.T) {
	h := NewPatchHandler(answeringSource(t, "", console.ErrNoWorkspace), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/patch", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"reason":"MGS9026"`) {
		t.Errorf("want the workspace-not-wired reason, got %q", w.Body.String())
	}
}

// A workspace root that no longer resolves is the workspace gone, and the answer names
// neither the path nor the error.
func TestContextHandler_MissingRootReturns503WithoutThePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "gone")
	const patch = "diff --git a/source.go b/source.go\n@@ -3 +3 @@\n-three\n+three\n"
	h := NewContextHandler(root, answeringSource(t, patch, nil), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/context?path=source.go&as_of="+changeset.PatchDigest(patch)+"&start=3&end=3", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), root) {
		t.Fatalf("the body names the workspace path: %q", w.Body.String())
	}
}

func TestDiffHandler_RejectsNonGet(t *testing.T) {
	h := NewPatchHandler(answeringSource(t, "", nil), nil)
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
	const patch = "diff --git a/source.go b/source.go\n@@ -3 +3 @@\n-three\n+three\n"
	h := NewContextHandler(root, answeringSource(t, patch, nil), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/context?path=source.go&as_of="+changeset.PatchDigest(patch)+"&start=3&end=3&radius=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var out contextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	want := contextResponse{Path: "source.go", AsOf: changeset.PatchDigest(patch), Start: 2, Lines: []string{"two", "three", "four"}}
	require.Equal(t, want, out)
}

func TestContextHandler_RejectsPathEscape(t *testing.T) {
	h := NewContextHandler(t.TempDir(), answeringSource(t, "", nil), nil)
	for _, path := range []string{"../secret", ".."} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/context?path="+path+"&start=1&end=1", nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("path %q: want 400, got %d", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), `"reason":"MGS9023"`) {
			t.Fatalf("path %q: want the invalid-request reason, got %q", path, w.Body.String())
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
	const patch = "diff --git a/reviewed.go b/reviewed.go\n@@ -1 +1 @@\n-old\n+new\n"
	h := NewContextHandler(root, answeringSource(t, patch, nil), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/context?path=private.go&as_of="+changeset.PatchDigest(patch)+"&start=1&end=1", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"reason":"MGS9024"`) {
		t.Fatalf("want the resource-not-found reason, got %q", w.Body.String())
	}
}

func TestContextHandler_RejectsStaleSnapshotAndNonRegularOrOversizedFiles(t *testing.T) {
	root := t.TempDir()
	patch := "diff --git a/reviewed.go b/reviewed.go\n@@ -1 +1 @@\n-old\n+new\n"
	src := newMockPatchSource(t)
	src.EXPECT().WorkingDiff(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, []string) (string, error) { return patch, nil }).Maybe()
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
	if !strings.Contains(stale.Body.String(), `"reason":"MGS9025"`) {
		t.Fatalf("stale snapshot: want the state-conflict reason, got %q", stale.Body.String())
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
		patch = "diff --git a/" + tc.name + " b/" + tc.name + "\n@@ -1 +1 @@\n-old\n+new\n"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/context?path="+tc.name+"&as_of="+changeset.PatchDigest(patch)+"&start=1&end=1", nil))
		if w.Code != tc.want {
			t.Fatalf("%s: want %d, got %d: %s", tc.name, tc.want, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"reason":"MGS9023"`) {
			t.Fatalf("%s: want the invalid-request reason, got %q", tc.name, w.Body.String())
		}
	}
}

func TestReviewHandler_GetReadsAttachedReviewWithoutMutatingIt(t *testing.T) {
	root := t.TempDir()
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)

	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/v1/diff/session", nil))
	if missing.Code != http.StatusConflict {
		t.Fatalf("missing session: want 409, got %d", missing.Code)
	}
	if !strings.Contains(missing.Body.String(), `"reason":"MGS9025"`) {
		t.Fatalf("missing session: want the state-conflict reason, got %q", missing.Body.String())
	}

	want := store.Attach(root, "main", types.Diff{Base: "main"}, "snapshot-a")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/session", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	// Compared as the wire form, so a field the review gains is covered without a decode
	// turning an empty slice into a nil one.
	wire, err := json.Marshal(want)
	require.NoError(t, err)
	assert.JSONEq(t, string(wire), w.Body.String())
	assert.Equal(t, "snapshot-a main", want.AsOf+" "+want.Base, "the served review is the one attached")
}

// post is one session mutation, for the publish cases below.
func post(t *testing.T, h *ReviewHandler, body string) *httptest.ResponseRecorder {
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
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")
	store.AddComment(root, types.DiffComment{Path: "a.go", Line: 4, Body: "why"}, types.DiffAuthorUnattributed)

	w := post(t, h, `{"op":"publish"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no review provider wired") {
		t.Fatalf("the reason must travel, got %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"reason":"MGS9029"`) {
		t.Fatalf("want the review-host-failed reason, got %q", w.Body.String())
	}

	// NOTHING may be marked published by a send that did not happen. A draft marked on a
	// failed publish is lost: it never reaches the host and never offers to go again.
	sess := store.Get(root)
	if len(sess.Comments) != 1 || sess.Comments[0].Published {
		t.Fatalf("a failed publish marked a draft: %#v", sess.Comments)
	}
}

// The answer op's `on` flag is the web client's accept or decline; the store records which.
func TestAnswerOpRecordsTheSuggestionStatus(t *testing.T) {
	root := t.TempDir()
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")
	store.Suggest(root, types.DiffSuggestion{Path: "a.go", Reason: "one"})
	store.Suggest(root, types.DiffSuggestion{Path: "b.go", Reason: "two"})

	require.Equal(t, http.StatusOK, post(t, h, `{"op":"answer","id":"s1","on":true}`).Code)
	require.Equal(t, http.StatusOK, post(t, h, `{"op":"answer","id":"s2"}`).Code)

	got := store.Get(root).Suggestions
	require.Len(t, got, 2)
	assert.Equal(t, types.SuggestionAccepted, got[0].Status)
	assert.Equal(t, types.SuggestionDeclined, got[1].Status)
}

// Nothing drafted is not a failure. A reader who publishes twice, or who has drafted nothing,
// made no mistake and gets the session back unchanged.
func TestPublishingNothingIsNotAnError(t *testing.T) {
	root := t.TempDir()
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")

	if w := post(t, h, `{"op":"publish"}`); w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}

// An AGENT's comment is never publishable. It reaches this session through MCP, and the set
// sent is derived from the session rather than named by the request precisely so no caller can
// widen it: an agent's remark going out under the human's name is the failure this prevents.
func TestAnAgentCommentIsNotPublishable(t *testing.T) {
	root := t.TempDir()
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")
	store.AddComment(root, types.DiffComment{Path: "a.go", Line: 2, Body: "agent"}, types.DiffAuthorAgent)

	// Only an agent comment exists, so there is nothing to send and no provider is consulted.
	if w := post(t, h, `{"op":"publish"}`); w.Code != http.StatusOK {
		t.Fatalf("an agent draft must not be publishable, got %d: %s", w.Code, w.Body.String())
	}
}

// cleanBranchReview stands in for the workspace, which this package deliberately does not hold. It
// reports a branch and a clean tree, so no provider is consulted and no threads are placed.
type cleanBranchReview struct{}

func (cleanBranchReview) ReviewOrigin(context.Context) types.ReviewOrigin {
	return types.ReviewOrigin{Branch: "feat/x"}
}

func (cleanBranchReview) WorkingDiff(context.Context, []string) (string, error) { return "", nil }

// With no provider wired there is no review, and that is a 200 with a reason rather than an
// error. Every "cannot review here" (no provider, no pull request, an unreachable host)
// leaves the reader with the same options, and rendering any of them as a failure would
// accuse them of something they did not do.
func TestReviewLookupWithNoProviderIsNotAnError(t *testing.T) {
	h := NewReviewLookupHandler(cleanBranchReview{}, nil)

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
	if got.Comments == nil {
		t.Fatal("threads must be an empty array, not null")
	}
}

// A server with no workspace has no branch to look a review up for, and says so instead of
// panicking on a nil source.
func TestReviewLookupWithoutAWorkspace(t *testing.T) {
	h := NewReviewLookupHandler(nil, nil)

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
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)
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
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)
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
// With no provider wired there is nothing to decode, so what this pins is the shape (threads
// and reason travel together on one 200) rather than the decode itself, which is pinned in
// internal/interp/bindings.
func TestAReviewReadCarriesThreadsAndItsReasonTogether(t *testing.T) {
	h := NewReviewLookupHandler(cleanBranchReview{}, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/review", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got diffReviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Comments == nil {
		t.Fatal("threads must be an empty array even when a reason is set")
	}
}

// patchedReviewWorkspace answers with a real patch, so thread placement can be exercised end to end
// through the route rather than only in internal/diff's own tests.
type patchedReviewWorkspace struct{ patch string }

func (f patchedReviewWorkspace) ReviewOrigin(context.Context) types.ReviewOrigin {
	return types.ReviewOrigin{Branch: "feat/x"}
}

func (f patchedReviewWorkspace) WorkingDiff(context.Context, []string) (string, error) {
	return f.patch, nil
}

// place resolves a thread onto the hunk holding its line, so the terminal viewer and the console read one answer.
// Exercised here because the route is where the patch and the threads meet; internal/diff
// tests the arithmetic, and this tests that the route feeds it the right patch.
func TestReviewRoutePlacesThreadsAgainstTheWorkingPatch(t *testing.T) {
	h := NewReviewLookupHandler(patchedReviewWorkspace{patch: "diff --git a/a.go b/a.go\n" +
		"--- a/a.go\n+++ b/a.go\n@@ -10,3 +10,3 @@\n ten\n-old\n+new\n"}, nil)

	got := h.place(t.Context(), []types.ReviewComment{
		{ID: "t1", Path: "a.go", Line: 11, Hunk: -1},
		{ID: "t2", Path: "a.go", Line: 900, Hunk: -1},
		{ID: "t3", Path: "other.go", Line: 1, Hunk: -1},
	})

	require.Len(t, got, 3, "every thread survives placement")
	assert.Equal(t, 0, got[0].Hunk, "a line inside a hunk places onto it")
	assert.Equal(t, -1, got[1].Hunk, "a line outside every hunk stays unplaced, not dropped")
	assert.Equal(t, -1, got[2].Hunk, "and so does a file this changeset does not touch")
}

// An unreadable patch leaves every thread where it was rather than losing them: a remark shown
// against its file is worth far more than one withheld because its line could not be resolved.
func TestReviewRouteKeepsThreadsWhenThePatchCannotBeRead(t *testing.T) {
	h := NewReviewLookupHandler(patchedReviewWorkspace{patch: ""}, nil)
	in := []types.ReviewComment{{ID: "t1", Path: "a.go", Line: 11, Hunk: -1}}
	assert.Equal(t, in, h.place(t.Context(), in))
}

// Publishing must not mark a draft the provider cannot anchor. Such a draft would be lost
// twice: never sent, and never eligible again, because publish only considers unpublished ones.
func TestPublishRefusesWhenNoDraftCanBeAnchored(t *testing.T) {
	root := t.TempDir()
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")
	store.AddComment(root, types.DiffComment{Path: "a.go", Body: "no line"}, types.DiffAuthorUnattributed)

	w := post(t, h, `{"op":"publish"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no line to anchor to") {
		t.Fatalf("the reason must name the problem, got %q", w.Body.String())
	}
	// And it stays a draft, so it can go once it has a line.
	if sess := store.Get(root); sess.Comments[0].Published {
		t.Fatal("an unanchorable draft must not be marked published")
	}
}

// withReviewProvider registers a canned provider for the whole review path, so the handler's
// SUCCESS branches can be exercised. Without one, these routes could only ever be tested
// failing, which left the reply and publish paths, where the real bugs were, uncovered.
func withReviewProvider(t *testing.T, threads []types.ReviewComment) {
	t.Helper()
	selectReviewProvider(t, register.ReviewSpell(t, types.ReviewTarget{ID: "482", Repo: "acme/acme"}, threads))
}

// selectReviewProvider makes the registered spell name the review provider for this test.
func selectReviewProvider(t *testing.T, name string) {
	t.Helper()
	prev := bindings.ReviewProvider()
	bindings.SetReviewProvider(name)
	t.Cleanup(func() { bindings.SetReviewProvider(prev) })
}

// The success path: an anchored draft leaves, and only then is it marked. A draft marked on a
// send that did not happen is lost twice over.
func TestPublishMarksExactlyTheDraftsThatLeft(t *testing.T) {
	withReviewProvider(t, nil)
	root := t.TempDir()
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")
	store.AddComment(root, types.DiffComment{Path: "a.go", Line: 4, Body: "anchored"}, types.DiffAuthorUnattributed)
	store.AddComment(root, types.DiffComment{Path: "b.go", Body: "no line"}, types.DiffAuthorUnattributed)
	store.AddComment(root, types.DiffComment{Path: "c.go", Line: 9, Body: "agent"}, types.DiffAuthorAgent)

	w := post(t, h, `{"op":"publish","summary":"self-review"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	byBody := map[string]bool{}
	for _, c := range store.Get(root).Comments {
		byBody[c.Body] = c.Published
	}
	assert.True(t, byBody["anchored"], "an anchored human draft left and is marked")
	assert.False(t, byBody["no line"], "an unanchorable draft stays a draft")
	assert.False(t, byBody["agent"], "an agent's remark is never published under the person's name")

	// Publishing again refuses, and refusing is right: the anchored draft has gone, so all
	// that is left is the one no provider can place. Answering "fine" would tell the reader
	// their remark went out.
	again := post(t, h, `{"op":"publish"}`)
	assert.Equal(t, http.StatusBadGateway, again.Code)
	assert.Contains(t, again.Body.String(), "no line to anchor to")
}

// With every draft sent, publishing again is a no-op rather than a complaint: the reader made
// no mistake.
func TestPublishingWithEverythingAlreadySentIsNotAnError(t *testing.T) {
	withReviewProvider(t, nil)
	root := t.TempDir()
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")
	store.AddComment(root, types.DiffComment{Path: "a.go", Line: 4, Body: "anchored"}, types.DiffAuthorUnattributed)

	require.Equal(t, http.StatusOK, post(t, h, `{"op":"publish"}`).Code)
	require.Equal(t, http.StatusOK, post(t, h, `{"op":"publish"}`).Code)
}

// The bug this pins: reply used to answer 409 when no session was attached, AFTER the reply had
// already reached the host. The obvious response to a 409 is to send again, which puts the same
// sentence in a colleague's inbox twice.
func TestReplySucceedsWithNoSessionAttached(t *testing.T) {
	withReviewProvider(t, nil)
	root := t.TempDir()
	h := NewReviewHandler(ReviewOptions{
		Sessions: changeset.NewStore(""), Workspace: cleanBranchReview{}, Root: root,
	}, nil)

	w := post(t, h, `{"op":"reply","id":"th1","body":"agreed"}`)
	assert.Equal(t, http.StatusOK, w.Code, "a delivered reply must never read as a failure")
}

func TestReplySucceedsAndLeavesTheSessionAlone(t *testing.T) {
	withReviewProvider(t, nil)
	root := t.TempDir()
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")

	require.Equal(t, http.StatusOK, post(t, h, `{"op":"reply","id":"th1","body":"agreed"}`).Code)
	assert.Empty(t, store.Get(root).Comments, "a reply belongs to the host's record, not this session")
}

// A reply is addressed to a conversation by its first comment. Root names it, so a client holding
// a reply need not find the root first, and it wins over ID when both arrive.
func TestReplyGoesIntoTheNamedConversation(t *testing.T) {
	threads := recordReplies(t, "")

	root := t.TempDir()
	h := NewReviewHandler(ReviewOptions{
		Sessions: changeset.NewStore(""), Workspace: cleanBranchReview{}, Root: root,
	}, nil)

	for _, body := range []string{
		`{"op":"reply","id":"th1","body":"agreed"}`,
		`{"op":"reply","id":"th2","root":"th1","body":"agreed"}`,
		`{"op":"reply","root":"th1","body":"agreed"}`,
	} {
		require.Equal(t, http.StatusOK, post(t, h, body).Code, body)
	}
	assert.Equal(t, []string{"th1", "th1", "th1"}, threads(),
		"no root answers the thread addressed; a root answers its thread, with or without an id")

	empty := post(t, h, `{"op":"reply","body":"agreed"}`)
	assert.Equal(t, http.StatusBadRequest, empty.Code, "neither an id nor a root names no thread")
	assert.Len(t, threads(), 3, "and the host was not asked")
}

// recordReplies selects a provider whose review 482 holds the threads the Buzz expression
// threadsBody returns ("" for none) and whose reply_review reports each thread it is asked to
// answer to a local host. It returns the threads asked, in order.
func recordReplies(t *testing.T, threadsBody string) func() []string {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, r.URL.Query().Get("thread"))
	}))
	t.Cleanup(host.Close)
	if threadsBody == "" {
		threadsBody = "[<ReviewComment>]"
	}
	selectReviewProvider(t, register.Spell(t, "reply-recorder", spells.WithFuncSource(`import "magus/spell";
import "http";
export fun find_review(target: Target, req: FindReviewRequest) > ReviewTarget {
    return ReviewTarget{ id = "482", repo = "acme/acme" };
}
export fun review_threads(target: Target, req: ReviewRef) > [ReviewComment] {
    return `+threadsBody+`;
}
export fun reply_review(target: Target, req: ReplyReviewRequest) > Ack !> any {
    http\get("`+host.URL+`/?thread=" + req.thread);
    return Ack{ ok = true };
}`)))
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(asked)
	}
}

// A client holding only the id of a reply it was shown can answer it: the review is read and the
// id resolved to its thread's, because a host attaches a reply to a thread by its top-level
// comment and answering a reply's own id would open nothing.
func TestReplyToAReplyIdGoesToItsThread(t *testing.T) {
	asked := recordReplies(t, `[
        ReviewComment{ id = "th1", path = "a.go", body = "why" },
        ReviewComment{ id = "th2", root = "th1", body = "because" },
        ReviewComment{ id = "x", root = "x", body = "self-rooted" },
    ]`)
	h := NewReviewHandler(ReviewOptions{
		Sessions: changeset.NewStore(""), Workspace: cleanBranchReview{}, Root: t.TempDir(),
	}, nil)

	for _, body := range []string{
		`{"op":"reply","id":"th2","body":"agreed"}`,
		`{"op":"reply","root":"th2","body":"agreed"}`,
		`{"op":"reply","id":"x","body":"agreed"}`,
		`{"op":"reply","id":"unknown","body":"agreed"}`,
	} {
		require.Equal(t, http.StatusOK, post(t, h, body).Code, body)
	}

	assert.Equal(t, []string{"th1", "th1", "x", "unknown"}, asked(),
		"a reply resolves to its thread, a self-rooted comment is its own, and what the review does not hold goes through for the host to refuse")
}

// The whole route, end to end: the provider's threads come back placed against the working
// patch, so the client renders them beside the code rather than working the anchors out itself.
func TestReviewRouteServesPlacedThreads(t *testing.T) {
	withReviewProvider(t, []types.ReviewComment{
		{ID: "t1", Path: "a.go", Line: 11, Author: "priya", Body: "why"},
		{ID: "t2", Path: "a.go", Line: 900, Author: "marcus", Body: "moved"},
	})
	h := NewReviewLookupHandler(patchedReviewWorkspace{patch: "diff --git a/a.go b/a.go\n" +
		"--- a/a.go\n+++ b/a.go\n@@ -10,3 +10,3 @@\n ten\n-old\n+new\n"}, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/review", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got diffReviewResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	// The verdict set, the state and the threads are the provider's answer and are checked
	// below or by their own tests, so they are copied across; no workspace is wired, so no host.
	assert.Equal(t, diffReviewResponse{
		ID:           "482",
		Repo:         "acme/acme",
		State:        got.State,
		Verdicts:     got.Verdicts,
		VerdictLimit: got.VerdictLimit,
		Comments:     got.Comments,
	}, got)
	require.Len(t, got.Comments, 2)
	assert.Equal(t, 0, got.Comments[0].Hunk, "a line inside a hunk arrives placed")
	assert.Equal(t, -1, got.Comments[1].Hunk, "one outside every hunk arrives unplaced, not dropped")
	assert.Empty(t, got.Reason, "a clean read carries no reason")
}

// A malformed thread does not pass unmentioned: the reason rides on the same 200, so the diff
// still opens.
func TestReviewRouteCarriesTheReasonAThreadCouldNotBeRead(t *testing.T) {
	selectReviewProvider(t, register.Spell(t, "malformed-review", spells.WithFuncSource(`import "magus/spell";
export fun find_review(target: Target, req: FindReviewRequest) > ReviewTarget {
    return ReviewTarget{ id = "482", repo = "acme/acme" };
}
export fun review_threads(target: Target, req: ReviewRef) > any {
    return [{"id": "t1", "path": "a.go", "line": 11, "body": "readable"}, {"id": "t2", "line": "not a number"}];
}`)))
	h := NewReviewLookupHandler(patchedReviewWorkspace{patch: ""}, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/review", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var got diffReviewResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	// Strict: one bad row fails the read, never a review with a remark silently missing.
	assert.Empty(t, got.Comments)
	assert.Contains(t, got.Reason, "want int", "what could not be read is stated")
}

// The reader is told WHERE a publish would go before anything leaves, and an appliance is a
// very different destination from github.com. Every spelling of a remote has to reduce to the
// same recognizable host, or the sentence naming it is worse than not saying anything.
func TestRemoteHostNamesTheDestination(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"git@github.com:acme/acme.git", "github.com"},
		{"https://github.com/acme/acme.git", "github.com"},
		{"ssh://git@github.acme.com/team/svc.git", "github.acme.com"},
		{"https://github.acme.com:8443/team/svc", "github.acme.com"},
		// Not a URL this understands: the client names the repo alone rather than inventing
		// a host the reader would then trust.
		{"/srv/git/bare.git", ""},
		{"", ""},
	} {
		assert.Equal(t, tc.want, remoteHost(tc.remote), tc.remote)
	}
}

// Backing out of a staged remark is part of the transaction. It refuses anything that is not
// the reader's own unsent draft: a published remark cannot be unsaid by deleting the local
// copy, and an agent's is not theirs to remove.
func TestDiscardRemovesOnlyAnUnsentHumanDraft(t *testing.T) {
	root := t.TempDir()
	store := changeset.NewStore("")
	h := NewReviewHandler(ReviewOptions{Sessions: store, Workspace: cleanBranchReview{}, Root: root}, nil)
	store.Attach(root, "main", types.Diff{Base: "main"}, "a")
	mine := store.AddComment(root, types.DiffComment{Path: "a.go", Line: 1, Body: "mine"}, types.DiffAuthorUnattributed)
	store.AddComment(root, types.DiffComment{Path: "a.go", Line: 2, Body: "theirs"}, types.DiffAuthorAgent)
	sent := store.AddComment(root, types.DiffComment{Path: "a.go", Line: 3, Body: "sent"}, types.DiffAuthorUnattributed)
	store.MarkPublished(root, sent.Comments[len(sent.Comments)-1].ID)

	ids := map[string]string{}
	for _, c := range store.Get(root).Comments {
		ids[c.Body] = c.ID
	}
	_ = mine

	require.Equal(t, http.StatusOK, post(t, h, `{"op":"discard","id":"`+ids["mine"]+`"}`).Code)
	require.Equal(t, http.StatusOK, post(t, h, `{"op":"discard","id":"`+ids["theirs"]+`"}`).Code)
	require.Equal(t, http.StatusOK, post(t, h, `{"op":"discard","id":"`+ids["sent"]+`"}`).Code)

	left := map[string]bool{}
	for _, c := range store.Get(root).Comments {
		left[c.Body] = true
	}
	assert.False(t, left["mine"], "an unsent draft of the reader's own is discardable")
	assert.True(t, left["theirs"], "an agent's remark is not the reader's to remove")
	assert.True(t, left["sent"], "a published remark cannot be unsaid by deleting it here")
}

// cannedBranches answers the branch lookup without a repository.
type cannedBranches struct {
	limit int
	out   []types.BranchChange
	err   error
}

func (f *cannedBranches) BranchChanges(_ context.Context, limit int) ([]types.BranchChange, error) {
	f.limit = limit
	return f.out, f.err
}

func TestDiffBranchesServesTheOverlapAndPassesTheCap(t *testing.T) {
	src := &cannedBranches{out: []types.BranchChange{{Ref: "theirs", Paths: []string{"a.go"}}}}
	rec := httptest.NewRecorder()
	NewBranchesHandler(src, nil).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/diff/branches", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	var got diffBranchesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []types.BranchChange{{Ref: "theirs", Paths: []string{"a.go"}}}, got.Branches)
	// The cap reaches the backend rather than being applied after the forks were already paid.
	assert.Equal(t, branchLimit, src.limit)
}

// A server with no workspace answers an empty ARRAY, never null: a client iterates the field, and
// a null would make every caller guard a state that means what empty already means.
func TestDiffBranchesWithNoWorkspaceIsAnEmptyArray(t *testing.T) {
	rec := httptest.NewRecorder()
	NewBranchesHandler(nil, nil).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/diff/branches", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"branches":[]`)
}

// Serving the conversation is not the same as the reader seeing it. The lookup marks threads new
// by READING the watermark and must never advance it: an aborted fetch, a refresh mid-flight or a
// second tab would otherwise consume the marks, and the notification with them, since the job
// that raises it compares against the same watermark. The client says when it has drawn them,
// through the session's `seen` op.
func TestReviewLookupMarksNewWithoutConsumingTheWatermark(t *testing.T) {
	withReviewProvider(t, []types.ReviewComment{{ID: "t1", Path: "a.go", Line: 1, Body: "first", Author: "priya"}})
	root := t.TempDir()
	store := changeset.NewStore(t.TempDir())
	store.Attach(root, "", types.Diff{}, "")

	h := NewReviewLookupHandler(cleanBranchReview{}, nil)
	h.Sessions, h.Root = store, root

	// Twice, as a reader who refreshed would. Both answers must still say new.
	for attempt := range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/review", nil))
		var got diffReviewResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		require.Len(t, got.Comments, 1)
		assert.True(t, got.Comments[0].New, "attempt %d: serving must not consume the mark", attempt)
	}

	// Only the reader's claim moves it, and then the thread stops being new.
	store.MarkCommentsSeen(root, []string{"t1"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/review", nil))
	var after diffReviewResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &after))
	require.Len(t, after.Comments, 1)
	assert.False(t, after.Comments[0].New, "once the reader has seen it, it is not new again")
}

// A backend without the capability must SAY so. "Nobody else is touching this file" is
// reassurance; "magus cannot tell you on this backend yet" is a gap in magus, and rendering both
// as an empty list tells the reader the first when the truth is the second. The marker is only
// worth having if its absence can be trusted.
func TestDiffBranchesNamesTheBackendThatCannotAnswer(t *testing.T) {
	src := &cannedBranches{err: fmt.Errorf("hg does not report branch changes: %w", types.ErrVCSUnsupported)}
	rec := httptest.NewRecorder()
	NewBranchesHandler(src, nil).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/diff/branches", nil))

	// 200, not an error status: the reader did nothing wrong and their diff must still open.
	assert.Equal(t, http.StatusOK, rec.Code)
	var got diffBranchesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got.Branches)
	assert.Contains(t, got.Unsupported, "hg", "the gap names the backend that fell short")
}

// A supported backend with nothing to report says nothing is unsupported, so the two states stay
// distinguishable from the other side too.
func TestDiffBranchesLeavesUnsupportedEmptyWhenTheBackendAnswered(t *testing.T) {
	rec := httptest.NewRecorder()
	NewBranchesHandler(&cannedBranches{}, nil).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/diff/branches", nil))

	var got diffBranchesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got.Unsupported)
}
