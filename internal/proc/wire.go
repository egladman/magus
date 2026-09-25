package proc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/types"
)

// maxBodyBytes caps a proc request body. Requests from child processes are untrusted in
// principle; this stops an oversized body exhausting server memory before the args-count
// check fires.
const maxBodyBytes = 4 << 20 // 4 MiB

// socketClient is an HTTP client whose every connection dials ep, whatever host a request
// names. Keep-alives are off: each call is one exchange, and an idle connection held open
// would count as a live client against a server trying to shut down.
func socketClient(ep endpoint.Endpoint) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return ep.Dial(ctx) },
		DisableKeepAlives: true,
	}}
}

// socketURL is path on a socket client. The host is a placeholder the socket ignores.
func socketURL(path string) string { return "http://magus" + path }

// outdatedMarker is how net/http reports the old line protocol's reply: the server read an
// HTTP request line as a malformed frame and answered with a one-line JSON error, which the
// client's HTTP parser rejects as a malformed status line ("malformed HTTP status code",
// "malformed HTTP response", by which word of the JSON it trips on). Only a magus process
// listens in the socket directory, so nothing else answers there in something not HTTP.
const outdatedMarker = "malformed HTTP "

// outdated rebuilds a transport error from a server still on the old protocol as the coded
// error that says to restart it, and returns every other error unchanged.
func outdated(addr string, err error) error {
	if err == nil || !strings.Contains(err.Error(), outdatedMarker) {
		return err
	}
	return types.DiagnosticErrorf(types.ServerProtocolOutdated,
		"the magus server at %s was started by an older magus and speaks its old socket protocol; restart it: pkill -TERM -f 'magus server' && magus server start", addr)
}

// ServerOutdated reports whether err (or any error it wraps) is a server still on the old
// line protocol, MGS3025. A caller that reads a failed query as "no server is running"
// checks this first: the server is running, and the fix is to restart it.
func ServerOutdated(err error) bool {
	var de *types.DiagnosticError
	return errors.As(err, &de) && de.Code == types.ServerProtocolOutdated
}

// refusalMessage reads a non-2xx body: a proc route's errorReply, or the AIP-193 error a
// guard in front of it wrote.
func refusalMessage(body []byte) string {
	var r struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &r) == nil {
		if r.Message != "" {
			return r.Message
		}
		if r.Error.Message != "" {
			return r.Error.Message
		}
	}
	return strings.TrimSpace(string(body))
}

// writeJSON answers v as JSON with status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		status, body = http.StatusInternalServerError, []byte(`{"message":"proc: encode reply"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// refuse answers err as an errorReply. A not-adopted refusal is 409: the server is alive and
// answered, it will not take this call. Anything else is 400 when the request itself is at
// fault and 500 otherwise.
func refuse(w http.ResponseWriter, status int, err error) {
	if NotAdopted(err) {
		status = http.StatusConflict
	}
	writeJSON(w, status, errorReply{Message: err.Error()})
}

// decodeBody reads r's JSON body into v, capped at maxBodyBytes.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return fmt.Errorf("proc: request body exceeds %d bytes", maxBodyBytes)
		}
		return fmt.Errorf("proc: read request: %w", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("proc: decode request: %w", err)
	}
	return nil
}
