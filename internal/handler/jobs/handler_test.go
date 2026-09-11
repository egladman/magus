package jobs

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// fakeJobSource is a jobSource returning canned rows or a fixed error.
type fakeJobSource struct {
	jobs []types.Job
	err  error
}

func (f fakeJobSource) List() ([]types.Job, error) { return f.jobs, f.err }

func TestJobsHandler_Returns200WithJobs(t *testing.T) {
	src := fakeJobSource{jobs: []types.Job{
		{
			ID: "job-a", Goal: "ship the store", Checkpoint: "60dc9151",
			WritePaths: []string{"internal/job"}, State: types.StateRunning,
			Updated:  1755300000,
			Releases: []types.JobRelease{{Path: "types/job.go", Digest: "sha256:abc", ReleasedAt: 1755299000}},
		},
		{ID: "scout", ReadOnly: true, State: types.StateNoReturn},
	}}
	h := NewHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var out types.JobList
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want valid JSON: %v; body %s", err, w.Body.String())
	}
	if len(out.Jobs) != 2 {
		t.Fatalf("want 2 rows, got %d", len(out.Jobs))
	}
	// The join key the console drawer needs, and the two fields a row cannot be read
	// without: which state it ended in, and what tree it was handed.
	if out.Jobs[0].ID != "job-a" || out.Jobs[0].Checkpoint != "60dc9151" {
		t.Errorf("want the declared row verbatim, got %+v", out.Jobs[0])
	}
	if out.Jobs[1].State != types.StateNoReturn || !out.Jobs[1].ReadOnly {
		t.Errorf("want the abbreviated no_return row, got %+v", out.Jobs[1])
	}
	// The heartbeat: a reader watching for a job nobody has touched needs the row's
	// own timestamp, not the moment it happened to read the route.
	if out.Jobs[0].Updated != 1755300000 {
		t.Errorf("want the row's updated stamp on the wire, got %d", out.Jobs[0].Updated)
	}
	// What the next agent inherits, and which version of it.
	if len(out.Jobs[0].Releases) != 1 || out.Jobs[0].Releases[0].Digest != "sha256:abc" {
		t.Errorf("want the released path and its digest, got %+v", out.Jobs[0].Releases)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("want no-store, got %q", got)
	}
}

// The overlap is derived on the read and stored nowhere, so the route reports it
// without either row saying anything about the other. A fact for the reader, not a
// verdict: nothing is blocked, reordered, or failed on account of it.
func TestJobsHandler_ReportsOverlappingWritePaths(t *testing.T) {
	src := fakeJobSource{jobs: []types.Job{
		{ID: "job-a", WritePaths: []string{"internal/job"}, State: types.StateRunning},
		{ID: "job-b", WritePaths: []string{"internal/job/store.go"}, State: types.StateDeclared},
		{ID: "job-done", WritePaths: []string{"internal/job"}, State: types.StatePass},
	}}
	h := NewHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil))

	var out types.JobList
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want valid JSON: %v; body %s", err, w.Body.String())
	}
	if len(out.Overlaps) != 1 {
		t.Fatalf("want one pair - the finished job is not competing for anything - got %+v", out.Overlaps)
	}
	if out.Overlaps[0].JobA != "job-a" || out.Overlaps[0].JobB != "job-b" {
		t.Errorf("want the pair in store order, got %+v", out.Overlaps[0])
	}
	// Each side's own declaration, kept apart: they are different strings here, and a
	// reader who cannot tell which job claimed which has nothing to act on.
	if len(out.Overlaps[0].PathsA) != 1 || out.Overlaps[0].PathsA[0] != "internal/job" {
		t.Errorf("want job-a's declaration, got %v", out.Overlaps[0].PathsA)
	}
	if len(out.Overlaps[0].PathsB) != 1 || out.Overlaps[0].PathsB[0] != "internal/job/store.go" {
		t.Errorf("want job-b's declaration, got %v", out.Overlaps[0].PathsB)
	}
}

func TestJobsHandler_EmptyStoreServesEmptyList(t *testing.T) {
	h := NewHandler(fakeJobSource{}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	// [] not null: a workspace where nobody has handed out a job yet is empty, not broken.
	if got := w.Body.String(); got != `{"jobs":[]}` {
		t.Errorf(`want {"jobs":[]}, got %s`, got)
	}
}

func TestJobsHandler_ErrorReturns500(t *testing.T) {
	h := NewHandler(fakeJobSource{err: errors.New("corrupt store")}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", w.Code)
	}
}

func TestJobsHandler_MethodGate(t *testing.T) {
	h := NewHandler(fakeJobSource{}, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/api/v1/jobs", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("want 204 for the CORS preflight, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405; the write door is the MCP tool, not this endpoint, got %d", w.Code)
	}
}
