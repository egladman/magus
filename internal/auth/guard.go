package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"os"
)

// The daemon's credentials form a tiered hierarchy, and the verifiers below are how
// mounts pick their tier. There is deliberately NO verifier that spans /mcp and the
// console: that is the separation this file exists to enforce.
//
//	cli token       operator ("god") credential: everything, INCLUDING token
//	                management (mint/revoke). Verified by VerifyCLIBearer, and
//	                composed into both surface verifiers as the bootstrap tier.
//	connector token MCP-client credential: /mcp and NOTHING ELSE. Never token
//	                management - a client credential must not be able to mint or
//	                revoke credentials (privilege self-replication) - and, since
//	                this split, never the console either.
//	console token   the PWA's credential: the console read/control surfaces and
//	                never /mcp. Stored beside connector tokens and told apart by
//	                ClientScope, which the verifiers filter on - one store, two
//	                surfaces, no overlap.
//	share token     read-only viewer credential: lives solely on the ephemeral
//	                LAN share listener (its own per-session verifier); no verifier
//	                here ever accepts it.
//
// Why the split. Both surfaces used to share one verifier, so a connector token
// minted for an agent could drive the console's mutating routes, and a token pasted
// into a console URL was accepted at /mcp - meaning a leaked console link reached the
// whole agent tool surface. Neither is true now.

// VerifyMCPBearer reports whether presented may use /mcp: the retrievable cli token
// OR a non-expired named connector token. Both tiers are re-read from disk on every
// call, so a rotate, create, or revoke takes effect without restarting the daemon,
// and each fails closed on a load error.
//
// A CONSOLE token is rejected here by construction - it is not consulted - so a
// credential handed to a browser cannot reach the agent tool surface.
func VerifyMCPBearer(presented string) bool {
	if VerifyCLIBearer(presented) {
		return true
	}
	if store, err := LoadConnectorStore(); err == nil && store.VerifyScope(presented, ScopeMCP) {
		return true
	}
	return false
}

// VerifyConsoleBearer reports whether presented may use the console surfaces: /api/
// and the console Connect services.
//
// It accepts the operator tier or a non-expired token minted with ScopeConsole. A
// CONNECTOR token is rejected: it is scoped to /mcp, and the scan skips it.
//
// The operator token stays valid on both surfaces deliberately: it is the bootstrap
// credential and the CLI's own reads depend on it. That is a named exception, not a
// residue of the old single-tier design.
func VerifyConsoleBearer(presented string) bool {
	if VerifyCLIBearer(presented) {
		return true
	}
	if store, err := LoadConnectorStore(); err == nil && store.VerifyScope(presented, ScopeConsole) {
		return true
	}
	return false
}

// VerifyCLIBearer reports whether presented is exactly the retrievable cli
// token - the OPERATOR tier and nothing else. Connector and share tokens never
// match here. It exists as its own narrow verifier so privileged mounts (token
// management) can be guarded at the guard level rather than trusting a handler
// to re-check the caller's class; both surface verifiers compose it as their
// bootstrap tier. The token is re-read from disk on every call (rotation takes
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
func Resolve(ctx context.Context, log *slog.Logger) (string, error) {
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
	log.WarnContext(ctx, "[AGENT] generated a new MCP auth token; retrieve it with `magus config mcp token print`",
		slog.String("path", path),
	)
	return tok, nil
}
