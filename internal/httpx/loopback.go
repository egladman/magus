package httpx

import (
	"net"
	"net/http"
)

// The tool-page RPCs (ViewerService, StatusService) are meant to be reachable only from
// the local machine - they expose a workspace's run output and live process state, which
// must never be served to the network. Binding the server to 127.0.0.1 is the first
// line; this handler-level guard is the second (defense in depth), so a misconfigured
// bind can't leak the RPCs. It belongs here, not in protovalidate: protovalidate checks
// message FIELDS, and the caller's network origin is not a field - it is a transport
// property, enforced by an interceptor around the handlers.

// IsLoopbackAddr reports whether a "host:port" (or bare host) address is on the local
// loopback interface (127.0.0.0/8 or ::1). A hostname that does not parse as a literal
// loopback IP is treated as NOT loopback - "localhost" is deliberately rejected because
// it can be re-pointed via /etc/hosts; require the literal IP, matching the browser
// bridge's loopback rule.
func IsLoopbackAddr(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// RequireLoopbackPeer wraps an HTTP handler (or a Connect handler mounted on one) so a
// request whose PEER (r.RemoteAddr) is not loopback is refused before it reaches the RPC.
// This checks the transport peer, not the Host header (that is GuardRebind's job). Applied
// once around the tool-page mux; the interceptor form for a pure Connect server checks the
// same via the peer address.
func RequireLoopbackPeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsLoopbackAddr(r.RemoteAddr) {
			http.Error(w, "forbidden: local access only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
