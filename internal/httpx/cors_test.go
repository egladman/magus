package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
