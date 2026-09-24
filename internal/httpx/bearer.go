package httpx

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// Verifier authenticates a presented bearer token: it names the credential the token is, or
// reports false. It must fail closed on any error. It decides nothing about routes; that is
// the guard's Need against the credential's Grant.
type Verifier func(presented string) (types.Credential, bool)

// reverifyEvery is how often an admitted request's token is checked again while its handler
// runs. It is what reaches a stream a token opened: revoke or expire the token and the
// request's context is cancelled within this long.
var reverifyEvery = 5 * time.Second

// BearerGuard admits a request when verify names a credential AND that credential's Grant
// allows need. It is the one place magus decides whether a credential may use a route. It
// refuses to build with a need that is zero or invalid (see types.Need.Validate).
//
// The token is read ONLY from the `Authorization: Bearer <token>` header: a bearer token must
// not travel in a URL, where it lands in access logs, proxy logs and history (RFC 6750
// section 2.3). An EventSource route that cannot set a header opts in to the query carrier
// with [BearerGuardWithQueryToken].
//
// Refusals, in format: 401 MGS9011 when no token was presented; 401 MGS9001 when the token is
// not a credential here (wrong, expired, revoked, of a class this listener does not accept, or
// the operator token from a peer that is not loopback), never saying which; 403 MGS9015 when
// it is a credential whose grant is below need, naming the need. An admitted request reaches
// next with the credential and the rpc entry point on its context, and a context the guard
// cancels as soon as the token stops verifying or stops allowing need (checked every
// reverifyEvery), so a long-lived stream ends when its token is revoked or expires.
func BearerGuard(format rpcerr.Format, verify Verifier, need types.Need, next http.Handler) (http.Handler, error) {
	if err := need.Validate(); err != nil {
		return nil, fmt.Errorf("httpx: %w", err)
	}
	return guard(format, verify, func(*http.Request) types.Need { return need }, headerToken, next), nil
}

// BearerGuardWithQueryToken is [BearerGuard] that also accepts the token from a `?token=`
// query parameter, preferring the header. Use it ONLY for a route a browser EventSource
// connects to, which cannot set a header; keep it off /mcp.
func BearerGuardWithQueryToken(format rpcerr.Format, verify Verifier, need types.Need, next http.Handler) (http.Handler, error) {
	if err := need.Validate(); err != nil {
		return nil, fmt.Errorf("httpx: %w", err)
	}
	return guard(format, verify, func(*http.Request) types.Need { return need }, presentedToken, next), nil
}

// ProcedureGuard is [BearerGuard] for a Connect service, holding each procedure to its own
// need: needs maps a procedure path ("/magus.job.v1alpha1.JobService/RunJob") to it. A path it
// does not name is held to the strictest need in it, and reaches a handler that answers it
// not found. It refuses to build with an empty table or an invalid need.
func ProcedureGuard(format rpcerr.Format, verify Verifier, needs map[string]types.Need, next http.Handler) (http.Handler, error) {
	if len(needs) == 0 {
		return nil, errors.New("httpx: a procedure guard needs at least one procedure")
	}
	var strictest types.Need
	for proc, n := range needs {
		if err := n.Validate(); err != nil {
			return nil, fmt.Errorf("httpx: %s: %w", proc, err)
		}
		if strictest == (types.Need{}) || stricter(n, strictest) {
			strictest = n
		}
	}
	needOf := func(r *http.Request) types.Need {
		if n, ok := needs[r.URL.Path]; ok {
			return n
		}
		return strictest
	}
	return guard(format, verify, needOf, headerToken, next), nil
}

// stricter orders needs so the fallback for an unknown procedure is the one fewest grants
// meet: token management, then any write, then a read.
func stricter(a, b types.Need) bool {
	rank := func(n types.Need) int {
		if n.Surface == types.SurfaceTokens {
			return 10
		}
		return int(n.Level)
	}
	return rank(a) > rank(b)
}

func guard(format rpcerr.Format, verify Verifier, needOf func(*http.Request) types.Need, extract func(*http.Request) (string, bool), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := extract(r)
		if !ok {
			format.Write(w, r, bearerMissing)
			return
		}
		need := needOf(r)
		cred, ok := admit(verify, presented, r)
		if !ok {
			format.Write(w, r, bearerRejected)
			return
		}
		if !cred.Grant.Allows(need) {
			format.Write(w, r, grantBelow(need, cred.Grant))
			return
		}
		ctx, cancel := context.WithCancel(trail.ContextWithEntryPoint(trail.ContextWithCredential(r.Context(), cred), types.EntryPointRPC))
		defer cancel()
		go reverify(ctx, cancel, verify, presented, need, r)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// admit verifies presented, and refuses the operator token from a peer that is not loopback,
// judged by the TCP peer address and never a header. A server bound beyond loopback
// (mcp.insecure_bind) serves stored tokens to the network; the operator token stays local.
func admit(verify Verifier, presented string, r *http.Request) (types.Credential, bool) {
	cred, ok := verify(presented)
	if !ok {
		return types.Credential{}, false
	}
	if cred.Class == types.ClassOperator && !isLoopbackAddr(r.RemoteAddr) {
		return types.Credential{}, false
	}
	return cred, true
}

// reverify cancels the request when its token stops verifying or stops allowing need. It
// returns when ctx ends, which the guard ensures by cancelling ctx when the handler returns.
func reverify(ctx context.Context, cancel context.CancelFunc, verify Verifier, presented string, need types.Need, r *http.Request) {
	tick := time.NewTicker(reverifyEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if cred, ok := admit(verify, presented, r); !ok || !cred.Grant.Allows(need) {
				cancel()
				return
			}
		}
	}
}

// grantBelow is the 403 for a valid credential below the need. It names the need, and names a
// mint only where a mint can reach it: nothing a person can mint reaches token management.
func grantBelow(need types.Need, held types.Grant) rpcerr.Error {
	heldText := held.String()
	if heldText == "" {
		heldText = "nothing"
	}
	msg := "this route needs " + need.String() + " and the token presented holds " + heldText
	switch {
	case need.Surface == types.SurfaceTokens:
		msg += "; only the operator token reaches it, used from the user's own shell"
	case need.Surface == types.SurfaceMCP:
		msg += "; a connector token reaches it: magus config mcp connector create"
	case need.Level == types.LevelWrite:
		msg += "; a console token reaches it: magus config console token create"
	default:
		msg += "; a viewer token reaches it: magus config console token create --viewer"
	}
	return rpcerr.Error{Code: connect.CodePermissionDenied, Reason: types.GrantInsufficient, Message: msg}
}

// Telling a missing token from a refused one reveals only what the caller already knows,
// whether it sent one. Wrong, expired, revoked and wrong-class stay one answer, so a caller
// cannot probe which tokens exist.
var (
	bearerMissing = rpcerr.Error{
		Code:    connect.CodeUnauthenticated,
		Reason:  types.BearerMissing,
		Message: "the request carried no bearer token; send one as `Authorization: Bearer <token>`. Mint one with: magus config mcp connector create, or magus config console token create",
	}
	bearerRejected = rpcerr.Error{
		Code:    connect.CodeUnauthenticated,
		Reason:  types.BearerRejected,
		Message: "the server rejected the bearer token: it is wrong, expired, or revoked. Mint one with: magus config mcp connector create, or magus config console token create",
	}
)

// SingleTokenVerifier returns a [Verifier] that accepts exactly the one token expected yields,
// as a classless credential holding grant. The digests are compared in constant time, so the
// check reveals neither the secret's bytes nor its length. A load error fails closed.
//
// The per-run page servers use it, and their token stays unprefixed and outside auth's
// classes: it is minted, held and checked inside one process for one page, is never stored,
// and no other listener could accept it, so there is no store for a prefix to route to.
func SingleTokenVerifier(expected func() (string, error), grant types.Grant) Verifier {
	return func(presented string) (types.Credential, bool) {
		want, err := expected()
		if err != nil {
			return types.Credential{}, false
		}
		got := sha256.Sum256([]byte(presented))
		wantSum := sha256.Sum256([]byte(want))
		if subtle.ConstantTimeCompare(got[:], wantSum[:]) != 1 {
			return types.Credential{}, false
		}
		return types.Credential{Grant: grant}, true
	}
}

// headerToken extracts the token from the Authorization header only.
func headerToken(r *http.Request) (string, bool) {
	return bearerToken(r.Header.Get("Authorization"))
}

// presentedToken prefers the `Authorization: Bearer` header and falls back to a `?token=`
// query parameter.
func presentedToken(r *http.Request) (string, bool) {
	if tok, ok := headerToken(r); ok {
		return tok, true
	}
	if tok := r.URL.Query().Get("token"); tok != "" {
		return tok, true
	}
	return "", false
}

// bearerToken extracts the credential from an Authorization header value. The scheme is
// matched case-insensitively per RFC 6750; the token itself is not.
func bearerToken(header string) (string, bool) {
	const scheme = "bearer "
	if len(header) < len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	tok := strings.TrimSpace(header[len(scheme):])
	if tok == "" {
		return "", false
	}
	return tok, true
}
