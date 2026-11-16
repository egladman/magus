package proc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/egladman/magus/internal/log/attr"
)

// StopTimeout bounds how long a stop waits to confirm a server or broker is gone after a
// shutdown request. In-flight builds drain first, so allow generous headroom before
// declaring the stop unverified.
const StopTimeout = 30 * time.Second

// WaitSocketGone polls addr until it stops answering or the timeout elapses. It is the
// verification half of a stop: a shutdown reply only means the request was accepted, so
// stop confirms the socket has actually gone quiet before reporting success.
func WaitSocketGone(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if !SocketLive(ctx, addr) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s still answers after %s", addr, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// StopStalePools shuts down live per-process pool parents whose display version
// differs from selfVersion. Same Shutdown + WaitSocketGone path as a server stop; the
// parents are leftover magus mcp / run processes, not the persistent server.
func StopStalePools(ctx context.Context, selfVersion string) error {
	stale, err := StalePools(ctx, selfVersion)
	if err != nil {
		return fmt.Errorf("server stop --pools: %w", err)
	}
	if len(stale) == 0 {
		slog.InfoContext(ctx, "no stale pool parents", attr.Notice(""), attr.Component("magus"))
		return nil
	}
	var failed int
	for _, p := range stale {
		if err := Shutdown(ctx, p.Addr); err != nil {
			slog.ErrorContext(ctx, fmt.Sprintf("stop pool parent pid %d (%s)", p.ParentPID, p.Version), attr.Notice(""), attr.Component("magus"), attr.Error(err))
			failed++
			continue
		}
		if err := WaitSocketGone(ctx, p.Addr, StopTimeout); err != nil {
			slog.ErrorContext(ctx, fmt.Sprintf("stop pool parent pid %d (%s)", p.ParentPID, p.Version), attr.Notice(""), attr.Component("magus"), attr.Error(err))
			failed++
			continue
		}
		slog.InfoContext(ctx, fmt.Sprintf("stopped pool parent (pid %d, %s)", p.ParentPID, p.Version), attr.Notice(""), attr.Component("magus"))
	}
	if failed > 0 {
		return fmt.Errorf("server stop --pools: %d of %d parent(s) did not stop", failed, len(stale))
	}
	return nil
}
