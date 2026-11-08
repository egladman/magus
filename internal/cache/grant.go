package cache

import (
	"context"

	"github.com/egladman/magus/internal/proc/spawn"
)

// grantedClaim hands Run the admission RunAll took for the step, so a miss reports it.
// An option rather than a context value, because a nested step runs under its parent's
// context and would otherwise report the parent's claim as its own.
func grantedClaim(claim stepClaim) RunOption {
	return func(rc *runCtx) { rc.claim = claim }
}

// stepSlots is the limiter weight one step takes: Step.Slots clamped to [1, budget], so
// a heavy step can throttle the parallel work around itself.
//
// 0 means one slot, and a request above the budget would exceed capacity and error, so
// it caps. A request >= the budget holds every slot and no peer can enter fn while it
// runs; it takes no isolation lock, so unlike an exclusive step it does not also
// serialize replays. On an unlimited limiter (budget <= 0) there is nothing to throttle
// against and AcquireN returns immediately.
func stepSlots(s Step, lim *Limiter) int {
	slots := s.Slots
	if slots < 1 {
		slots = 1
	}
	if budget := lim.Capacity(); budget > 0 && slots > budget {
		slots = budget
	}
	return slots
}

// runSeated runs a step's body holding what admission seated it with: the limiter it
// draws from, its slots, and a jobserver that holds make, cargo and any other client of
// the GNU make protocol to those slots (spawn.SeatJobserver says when there is none).
//
// Inside Cache.Run's callback, so a replayed step opens no pipe, and the pool closes the
// moment the body returns: whatever tokens a child still holds die with it.
func runSeated(ctx context.Context, lim *Limiter, slots int, fn func(context.Context) error) error {
	ctx = ContextWithLimiter(ctx, lim)
	ctx = WithSlotsHeld(ctx, slots)
	ctx, closeJobserver, err := spawn.SeatJobserver(ctx, slots)
	if err != nil {
		return err
	}
	defer closeJobserver()
	return fn(ctx)
}
