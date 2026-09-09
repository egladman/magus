package cache

import "context"

type (
	limiterKey  struct{}
	cacheKey    struct{}
	progressKey struct{}
)

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
