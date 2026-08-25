package status

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// fixedOrigin stands in for the workspace, which this package deliberately does not hold.
// Shared with the session tests beside these.
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
