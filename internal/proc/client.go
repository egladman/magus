package proc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/codec"
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

	ep, err := ParseEndpoint(raw)
	if err != nil {
		return 0, fmt.Errorf("proc: forward: invalid MAGUS_DAEMON_SOCKET: %w", err)
	}

	conn, err := ep.Dial(ctx)
	if err != nil {
		return 0, fmt.Errorf("proc: forward: dial %s: %w", ep, err)
	}
	defer func() { _ = conn.Close() }()

	cwd, _ := os.Getwd()
	req := RunRequest{Args: args, Version: version, Cwd: cwd, Root: root, Protocol: ProtocolV2}
	if err := writeFrame(conn, typeRun, req); err != nil {
		return 0, fmt.Errorf("proc: forward: write: %w", err)
	}

	typ, line, err := readFrame(conn)
	if err != nil {
		return 0, fmt.Errorf("proc: forward: read: %w", err)
	}
	if typ == typeError {
		var er ErrorReply
		if e := codec.Unmarshal(line, &er); e == nil && er.Message != "" {
			return 0, remapSkewError(er.Message)
		}
		return 0, fmt.Errorf("proc: forward: server error (undecodable)")
	}
	if typ != typeRunReply {
		return 0, fmt.Errorf("proc: forward: unexpected reply type %q", typ)
	}

	var reply RunReply
	if err := codec.Unmarshal(line, &reply); err != nil {
		return 0, fmt.Errorf("proc: forward: decode reply: %w", err)
	}
	return reply.ExitCode, nil // reply.Err is informational; callers observe failure via ExitCode
}

// QueryStatus dials the proc server at addr and returns a live pool snapshot.
// addr accepts a unix:// URL or a bare path.
func QueryStatus(ctx context.Context, addr string) (*StatusReply, error) {
	ep, err := ParseEndpoint(addr)
	if err != nil {
		return nil, fmt.Errorf("proc: query: invalid address: %w", err)
	}
	conn, err := ep.Dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("proc: query: dial %s: %w", ep, err)
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(statusQueryTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline) // always succeeds on unix sockets

	req := StatusRequest{Magic: StatusMagic, Protocol: ProtocolV2}
	if err := writeFrame(conn, typeStatus, req); err != nil {
		return nil, fmt.Errorf("proc: query: write: %w", err)
	}

	typ, line, err := readFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("proc: query: read: %w", err)
	}
	if typ == typeError {
		var er ErrorReply
		if e := codec.Unmarshal(line, &er); e == nil && er.Message != "" {
			return nil, fmt.Errorf("proc: query: server error: %s", er.Message)
		}
		return nil, fmt.Errorf("proc: query: server error (undecodable)")
	}
	if typ != typeStatusReply {
		return nil, fmt.Errorf("proc: query: unexpected reply type %q", typ)
	}

	var reply StatusReply
	if err := codec.Unmarshal(line, &reply); err != nil {
		return nil, fmt.Errorf("proc: query: decode reply: %w", err)
	}
	return &reply, nil
}

// Shutdown dials the proc server at addr and requests a graceful shutdown.
// addr accepts a unix:// URL or a bare path.
func Shutdown(ctx context.Context, addr string) error {
	ep, err := ParseEndpoint(addr)
	if err != nil {
		return fmt.Errorf("proc: shutdown: invalid address: %w", err)
	}
	conn, err := ep.Dial(ctx)
	if err != nil {
		return fmt.Errorf("proc: shutdown: dial %s: %w", ep, err)
	}
	defer func() { _ = conn.Close() }()

	req := ShutdownRequest{Magic: ShutdownMagic, Protocol: ProtocolV2}
	if err := writeFrame(conn, typeShutdown, req); err != nil {
		return fmt.Errorf("proc: shutdown: write: %w", err)
	}

	typ, line, err := readFrame(conn)
	if err != nil {
		return fmt.Errorf("proc: shutdown: read: %w", err)
	}
	if typ == typeError {
		var er ErrorReply
		if e := codec.Unmarshal(line, &er); e == nil && er.Message != "" {
			return fmt.Errorf("proc: shutdown: server error: %s", er.Message)
		}
		return fmt.Errorf("proc: shutdown: server error (undecodable)")
	}
	if typ != typeShutdownReply {
		return fmt.Errorf("proc: shutdown: unexpected reply type %q", typ)
	}
	return nil
}

// AcquireService asks the daemon at addr to start (or reuse) a shared service and
// keep it warm past this invocation, returning once it is ready. addr accepts a
// unix:// URL or a bare path.
func AcquireService(ctx context.Context, addr, key string, svc types.Service) error {
	ep, err := ParseEndpoint(addr)
	if err != nil {
		return fmt.Errorf("proc: service.acquire: invalid address: %w", err)
	}
	conn, err := ep.Dial(ctx)
	if err != nil {
		return fmt.Errorf("proc: service.acquire: dial %s: %w", ep, err)
	}
	defer func() { _ = conn.Close() }()

	req := ServiceAcquireRequest{Protocol: ProtocolV2, Key: key, Service: svc}
	if err := writeFrame(conn, typeServiceAcquire, req); err != nil {
		return fmt.Errorf("proc: service.acquire: write: %w", err)
	}
	typ, line, err := readFrame(conn)
	if err != nil {
		return fmt.Errorf("proc: service.acquire: read: %w", err)
	}
	if typ == typeError {
		var er ErrorReply
		if e := codec.Unmarshal(line, &er); e == nil && er.Message != "" {
			return fmt.Errorf("proc: service.acquire: server error: %s", er.Message)
		}
		return fmt.Errorf("proc: service.acquire: server error (undecodable)")
	}
	if typ != typeServiceAcquireReply {
		return fmt.Errorf("proc: service.acquire: unexpected reply type %q", typ)
	}
	var reply ServiceAcquireReply
	if err := codec.Unmarshal(line, &reply); err != nil {
		return fmt.Errorf("proc: service.acquire: decode reply: %w", err)
	}
	if reply.Err != "" {
		return fmt.Errorf("proc: service.acquire: %s", reply.Err)
	}
	return nil
}

// ReleaseService tells the daemon at addr that this invocation no longer needs the
// shared service for key; the daemon keeps it warm and reaps it later. addr accepts
// a unix:// URL or a bare path.
func ReleaseService(ctx context.Context, addr, key string) error {
	ep, err := ParseEndpoint(addr)
	if err != nil {
		return fmt.Errorf("proc: service.release: invalid address: %w", err)
	}
	conn, err := ep.Dial(ctx)
	if err != nil {
		return fmt.Errorf("proc: service.release: dial %s: %w", ep, err)
	}
	defer func() { _ = conn.Close() }()

	// Release is quick bookkeeping on the daemon (drop a ref, arm the idle timer), so
	// bound it: a wedged daemon must not block the run's teardown forever.
	deadline := time.Now().Add(statusQueryTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	req := ServiceReleaseRequest{Protocol: ProtocolV2, Key: key}
	if err := writeFrame(conn, typeServiceRelease, req); err != nil {
		return fmt.Errorf("proc: service.release: write: %w", err)
	}
	typ, line, err := readFrame(conn)
	if err != nil {
		return fmt.Errorf("proc: service.release: read: %w", err)
	}
	if typ == typeError {
		var er ErrorReply
		if e := codec.Unmarshal(line, &er); e == nil && er.Message != "" {
			return fmt.Errorf("proc: service.release: server error: %s", er.Message)
		}
		return fmt.Errorf("proc: service.release: server error (undecodable)")
	}
	if typ != typeServiceReleaseReply {
		return fmt.Errorf("proc: service.release: unexpected reply type %q", typ)
	}
	return nil
}

// StopAllServices asks the daemon at addr to stop every service it hosts (leaving the
// daemon running) and returns how many were stopped. addr accepts a unix:// URL or a
// bare path.
func StopAllServices(ctx context.Context, addr string) (int, error) {
	ep, err := ParseEndpoint(addr)
	if err != nil {
		return 0, fmt.Errorf("proc: service.stopall: invalid address: %w", err)
	}
	conn, err := ep.Dial(ctx)
	if err != nil {
		return 0, fmt.Errorf("proc: service.stopall: dial %s: %w", ep, err)
	}
	defer func() { _ = conn.Close() }()

	if err := writeFrame(conn, typeServiceStopAll, ServiceStopAllRequest{Protocol: ProtocolV2}); err != nil {
		return 0, fmt.Errorf("proc: service.stopall: write: %w", err)
	}
	typ, line, err := readFrame(conn)
	if err != nil {
		return 0, fmt.Errorf("proc: service.stopall: read: %w", err)
	}
	if typ == typeError {
		var er ErrorReply
		if e := codec.Unmarshal(line, &er); e == nil && er.Message != "" {
			return 0, fmt.Errorf("proc: service.stopall: server error: %s", er.Message)
		}
		return 0, fmt.Errorf("proc: service.stopall: server error (undecodable)")
	}
	if typ != typeServiceStopAllReply {
		return 0, fmt.Errorf("proc: service.stopall: unexpected reply type %q", typ)
	}
	var reply ServiceStopAllReply
	if err := codec.Unmarshal(line, &reply); err != nil {
		return 0, fmt.Errorf("proc: service.stopall: decode reply: %w", err)
	}
	return reply.Count, nil
}

// remapSkewError converts a server-side error string back to its typed sentinel to preserve errors.Is() behaviour.
func remapSkewError(msg string) error {
	switch msg {
	case ErrProtocolSkew.Error():
		return ErrProtocolSkew
	case ErrVersionSkew.Error():
		return ErrVersionSkew
	case ErrCycleDetected.Error():
		return ErrCycleDetected
	}
	if strings.HasPrefix(msg, ErrNotAdoptable.Error()+":") {
		return fmt.Errorf("%w%s", ErrNotAdoptable, strings.TrimPrefix(msg, ErrNotAdoptable.Error()))
	}
	return errors.New(msg)
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
