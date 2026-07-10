package httpx

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gzipBody = `{"definition":"test","node_count":1}`

func gzipJSONHandler() http.Handler {
	return Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"abc"`)
		_, _ = w.Write([]byte(gzipBody))
	}))
}

func TestGzip_CompressesWhenAccepted(t *testing.T) {
	h := gzipJSONHandler()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	assert.Contains(t, w.Header().Get("Vary"), "Accept-Encoding")
	assert.Equal(t, `"abc"`, w.Header().Get("ETag"), "ETag survives compression")

	gr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	defer gr.Close()
	raw, err := io.ReadAll(gr)
	require.NoError(t, err)
	assert.Equal(t, gzipBody, string(raw))
}

func TestGzip_PassthroughWhenNotAccepted(t *testing.T) {
	h := gzipJSONHandler()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Empty(t, w.Header().Get("Content-Encoding"))
	assert.Equal(t, gzipBody, w.Body.String())
}

// TestGzip_NoBodyStatusNotCompressed confirms a 304 (or any bodyless status) is not gzipped,
// so a conditional GET returns an empty body without a bogus gzip header.
func TestGzip_NoBodyStatusNotCompressed(t *testing.T) {
	h := Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusNotModified, w.Code)
	assert.Empty(t, w.Header().Get("Content-Encoding"))
	assert.Empty(t, w.Body.Bytes())
}
