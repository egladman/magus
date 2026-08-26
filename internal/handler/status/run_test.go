package status

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRunSource struct {
	targets map[string][]string
}

func (f fakeRunSource) ProjectTargets(_ context.Context, project string) []string {
	return f.targets[project]
}

// newTestRunHandler wires a handler over a declared target set, recording what it submits and
// reporting nothing in flight. Callers override socket/statusFn per case.
func newTestRunHandler(t *testing.T, cacheDir string, submitted *[][]string) *DiffRunHandler {
	t.Helper()
	h := NewDiffRunHandler(fakeRunSource{targets: map[string][]string{
		"libs/authkit": {"test", "lint"},
	}}, cacheDir, "v0-test", nil)
	h.socket = func() string { return "test-socket" }
	h.statusFn = func(context.Context, string) (*proc.StatusReply, error) {
		return &proc.StatusReply{}, nil
	}
	h.submitFn = func(_ context.Context, _ string, argv []string, _ string) (string, error) {
		*submitted = append(*submitted, argv)
		return "inv1", nil
	}
	return h
}

func postRun(t *testing.T, h *DiffRunHandler, body string) diffRunResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/diff/run", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code)
	var out diffRunResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// TestDiffRunRefusesAnUndeclaredTarget is the handler half of the guarantee that a browser can
// only ask for work the magusfile already defines. It must never reach the submit path.
func TestDiffRunRefusesAnUndeclaredTarget(t *testing.T) {
	var submitted [][]string
	h := newTestRunHandler(t, t.TempDir(), &submitted)

	out := postRun(t, h, `{"target":"sh","project":"libs/authkit"}`)

	assert.Empty(t, submitted, "an undeclared target must not be submitted")
	assert.Contains(t, out.Undeclared, "sh")
	assert.Equal(t, []string{"test", "lint"}, out.Available,
		"naming what IS declared is what makes the refusal actionable")
	assert.Equal(t, "unknown", out.State)
}

// TestDiffRunRefusesAnUnknownProject covers the other half of the pair: a project the workspace
// does not know declares nothing, so every target in it is undeclared.
func TestDiffRunRefusesAnUnknownProject(t *testing.T) {
	var submitted [][]string
	h := newTestRunHandler(t, t.TempDir(), &submitted)

	out := postRun(t, h, `{"target":"test","project":"../../etc"}`)

	assert.Empty(t, submitted)
	assert.Contains(t, out.Undeclared, "../../etc")
}

// TestDiffRunSubmitsADeclaredTarget pins the argv shape the daemon's dispatch allowlist matches
// on. A drift here and every inline run is refused as unadoptable.
func TestDiffRunSubmitsADeclaredTarget(t *testing.T) {
	var submitted [][]string
	h := newTestRunHandler(t, t.TempDir(), &submitted)

	out := postRun(t, h, `{"target":"test","project":"libs/authkit"}`)

	require.Len(t, submitted, 1)
	assert.Equal(t, []string{"run", "test", "libs/authkit"}, submitted[0])
	assert.Equal(t, "running", out.State)
	assert.True(t, out.Started)
}

// TestDiffRunReportsAlreadyRunning covers the concurrency the surface has to be honest about:
// the reader may already be running this target in their own terminal. Starting a second one is
// wrong, and so is a reply that looks like this request started it.
func TestDiffRunReportsAlreadyRunning(t *testing.T) {
	var submitted [][]string
	h := newTestRunHandler(t, t.TempDir(), &submitted)
	h.statusFn = func(context.Context, string) (*proc.StatusReply, error) {
		return &proc.StatusReply{Calls: []proc.Call{{Args: []string{"run", "test", "libs/authkit"}, Inv: "inv0"}}}, nil
	}

	out := postRun(t, h, `{"target":"test","project":"libs/authkit"}`)

	assert.Empty(t, submitted, "an in-flight run must not be started again")
	assert.Equal(t, "running", out.State)
	assert.False(t, out.Started, "this request did not start it")
}

// TestDiffRunReadsTheLastVerdictFromTheTrail is the poll path: nothing in flight, so the answer
// is whatever the last run of this exact target decided.
func TestDiffRunReadsTheLastVerdictFromTheTrail(t *testing.T) {
	dir := t.TempDir()
	var submitted [][]string
	h := newTestRunHandler(t, dir, &submitted)
	trail.Append(context.Background(), dir, trail.Event{
		Ts:      1000,
		Kind:    trail.KindJob,
		Actor:   "daemon",
		Action:  "run test libs/authkit",
		Outcome: trail.OutcomeError,
		Error:   "2 tests failed",
		DurMs:   4200,
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/diff/run?target=test&project=libs/authkit", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var out diffRunResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))

	assert.Empty(t, submitted, "a GET reports, it never starts work")
	assert.Equal(t, "failed", out.State)
	assert.Equal(t, "2 tests failed", out.Error)
	assert.Equal(t, int64(4200), out.DurationMs)
	assert.Equal(t, int64(5200), out.FinishedMs)
}

// TestDiffRunReportsNoVerdictAsUnknown keeps "nobody has run this" distinct from "this failed".
// Collapsing the two would put a red mark on a target that has never been judged.
func TestDiffRunReportsNoVerdictAsUnknown(t *testing.T) {
	var submitted [][]string
	h := newTestRunHandler(t, t.TempDir(), &submitted)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/diff/run?target=lint&project=libs/authkit", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var out diffRunResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))

	assert.Equal(t, "unknown", out.State)
	assert.Empty(t, out.Error)
}

// TestDiffRunSurfacesASubmitFailure: a daemon that cannot accept the work must say so, not
// leave the surface polling a run that was never started.
func TestDiffRunSurfacesASubmitFailure(t *testing.T) {
	var submitted [][]string
	h := newTestRunHandler(t, t.TempDir(), &submitted)
	h.submitFn = func(context.Context, string, []string, string) (string, error) {
		return "", errors.New("daemon is shutting down")
	}

	out := postRun(t, h, `{"target":"test","project":"libs/authkit"}`)

	assert.Equal(t, "failed", out.State)
	assert.Contains(t, out.Error, "shutting down")
	assert.False(t, out.Started)
}
