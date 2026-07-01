package buzz

import (
	"context"
	"time"
)

// TargetObserver is notified as the pool runs targets. The pool calls it once per
// target per run (the target memo collapses repeat dependents into a single run), so
// an observer sees each target exactly once with its wall-clock duration and outcome.
//
// It is optional: attach one with WithObserver. With none set the pool runs unchanged,
// so this adds no cost and no behaviour change to callers that do not opt in.
type TargetObserver interface {
	// TargetEnd reports a finished target: its name, how long its function ran, and the
	// error it returned (nil on success). Called after the target function returns.
	TargetEnd(ctx context.Context, name string, elapsed time.Duration, err error)
}

type observerKey struct{}

// WithObserver returns ctx carrying obs, which Pool.execute notifies for each target.
func WithObserver(ctx context.Context, obs TargetObserver) context.Context {
	return context.WithValue(ctx, observerKey{}, obs)
}

// observerFrom returns the observer attached to ctx, or nil.
func observerFrom(ctx context.Context) TargetObserver {
	obs, _ := ctx.Value(observerKey{}).(TargetObserver)
	return obs
}
