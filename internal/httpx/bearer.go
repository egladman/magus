package httpx

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// Verifier authenticates a presented bearer token: it names the credential the token is, or
// reports false. It must fail closed on any error. It decides nothing about routes; that is
// the guard's Need against the credential's Grant.
type Verifier func(presented string) (types.Credential, bool)

// BearerGuard admits a request when verify names a credential AND that credential's Grant
// allows need. It is the one place magus decides whether a credential may use a route.
//
// The token is read ONLY from the `Authorization: Bearer <token>` header: a bearer token must
// not travel in a URL, where it lands in access logs, proxy logs and history (RFC 6750
// section 2.3). An EventSource route that cannot set a header opts in to the query carrier
// with [BearerGuardWithQueryToken].
//
// Refusals, in format: 401 MGS9011 when no token was presented; 401 MGS9001 when the token is
// not a credential here (wrong, expired, revoked, or of a class this listener does not
// accept), never saying which; 403 MGS9015 when it is a credential whose grant is below need,
// naming the need, which tells the holder only that their own token is valid. An admitted
// request reaches next with the credential and the rpc entry point on its context
// (trail.CredentialFromContext, trail.EntryPointFromContext).
func BearerGuard(format rpcerr.Format, verify Verifier, need types.Need, next http.Handler) http.Handler {
	return guard(format, verify, need, headerToken, next)
}

// BearerGuardWithQueryToken is [BearerGuard] that also accepts the token from a `?token=`
// query parameter, preferring the header. Use it ONLY for a route a browser EventSource
// connects to, which cannot set a header; keep it off /mcp.
func BearerGuardWithQueryToken(format rpcerr.Format, verify Verifier, need types.Need, next http.Handler) http.Handler {
	return guard(format, verify, need, presentedToken, next)
}

func guard(format rpcerr.Format, verify Verifier, need types.Need, extract func(*http.Request) (string, bool), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := extract(r)
		if !ok {
			format.Write(w, r, bearerMissing)
			return
		}
		cred, ok := verify(presented)
		if !ok {
			format.Write(w, r, bearerRejected)
			return
		}
		if !cred.Grant.Allows(need) {
			format.Write(w, r, rpcerr.Error{
				Code:   connect.CodePermissionDenied,
				Reason: types.GrantInsufficient,
				Message: "this route needs " + need.String() + " and the token presented holds " + grantPhrase(cred.Grant) +
					"; mint one that reaches it with: magus config console token create, or magus config mcp connector create",
			})
			return
		}
		ctx := trail.ContextWithEntryPoint(trail.ContextWithCredential(r.Context(), cred), types.EntryPointRPC)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func grantPhrase(g types.Grant) string {
	if s := g.String(); s != "" {
		return s
	}
	return "nothing"
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
		Message: "the daemon rejected the bearer token: it is wrong, expired, or revoked. Mint one with: magus config mcp connector create, or magus config console token create",
	}
)

// SingleTokenVerifier returns a [Verifier] that accepts exactly the one token expected yields,
// as a classless credential holding grant. The digests are compared in constant time, so the
// check reveals neither the secret's bytes nor its length. A load error fails closed. The
// per-run page servers use it for their per-run token.
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
