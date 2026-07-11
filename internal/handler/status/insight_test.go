package status

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

// fakeInsightSource is an insightSource returning canned values or a fixed error.
type fakeInsightSource struct {
	view       types.InsightView
	report     types.FlakeReport
	insightErr error
	flakeErr   error
}

func (f fakeInsightSource) Insight(context.Context) (types.InsightView, error) {
	return f.view, f.insightErr
}

func (f fakeInsightSource) Flake(context.Context) (types.FlakeReport, error) {
	return f.report, f.flakeErr
}

// --- InsightHandler ---

func TestInsightHandler_Returns200WithJSON(t *testing.T) {
	src := fakeInsightSource{view: types.InsightView{Hotspots: types.HotspotOutput{Commits: 42}}}
	h := NewInsightHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/insight", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var out types.InsightView
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want valid JSON: %v; body %s", err, w.Body.String())
	}
	if out.Hotspots.Commits != 42 {
		t.Errorf("want commits 42, got %d", out.Hotspots.Commits)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("want no-store, got %q", got)
	}
}

func TestInsightHandler_NoWorkspaceReturns503(t *testing.T) {
	h := NewInsightHandler(fakeInsightSource{insightErr: console.ErrNoWorkspace}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/insight", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", w.Code)
	}
}

func TestInsightHandler_ErrorReturns500(t *testing.T) {
	h := NewInsightHandler(fakeInsightSource{insightErr: errors.New("scan boom")}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/insight", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", w.Code)
	}
}

func TestInsightHandler_MethodNotAllowed(t *testing.T) {
	h := NewInsightHandler(fakeInsightSource{}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/insight", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

func TestInsightHandler_OptionsNoContent(t *testing.T) {
	h := NewInsightHandler(fakeInsightSource{}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/api/v1/insight", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("want 204 for preflight, got %d", w.Code)
	}
}

// --- FlakeHandler ---

func TestFlakeHandler_Returns200WithJSON(t *testing.T) {
	src := fakeInsightSource{report: types.FlakeReport{
		Threshold: 0.05,
		Targets:   []types.FlakeTarget{{Project: "p", Target: "test", Score: 0.1, Flaky: true}},
	}}
	h := NewFlakeHandler(src, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/flake", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var out types.FlakeReport
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("want valid JSON: %v; body %s", err, w.Body.String())
	}
	if out.Threshold != 0.05 || len(out.Targets) != 1 || out.Targets[0].Project != "p" {
		t.Errorf("unexpected report: %+v", out)
	}
}

func TestFlakeHandler_ErrorReturns500(t *testing.T) {
	h := NewFlakeHandler(fakeInsightSource{flakeErr: errors.New("read boom")}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/flake", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", w.Code)
	}
}

func TestFlakeHandler_MethodNotAllowed(t *testing.T) {
	h := NewFlakeHandler(fakeInsightSource{}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/flake", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}
