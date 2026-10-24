//go:build !linux && !darwin

package pipe

import (
	"context"
	"time"
)

// pollEvery paces the wait where no process-exit event is wired up.
const pollEvery = 100 * time.Millisecond

// awaitExit has no process-exit event to block on here, so it waits one pollEvery and the
// caller checks its stages again. pipepeer proves no upstream on these platforms, so no
// run reaches it today.
func awaitExit(ctx context.Context, _ []int) error { return awaitTick(ctx) }

// awaitTick is procwait_linux.go's, without its notice: here the tick is the only wait.
func awaitTick(ctx context.Context) error {
	t := time.NewTimer(pollEvery)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
