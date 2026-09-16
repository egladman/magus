package types

import (
	"context"
	"sync/atomic"
	"time"
)

type dependencyWaitKey struct{}

// TrackDependencyWait installs a fresh accumulator for the time a target body spends
// inside ctx.needs. Every body gets one, whether or not it declares a ceiling.
//
// Fresh per body, not inherited: a composed target's own dependency time belongs to ITS
// body, and the parent already counts the whole child (queueing, work and all) as one
// span of its own dependency time. Sharing one accumulator would double it, so the
// install must not be conditional on the body declaring a ceiling.
func TrackDependencyWait(ctx context.Context) context.Context {
	return context.WithValue(ctx, dependencyWaitKey{}, new(atomic.Int64))
}

// AddDependencyWait records d against the nearest enclosing body, and is a no-op outside
// one. Safe for concurrent callers.
func AddDependencyWait(ctx context.Context, d time.Duration) {
	if n, ok := ctx.Value(dependencyWaitKey{}).(*atomic.Int64); ok {
		n.Add(int64(d))
	}
}

// DependencyWait is how long this body has spent waiting on the targets it composes:
// dispatching them, queueing for their admission, and running them. Zero for a leaf
// target and for a body that has not reached a ctx.needs yet.
func DependencyWait(ctx context.Context) time.Duration {
	n, ok := ctx.Value(dependencyWaitKey{}).(*atomic.Int64)
	if !ok {
		return 0
	}
	return time.Duration(n.Load())
}
