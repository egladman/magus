package cache

import "context"

type (
	limiterKey  struct{}
	cacheKey    struct{}
	slotHeldKey struct{}
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

// ContextWithCache stores c in ctx for magusfile bindings (e.g. magus.bust_cache).
func ContextWithCache(ctx context.Context, c *Cache) context.Context {
	return context.WithValue(ctx, cacheKey{}, c)
}

// CacheFromContext retrieves the Cache stored by ContextWithCache, or nil.
func CacheFromContext(ctx context.Context) *Cache {
	v, _ := ctx.Value(cacheKey{}).(*Cache)
	return v
}
