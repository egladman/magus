package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// Resolve loads the MCP bearer token, generating and persisting one on first
// use. The MCP server fails closed if Resolve returns an error — the endpoint
// never serves without a token.
//
// The secret is deliberately never logged: the daemon's logger commonly lands
// in journald/nohup.out, and a 256-bit shared secret must not persist there.
// On generation Resolve logs only a notice; the operator retrieves the value
// out-of-band via `magus config mcp token print`.
func Resolve(log *slog.Logger) (string, error) {
	tok, err := Load()
	if err == nil {
		return tok, nil
	}
	if !errors.Is(err, ErrNoToken) {
		return "", err
	}

	tok, err = Generate()
	if err != nil {
		return "", err
	}
	path, err := SaveNew(tok)
	if err != nil {
		// A concurrent writer (a racing CLI `generate`) won the create. Adopt
		// the token they persisted rather than clobbering it.
		if errors.Is(err, os.ErrExist) {
			return Load()
		}
		return "", err
	}
	log.Warn("[AGENT] generated a new MCP auth token; retrieve it with `magus config mcp token print`",
		slog.String("path", path),
	)
	return tok, nil
}

// Guard rejects any request that does not carry a valid
// `Authorization: Bearer <token>` header. The expected token is fetched via
// load on every request, so a rotation (`magus config mcp token generate
// --force`) or revoke takes effect without restarting the daemon; pass
// auth.Load. The check compares the SHA-256 digests of the presented and
// expected tokens with subtle.ConstantTimeCompare: the digests are equal-length,
// so the comparison reveals neither the secret's bytes nor its length. (Hashing
// an attacker-controlled input is itself length-dependent, but that timing
// channel is independent of the secret.) Any load error (including a revoked
// token) fails closed. Failures return 401 with a WWW-Authenticate challenge and
// a generic body that does not distinguish a missing token from a wrong one.
func Guard(load func() (string, error), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			unauthorized(w)
			return
		}
		expected, err := load()
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
	w.Header().Set("WWW-Authenticate", `Bearer realm="magus mcp"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
