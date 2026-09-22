package httpx

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/egladman/magus/internal/json"
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

// errorDomain is the ErrorInfo domain of every MGS reason: the Go module that mints them.
const errorDomain = "github.com/egladman/magus"

var connectErrors = connect.NewErrorWriter()

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
	if e == FormatConnect {
		// A failed write means the client is gone; there is no one left to tell.
		_ = connectErrors.Write(w, r, f.connectError())
		return
	}
	writeJSONError(w, f)
}

// message names the code and its page, so a client that shows only the message, and never
// decodes the details, still gives the reader something to look up.
func (f Refusal) message() string { return types.FormatDiagnostic(f.Reason, f.Message) }

func (f Refusal) details() []proto.Message {
	return []proto.Message{
		&errdetails.ErrorInfo{Reason: string(f.Reason), Domain: errorDomain},
		&errdetails.Help{Links: []*errdetails.Help_Link{{Description: f.Title, Url: types.CodeURL(f.Reason)}}},
	}
}

func (f Refusal) connectError() *connect.Error {
	err := connect.NewError(f.Code, errors.New(f.message()))
	for _, m := range f.details() {
		if d, derr := connect.NewErrorDetail(m); derr == nil {
			err.AddDetail(d)
		}
	}
	return err
}

// statusBody is AIP-193's HTTP/1.1+JSON rendering of google.rpc.Status: code is the HTTP
// status, status the google.rpc.Code name, details proto3-JSON Any values.
type statusBody struct {
	Error struct {
		Code    int               `json:"code"`
		Message string            `json:"message"`
		Status  string            `json:"status"`
		Details []json.RawMessage `json:"details"`
	} `json:"error"`
}

func writeJSONError(w http.ResponseWriter, f Refusal) {
	status := httpStatus(f.Code)
	var b statusBody
	b.Error.Code = status
	b.Error.Message = f.message()
	b.Error.Status = strings.ToUpper(f.Code.String())
	for _, m := range f.details() {
		a, err := anypb.New(m)
		if err != nil {
			continue
		}
		raw, err := protojson.Marshal(a)
		if err != nil {
			continue
		}
		b.Error.Details = append(b.Error.Details, raw)
	}
	body, err := json.Marshal(b)
	if err != nil {
		http.Error(w, f.message(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// httpStatus is google/rpc/code.proto's HTTP mapping, which Connect's matches.
func httpStatus(c connect.Code) int {
	switch c {
	case connect.CodeCanceled:
		return 499
	case connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodeOutOfRange:
		return http.StatusBadRequest
	case connect.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeAlreadyExists, connect.CodeAborted:
		return http.StatusConflict
	case connect.CodePermissionDenied:
		return http.StatusForbidden
	case connect.CodeResourceExhausted:
		return http.StatusTooManyRequests
	case connect.CodeUnimplemented:
		return http.StatusNotImplemented
	case connect.CodeUnavailable:
		return http.StatusServiceUnavailable
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}
