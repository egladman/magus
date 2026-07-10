package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsLoopbackAddr(t *testing.T) {
	loopback := []string{"127.0.0.1:7391", "127.0.0.1", "[::1]:80", "::1", "127.5.5.5:1"}
	for _, a := range loopback {
		assert.True(t, IsLoopbackAddr(a), "%q should be loopback", a)
	}
	notLoopback := []string{"192.168.1.10:80", "10.0.0.1", "0.0.0.0:80", "localhost:80", "example.com:443", ""}
	for _, a := range notLoopback {
		assert.False(t, IsLoopbackAddr(a), "%q must NOT be treated as loopback", a)
	}
}

func TestRequireLoopbackGuard(t *testing.T) {
	ok := RequireLoopback(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	local := httptest.NewRequest(http.MethodGet, "/api", nil)
	local.RemoteAddr = "127.0.0.1:5555"
	rr := httptest.NewRecorder()
	ok.ServeHTTP(rr, local)
	assert.Equal(t, http.StatusOK, rr.Code)

	remote := httptest.NewRequest(http.MethodGet, "/api", nil)
	remote.RemoteAddr = "203.0.113.7:5555"
	rr = httptest.NewRecorder()
	ok.ServeHTTP(rr, remote)
	assert.Equal(t, http.StatusForbidden, rr.Code, "a non-loopback peer is refused")
}
