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
// The aggregate is a MAXIMUM, never a sum. A target that forks a compiler, then
// a linker, then a test binary reached its high-water mark in whichever one
// peaked; adding them together would describe a machine that never existed. It
// is the peak that decides whether two targets fit on one runner.

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
