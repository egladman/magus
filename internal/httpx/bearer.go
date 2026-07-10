package httpx

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// BearerGuard rejects any request that does not present a valid per-endpoint
// token. The token is accepted from EITHER an `Authorization: Bearer <token>`
// header (what a fetch() client sends) OR a `?token=<token>` query parameter
// (the fallback for a browser EventSource, which cannot set headers). Every
// loopback HTTP endpoint shares this one guard; only the token SOURCE differs:
// the daemon reads a persistent token file (pass auth.Load) while the ephemeral
// live and blob servers mint a per-run random token.
//
// The expected token is fetched via token on every request, so a rotation or
// revoke takes effect without restarting the server. The check compares the
// SHA-256 digests of the presented and expected tokens with
// subtle.ConstantTimeCompare: the digests are equal-length, so the comparison
// reveals neither the secret's bytes nor its length. (Hashing an
// attacker-controlled input is itself length-dependent, but that timing channel
// is independent of the secret.) Any load error (including a revoked token)
// fails closed. Failures return 401 with a WWW-Authenticate challenge and a
// generic body that does not distinguish a missing token from a wrong one.
func BearerGuard(token func() (string, error), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := presentedToken(r)
		if !ok {
			unauthorized(w)
			return
		}
		expected, err := token()
		if err != nil {
			unauthorized(w)
			return
		}
		got := sha256.Sum256([]byte(presented))
		want := sha256.Sum256([]byte(expected))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
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
