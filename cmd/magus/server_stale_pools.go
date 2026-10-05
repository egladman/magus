package main

import (
	"context"
	"fmt"
	"os"

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
		fmt.Fprintln(os.Stderr, "magus: no stale pool parents")
		return nil
	}
	var failed int
	for _, p := range stale {
		if err := proc.Shutdown(ctx, p.Addr); err != nil {
			fmt.Fprintf(os.Stderr, "magus: stop pool parent pid %d (%s): %v\n", p.ParentPID, p.Version, err)
			failed++
			continue
		}
		if err := waitSocketGone(ctx, p.Addr, stopTimeout); err != nil {
			fmt.Fprintf(os.Stderr, "magus: stop pool parent pid %d (%s): %v\n", p.ParentPID, p.Version, err)
			failed++
			continue
		}
		fmt.Fprintf(os.Stderr, "magus: stopped pool parent (pid %d, %s)\n", p.ParentPID, p.Version)
	}
	if failed > 0 {
		return fmt.Errorf("server stop --pools: %d of %d parent(s) did not stop", failed, len(stale))
	}
	return nil
}
