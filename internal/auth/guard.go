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
//	                never /mcp. Two tiers - ScopeConsole may change things,
//	                ScopeConsoleRead is a viewer and reaches only the read routes.
//	                Stored beside connector tokens and told apart by ClientScope,
//	                which the verifiers filter on: one store, no overlap.
//	share token     read-only viewer credential: lives solely on the ephemeral
//	                LAN share listener (its own per-session verifier); no verifier
//	                here ever accepts it.
//
// Why the split. Both surfaces used to share one verifier, so a connector token
// minted for an agent could drive the console's mutating routes, and a token pasted
// into a console URL was accepted at /mcp, meaning a leaked console link reached the
// whole agent tool surface. Neither is true now.

// CLICredential is the name every verifier reports for the cli token. The cli token is
// one unnamed file, so this names the credential, not whoever holds it.
const CLICredential = "cli"

// Every verifier below reports the NAME of the credential presented matched: the cli
// token as [CLICredential], a connector or console token as the name it was minted
// with. The name is what the trail records, so a viewer and the cli token are told
// apart; it proves possession of that credential, not who holds it.

// VerifyMCPBearer reports whether presented may use /mcp: the retrievable cli token
// OR a non-expired named connector token. Both tiers are re-read from disk on every
// call, so a rotate, create, or revoke takes effect without restarting the daemon,
// and each fails closed on a load error.
//
// A CONSOLE token is rejected here by construction (it is not consulted), so a
// credential handed to a browser cannot reach the agent tool surface.
func VerifyMCPBearer(presented string) (credential string, ok bool) {
	if name, ok := VerifyCLIBearer(presented); ok {
		return name, true
	}
	return verifyStored(presented, ScopeMCP)
}

// VerifyConsoleBearer reports whether presented may use the console surfaces: /api/
// and the console Connect services.
//
// It accepts the cli token or a non-expired token minted with ScopeConsole. A
// CONNECTOR token is rejected: it is scoped to /mcp, and the scan skips it.
//
// The cli token stays valid on both surfaces deliberately: it is the bootstrap
// credential and the CLI's own reads depend on it. That is a named exception, not a
// residue of the old single-tier design.
func VerifyConsoleBearer(presented string) (credential string, ok bool) {
	if name, ok := VerifyCLIBearer(presented); ok {
		return name, true
	}
	return verifyStored(presented, ScopeConsole)
}

// VerifyConsoleReadBearer guards the console's READ surface: the cli token, a full
// console token, or a viewer token (ScopeConsoleRead).
//
// A viewer token is accepted HERE and nowhere else, which is what makes it a viewer:
// the mutating console mounts (JobService, MemoryService, the share trigger) use
// VerifyConsoleBearer, which does not consult ScopeConsoleRead, so a leaked viewer
// credential can read the console and change nothing. A full console token is
// accepted too: the write tier is a superset of the read tier, not a sibling.
func VerifyConsoleReadBearer(presented string) (credential string, ok bool) {
	if name, ok := VerifyConsoleBearer(presented); ok {
		return name, true
	}
	return verifyStored(presented, ScopeConsoleRead)
}

// verifyStored matches presented against the named tokens of one scope, failing closed
// when the store will not load.
func verifyStored(presented string, scope ClientScope) (string, bool) {
	store, err := LoadConnectorStore()
	if err != nil {
		return "", false
	}
	return store.VerifyScope(presented, scope)
}

// VerifyCLIBearer reports whether presented is exactly the retrievable cli token and
// nothing else, naming it [CLICredential]. Connector and share tokens never match
// here. It exists as its own narrow verifier so privileged mounts (token management)
// can be guarded at the guard level rather than trusting a handler to re-check the
// caller's class; both surface verifiers compose it as their bootstrap tier. The
// token is re-read from disk on every call (rotation takes effect immediately) and a
// load error fails closed.
func VerifyCLIBearer(presented string) (credential string, ok bool) {
	tok, err := Load()
	if err != nil {
		return "", false
	}
	got := sha256.Sum256([]byte(presented))
	want := sha256.Sum256([]byte(tok))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		return "", false
	}
	return CLICredential, true
}

// Resolve loads the MCP bearer token, generating and persisting one on first
// use. The MCP server fails closed if Resolve returns an error — the endpoint
// never serves without a token.
//
// The secret is deliberately never logged: the daemon's logger commonly lands
// in journald/nohup.out, and a 256-bit shared secret must not persist there.
// On generation Resolve logs only a notice; the operator retrieves the value
// out-of-band via `magus config token print`.
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
	log.WarnContext(ctx, "[AGENT] generated a new MCP auth token; retrieve it with `magus config token print`",
		slog.String("path", path),
	)
	return tok, nil
}
