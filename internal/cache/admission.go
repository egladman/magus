package cache

import "context"

// admission is everything a step holds while it runs: its limiter slots, its
// machine-wide claim, and its run-isolation lease. It travels as ONE context value so a
// site dispatching child work states the whole hold in one place.
//
// Three independent markers is the shape that produced two bugs on 2026-09-08, both a
// child context carrying the wrong SUBSET of what its parent held. A needs child took a
// second machine claim the parent's figure already covered and then queued forever
// behind the parent that was blocked waiting for it; an exclusive step's fan-out dropped
// its write lock and stopped excluding anything. Neither site looked wrong, because
// nothing named what a step was supposed to be carrying.
//
// The zero value holds nothing, which is the right reading for work dispatched outside
// a step: an unmarked context has taken no seat anywhere.
type admission struct {
	// slots is how many limiter slots the step holds. A hand-back site (Yield,
	// os.with_slots, archive.*) must release exactly this many so it gives back its
	// whole hold, not one slot: a weighted step holds more than one, and releasing only
	// one would leave it pinning slots it then blocks trying to re-reserve.
	slots int
	// machineClaim says a claim on the machine budget is held, so anything admitted
	// beneath this step takes none of its own.
	machineClaim bool
	// isolation is the run-isolation lease, nil outside an admitted step.
	isolation *runIsolationLease
}

type admissionKey struct{}

func admissionFrom(ctx context.Context) admission {
	held, _ := ctx.Value(admissionKey{}).(admission)
	return held
}

// on returns ctx carrying a, replacing whatever hold it described before.
func (a admission) on(ctx context.Context) context.Context {
	return context.WithValue(ctx, admissionKey{}, a)
}

// WithSlotsHeld marks ctx as holding n limiter slots.
func WithSlotsHeld(ctx context.Context, n int) context.Context {
	held := admissionFrom(ctx)
	held.slots = n
	return held.on(ctx)
}

// WithSlotHeld marks ctx as holding a single limiter slot.
func WithSlotHeld(ctx context.Context) context.Context {
	return WithSlotsHeld(ctx, 1)
}

// WithoutSlotHeld clears the slot-held marker for child work dispatched without a slot.
func WithoutSlotHeld(ctx context.Context) context.Context {
	return WithSlotsHeld(ctx, 0)
}

// SlotsHeld reports how many limiter slots ctx is marked as holding (0 if none).
func SlotsHeld(ctx context.Context) int {
	return admissionFrom(ctx).slots
}

// SlotHeld reports whether ctx is marked as holding at least one limiter slot.
func SlotHeld(ctx context.Context) bool {
	return SlotsHeld(ctx) > 0
}
