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

// OpenCORS answers the preflight before the guard, and lets only an admitted response carry
// Access-Control-Allow-Origin: a refusal stays opaque to the page. Neither half allows
// credentials.
func TestOpenCORS(t *testing.T) {
	preflight, allow := OpenCORS(CORSPolicy{
		Methods:       []string{"GET", "POST"},
		AllowHeaders:  []string{"Authorization", "Content-Type"},
		ExposeHeaders: []string{"Mcp-Session-Id"},
	})
	guarded := 0
	h := preflight(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guarded++
		if r.Header.Get("Authorization") != "Bearer ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		allow(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(w, r)
	}))
	send := func(method, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/mcp", nil)
		r.Header.Set("Origin", "https://anywhere.example")
		r.Header.Set("Access-Control-Request-Method", "POST")
		r.Header.Set("Access-Control-Request-Private-Network", "true")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	w := send(http.MethodOptions, "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Zero(t, guarded, "the preflight never reaches the guard")
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, POST", w.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Authorization, Content-Type", w.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Private-Network"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))

	w = send(http.MethodPost, "wrong")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	for _, k := range []string{"Access-Control-Allow-Origin", "Access-Control-Expose-Headers", "Access-Control-Allow-Private-Network", "Access-Control-Allow-Credentials"} {
		assert.Empty(t, w.Header().Get(k), "a refusal carries no %s", k)
	}

	w = send(http.MethodPost, "ok")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"), "an admitted response is readable")
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
