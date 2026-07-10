package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestDnsRebindGuard(t *testing.T) {
	t.Parallel()

	loopback := AllowedHosts(netip.MustParseAddrPort("127.0.0.1:7391"))

	// serve drives one request with the given Host and Origin (empty = none)
	// through the guard and returns the status code.
	serve := func(host, origin string) int {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rr := httptest.NewRecorder()
		GuardRebind(loopback, okHandler).ServeHTTP(rr, req)
		return rr.Code
	}

	t.Run("host variations", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, http.StatusOK, serve("127.0.0.1:7391", ""), "loopback IP with port")
		assert.Equal(t, http.StatusOK, serve("127.0.0.1", ""), "loopback IP bare")
		assert.Equal(t, http.StatusOK, serve("localhost", ""), "localhost")
		assert.Equal(t, http.StatusOK, serve("localhost:7391", ""), "localhost with port")
		assert.Equal(t, http.StatusOK, serve("[::1]:7391", ""), "IPv6 loopback")
		assert.Equal(t, http.StatusOK, serve("::ffff:127.0.0.1", ""), "IPv4-mapped loopback")
		assert.Equal(t, http.StatusOK, serve("127.0.0.2", ""), "127.x loopback (whole /8 is loopback)")
		assert.Equal(t, http.StatusForbidden, serve("attacker.com", ""), "external host")
		assert.Equal(t, http.StatusForbidden, serve("evil.com:80", ""), "external host with port")
	})

	t.Run("origin variations", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, http.StatusOK, serve("127.0.0.1:7391", ""), "no origin")
		assert.Equal(t, http.StatusOK, serve("127.0.0.1:7391", "http://127.0.0.1:7391"), "loopback origin")
		assert.Equal(t, http.StatusForbidden, serve("127.0.0.1:7391", "http://evil.com"), "external origin")
		assert.Equal(t, http.StatusForbidden, serve("127.0.0.1:7391", "null"), "null origin")
		assert.Equal(t, http.StatusForbidden, serve("127.0.0.1:7391", "://bad"), "malformed origin")
	})
}

func TestAllowedHosts(t *testing.T) {
	t.Parallel()

	// allowed reports whether host passes the guard built for bind addr.
	allowed := func(addr, host string) bool {
		return isAllowedHost(host, AllowedHosts(netip.MustParseAddrPort(addr)))
	}

	t.Run("default loopback addr", func(t *testing.T) {
		t.Parallel()
		assert.True(t, allowed("127.0.0.1:7391", "127.0.0.1"))
		assert.True(t, allowed("127.0.0.1:7391", "localhost"))
		assert.True(t, allowed("127.0.0.1:7391", "::1"))
		assert.True(t, allowed("127.0.0.1:7391", "::ffff:127.0.0.1")) // IPv4-mapped → Unmap → IsLoopback
		assert.True(t, allowed("127.0.0.1:7391", "127.0.0.2"))        // whole 127.0.0.0/8 is loopback
		assert.False(t, allowed("127.0.0.1:7391", "attacker.com"))
	})

	t.Run("wildcard bind allows only loopback, not the wildcard IP", func(t *testing.T) {
		t.Parallel()
		assert.True(t, allowed("0.0.0.0:7391", "127.0.0.1"))
		assert.False(t, allowed("0.0.0.0:7391", "0.0.0.0"))
	})

	t.Run("concrete non-loopback bind also allows that host", func(t *testing.T) {
		t.Parallel()
		assert.True(t, allowed("192.168.1.5:7391", "192.168.1.5"))
		assert.True(t, allowed("192.168.1.5:7391", "127.0.0.1"))
		assert.False(t, allowed("192.168.1.5:7391", "attacker.com"))
	})
}
