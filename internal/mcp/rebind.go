//go:build mcp

package mcp

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// allowedSet is the accept-list used by dnsRebindGuard.
// Loopback IPs (127.0.0.0/8, ::1) are always allowed via IsLoopback() so no
// static map is needed for them. extra holds an optional concrete non-loopback
// bind host that the operator has deliberately configured (zero = not set).
// Non-IP hostnames like "localhost" live in names.
type allowedSet struct {
	extra netip.Addr
	names map[string]struct{} // lowercase
}

// dnsRebindGuard rejects requests that a browser could forge via DNS rebinding.
// It validates two headers on every request to /mcp:
//
//   - Host (always present): the parsed hostname must be in allowed.
//   - Origin (browser-only): if present, the parsed hostname must be in allowed.
//     Absent Origin is allowed — non-browser MCP clients (CLI, Claude Desktop,
//     curl) do not send it.
//
// Health routes (/livez, /readyz, /healthz) are mounted outside this middleware
// and are deliberately left unguarded.
func dnsRebindGuard(allowed allowedSet, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAllowedHost(r.Host, allowed) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if orig := r.Header.Get("Origin"); orig != "" {
			u, err := url.Parse(orig)
			if err != nil || !isAllowedHost(u.Host, allowed) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// allowedHosts builds the allowedSet for dnsRebindGuard from the server's
// already-parsed bind address. Loopback addresses are handled dynamically by
// isAllowedHost via IsLoopback(), so they need no explicit entry. When addr
// contains a concrete non-loopback, non-unspecified host it is stored in extra
// so operators who deliberately bind to a LAN IP can still reach /mcp.
func allowedHosts(addr netip.AddrPort) allowedSet {
	set := allowedSet{
		names: map[string]struct{}{"localhost": {}},
	}
	host := addr.Addr().Unmap()
	if host.IsValid() && !host.IsUnspecified() && !host.IsLoopback() {
		set.extra = host
	}
	return set
}

// isAllowedHost reports whether the hostname extracted from headerHost is
// permitted. headerHost may be "host", "host:port", or "[::1]:port".
// IP addresses are parsed and Unmap'd so IPv4-mapped IPv6 addresses match
// their IPv4 equivalents; the whole loopback range (127.0.0.0/8, ::1) is
// accepted via IsLoopback() without needing explicit map entries.
func isAllowedHost(headerHost string, allowed allowedSet) bool {
	host := headerHost
	if h, _, err := net.SplitHostPort(headerHost); err == nil {
		host = h
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if ip.IsLoopback() {
			return true
		}
		return allowed.extra.IsValid() && ip == allowed.extra
	}
	_, ok := allowed.names[strings.ToLower(host)]
	return ok
}
