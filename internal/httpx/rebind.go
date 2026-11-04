package httpx

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/types"
)

// AllowedSet is the accept-list used by GuardRebind.
// Loopback IPs (127.0.0.0/8, ::1) are always allowed via IsLoopback() so no
// static map is needed for them. extra holds an optional concrete non-loopback
// bind host that the operator has deliberately configured (zero = not set).
// Non-IP hostnames like "localhost" live in names.
// origins pass the Origin check and never the Host check:
// a page is trusted, its name is not.
type AllowedSet struct {
	extra   netip.Addr
	names   map[string]struct{} // lowercase
	origins map[string]struct{} // exact scheme://host[:port]
}

// GuardRebind rejects requests that a browser could forge via DNS rebinding.
// It validates two headers on every request it wraps:
//
//   - Host (always present): the parsed hostname must be in allowed.
//   - Origin (browser-only): if present, it must be an exact allowed origin
//     or name an allowed host. Absent Origin is allowed: non-browser clients
//     (a CLI, a desktop app, curl) do not send it.
//
// The server wraps its console routes in it. /mcp and the health routes
// (/livez, /readyz, /healthz) are mounted outside it: /mcp trusts its bearer
// token alone, and the health routes are deliberately unguarded.
func GuardRebind(format rpcerr.Format, allowed AllowedSet, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAllowedHost(r.Host, allowed) {
			format.Write(w, r, hostNotAllowed)
			return
		}
		if orig := r.Header.Get("Origin"); orig != "" && !isAllowedOrigin(orig, allowed) {
			format.Write(w, r, hostNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var hostNotAllowed = rpcerr.Error{
	Code:    connect.CodePermissionDenied,
	Reason:  types.HostNotAllowed,
	Message: "the server serves only loopback hosts and the origins in mcp.allowed_origins; this request named another host in its Host or Origin header",
}

// AllowOrigin returns a copy of a that also accepts origin in the Origin
// header, or an error when origin is inexact. The receiver is not mutated, so
// widening one route never leaks into the set other routes share.
func (a AllowedSet) AllowOrigin(origin string) (AllowedSet, error) {
	exact, err := exactOrigin(origin)
	if err != nil {
		return AllowedSet{}, err
	}
	origins := make(map[string]struct{}, len(a.origins)+1)
	for k := range a.origins {
		origins[k] = struct{}{}
	}
	origins[exact] = struct{}{}
	return AllowedSet{extra: a.extra, names: a.names, origins: origins}, nil
}

// exactOrigin returns s when it is what a browser sends as an Origin: a
// lowercase http or https scheme://host[:port] and nothing more. An entry no
// browser can send would trust nothing while reading as if it did.
func exactOrigin(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || s != strings.ToLower(fmt.Sprintf("%s://%s", u.Scheme, u.Host)) {
		return "", fmt.Errorf("httpx: origin: %q is not an exact http or https scheme://host origin", s)
	}
	return s, nil
}

func isAllowedOrigin(origin string, allowed AllowedSet) bool {
	if _, ok := allowed.origins[origin]; ok {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && isAllowedHost(u.Host, allowed)
}

// AllowedHosts builds the AllowedSet for GuardRebind from the server's
// already-parsed bind address. Loopback addresses are handled dynamically by
// isAllowedHost via IsLoopback(), so they need no explicit entry. When addr
// contains a concrete non-loopback, non-unspecified host it is stored in extra
// so operators who deliberately bind to a LAN IP can still reach the console routes.
func AllowedHosts(addr netip.AddrPort) AllowedSet {
	set := AllowedSet{
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
func isAllowedHost(headerHost string, allowed AllowedSet) bool {
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
