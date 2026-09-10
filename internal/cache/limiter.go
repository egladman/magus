package cache

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"
)

// Limiter is a FIFO-fair weighted semaphore that bounds concurrent work and
// exposes live occupancy metrics for the pool inspector.
type Limiter struct {
	sem     *semaphore.Weighted
	cap     int
	running atomic.Int64
	queued  atomic.Int64
	// watch records who holds the slots and what each holder is waiting on, so a wait
	// nothing in this process can end is refused rather than hung. Nil on an unlimited
	// limiter, where no caller ever queues. See deadlock.go.
	watch *slotWatch

	onAcquire atomic.Pointer[func(waitNs int64, n int)]
	onRelease atomic.Pointer[func(n int)]
	onWait    atomic.Pointer[func(delta int)]
}

// SetHooks installs optional callbacks fired on every Acquire/Release. Must not block.
// Stored atomically so a SetHooks racing with concurrent Acquire/Release is safe.
//
// onWait mirrors the internal queued counter exactly: it fires with +n the instant a caller
// begins waiting for n slots (before the blocking Acquire) and with -n once that Acquire
// returns, whether it acquired or the context was cancelled. Net inflight of onWait tracks
// [Limiter.Snapshot]'s Queued.
func (l *Limiter) SetHooks(onAcquire func(waitNs int64, n int), onRelease func(n int), onWait func(delta int)) {
	l.onAcquire.Store(&onAcquire)
	l.onRelease.Store(&onRelease)
	l.onWait.Store(&onWait)
}

// NewLimiter returns a Limiter with capacity n. n <= 0 means unlimited
// (Acquire and AcquireN always succeed immediately).
func NewLimiter(n int) *Limiter {
	l := &Limiter{cap: n, watch: newSlotWatch(n)}
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
	l.queued.Add(int64(n))
	if fn := l.onWait.Load(); fn != nil && *fn != nil {
		(*fn)(n)
	}
	defer func() {
		l.queued.Add(-int64(n))
		if fn := l.onWait.Load(); fn != nil && *fn != nil {
			(*fn)(-n)
		}
	}()
	start := time.Now()
	if err := l.sem.Acquire(ctx, int64(n)); err != nil {
		return err
	}
	l.running.Add(int64(n))
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
	l.running.Add(-int64(n))
	l.sem.Release(int64(n))
	if fn := l.onRelease.Load(); fn != nil && *fn != nil {
		(*fn)(n)
	}
}

// Yield releases the caller's slots for the duration of fn and re-acquires them before
// returning. It releases every slot the caller holds (SlotsHeld(ctx), floored at 1): a
// weighted step that released only one would pin the rest while fn's own AcquireN blocks
// on them. Re-acquire uses a non-cancellable context so the caller always returns holding
// its slots (RunAll releases unconditionally; a slotless return would panic), and it
// re-enters the FIFO queue at the back.
//
// The floor of 1 is the contract for a caller holding a slot the CONTEXT does not name,
// which proc's server does at both of its Yield sites: it takes its admission slot with a
// raw Acquire, so an unmarked ctx there means one slot held, not none. Callers holding
// nothing (magus.go's spell fan-out, proc.RunChildSync) check SlotHeld and never reach
// here, which is what keeps the floor from over-releasing.
//
// Trade-off: the non-cancellable re-acquire can block a returning yield on a saturated
// limiter even after ctx is cancelled, slowing shutdown until peers free the slots.
func (l *Limiter) Yield(ctx context.Context, fn func() error) error {
	n := SlotsHeld(ctx)
	if n < 1 {
		n = 1
	}
	// The hold record has to say so too: a step whose slots are back in the pool occupies
	// nothing, and a watch that still counted them would read a pool with room as full.
	hold := admissionFrom(ctx).hold
	defer hold.yield()()
	l.ReleaseN(n)
	defer l.reacquireYielded(ctx, n)
	return fn()
}

// reacquireYielded takes back the slots Yield handed out. Non-cancellable, since the
// caller must return holding them; but VISIBLE to the slot watch, registered as a
// waiter so a pool wedged with only re-acquirers queued still reads as wedged and the
// refusal names them. The refusal cannot end this wait (nothing may return slotless),
// so what ends it is the refusal ending every other waiter, whose steps then unwind and
// release.
func (l *Limiter) reacquireYielded(ctx context.Context, n int) {
	if w := l.watch; w != nil {
		waiter := w.beginWait(fmt.Sprintf("%s (taking back yielded slots)", admissionFrom(ctx).hold.name()), n, func() {})
		// The refusal it may carry is for the waiters the verdict could end; this one it
		// could not, and the caller returns holding its slots either way.
		defer func() { _ = w.endWait(waiter) }()
	}
	_ = l.AcquireN(context.WithoutCancel(ctx), n)
}

// LimiterStats is a point-in-time view of the concurrency pool.
type LimiterStats struct {
	Capacity int // total slots; 0 = unlimited
	Running  int // currently acquired slots
	Queued   int // slots currently blocked in Acquire/AcquireN
}

// Capacity returns the limiter's slot capacity. 0 means unlimited.
func (l *Limiter) Capacity() int { return l.cap }

// Snapshot returns a point-in-time view of the limiter.
func (l *Limiter) Snapshot() LimiterStats {
	return LimiterStats{
		Capacity: l.cap,
		Running:  int(l.running.Load()),
		Queued:   int(l.queued.Load()),
	}
}

// logPool emits one cache.pool event carrying the limiter's current occupancy, the feed
// behind the interactive pool status line.
//
// The numbers are a point-in-time sample: peers acquire and release while this reads
// them, so a reader may see a count that never existed at any single instant. Cheap and
// current is the right trade for a status line, and it is why nothing downstream should
// compute from these values; [Limiter.Snapshot] is the authoritative view.
func (c *Cache) logPool(ctx context.Context, lim *Limiter) {
	if lim == nil {
		return
	}
	// Only a handler with a live region can show this. Emitting regardless would put two
	// events per step into JSON output on stdout, where machine consumers read results,
	// and would bury the results in CI logs.
	ph, ok := c.log.Handler().(*PrettyHandler)
	if !ok || !ph.RendersBand() {
		return
	}
	s := lim.Snapshot()
	c.log.LogAttrs(ctx, slog.LevelInfo, "cache.pool",
		slog.Int("capacity", s.Capacity),
		slog.Int("running", s.Running),
		slog.Int("queued", s.Queued),
	)
}
