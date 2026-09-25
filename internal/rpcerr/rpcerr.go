// Package rpcerr builds the errors the server answers with. Each is one google.rpc.Status
// (AIP-193): a canonical code, a message, and typed details led by an ErrorInfo whose reason
// is the MGS code. The same Status renders as a Connect error on a Connect service and as
// AIP-193's HTTP/1.1+JSON everywhere else, so a client reads the reason identically from both.
package rpcerr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

const (
	// Domain is the ErrorInfo domain of every MGS reason: the Go module that mints them.
	Domain = "github.com/egladman/magus"
	// BuzzDomain is the ErrorInfo domain of a BZZ code, minted by its own module.
	BuzzDomain = "github.com/egladman/magus/libs/gopherbuzz"
)

// Error is one server error before it is rendered. Reason is required: AIP-193 puts an
// ErrorInfo on every error, and the MGS code is its reason.
type Error struct {
	Code    connect.Code
	Reason  types.DiagnosticCode
	Message string
	// Metadata keys are lowerCamelCase; once a key ships for a reason it keeps shipping.
	Metadata map[string]string
	// Links follow the reason's own Help link.
	Links []*errdetails.Help_Link
	// Details are the typed details besides ErrorInfo and Help, each type at most once.
	Details []proto.Message
	// HTTPStatus replaces the status Code maps to, for an HTTP condition no google.rpc code
	// names, such as 405. FormatJSON alone reads it: FormatConnect always answers the status
	// Connect's protocol pairs with Code. Zero keeps the mapping.
	HTTPStatus int
}

// titles names each reason in its Help link, the heading of the reason's own page under
// docs/reference/codes, which TestTitlesMatchTheCodePages holds them to. Every MGS9xxx code
// is here, since that range is the server's and any of it can reach a writer;
// TestEveryServerReasonHasATitle holds that.
var titles = map[types.DiagnosticCode]string{
	types.WorkspaceLoadFailed:       "workspace failed to load",
	types.WorkspaceStillLoading:     "workspace still loading",
	types.BearerRejected:            "bearer token rejected",
	types.InsecureTokenPermissions:  "insecure token file permissions",
	types.TokenStoreTooNew:          "token store is too new",
	types.NoAuthToken:               "no auth token configured",
	types.TokenNameExists:           "token name already exists",
	types.TokenNotFound:             "token not found",
	types.HostNotAllowed:            "host not allowed",
	types.LoopbackPeerRequired:      "local access only",
	types.ShareBoundToAnotherDevice: "share link bound to another device",
	types.ConsoleFileWithheld:       "console file withheld",
	types.BearerMissing:             "no bearer token presented",
	types.MethodNotAllowed:          "method not allowed",
	types.ConsoleNotBuilt:           "console not built",
	types.ShareUnavailable:          "share listener unavailable",
	types.GrantInsufficient:         "grant below the route's need",
	types.OperatorTokenFormat:       "operator token predates the class prefix",
	types.TokenStoreTooOld:          "token store predates grants",
	types.TokenLifetimeOutOfRange:   "token lifetime outside its bound",
	types.TokenRecordInvalid:        "invalid token record skipped",
	types.ShareRequestMalformed:     "malformed share request",
	types.TokenRequestInvalid:       "invalid token request",
	types.SocketPeerNotOwner:        "socket peer is not the server's user",
}

// Error returns the rendered message, so an Error can travel as a Go error.
func (e Error) Error() string { return e.message() }

// message names the code and its page, so a client that shows only the message, and never
// decodes the details, still gives the reader something to look up.
func (e Error) message() string { return types.FormatDiagnostic(e.Reason, e.Message) }

func (e Error) details() []proto.Message {
	out := make([]proto.Message, 0, len(e.Details)+2)
	out = append(out, &errdetails.ErrorInfo{Reason: string(e.Reason), Domain: Domain, Metadata: e.Metadata})
	out = append(out, e.Details...)
	title, ok := titles[e.Reason]
	if !ok {
		title = string(e.Reason)
	}
	links := append([]*errdetails.Help_Link{{Description: title, Url: types.CodeURL(e.Reason)}}, e.Links...)
	return append(out, &errdetails.Help{Links: links})
}

// droppedDetail names a detail left out because it failed to marshal.
func droppedDetail(m proto.Message, err error) error {
	return fmt.Errorf("rpcerr: dropped a %T detail that failed to marshal: %w", m, err)
}

// Status is e as a google.rpc.Status, the shape a resource carries in its error field. A
// detail that fails to marshal is left out rather than failing the whole status, and err
// names each one: the status is always usable, and err is for the caller to log.
//
// Marshaling is deterministic: ErrorInfo carries a map (Metadata), and the default
// non-deterministic marshal orders a map's entries from Go's randomized range order, so
// two Status values built from the same Error would encode to different Any bytes and
// compare unequal under proto.Equal.
func (e Error) Status() (*status.Status, error) {
	st := &status.Status{Code: int32(e.Code), Message: e.message()}
	var errs []error
	for _, m := range e.details() {
		a := new(anypb.Any)
		if err := anypb.MarshalFrom(a, m, proto.MarshalOptions{Deterministic: true}); err != nil {
			errs = append(errs, droppedDetail(m, err))
			continue
		}
		st.Details = append(st.Details, a)
	}
	return st, errors.Join(errs...)
}

// connectError is e as a Connect error, with dropped details named in err as Status does.
func (e Error) connectError() (*connect.Error, error) {
	cerr := connect.NewError(e.Code, errors.New(e.message()))
	var errs []error
	for _, m := range e.details() {
		d, err := connect.NewErrorDetail(m)
		if err != nil {
			errs = append(errs, droppedDetail(m, err))
			continue
		}
		cerr.AddDetail(d)
	}
	return cerr, errors.Join(errs...)
}

// logDropped reports details a renderer left out, so a client missing one can be traced.
func logDropped(ctx context.Context, err error) {
	if err != nil {
		slog.WarnContext(ctx, err.Error())
	}
}

// Format is the wire format one mount answers errors in, chosen where the route is
// mounted. Sniffing the request cannot choose it: connect-go's ErrorWriter classifies every
// GET and every application/* POST as Connect, which would hand /mcp a Connect envelope.
type Format uint8

const (
	// FormatJSON renders google.rpc.Status in AIP-193's HTTP/1.1+JSON shape,
	// {"error":{"code","message","status","details"}}.
	FormatJSON Format = iota
	// FormatConnect renders it as a Connect error, in the variant the request's content type
	// selects: unary JSON, a streaming end-of-stream message, or gRPC trailers.
	FormatConnect
)

// Write renders err in format f. It is how a guard, serveUnloaded, and a server-level route
// such as the share endpoint answer an error before or instead of running a request, so the
// no-store/no-sniff headers below apply to all of them rather than at each call site.
// A 401 always carries the RFC 6750 challenge, which MCP clients key their auth flow on.
//
// TODO: route handlers under /api/ still answer their own validation and internal errors
// with plain-text http.Error; each needs a reason code before it can come through here.
func (f Format) Write(w http.ResponseWriter, r *http.Request, e Error) {
	h := w.Header()
	if e.Code == connect.CodeUnauthenticated {
		challenge := `Bearer realm="magus"`
		// RFC 6750 section 3.1: error="invalid_token" only for a token that was presented;
		// a request with no credential gets the bare challenge.
		if e.Reason == types.BearerRejected {
			challenge += `, error="invalid_token"`
		}
		h.Set("WWW-Authenticate", challenge)
	}
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if f == FormatConnect {
		writeConnect(w, r, e)
		return
	}
	writeJSON(w, r, e)
}

var connectWriter = connect.NewErrorWriter()

// writeConnect writes e as a Connect error in the variant r's content type selects.
func writeConnect(w http.ResponseWriter, r *http.Request, e Error) {
	cerr, dropped := e.connectError()
	logDropped(r.Context(), dropped)
	// A failed write means the client is gone; there is no one left to tell.
	_ = connectWriter.Write(w, r, cerr)
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

// writeJSON writes e in AIP-193's HTTP/1.1+JSON shape.
func writeJSON(w http.ResponseWriter, r *http.Request, e Error) {
	st, dropped := e.Status()
	logDropped(r.Context(), dropped)
	code := connect.Code(st.GetCode())
	httpCode := httpStatus(code)
	if e.HTTPStatus != 0 {
		httpCode = e.HTTPStatus
	}
	var b statusBody
	b.Error.Code = httpCode
	b.Error.Message = st.GetMessage()
	b.Error.Status = strings.ToUpper(code.String())
	for _, a := range st.GetDetails() {
		raw, err := protojson.Marshal(a)
		if err != nil {
			logDropped(r.Context(), fmt.Errorf("rpcerr: dropped a %s detail that failed to render as JSON: %w", a.GetTypeUrl(), err))
			continue
		}
		b.Error.Details = append(b.Error.Details, raw)
	}
	body, err := json.Marshal(b)
	if err != nil {
		http.Error(w, st.GetMessage(), httpCode)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(httpCode)
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

// workspaceResource is the ResourceInfo type of a workspace: its wire message.
const workspaceResource = "magus.status.v1alpha1.Workspace"

// loadErrorViolation is the PreconditionFailure type of a load failure no code identifies,
// such as a parse error or an unreadable magus.yaml.
const loadErrorViolation = "LOAD_ERROR"

// WorkspaceFailed is what a call needing root answers while root is FAILED, and the Status
// the workspace itself carries. The reason is the proximate cause (MGS3016); each
// diagnostic the load stopped on is a PreconditionFailure violation, typed by its own code.
func WorkspaceFailed(root string, f *types.WorkspaceFailure) Error {
	if f == nil {
		f = &types.WorkspaceFailure{}
	}
	e := Error{
		Code:     connect.CodeFailedPrecondition,
		Reason:   types.WorkspaceLoadFailed,
		Message:  fmt.Sprintf("workspace %s failed to load: %s", root, f.Message),
		Metadata: map[string]string{"workspace": root},
	}
	pf := &errdetails.PreconditionFailure{}
	seen := map[string]bool{}
	for i, d := range f.Diagnostics {
		kind := string(d.Code)
		if kind == "" {
			kind = loadErrorViolation
		}
		at := fmt.Sprintf("%s:%d:%d", d.File, d.Line, d.Column)
		pf.Violations = append(pf.Violations, &errdetails.PreconditionFailure_Violation{
			Type: kind, Subject: at, Description: d.Message,
		})
		if i == 0 {
			e.Message = fmt.Sprintf("workspace %s failed to load: %s %s", root, at, d.Message)
			if d.Code != "" {
				e.Message = fmt.Sprintf("workspace %s failed to load: %s [%s] %s", root, at, d.Code, d.Message)
				e.Metadata["causeCode"] = string(d.Code)
				e.Metadata["causeDomain"] = codeDomain(d.Code)
			}
			e.Metadata["file"] = d.File
			e.Metadata["line"] = strconv.Itoa(d.Line)
			e.Metadata["column"] = strconv.Itoa(d.Column)
		}
		if d.URL != "" && !seen[d.URL] {
			seen[d.URL] = true
			e.Links = append(e.Links, &errdetails.Help_Link{Description: string(d.Code), Url: d.URL})
		}
	}
	if len(pf.Violations) == 0 {
		pf.Violations = []*errdetails.PreconditionFailure_Violation{{
			Type: loadErrorViolation, Subject: root, Description: f.Message,
		}}
	}
	e.Details = []proto.Message{pf, &errdetails.ResourceInfo{
		ResourceType: workspaceResource, ResourceName: root, Description: "failed to load",
	}}
	return e
}

// loadingRetry is how long a caller waits before retrying a workspace still loading. Loads
// take a second or two; a shorter hint only adds refusals.
const loadingRetry = 2 * time.Second

// WorkspaceLoading is what a call needing root answers while root is loading: UNAVAILABLE,
// because the same call succeeds once the load finishes.
func WorkspaceLoading(root string) Error {
	return Error{
		Code:     connect.CodeUnavailable,
		Reason:   types.WorkspaceStillLoading,
		Message:  fmt.Sprintf("workspace %s is still loading; retry shortly", root),
		Metadata: map[string]string{"workspace": root},
		Details: []proto.Message{
			&errdetails.RetryInfo{RetryDelay: durationpb.New(loadingRetry)},
			&errdetails.ResourceInfo{ResourceType: workspaceResource, ResourceName: root, Description: "loading"},
		},
	}
}

func codeDomain(c types.DiagnosticCode) string {
	if strings.HasPrefix(string(c), "BZZ") {
		return BuzzDomain
	}
	return Domain
}
