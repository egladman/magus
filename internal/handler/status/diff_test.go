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
