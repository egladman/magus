package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"os"
)

// VerifyBearer reports whether presented is an accepted daemon credential under
// the two-tier model: it matches the retrievable cli token (constant-time on the
// SHA-256 digests) OR a non-expired named connector token. Both tiers are
// re-read from disk on every call, so a rotate, create, or revoke takes effect
// without restarting the daemon. Each tier fails closed on a load error; when
// neither tier matches, VerifyBearer returns false. This is the verifier the
// daemon hands to httpx.BearerGuard.
func VerifyBearer(presented string) bool {
	if tok, err := Load(); err == nil {
		got := sha256.Sum256([]byte(presented))
		want := sha256.Sum256([]byte(tok))
		if subtle.ConstantTimeCompare(got[:], want[:]) == 1 {
			return true
		}
	}
	if store, err := LoadConnectorStore(); err == nil && store.Verify(presented) {
		return true
	}
	return false
}

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
