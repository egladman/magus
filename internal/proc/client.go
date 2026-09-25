package proc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// statusQueryTimeout caps the QueryStatus round-trip; prevents hung servers from blocking forever.
const statusQueryTimeout = 5 * time.Second

// Forward delegates args to the server [SocketEnv] names and returns the exit code.
// On any transport error callers should fall back to running locally.
// Pass "" for root when unknown; the server resolves it from Cwd.
func Forward(ctx context.Context, args []string, version, root string) (int, error) {
	addr := os.Getenv(SocketEnv)
	if addr == "" {
		return 0, fmt.Errorf("proc: forward: %s not set", SocketEnv)
	}

	cwd, _ := os.Getwd()
	// Send the adoption identity, not the raw display version: a dev build is fingerprinted
	// from its VCS stamp so it never matches a differently-built dev server (see
	// adoptionIdentity).
	// The ancestry travels with the request because the server, not this process, is what
	// takes the project locks for an adopted run: without it the server cannot tell a
	// lock held for THIS client's parent from one held for an unrelated client.
	// The lease travels for the same reason in the other direction: the server runs the
	// work, so it would otherwise attribute it to the server's environment (which is
	// nobody's lease) and the session journal would show adopted runs unattributed.
	// Read through trail.LeaseFromEnv rather than the raw variable so a malformed
	// value is dropped here, once, by the same rule every other channel applies.
	req := runRequest{
		Args: args, Version: adoptionIdentity(version), Cwd: cwd, Root: root,
		Ancestors: types.InvocationAncestorsFromContext(ctx),
		Lease:     trail.LeaseFromEnv(),
		Sandbox:   SandboxFloorFromContext(ctx),
	}
	reply, err := roundTrip[runReply](ctx, addr, runExchange, req)
	if err != nil {
		return 0, err
	}
	// An adopted run's failure reason has to be SAID. The adopted handler returns its error
	// to the server instead of printing it the way a local dispatch does, so this is the one
	// place it reaches the user. Emitted through slog so it renders exactly as the local
	// path's error does; the exit code is still what the caller returns on.
	if reply.ExitCode != 0 && reply.Err != "" {
		slog.ErrorContext(ctx, reply.Err)
	}
	return reply.ExitCode, nil
}

// QueryStatus asks the proc server at addr for a live pool snapshot.
// addr accepts a unix:// URL or a bare path.
func QueryStatus(ctx context.Context, addr string) (*StatusReply, error) {
	reply, err := roundTrip[StatusReply](ctx, addr, statusExchange, nil)
	if err != nil {
		return nil, err
	}
	return &reply, nil
}

// SubmitJob submits a fire-and-forget background job to the proc server at addr: the
// server runs `magus <args>` asynchronously and this returns as soon as it is accepted, with
// the job's invocation id (a Dashboard deep-link), or "" when an identical job already in
// flight absorbed it. It scopes the job to the caller's working directory (computed here,
// like Forward, so there is no transposition-prone dir argument); the server walks up from it
// to the workspace root. Used by the VCS refresh hook, which must not block a checkout. addr
// accepts a unix:// URL or a path. version is the caller's build version; it is sent as an
// adoption identity (see adoptionIdentity) so a background job is version-gated exactly like
// a forwarded run.
func SubmitJob(ctx context.Context, addr string, args []string, version string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("proc: job: getwd: %w", err)
	}
	req := jobRequest{Args: args, Version: adoptionIdentity(version), Cwd: cwd}
	reply, err := roundTrip[jobReply](ctx, addr, jobExchange, req)
	if err != nil {
		return "", err
	}
	return reply.Inv, nil
}

// Shutdown asks the proc server at addr to shut down gracefully. It returns once the server
// has accepted the request, not once it has exited.
// addr accepts a unix:// URL or a bare path.
func Shutdown(ctx context.Context, addr string) error {
	_, err := roundTrip[struct{}](ctx, addr, shutdownExchange, nil)
	return err
}

// RunChildSync yields the caller's concurrency slot for the duration of fn so a
// child magus process can acquire it, keeping the total budget flat. If lim is
// nil or no slot is held fn runs unchanged (avoids over-releasing the semaphore).
func RunChildSync(ctx context.Context, lim *cache.Limiter, fn func() error) error {
	if lim == nil || !cache.SlotHeld(ctx) {
		return fn()
	}
	return lim.Yield(ctx, fn)
}

// ReloadConfig asks the server to drop the workspaces it holds open, so the next command
// against each reopens it and re-reads its config. It reports how many were dropped and
// how many were left alone because a run was in flight.
//
// A partial reset that leaves the server running, for the case where editing
// magus.yaml would otherwise mean restarting it.
func ReloadConfig(ctx context.Context, addr string) (dropped, busy int, err error) {
	reply, err := roundTrip[configReloadReply](ctx, addr, configReloadExchange, nil)
	if err != nil {
		return 0, 0, err
	}
	return reply.Dropped, reply.Busy, nil
}
