package cache

import "context"

// admission is everything a step holds while it runs: its limiter slots, its
// machine-wide claim, and its run-isolation lease. One context value, so a site
// dispatching child work states the whole hold in one place.
//
// Three independent markers produced two bugs on 2026-09-08, each a child context
// carrying the wrong SUBSET of the parent's hold. A needs child took a second machine
// claim the parent's figure already covered, then queued forever behind the parent
// blocked waiting for it. An exclusive step's fan-out gave its isolation lease back and
// excluded nothing.
//
// The zero value holds nothing, which is the right reading for work dispatched outside
// a step: an unmarked context has taken no seat anywhere.
type admission struct {
	// slots is how many limiter slots the step holds. A hand-back site (Yield,
	// os.with_slots, archive.*) must release exactly this many: a weighted step holds
	// more than one, and releasing a single slot leaves it pinning the rest, then
	// blocking to re-reserve them.
	slots int
	// machineClaim says a claim on the machine budget is held, so anything admitted
	// beneath this step takes none of its own.
	machineClaim bool
	// isolation is the run-isolation lease, nil outside an admitted step.
	isolation *runIsolationLease
	// hold is this step's record in the limiter's slot watch, nil outside an admitted
	// step. It is what a blocking wait marks itself on, so a slot held by a step that
	// cannot proceed is distinguishable from one doing work.
	hold *slotHold
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

// withSlotHold returns ctx carrying the step's slot-watch record.
func withSlotHold(ctx context.Context, h *slotHold) context.Context {
	held := admissionFrom(ctx)
	held.hold = h
	return held.on(ctx)
}
