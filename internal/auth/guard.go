package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"os"
)

// The daemon's credentials form a three-tier hierarchy, and the two verifiers
// below are how mounts pick their tier:
//
//	cli token       operator ("god") credential: everything, INCLUDING token
//	                management (mint/revoke). Verified by VerifyCLIBearer.
//	connector token MCP-client credential: the data surfaces (/mcp, the console
//	                read/control services) but NEVER token management - a client
//	                credential must not be able to mint or revoke credentials
//	                (privilege self-replication). Accepted by VerifyBearer only.
//	share token     read-only viewer credential: lives solely on the ephemeral
//	                LAN share listener (its own per-session verifier); neither
//	                verifier here ever accepts it.
//
// VerifyBearer reports whether presented is an accepted daemon DATA-surface
// credential: the retrievable cli token OR a non-expired named connector token.
// Both tiers are re-read from disk on every call, so a rotate, create, or revoke
// takes effect without restarting the daemon. Each tier fails closed on a load
// error; when neither tier matches, VerifyBearer returns false. This is the
// verifier the daemon hands to httpx.BearerGuard for /mcp, /api, and the console
// Connect services.
func VerifyBearer(presented string) bool {
	if VerifyCLIBearer(presented) {
		return true
	}
	if store, err := LoadConnectorStore(); err == nil && store.Verify(presented) {
		return true
	}
	return false
}

// VerifyCLIBearer reports whether presented is exactly the retrievable cli
// token - the OPERATOR tier and nothing else. Connector and share tokens never
// match here. It exists as its own narrow verifier so privileged mounts (token
// management) can be guarded at the guard level rather than trusting a handler
// to re-check the caller's class; VerifyBearer composes it for the ordinary
// data surfaces. The token is re-read from disk on every call (rotation takes
// effect immediately) and a load error fails closed.
func VerifyCLIBearer(presented string) bool {
	tok, err := Load()
	if err != nil {
		return false
	}
	got := sha256.Sum256([]byte(presented))
	want := sha256.Sum256([]byte(tok))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
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
