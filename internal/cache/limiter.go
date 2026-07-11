package cache

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"
)

// Limiter is a FIFO-fair weighted semaphore that bounds concurrent work and
// exposes live occupancy metrics for the pool inspector.
type Limiter struct {
	sem     *semaphore.Weighted
	cap     int
	inUse   atomic.Int64
	waiting atomic.Int64

	onAcquire atomic.Pointer[func(waitNs int64, n int)]
	onRelease atomic.Pointer[func(n int)]
	onWait    atomic.Pointer[func(delta int)]
}

// SetHooks installs optional callbacks fired on every Acquire/Release. Must not block.
// Stored atomically so a SetHooks racing with concurrent Acquire/Release is safe.
//
// onWait mirrors the internal waiting counter exactly: it fires with +n the instant a caller
// begins waiting for n slots (before the blocking Acquire) and with -n once that Acquire
// returns, whether it acquired or the context was cancelled. Net inflight of onWait tracks
// [Limiter.Snapshot]'s Waiting.
func (l *Limiter) SetHooks(onAcquire func(waitNs int64, n int), onRelease func(n int), onWait func(delta int)) {
	l.onAcquire.Store(&onAcquire)
	l.onRelease.Store(&onRelease)
	l.onWait.Store(&onWait)
}

// NewLimiter returns a Limiter with capacity n. n <= 0 means unlimited
// (Acquire and AcquireN always succeed immediately).
func NewLimiter(n int) *Limiter {
	l := &Limiter{cap: n}
	if n > 0 {
		l.sem = semaphore.NewWeighted(int64(n))
	}
	return l
}

// Acquire blocks until 1 slot is available or ctx is cancelled.
// Returns ctx.Err() on cancellation.
func (l *Limiter) Acquire(ctx context.Context) error {
	return l.AcquireN(ctx, 1)
}

// AcquireN acquires n slots under FIFO fairness. n < 1 is floored to 1 (a defensive
// guard; every real caller already passes >= 1). A request above capacity can never
// be satisfied by any wait, so it fails immediately rather than blocking forever;
// callers decide whether to clamp first (RunAll, where a slot count is a coarse
// throttle) or surface the error (os.with_slots/archive, where reserving more slots
// than exist would desync the reservation from the tool's own worker count). Returns
// ctx.Err() on cancellation.
func (l *Limiter) AcquireN(ctx context.Context, n int) error {
	if l.sem == nil {
		return nil
	}
	if n < 1 {
		n = 1
	}
	if n > l.cap {
		return fmt.Errorf("limiter: acquire %d exceeds capacity %d", n, l.cap)
	}
	l.waiting.Add(int64(n))
	if fn := l.onWait.Load(); fn != nil && *fn != nil {
		(*fn)(n)
	}
	defer func() {
		l.waiting.Add(-int64(n))
		if fn := l.onWait.Load(); fn != nil && *fn != nil {
			(*fn)(-n)
		}
	}()
	start := time.Now()
	if err := l.sem.Acquire(ctx, int64(n)); err != nil {
		return err
	}
	l.inUse.Add(int64(n))
	if fn := l.onAcquire.Load(); fn != nil && *fn != nil {
		(*fn)(time.Since(start).Nanoseconds(), n)
	}
	return nil
}

// Release frees 1 previously acquired slot.
func (l *Limiter) Release() {
	l.ReleaseN(1)
}

// ReleaseN frees n previously acquired slots.
func (l *Limiter) ReleaseN(n int) {
	if l.sem == nil {
		return
	}
	l.inUse.Add(-int64(n))
	l.sem.Release(int64(n))
	if fn := l.onRelease.Load(); fn != nil && *fn != nil {
		(*fn)(n)
	}
}

// Yield releases the caller's slots for the duration of fn, then re-acquires them
// before returning. It releases every slot the caller holds (SlotsHeld(ctx), at
// least 1): a weighted step holds more than one, and releasing only one would leave
// it pinning slots that fn's own AcquireN then blocks on forever. The caller MUST
// hold a slot; a slotless caller would over-release the semaphore. Re-acquire uses a
// non-cancellable context so the caller always returns with its slots held (RunAll
// releases unconditionally; a slotless return would panic). The re-acquire re-enters
// the FIFO queue, so a yielding goroutine goes to the back.
//
// Trade-off: the non-cancellable re-acquire can block a returning yield on a saturated
// limiter even after ctx is cancelled, slowing shutdown until peers free the slots.
func (l *Limiter) Yield(ctx context.Context, fn func() error) error {
	n := SlotsHeld(ctx)
	if n < 1 {
		n = 1
	}
	l.ReleaseN(n)
	defer func() { _ = l.AcquireN(context.WithoutCancel(ctx), n) }()
	return fn()
}

// LimiterStats is a point-in-time view of the concurrency pool.
type LimiterStats struct {
	Capacity int // total slots; 0 = unlimited
	InUse    int // currently acquired slots
	Waiting  int // slots currently blocked in Acquire/AcquireN
}

// Capacity returns the limiter's slot capacity. 0 means unlimited.
func (l *Limiter) Capacity() int { return l.cap }

// Snapshot returns a point-in-time view of the limiter.
func (l *Limiter) Snapshot() LimiterStats {
	return LimiterStats{
		Capacity: l.cap,
		InUse:    int(l.inUse.Load()),
		Waiting:  int(l.waiting.Load()),
	}
}
