package types

import (
	"context"
	"maps"
	"sync"
)

// returnCapture collects each project's target return value during one run.
type returnCapture struct {
	mu   sync.Mutex
	vals map[string]any
}

type returnCaptureKey struct{}

// WithReturnCapture installs a sink for target return values, keyed by project
// path, and returns a reader for what was collected.
//
// This mirrors [WithExitCapture], with one difference that matters: a run fans
// out across projects concurrently, so the sink is mutex-guarded and the reader
// hands back a copy. The exit capture needs neither, because it is scoped to a
// single target invocation on one goroutine.
func WithReturnCapture(ctx context.Context) (context.Context, func() map[string]any) {
	r := &returnCapture{vals: map[string]any{}}
	return context.WithValue(ctx, returnCaptureKey{}, r), func() map[string]any {
		r.mu.Lock()
		defer r.mu.Unlock()
		return maps.Clone(r.vals)
	}
}

// CaptureReturn records projectPath's target return value if a sink is present.
// It is a no-op outside a captured run.
//
// A nil value is dropped rather than stored: a `> void` target returns nothing,
// and recording it would make "returned null" indistinguishable from "returned
// nothing at all", which is the difference between a field being absent and
// being present-but-null in the rendered result.
func CaptureReturn(ctx context.Context, projectPath string, v any) {
	if v == nil {
		return
	}
	r, ok := ctx.Value(returnCaptureKey{}).(*returnCapture)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vals[projectPath] = v
}

// CapturedReturn reads back what projectPath recorded, for the cache to store
// alongside the entry it is snapshotting.
//
// The cache needs this because a HIT never invokes the target: without the value
// on the entry, a target would print its result on the first run and nothing on
// the second, which is worse than not returning values at all. Store on snapshot,
// re-capture on replay, and the two runs agree.
func CapturedReturn(ctx context.Context, projectPath string) (any, bool) {
	r, ok := ctx.Value(returnCaptureKey{}).(*returnCapture)
	if !ok {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.vals[projectPath]
	return v, ok
}
