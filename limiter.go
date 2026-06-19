package magus

import (
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/workspace"
)

// Limiter is a weighted semaphore that caps concurrent spell executions.
// Obtain one with [NewLimiter] and share it across daemon workspaces via [WithLimiter].
type Limiter struct{ lim *cache.Limiter }

// NewLimiter creates a Limiter with capacity n. n ≤ 0 defaults to
// [DefaultConcurrency].
func NewLimiter(n int) *Limiter {
	if n <= 0 {
		n = cache.DefaultConcurrency()
	}
	return &Limiter{lim: cache.NewLimiter(n)}
}

// DefaultConcurrency returns the concurrency cap used when no explicit cap is set,
// resolved by precedence: the MAGUS_CONCURRENCY env var if set to a positive int,
// then 4 on GitHub-hosted runners (GITHUB_ACTIONS=true and RUNNER_ENVIRONMENT is not
// self-hosted), then min(NumCPU, 8).
func DefaultConcurrency() int { return cache.DefaultConcurrency() }

// Capacity returns the configured concurrency cap.
func (l *Limiter) Capacity() int { return l.lim.Capacity() }

// WithLimiter injects a pre-built Limiter (e.g. shared across daemon workspaces).
// When omitted, Open constructs a private limiter from magus.yaml/Concurrency.
func WithLimiter(l *Limiter) Option {
	return func(o *workspace.Load) { o.Limiter = l.lim }
}
