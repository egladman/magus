package viewer

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/journal"
	json "github.com/egladman/magus/internal/json"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRuns is a RunSource stub: a fixed run-log list, an inv->events map, and a ref->descriptor map,
// so the invocation handlers are exercised without a real on-disk store.
type fakeRuns struct {
	logs   []cache.RunLog
	events map[string][]journal.Event
	descs  map[string]cache.OutputDescriptor
	limit  int // the limit the handler asked for, so the cap is observable
}

func (f *fakeRuns) ListRunLogs(limit int) []cache.RunLog {
	f.limit = limit
	return f.logs
}

func (f *fakeRuns) InvocationEventsByID(inv string) (journal.Invocation, []journal.Event, error) {
	evs, ok := f.events[inv]
	if !ok {
		return journal.Invocation{}, nil, fs.ErrNotExist
	}
	return journal.InvocationFromEvents(inv, evs), evs, nil
}

func (f *fakeRuns) DescriptorByRef(ref string) (cache.OutputDescriptor, error) {
	d, ok := f.descs[ref]
	if !ok {
		return cache.OutputDescriptor{}, fs.ErrNotExist
	}
	return d, nil
}

func runFixture() *fakeRuns {
	return &fakeRuns{
		logs: []cache.RunLog{
			{Inv: "invabc", Arguments: []string{"affected", "ci"}, Trigger: "ci", StartedMs: 100, FinishedMs: 900, Status: "fail", MagusVersion: "v1"},
			{Inv: "invdef", Arguments: []string{"run", "build"}, StartedMs: 50, FinishedMs: 80, Status: "pass"},
		},
		events: map[string][]journal.Event{
			"invabc": {
				{Kind: journal.KindStarted, Ts: 100, MagusVersion: "v1", Command: &journal.Command{Arguments: []string{"affected", "ci"}, Trigger: journal.TriggerCI}},
				{Kind: journal.KindOutput, Ts: 200, Project: "pkg/a", Target: "build", Stream: journal.StreamStdout, Text: "compiling"},
				{Kind: journal.KindResult, Ts: 300, Project: "pkg/a", Target: "build", Status: journal.StatusPass, Ref: "out11111111", DurMs: 100},
				{Kind: journal.KindFinished, Ts: 900, Status: journal.StatusFail},
			},
		},
		descs: map[string]cache.OutputDescriptor{"out11111111": {Ref: "out11111111", Inv: "invabc"}},
	}
}

func TestRunsHandlerListsInvocationsAsJSON(t *testing.T) {
	src := runFixture()
	rr := httptest.NewRecorder()
	NewRunsHandler(src, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/runs", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	var body struct {
		Runs []runLog `json:"runs"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Len(t, body.Runs, 2)
	assert.Equal(t, runLog{
		Inv: "invabc", Arguments: []string{"affected", "ci"}, Trigger: "ci",
		StartedMs: 100, FinishedMs: 900, Status: "fail", MagusVersion: "v1",
	}, body.Runs[0])
	assert.Equal(t, defaultRunLimit, src.limit, "an unparameterized read takes the default cap")
}

func TestRunsHandlerCapsTheLimitAtWhatTheStoreRetains(t *testing.T) {
	src := runFixture()
	rr := httptest.NewRecorder()
	NewRunsHandler(src, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/runs?limit=100000", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, cache.DefaultMaxRuns, src.limit)
}

func TestRunsHandlerRejectsABadLimit(t *testing.T) {
	for _, q := range []string{"?limit=0", "?limit=-3", "?limit=lots"} {
		rr := httptest.NewRecorder()
		NewRunsHandler(runFixture(), nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/runs"+q, nil))
		assert.Equal(t, http.StatusBadRequest, rr.Code, "limit %q", q)
	}
}

func TestRunsHandlerRejectsNonGET(t *testing.T) {
	rr := httptest.NewRecorder()
	NewRunsHandler(runFixture(), nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/runs", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestRunHandlerServesTheJournalAsProtobuf(t *testing.T) {
	rr := httptest.NewRecorder()
	NewRunHandler(runFixture(), nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/run?inv=invabc", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/octet-stream", rr.Header().Get("Content-Type"))
	var j viewerv1.Journal
	require.NoError(t, proto.Unmarshal(rr.Body.Bytes(), &j))
	require.NotNil(t, j.GetInvocation())
	assert.Equal(t, "invabc", j.GetInvocation().GetId())
	assert.Equal(t, []string{"affected", "ci"}, j.GetInvocation().GetCommand().GetArguments())
	require.Len(t, j.GetEvents(), 4, "the whole stream is served, lifecycle events included")
	assert.Equal(t, viewerv1.Kind_KIND_RESULT, j.GetEvents()[2].GetKind())
	assert.Equal(t, "out11111111", j.GetEvents()[2].GetRef())
}

// A ref addresses the RUN that produced it, so the browser can open one target's output in the
// context of the command that scheduled it without a second round trip to resolve the id.
func TestRunHandlerResolvesARefToItsInvocation(t *testing.T) {
	rr := httptest.NewRecorder()
	NewRunHandler(runFixture(), nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/run?ref=out11111111", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	var j viewerv1.Journal
	require.NoError(t, proto.Unmarshal(rr.Body.Bytes(), &j))
	assert.Equal(t, "invabc", j.GetInvocation().GetId())
}

func TestRunHandlerRejectsAmbiguousAndMissingAddresses(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		code int
	}{
		{"no address at all", "/api/v1/run", http.StatusBadRequest},
		{"both addresses", "/api/v1/run?inv=invabc&ref=out11111111", http.StatusBadRequest},
		{"unknown ref", "/api/v1/run?ref=out99999999", http.StatusNotFound},
		// The coarser rotation cap on run journals means an output can outlive its run. The 404 is
		// what tells the browser to fall back to /api/v1/output rather than reporting an error.
		{"rotated-away journal", "/api/v1/run?inv=invdef", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			NewRunHandler(runFixture(), nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.url, nil))
			assert.Equal(t, tc.code, rr.Code)
		})
	}
}

func TestRunHandlerRejectsNonGET(t *testing.T) {
	rr := httptest.NewRecorder()
	NewRunHandler(runFixture(), nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/run?inv=invabc", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}
