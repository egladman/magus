package httpx

import (
	"net/http"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/types"
)

// ErrorFormat is the wire format one mount answers refusals in, chosen where the route is
// mounted. Sniffing the request cannot choose it: connect-go's ErrorWriter classifies every
// GET and every application/* POST as Connect, which would hand /mcp a Connect envelope.
type ErrorFormat uint8

const (
	// FormatJSON renders google.rpc.Status in AIP-193's HTTP/1.1+JSON shape,
	// {"error":{"code","message","status","details"}}.
	FormatJSON ErrorFormat = iota
	// FormatConnect renders it as a Connect error, in the variant the request's content type
	// selects: unary JSON, a streaming end-of-stream message, or gRPC trailers.
	FormatConnect
)

// Write renders err in format e. It is the ONE way a guard or handler in this tree answers
// an error before or instead of running a request: every refusal (a credential, host, peer,
// or path check that failed) and serveUnloaded's direct writes go through it, so the
// no-store/no-sniff headers below apply everywhere rather than at each call site.
// A 401 always carries the RFC 6750 challenge, which MCP clients key their auth flow on.
func (e ErrorFormat) Write(w http.ResponseWriter, r *http.Request, err rpcerr.Error) {
	h := w.Header()
	if err.Code == connect.CodeUnauthenticated {
		challenge := `Bearer realm="magus"`
		// RFC 6750 section 3.1: error="invalid_token" only for a token that was presented;
		// a request with no credential gets the bare challenge.
		if err.Reason == types.BearerRejected {
			challenge += `, error="invalid_token"`
		}
		h.Set("WWW-Authenticate", challenge)
	}
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if e == FormatConnect {
		rpcerr.WriteConnect(w, r, err)
		return
	}
	rpcerr.WriteJSON(w, r, err)
}
