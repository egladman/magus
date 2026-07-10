package httpx

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Verifier decides whether a presented bearer token is accepted. It is the one
// knob that varies across loopback endpoints: the daemon passes auth.VerifyBearer
// (cli token or a non-expired named connector token), while the ephemeral live
// and blob servers pass SingleTokenVerifier over their per-run token.
type Verifier func(presented string) bool

// BearerGuard rejects any request whose token fails verify. The token is read
// ONLY from the `Authorization: Bearer <token>` header. This is the default and
// the right choice for every endpoint a non-browser client reaches (the MCP
// endpoint, plain fetch() clients): a bearer token must not travel in the URL,
// where it leaks into access logs, proxy logs, and browser history (RFC 6750
// section 2.3). For the browser-EventSource endpoints that genuinely cannot set
// a header, use [BearerGuardWithQueryToken] instead - an explicit opt-in, so a
// new mount is header-only unless it deliberately widens the carrier.
//
// verify is called on every request, so a rotate, create, or revoke takes effect
// without restarting the server; it must fail closed (return false) on any error.
// Failures return 401 with a WWW-Authenticate challenge and a generic body that
// does not distinguish a missing token from a wrong one.
func BearerGuard(verify Verifier, next http.Handler) http.Handler {
	return guard(verify, headerToken, next)
}

// BearerGuardWithQueryToken is [BearerGuard] that ALSO accepts the token from a
// `?token=<token>` query parameter, preferring the header when both are present.
// Use it ONLY for endpoints a browser EventSource connects to: EventSource cannot
// set an Authorization header, so the query carrier is the sole option. It is a
// deliberate, scoped exception to the header-only rule (RFC 6750 section 2.3) -
// keep it off the MCP endpoint, which every supported client reaches with a header.
func BearerGuardWithQueryToken(verify Verifier, next http.Handler) http.Handler {
	return guard(verify, presentedToken, next)
}

// guard is the shared 401-or-pass core; extract names the token carriers a given
// mount accepts (header-only, or header-plus-query).
func guard(verify Verifier, extract func(*http.Request) (string, bool), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := extract(r)
		if !ok {
			unauthorized(w)
			return
		}
		if !verify(presented) {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SingleTokenVerifier returns a [Verifier] that accepts exactly the one token
// yielded by expected. It compares the SHA-256 digests of the presented and
// expected tokens with subtle.ConstantTimeCompare: the digests are equal-length,
// so the comparison reveals neither the secret's bytes nor its length. (Hashing
// an attacker-controlled input is itself length-dependent, but that timing
// channel is independent of the secret.) A load error from expected fails closed.
// The ephemeral live and blob servers use this with their per-run token; the
// daemon uses a richer verifier (auth.VerifyBearer) instead.
func SingleTokenVerifier(expected func() (string, error)) Verifier {
	return func(presented string) bool {
		want, err := expected()
		if err != nil {
			return false
		}
		got := sha256.Sum256([]byte(presented))
		wantSum := sha256.Sum256([]byte(want))
		return subtle.ConstantTimeCompare(got[:], wantSum[:]) == 1
	}
}

// headerToken extracts the token from the Authorization header only.
func headerToken(r *http.Request) (string, bool) {
	return bearerToken(r.Header.Get("Authorization"))
}

// presentedToken extracts the token a request carries, preferring the
// `Authorization: Bearer` header and falling back to a `?token=` query
// parameter. It reports false when neither carrier supplies a non-empty token.
func presentedToken(r *http.Request) (string, bool) {
	if tok, ok := headerToken(r); ok {
		return tok, true
	}
	if tok := r.URL.Query().Get("token"); tok != "" {
		return tok, true
	}
	return "", false
}

// bearerToken extracts the credential from an Authorization header value. The
// scheme is matched case-insensitively per RFC 6750; the token itself is not.
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

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="magus"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
