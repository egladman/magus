package diff

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/types"
)

// fakeThreadWorkspace answers the three reads the thread route makes: where the review is, the
// patch, and the annotated changeset for the paths in it.
type fakeThreadWorkspace struct{ patch string }

func (fakeThreadWorkspace) ReviewOrigin(context.Context) types.ReviewOrigin {
	return types.ReviewOrigin{Branch: "feat/x"}
}

func (w fakeThreadWorkspace) WorkingDiff(context.Context, []string) (string, error) {
	return w.patch, nil
}

// DiffWith refuses a call that does not set SkipOrder: a thread record reads the code around one
// thread and must never pay for the reading order of the whole changeset.
func (fakeThreadWorkspace) DiffWith(_ context.Context, paths []string, opts types.DiffOptions) (types.Diff, error) {
	if !opts.SkipOrder {
		return types.Diff{}, errors.New("the thread asked for the reading order")
	}
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

func TestThreadRouteServesTheRecordForAThread(t *testing.T) {
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

	var got ThreadReply
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	reach := 9
	assert.Equal(t, types.DiffThread{
		ID:   "t1",
		Path: "a.go",
		Line: 11,
		Comments: []types.ReviewComment{
			{ID: "t1", Path: "a.go", Line: 11, Hunk: 0, Author: "priya", Body: "why new?"},
			{ID: "t2", Root: "t1", Path: "a.go", Hunk: 0, Author: "marcus", Body: "see the cache"},
		},
		Hunk: types.DiffThreadHunk{
			Index: 0, Source: "patch",
			Lines: []string{"@@ -10,3 +10,3 @@ func New() {", " ten", "-old", "+new"},
			Note:  "hunk 0 of a.go, as it stands now",
		},
		InChangeset: true,
		Project:     "root",
		Role:        types.DiffRoleSource,
		Reach:       &reach,
		Symbols:     []types.DiffSymbol{{ID: "m a/New().", Label: "New", Qualified: "New", FileCount: 9, RefCount: 12}},
		Notes:       []string{"note new-is-cheap anchors file:a.go"},
		Change:      "The change touches 1 file(s) in root.",
		Unmeasured:  []string{"coverage: no coverage run has been observed for this file"},
	}, got.DiffThread)
	assert.Contains(t, got.Text, "  priya:\n    > why new?\n  marcus:\n    > see the cache\n", "the text is the one --thread prints")
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

	var got ThreadReply
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

	var got ThreadReply
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Contains(t, got.Unmeasured, "note anchors: this server has no notes store wired, so none was joined")
	assert.Empty(t, got.Notes)
	assert.Contains(t, got.Text, "note anchors: this server has no notes store wired, so none was joined")
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
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "reading a thread is a GET and nothing else")
}

func TestThreadRouteWithNoReviewOpenIsNotFound(t *testing.T) {
	// No provider is wired here, so no review can be found.
	h := NewThreadHandler(ThreadOptions{Workspace: fakeThreadWorkspace{}}, nil)
	assert.Equal(t, http.StatusNotFound, getThread(h, "?id=t1").Code)

	assert.Equal(t, http.StatusNotFound, getThread(NewThreadHandler(ThreadOptions{}, nil), "?id=t1").Code,
		"a server with no workspace has no review to read")
}

// A misdeclared notes store is a fault the person has to fix, so the route reports it rather than
// serving a record whose notes read as a clean tree nobody checked.
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
