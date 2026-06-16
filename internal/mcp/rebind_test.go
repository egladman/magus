//go:build mcp

package mcp

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestDnsRebindGuard(t *testing.T) {
	t.Parallel()

	loopback := allowedHosts(netip.MustParseAddrPort("127.0.0.1:7391"))

	cases := []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		// Host variations
		{"loopback IP with port", "127.0.0.1:7391", "", http.StatusOK},
		{"loopback IP bare", "127.0.0.1", "", http.StatusOK},
		{"localhost", "localhost", "", http.StatusOK},
		{"localhost with port", "localhost:7391", "", http.StatusOK},
		{"IPv6 loopback", "[::1]:7391", "", http.StatusOK},
		{"IPv4-mapped loopback", "::ffff:127.0.0.1", "", http.StatusOK},
		{"127.x loopback", "127.0.0.2", "", http.StatusOK}, // whole /8 is loopback
		{"external host", "attacker.com", "", http.StatusForbidden},
		{"external host with port", "evil.com:80", "", http.StatusForbidden},

		// Origin variations (loopback Host throughout)
		{"no origin", "127.0.0.1:7391", "", http.StatusOK},
		{"loopback origin", "127.0.0.1:7391", "http://127.0.0.1:7391", http.StatusOK},
		{"external origin", "127.0.0.1:7391", "http://evil.com", http.StatusForbidden},
		{"null origin", "127.0.0.1:7391", "null", http.StatusForbidden},
		{"malformed origin", "127.0.0.1:7391", "://bad", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rr := httptest.NewRecorder()
			dnsRebindGuard(loopback, okHandler).ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Errorf("host=%q origin=%q: got %d, want %d", tc.host, tc.origin, rr.Code, tc.want)
			}
		})
	}
}

func TestAllowedHosts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		addr    string
		host    string
		allowed bool
	}{
		// Default loopback addr
		{"127.0.0.1:7391", "127.0.0.1", true},
		{"127.0.0.1:7391", "localhost", true},
		{"127.0.0.1:7391", "::1", true},
		{"127.0.0.1:7391", "::ffff:127.0.0.1", true}, // IPv4-mapped → Unmap → IsLoopback
		{"127.0.0.1:7391", "127.0.0.2", true},        // whole 127.0.0.0/8 is loopback
		{"127.0.0.1:7391", "attacker.com", false},

		// Wildcard bind — only loopback allowed, not the wildcard IP itself
		{"0.0.0.0:7391", "127.0.0.1", true},
		{"0.0.0.0:7391", "0.0.0.0", false},

		// Concrete non-loopback bind — that host also allowed
		{"192.168.1.5:7391", "192.168.1.5", true},
		{"192.168.1.5:7391", "127.0.0.1", true},
		{"192.168.1.5:7391", "attacker.com", false},
	}

	for _, tc := range cases {
		t.Run(tc.addr+"/"+tc.host, func(t *testing.T) {
			t.Parallel()
			set := allowedHosts(netip.MustParseAddrPort(tc.addr))
			got := isAllowedHost(tc.host, set)
			if got != tc.allowed {
				t.Errorf("allowedHosts(%q) isAllowedHost(%q): got %v, want %v", tc.addr, tc.host, got, tc.allowed)
			}
		})
	}
}
