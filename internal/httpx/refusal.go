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
	// JSONErrors renders google.rpc.Status in AIP-193's HTTP/1.1+JSON shape,
	// {"error":{"code","message","status","details"}}.
	JSONErrors ErrorFormat = iota
	// ConnectErrors renders it as a Connect error, in the variant the request's content type
	// selects: unary JSON, a streaming end-of-stream message, or gRPC trailers.
	ConnectErrors
)

// Refusal is a request turned away before any handler ran: a credential, host, peer, or
// path check failed. Every refusal carries an MGS code, sent as a google.rpc.ErrorInfo
// reason beside a google.rpc.Help link to the code's docs page (AIP-193).
type Refusal struct {
	Code   connect.Code
	Reason types.DiagnosticCode
	// Title names the code in the Help link, the same on every occurrence.
	Title   string
	Message string
}

// Refuse writes f in format e. It is the only way a guard in this tree answers a refusal.
// A 401 always carries the RFC 6750 challenge, which MCP clients key their auth flow on.
func (e ErrorFormat) Refuse(w http.ResponseWriter, r *http.Request, f Refusal) {
	h := w.Header()
	if f.Code == connect.CodeUnauthenticated {
		challenge := `Bearer realm="magus"`
		// RFC 6750 section 3.1: error="invalid_token" only for a token that was presented;
		// a request with no credential gets the bare challenge.
		if f.Reason == types.BearerRejected {
			challenge += `, error="invalid_token"`
		}
		h.Set("WWW-Authenticate", challenge)
	}
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	e.Write(w, r, rpcerr.Error{Code: f.Code, Reason: f.Reason, Title: f.Title, Message: f.Message})
}

// Write renders err in format e, for a handler that answers an error on a mount whose
// format it was given.
func (e ErrorFormat) Write(w http.ResponseWriter, r *http.Request, err rpcerr.Error) {
	if e == ConnectErrors {
		rpcerr.WriteConnect(w, r, err)
		return
	}
	rpcerr.WriteJSON(w, err.Status())
}
