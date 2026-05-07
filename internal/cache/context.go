package cache

import "context"

type (
	limiterKey  struct{}
	cacheKey    struct{}
	slotHeldKey struct{}
)

// WithSlotHeld marks ctx as holding a limiter slot. Yield checks this to avoid
// over-releasing when a slotless child goroutine calls back into dispatch.
func WithSlotHeld(ctx context.Context) context.Context {
	return context.WithValue(ctx, slotHeldKey{}, true)
}

// WithoutSlotHeld clears the slot-held marker for child work dispatched without a slot.
func WithoutSlotHeld(ctx context.Context) context.Context {
	return context.WithValue(ctx, slotHeldKey{}, false)
}

// SlotHeld reports whether ctx is marked as holding a limiter slot.
func SlotHeld(ctx context.Context) bool {
	v, _ := ctx.Value(slotHeldKey{}).(bool)
	return v
}

// ContextWithLimiter stores lim in ctx for nested callers (e.g. magus.dispatch) to yield their slot.
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
