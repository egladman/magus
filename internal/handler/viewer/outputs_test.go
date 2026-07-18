package viewer

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOutputs is an OutputSource stub: a fixed descriptor list plus a ref->bytes map, so the handler
// tests exercise wire behavior without a real on-disk store.
type fakeOutputs struct {
	descs []cache.OutputDescriptor
	blobs map[string][]byte
}

func (f fakeOutputs) ListDescriptors() []cache.OutputDescriptor { return f.descs }
func (f fakeOutputs) ByRef(ref string) ([]byte, cache.OutputDescriptor, error) {
	b, ok := f.blobs[ref]
	if !ok {
		return nil, cache.OutputDescriptor{}, fs.ErrNotExist
	}
	return b, cache.OutputDescriptor{Ref: ref}, nil
}

func TestOutputsHandlerListsRunsAsJSON(t *testing.T) {
	src := fakeOutputs{descs: []cache.OutputDescriptor{
		{Ref: "out11111111", Project: "pkg/a", Target: "build:rw", Inv: "invabc", Failed: true, ErrMsg: "boom", TimestampMs: 200, DurationMs: 1200},
		{Ref: "out22222222", Project: "pkg/a", Target: "test", TimestampMs: 100},
	}}
	rr := httptest.NewRecorder()
	NewOutputsHandler(src, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/outputs", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	var body struct {
		Outputs []runSummary `json:"outputs"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Len(t, body.Outputs, 2)
	assert.Equal(t, "out11111111", body.Outputs[0].Ref)
	assert.Equal(t, "build:rw", body.Outputs[0].Target, "the repro target is served verbatim")
	assert.True(t, body.Outputs[0].Failed)
	assert.Equal(t, "boom", body.Outputs[0].Error)
	assert.Equal(t, "invabc", body.Outputs[0].Inv)
}

func TestOutputsHandlerRejectsNonGET(t *testing.T) {
	rr := httptest.NewRecorder()
	NewOutputsHandler(fakeOutputs{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/outputs", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestOutputHandlerServesVerbatimBytes(t *testing.T) {
	src := fakeOutputs{blobs: map[string][]byte{"out11111111": []byte("lint: undefined symbol foo\n")}}
	rr := httptest.NewRecorder()
	NewOutputHandler(src, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/output?ref=out11111111", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "text/plain")
	assert.Equal(t, "lint: undefined symbol foo\n", rr.Body.String())
}

func TestOutputHandlerMissingRefIs400(t *testing.T) {
	rr := httptest.NewRecorder()
	NewOutputHandler(fakeOutputs{}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/output", nil))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestOutputHandlerUnknownRefIs404(t *testing.T) {
	rr := httptest.NewRecorder()
	NewOutputHandler(fakeOutputs{blobs: map[string][]byte{}}, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/output?ref=outdeadbeef", nil))
	assert.Equal(t, http.StatusNotFound, rr.Code)
}
