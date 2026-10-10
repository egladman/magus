package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
)

// stopStalePools shuts down live per-process pool parents whose display version
// differs from this binary. Same Shutdown + waitSocketGone path as serverStop; the
// parents are leftover magus mcp / run processes, not the persistent server.
func stopStalePools(ctx context.Context) error {
	stale, err := proc.StalePools(ctx, version)
	if err != nil {
		return fmt.Errorf("server stop --pools: %w", err)
	}
	if len(stale) == 0 {
		slog.InfoContext(ctx, "no stale pool parents", attr.Notice(""), attr.Component("magus"))
		return nil
	}
	var failed int
	for _, p := range stale {
		if err := proc.Shutdown(ctx, p.Addr); err != nil {
			slog.ErrorContext(ctx, fmt.Sprintf("stop pool parent pid %d (%s): %v", p.ParentPID, p.Version, err), attr.Notice(""), attr.Component("magus"))
			failed++
			continue
		}
		if err := waitSocketGone(ctx, p.Addr, stopTimeout); err != nil {
			slog.ErrorContext(ctx, fmt.Sprintf("stop pool parent pid %d (%s): %v", p.ParentPID, p.Version, err), attr.Notice(""), attr.Component("magus"))
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
