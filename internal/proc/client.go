package proc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// statusQueryTimeout caps the QueryStatus round-trip; prevents hung daemons from blocking forever.
const statusQueryTimeout = 5 * time.Second

// Forward dials MAGUS_DAEMON_SOCKET, delegates args, and returns the exit code.
// On any transport error callers should fall back to running locally.
// Pass "" for root when unknown; the daemon resolves it from Cwd.
func Forward(ctx context.Context, args []string, version, root string) (int, error) {
	raw := os.Getenv("MAGUS_DAEMON_SOCKET")
	if raw == "" {
		return 0, fmt.Errorf("proc: forward: MAGUS_DAEMON_SOCKET not set")
	}

	ep, err := endpoint.Parse(raw)
	if err != nil {
		return 0, fmt.Errorf("proc: forward: invalid MAGUS_DAEMON_SOCKET: %w", err)
	}

	conn, err := ep.Dial(ctx)
	if err != nil {
		return 0, fmt.Errorf("proc: forward: dial %s: %w", ep, err)
	}
	defer func() { _ = conn.Close() }()

	cwd, _ := os.Getwd()
	// Send the adoption identity, not the raw display version: a dev build is fingerprinted
	// from its VCS stamp so it never matches a differently-built dev daemon (see
	// adoptionIdentity). A stale pre-fix daemon compares this against its stored "unknown"
	// and mismatches, so the fix fails closed against old daemons too.
	// The ancestry travels with the request because the daemon, not this process, is what
	// takes the project locks for an adopted run: without it the daemon cannot tell a
	// lock held for THIS client's parent from one held for an unrelated client.
	// The lease travels for the same reason in the other direction: the daemon runs the
	// work, so it would otherwise attribute it to the daemon's environment (which is
	// nobody's lease) and the session journal would show adopted runs unattributed.
	// Read through trail.LeaseFromEnv rather than the raw variable so a malformed
	// value is dropped here, once, by the same rule every other channel applies.
	req := runRequest{
		Args: args, Version: adoptionIdentity(version), Cwd: cwd, Root: root, Protocol: protocolV2,
		Ancestors: types.InvocationAncestorsFromContext(ctx),
		Lease:     trail.LeaseFromEnv(),
	}
	if err := writeFrame(conn, typeRun, req); err != nil {
		return 0, fmt.Errorf("proc: forward: write: %w", err)
	}

	typ, line, err := readFrameCtx(ctx, conn)
	if err != nil {
		return 0, fmt.Errorf("proc: forward: read: %w", err)
	}
	if typ == typeError {
		var er errorReply
		if e := json.Unmarshal(line, &er); e == nil && er.Message != "" {
			return 0, decodeWireError(er.Message)
		}
		return 0, fmt.Errorf("proc: forward: server error (undecodable)")
	}
	if typ != typeRunReply {
		return 0, fmt.Errorf("proc: forward: unexpected reply type %q", typ)
	}

	var reply runReply
	if err := json.Unmarshal(line, &reply); err != nil {
		return 0, fmt.Errorf("proc: forward: decode reply: %w", err)
	}
	// An adopted run's failure reason has to be SAID. The server hands it back in
	// reply.Err and this used to drop it, on the reasoning that the exit code is what
	// callers act on, which is true, and left the user with a bare status and no reason,
	// because the adopted handler returns its error to the server instead of printing it
	// the way a local dispatch does. Emitted through slog so it renders exactly as the
	// local path's error does; the exit code is still what the caller returns on.
	if reply.ExitCode != 0 && reply.Err != "" {
		slog.ErrorContext(ctx, reply.Err)
	}
	return reply.ExitCode, nil
}

// QueryStatus dials the proc server at addr and returns a live pool snapshot.
// addr accepts a unix:// URL or a bare path.
func QueryStatus(ctx context.Context, addr string) (*StatusReply, error) {
	req := statusRequest{Magic: statusMagic, Protocol: protocolV2}
	reply, err := roundTrip[StatusReply](ctx, addr, statusExchange, req)
	if err != nil {
		return nil, err
	}
	return &reply, nil
}

// SubmitJob dials the proc server at addr and submits a fire-and-forget background job:
// the daemon runs `magus <args>` asynchronously and this returns as soon as it is
// accepted, with the job's invocation id (a Dashboard deep-link). It scopes the job to
// the caller's working directory (computed here, like Forward, so there is no
// transposition-prone dir argument); the daemon walks up from it to the workspace root.
// Used by the VCS refresh hook, which must not block a checkout. addr accepts a unix://
// URL or a path. version is the caller's build version; it is sent as an adoption identity
// (see adoptionIdentity) so a background job is version-gated exactly like a forwarded run
// - a stale dev daemon will not silently run a fresh client's job with the wrong code.
func SubmitJob(ctx context.Context, addr string, args []string, version string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("proc: job: getwd: %w", err)
	}
	req := jobRequest{Magic: jobMagic, Args: args, Version: adoptionIdentity(version), Protocol: protocolV2, Cwd: cwd}
	reply, err := roundTrip[jobReply](ctx, addr, jobExchange, req)
	if err != nil {
		return "", err
	}
	if reply.Err != "" {
		return "", fmt.Errorf("proc: job: %s", reply.Err)
	}
	return reply.Inv, nil
}

// Shutdown dials the proc server at addr and requests a graceful shutdown.
// addr accepts a unix:// URL or a bare path.
func Shutdown(ctx context.Context, addr string) error {
	req := shutdownRequest{Magic: shutdownMagic, Protocol: protocolV2}
	_, err := roundTrip[shutdownReply](ctx, addr, shutdownExchange, req)
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
	reply, err := roundTrip[configReloadReply](ctx, addr, configReloadExchange, configReloadRequest{Protocol: protocolV2})
	if err != nil {
		return 0, 0, err
	}
	return reply.Dropped, reply.Busy, nil
}
