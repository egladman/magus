package diff

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/types"
)

// fakeThreadWorkspace answers the three reads the brief route makes: where the review is, the
// patch, and the annotated changeset for the paths in it.
type fakeThreadWorkspace struct{ patch string }

func (fakeThreadWorkspace) ReviewOrigin(context.Context) types.ReviewOrigin {
	return types.ReviewOrigin{Branch: "feat/x"}
}

func (w fakeThreadWorkspace) WorkingDiff(context.Context, []string) (string, error) {
	return w.patch, nil
}

func (fakeThreadWorkspace) Diff(_ context.Context, paths []string) (types.Diff, error) {
	reach := 9
	return types.Diff{
		Base:         "working",
		SeedProjects: []string{"root"},
		Files: []types.DiffFile{{
			Path: paths[0], Project: "root", Role: types.DiffRoleSource, Reach: &reach,
			Symbols: []types.DiffSymbol{{ID: "m a/New().", Label: "New", Qualified: "New", FileCount: 9, RefCount: 12}},
			Hunks:   []types.DiffHunk{{Index: 0, NewStart: 10, NewCount: 3, Symbols: []string{"m a/New()."}}},
		}},
	}, nil
}

const threadRoutePatch = "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -10,3 +10,3 @@ func New() {\n ten\n-old\n+new\n"

func getThread(h http.Handler, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/diff/thread"+query, nil))
	return w
}

func TestThreadRouteServesTheBriefForAThread(t *testing.T) {
	withReviewProvider(t, []any{
		map[string]any{"id": "t1", "path": "a.go", "line": float64(11), "author": "priya", "body": "why new?"},
		map[string]any{"id": "t2", "root": "t1", "author": "marcus", "body": "see the cache"},
	})
	h := NewThreadHandler(ThreadOptions{
		Workspace: fakeThreadWorkspace{patch: threadRoutePatch},
		Anchors: func(_ context.Context, rev types.Diff) ([]review.AnchorHit, error) {
			return []review.AnchorHit{{Note: "new-is-cheap", Kind: notes.AnchorFile, Target: "a.go", Matched: rev.Files[0].Path}}, nil
		},
	}, nil)

	w := getThread(h, "?id=t1")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got review.ThreadBriefResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "t1", got.ID)
	assert.Contains(t, got.Brief, "- priya:\n  > why new?\n- marcus:\n  > see the cache")
	assert.Contains(t, got.Brief, "- where: a.go:11")
	assert.Contains(t, got.Brief, "+new")
	assert.Contains(t, got.Brief, "`New` - referenced from 9 file(s), 12 reference(s)")
	assert.Contains(t, got.Brief, "- note new-is-cheap anchors file:a.go")
	assert.NotContains(t, got.Brief, "no notes store wired")
}

// TestThreadRouteAnswersAReplyIdWithTheThreadId. A client holding the id of the reply it just saw
// arrive gets the thread, and learns the thread id to key it by.
func TestThreadRouteAnswersAReplyIdWithTheThreadId(t *testing.T) {
	withReviewProvider(t, []any{
		map[string]any{"id": "t1", "path": "a.go", "line": float64(11), "author": "priya", "body": "why new?"},
		map[string]any{"id": "t2", "root": "t1", "author": "marcus", "body": "see the cache"},
	})
	h := NewThreadHandler(ThreadOptions{Workspace: fakeThreadWorkspace{patch: threadRoutePatch}}, nil)

	w := getThread(h, "?id=t2")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got review.ThreadBriefResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "t1", got.ID)
}

// TestThreadRouteNamesWhatItCouldNotJoin. A server with no notes wiring must not let an empty
// anchors section read as "no note anchors this file".
func TestThreadRouteNamesWhatItCouldNotJoin(t *testing.T) {
	withReviewProvider(t, []any{
		map[string]any{"id": "t1", "path": "a.go", "line": float64(11), "author": "priya", "body": "why new?"},
	})
	h := NewThreadHandler(ThreadOptions{Workspace: fakeThreadWorkspace{patch: threadRoutePatch}}, nil)

	w := getThread(h, "?id=t1")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got review.ThreadBriefResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Contains(t, got.Brief, "note anchors: this server has no notes store wired, so none was joined")
	assert.NotContains(t, got.Brief, "## Notes anchored here")
}

func TestThreadRouteRefusesWhatItCannotAnswer(t *testing.T) {
	withReviewProvider(t, []any{
		map[string]any{"id": "t1", "path": "a.go", "line": float64(11), "author": "priya", "body": "why new?"},
	})
	h := NewThreadHandler(ThreadOptions{Workspace: fakeThreadWorkspace{patch: threadRoutePatch}}, nil)

	assert.Equal(t, http.StatusBadRequest, getThread(h, "").Code, "no id")
	assert.Equal(t, http.StatusBadRequest, getThread(h, "?id=%20").Code, "a blank id")
	unknown := getThread(h, "?id=t404")
	assert.Equal(t, http.StatusNotFound, unknown.Code)
	assert.Contains(t, unknown.Body.String(), "t404")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/diff/thread?id=t1", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "reading a brief is a GET and nothing else")
}

func TestThreadRouteWithNoReviewOpenIsNotFound(t *testing.T) {
	// No provider is wired here, so no review can be found.
	h := NewThreadHandler(ThreadOptions{Workspace: fakeThreadWorkspace{}}, nil)
	assert.Equal(t, http.StatusNotFound, getThread(h, "?id=t1").Code)

	assert.Equal(t, http.StatusNotFound, getThread(NewThreadHandler(ThreadOptions{}, nil), "?id=t1").Code,
		"a server with no workspace has no review to brief")
}

// A misdeclared notes store is a fault the person has to fix, so the route reports it rather than
// serving a brief whose anchors section reads as a clean tree nobody checked.
func TestThreadRouteReportsAnAnchorsFailure(t *testing.T) {
	withReviewProvider(t, []any{
		map[string]any{"id": "t1", "path": "a.go", "line": float64(11), "author": "priya", "body": "why new?"},
	})
	h := NewThreadHandler(ThreadOptions{
		Workspace: fakeThreadWorkspace{patch: threadRoutePatch},
		Anchors: func(context.Context, types.Diff) ([]review.AnchorHit, error) {
			return nil, errors.New("note anchors: knowledge.notes.shared escapes the workspace")
		},
	}, nil)

	w := getThread(h, "?id=t1")

	assert.GreaterOrEqual(t, w.Code, http.StatusInternalServerError, w.Body.String())
}
