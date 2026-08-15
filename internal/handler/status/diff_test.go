package status

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/service/console"
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
