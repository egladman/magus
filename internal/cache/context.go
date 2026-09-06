package cache

import "context"

type (
	limiterKey  struct{}
	cacheKey    struct{}
	slotHeldKey struct{}
	progressKey struct{}
)

// WithSlotsHeld marks ctx as holding n limiter slots. A hand-back site (Yield,
// os.with_slots, archive.*) must release exactly n so it gives back its whole
// hold, not one slot: a weighted step holds more than one, and releasing only
// one would leave it pinning slots it then blocks trying to re-reserve.
func WithSlotsHeld(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, slotHeldKey{}, n)
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
	n, _ := ctx.Value(slotHeldKey{}).(int)
	return n
}

// SlotHeld reports whether ctx is marked as holding at least one limiter slot.
func SlotHeld(ctx context.Context) bool {
	return SlotsHeld(ctx) > 0
}

// ContextWithLimiter stores lim in ctx for nested callers (e.g. magus.needs) to yield their slot.
func ContextWithLimiter(ctx context.Context, lim *Limiter) context.Context {
	return context.WithValue(ctx, limiterKey{}, lim)
}

// LimiterFromContext retrieves the Limiter stored by ContextWithLimiter, or nil.
func LimiterFromContext(ctx context.Context) *Limiter {
	v, _ := ctx.Value(limiterKey{}).(*Limiter)
	return v
}

// ContextWithProgress installs p so the accounting edges can beat it without the
// heartbeat being threaded through every signature.
func ContextWithProgress(ctx context.Context, p *Progress) context.Context {
	return context.WithValue(ctx, progressKey{}, p)
}

// ProgressFromContext retrieves the heartbeat stored by ContextWithProgress, or nil.
// Every [Progress] method is nil-safe, so a caller need not check.
func ProgressFromContext(ctx context.Context) *Progress {
	v, _ := ctx.Value(progressKey{}).(*Progress)
	return v
}

// NewContext stores c in ctx for magusfile bindings (e.g. magus.bust_cache).
func NewContext(ctx context.Context, c *Cache) context.Context {
	return context.WithValue(ctx, cacheKey{}, c)
}

// FromContext retrieves the Cache stored by NewContext, or nil.
func FromContext(ctx context.Context) *Cache {
	v, _ := ctx.Value(cacheKey{}).(*Cache)
	return v
}
