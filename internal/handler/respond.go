package handler

import (
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/types"
)

// AllowGet answers a CORS preflight (204) and rejects non-GET methods (405), returning false
// when the caller should stop.
//
// Here rather than beside any one route: every read handler in every handler package applies the
// same gate, and the copy that lived next to the insight route was the one six unrelated files
// reached across for.
func AllowGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	if r.Method != http.MethodGet {
		RefuseMethod(w, r, http.MethodGet)
		return false
	}
	return true
}

// RefuseMethod answers a request whose method the route does not serve: 405 with the methods
// it does serve in Allow, as the structured refusal every /api/ guard answers with.
func RefuseMethod(w http.ResponseWriter, r *http.Request, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	rpcerr.FormatJSON.Write(w, r, rpcerr.Error{
		// UNIMPLEMENTED is the google.rpc code HTTP transcoding pairs with 405.
		Code:       connect.CodeUnimplemented,
		Reason:     types.MethodNotAllowed,
		Message:    fmt.Sprintf("%s %s is not served; use %s", r.Method, r.URL.Path, strings.Join(allowed, " or ")),
		HTTPStatus: http.StatusMethodNotAllowed,
	})
}

// MaxWireBodyBytes caps the request body a server HTTP handler will read into memory.
// It is generous for real payloads (a unified diff, a patch, a review remark) but
// bounds an authenticated or LAN-reachable client (a connector-token MCP client, a
// share viewer) so a multi-gigabyte POST cannot exhaust server memory. The proc socket
// path caps its frames separately (internal/proc).
const MaxWireBodyBytes = 8 << 20 // 8 MiB

// LimitRequestBody caps r.Body at MaxWireBodyBytes so a later decode fails once the body
// exceeds the cap instead of reading an unbounded stream. Call it before reading the body.
func LimitRequestBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxWireBodyBytes)
}

// WriteJSON marshals v and writes it as an uncached JSON body, matching the read handlers'
// no-store posture: these reads reflect live server state.
func WriteJSON(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "marshal error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
