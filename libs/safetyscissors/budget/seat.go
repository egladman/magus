package budget

import "context"

// seat is everything a step holds while it runs: its limiter slots, whether it holds a
// machine-wide claim, and its record in the slot watch. One context value, so a site
// dispatching child work states the whole hold in one place.
//
// Independent markers produced two bugs on 2026-09-08, each a child context carrying the
// wrong SUBSET of the parent's hold. A child took a second machine claim the parent's
// figure already covered, then queued forever behind the parent blocked waiting for it.
//
// The zero value holds nothing, which is the right reading for work dispatched outside a
// step: an unmarked context has taken no seat anywhere.
type seat struct {
	// slots is how many limiter slots the step holds. A hand-back site must release
	// exactly this many: a weighted step holds more than one, and releasing a single slot
	// leaves it pinning the rest, then blocking to re-reserve them.
	slots int
	// machineClaim says a claim on the machine budget is held, so anything admitted
	// beneath this step takes none of its own.
	machineClaim bool
	// hold is this step's record in the limiter's slot watch, nil outside an admitted
	// step. It is what a blocking wait marks itself on, so a slot held by a step that
	// cannot proceed is distinguishable from one doing work.
	hold *slotHold
}

type seatKey struct{}

func seatFromContext(ctx context.Context) seat {
	held, _ := ctx.Value(seatKey{}).(seat)
	return held
}

// on returns ctx carrying s, replacing whatever hold it described before.
func (s seat) on(ctx context.Context) context.Context {
	return context.WithValue(ctx, seatKey{}, s)
}

// WithSlotsHeld marks ctx as holding n limiter slots, keeping the rest of its seat.
func WithSlotsHeld(ctx context.Context, n int) context.Context {
	held := seatFromContext(ctx)
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
	return seatFromContext(ctx).slots
}

// SlotHeld reports whether ctx is marked as holding at least one limiter slot.
func SlotHeld(ctx context.Context) bool {
	return SlotsHeld(ctx) > 0
}

// WithMachineClaim marks ctx as holding, or no longer holding, a claim on the machine
// budget, keeping the rest of its seat.
func WithMachineClaim(ctx context.Context, held bool) context.Context {
	s := seatFromContext(ctx)
	s.machineClaim = held
	return s.on(ctx)
}

// MachineClaimHeld reports whether ctx is marked as holding a machine claim, so work
// beneath it rides that claim instead of taking its own.
func MachineClaimHeld(ctx context.Context) bool {
	return seatFromContext(ctx).machineClaim
}

// withSlotHold returns ctx carrying the step's slot-watch record.
func withSlotHold(ctx context.Context, h *slotHold) context.Context {
	held := seatFromContext(ctx)
	held.hold = h
	return held.on(ctx)
}
