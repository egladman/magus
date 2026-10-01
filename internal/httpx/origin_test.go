package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOrigin(t *testing.T) {
	got, err := ParseOrigin("https://eli.gladman.cc/magus/logs/")
	require.NoError(t, err)
	assert.Equal(t, "https://eli.gladman.cc", got)

	_, err = ParseOrigin("not-a-url")
	assert.Error(t, err)
}

// OpenCORS answers any Origin with "*", never with credentials, and answers the preflight
// itself so the wrapped guard never sees a tokenless OPTIONS.
func TestOpenCORS(t *testing.T) {
	reached := 0
	h := OpenCORS(CORSPolicy{
		Methods:       []string{"GET", "POST"},
		AllowHeaders:  []string{"Authorization", "Content-Type"},
		ExposeHeaders: []string{"Mcp-Session-Id"},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached++
		w.WriteHeader(http.StatusUnauthorized)
	}))

	r := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	r.Header.Set("Origin", "https://anywhere.example")
	r.Header.Set("Access-Control-Request-Method", "POST")
	r.Header.Set("Access-Control-Request-Private-Network", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Zero(t, reached, "the preflight never reaches the guard")
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, POST", w.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Authorization, Content-Type", w.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Private-Network"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))

	r = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Origin", "https://anywhere.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Equal(t, 1, reached, "every other method reaches the wrapped handler")
	assert.Equal(t, http.StatusUnauthorized, w.Code, "the wrapped handler's answer stands")
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"), "a refusal stays readable to the page")
	assert.Equal(t, "Mcp-Session-Id", w.Header().Get("Access-Control-Expose-Headers"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
}

// corsTestHandler wraps a 200 OK handler in CORSAllow for the given origins.
func corsTestHandler(origins ...string) http.Handler {
	return CORSAllow(origins...)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func TestCORSAllow_AllowedOrigin(t *testing.T) {
	h := corsTestHandler("https://example.com", "http://localhost:17391")
	for _, origin := range []string{"https://example.com", "http://localhost:17391"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, origin, w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSAllow_DisallowedOrigin_NoHeader(t *testing.T) {
	h := corsTestHandler("https://example.com")
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSAllow_NoOrigin_NoHeader(t *testing.T) {
	h := corsTestHandler("https://example.com")
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

// TestCORSAllow_EmptyOriginsIgnored confirms an empty allow-list entry (e.g. an unset site
// origin) never matches an empty Origin header.
func TestCORSAllow_EmptyOriginsIgnored(t *testing.T) {
	h := corsTestHandler("", "https://example.com")
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	r.Header.Set("Origin", "")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSAllow_PNAPreflight(t *testing.T) {
	h := corsTestHandler("https://example.com")
	r := httptest.NewRequest(http.MethodOptions, "/api/v1/graph", nil)
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	r.Header.Set("Access-Control-Request-Private-Network", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Private-Network"))
}

func TestCORSAllow_PNANotSetWhenNotRequested(t *testing.T) {
	h := corsTestHandler("https://example.com")
	r := httptest.NewRequest(http.MethodOptions, "/api/v1/graph", nil)
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Private-Network"))
}
