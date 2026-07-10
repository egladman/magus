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

// BearerGuard rejects any request whose presented token fails verify. The token
// is accepted from EITHER an `Authorization: Bearer <token>` header (what a
// fetch() client sends) OR a `?token=<token>` query parameter (the fallback for
// a browser EventSource, which cannot set headers). Every loopback HTTP endpoint
// shares this one guard; only the [Verifier] differs.
//
// verify is called on every request, so a rotate, create, or revoke takes effect
// without restarting the server; it must fail closed (return false) on any error.
// Failures return 401 with a WWW-Authenticate challenge and a generic body that
// does not distinguish a missing token from a wrong one.
func BearerGuard(verify Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := presentedToken(r)
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

// presentedToken extracts the token a request carries, preferring the
// `Authorization: Bearer` header and falling back to a `?token=` query
// parameter. It reports false when neither carrier supplies a non-empty token.
func presentedToken(r *http.Request) (string, bool) {
	if tok, ok := bearerToken(r.Header.Get("Authorization")); ok {
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
