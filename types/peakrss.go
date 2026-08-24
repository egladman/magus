package types

import (
	"context"
	"sync"
)

// Peak resident memory, collected per target execution.
//
// The exec layer already knows this number - the kernel counted it, and
// run.ExecResult.MaxRSSBytes reads it off the same ProcessState the exit code
// comes from - but the code that DECIDES what may run alongside what is the
// shard planner, several layers up, and nothing carried the number between
// them. This is that carrier, and it is deliberately the same shape as the
// return capture above it in this package: a context-scoped collector written
// by code far below the orchestrator and read once by the orchestrator.
//
// The aggregate ACROSS EXECS is a maximum, never a sum: a target that forks a
// compiler, then a linker, then a test binary reached its high-water mark in
// whichever one peaked, and adding those would describe a machine that never
// existed.
//
// What each exec contributes is a different question, and one this package used to
// get wrong. It is not one process: `go test` runs package binaries concurrently
// (-p defaults to GOMAXPROCS), each under -race carrying the race detector's
// shadow memory, and the kernel will not total them. wait4's ru_maxrss propagates
// up a reaped subtree as a MAXIMUM, so a driver that forked four concurrent 800MB
// children reports 801MB, measured on darwin. One instant of a real
// `go test -race ./internal/...` held 3.11GiB across 17 processes while the
// largest single one was 1.28GiB.
//
// run.ExecResult.MaxRSSBytes therefore folds the kernel's figure together with a
// sampled total of the live process tree, and reports the larger. A figure here is
// still a FLOOR, because sampling only sees the instants it looks at, but it no
// longer misses concurrency by construction.

type peakRSSKey struct{}

type peakRSS struct {
	mu  sync.Mutex
	max int64
}

// WithPeakRSS returns a context that collects the peak resident memory of every
// process executed under it. Install it once per unit of work you want a figure
// for; nested installs are independent, so an inner one does not feed its outer.
func WithPeakRSS(ctx context.Context) context.Context {
	return context.WithValue(ctx, peakRSSKey{}, &peakRSS{})
}

// RecordPeakRSS reports one process's peak resident memory in bytes. Calls with
// a non-positive value are ignored: the platforms that cannot report this
// (windows, wasm) and a process that never started both yield zero, and zero
// means UNKNOWN rather than "used nothing" - a planner that averaged it in
// would read an unmeasurable target as a free one.
func RecordPeakRSS(ctx context.Context, bytes int64) {
	if bytes <= 0 {
		return
	}
	c, ok := ctx.Value(peakRSSKey{}).(*peakRSS)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if bytes > c.max {
		c.max = bytes
	}
}

// PeakRSS returns the highest peak reported under ctx, or 0 when nothing was
// collected. RecordPeakRSS drops non-positive figures, so 0 already means "no
// process reported one" and a second return value would distinguish nothing.
func PeakRSS(ctx context.Context) int64 {
	c, ok := ctx.Value(peakRSSKey{}).(*peakRSS)
	if !ok {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max
}
