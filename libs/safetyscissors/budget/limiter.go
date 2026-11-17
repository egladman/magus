package budget

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"
)

// Limiter is a FIFO-fair weighted semaphore that bounds concurrent work in one process
// and exposes live occupancy. It is safe for concurrent use.
type Limiter struct {
	*pool
	// events hears every change to the slots taken through this Limiter, nil for none.
	events func(PoolEvent)
}

// pool is the slots every [Limiter] derived from one [NewLimiter] shares.
type pool struct {
	sem     *semaphore.Weighted
	cap     int
	running atomic.Int64
	queued  atomic.Int64
	// watch records who holds the slots and what each holder is waiting on, so a wait
	// nothing in this process can end is refused rather than hung. Nil on an unlimited
	// limiter, where no caller ever queues. See watch.go.
	watch *slotWatch
}

// LimiterOption configures a [Limiter] at construction.
type LimiterOption func(*limiterOptions)

type limiterOptions struct {
	refusal func(SlotDeadlock) error
	grace   time.Duration
	events  func(PoolEvent)
}

// WithRefusal sets the error a wedged pool hands every waiter, built from what the
// limiter saw. Without it the waiters get the [SlotDeadlock] itself.
func WithRefusal(fn func(SlotDeadlock) error) LimiterOption {
	return func(o *limiterOptions) { o.refusal = fn }
}

// WithPoolEvents has fn hear every change to the slots, as [PoolEvent] describes. fn
// must not block.
func WithPoolEvents(fn func(PoolEvent)) LimiterOption {
	return func(o *limiterOptions) { o.events = fn }
}

// DefaultDeadlockGrace is how long a pool must stay wedged before the wait is called
// impossible. A hand-off is not instantaneous: a step releases its slots in a defer while
// the peer that wanted them is already queued, so a sample taken in that window shows
// every slot held with nobody running. The grace outlives that window and nothing else.
const DefaultDeadlockGrace = 3 * time.Second

// PoolEvent is one change to a Limiter's slots; at most one of Queued and Running is
// non-zero. Queued is +n the instant a caller begins waiting for n slots (before the
// blocking acquire) and -n once that acquire returns, whether it acquired or its context
// was cancelled, so the running sum of Queued tracks [Limiter.Snapshot]'s Queued. Running is
// +n when a caller acquires n slots and -n when it releases them. Wait is how long the
// acquire waited, set only with a positive Running. Shortfall is how many slots a
// [Limiter.ReleaseN] was asked to free beyond those running, which it did not free; an
// event with only Shortfall set is a release that found nothing running.
type PoolEvent struct {
	Queued    int
	Running   int
	Wait      time.Duration
	Shortfall int
}

// fire reports ev to the limiter's events, if any.
func (l *Limiter) fire(ev PoolEvent) {
	if l.events != nil {
		l.events(ev)
	}
}

// NewLimiter returns a Limiter with capacity n. n <= 0 means unlimited (Acquire and
// AcquireN always succeed immediately).
func NewLimiter(n int, opts ...LimiterOption) *Limiter {
	o := limiterOptions{grace: DefaultDeadlockGrace}
	for _, opt := range opts {
		opt(&o)
	}
	p := &pool{cap: n, watch: newSlotWatch(n, o)}
	if n > 0 {
		p.sem = semaphore.NewWeighted(int64(n))
	}
	return &Limiter{pool: p, events: o.events}
}

// WithPoolEvents returns a Limiter sharing l's slots, holders and waiters whose own
// acquires and releases fn hears in place of l's events. l is unchanged. It is for a
// caller handed a limiter built before what fn records into existed: each user of a
// shared pool reports what it took.
func (l *Limiter) WithPoolEvents(fn func(PoolEvent)) *Limiter {
	return &Limiter{pool: l.pool, events: fn}
}

// Acquire blocks until 1 slot is available or ctx is cancelled.
// Returns ctx.Err() on cancellation.
func (l *Limiter) Acquire(ctx context.Context) error {
	return l.AcquireN(ctx, 1)
}

// AcquireN acquires n slots under FIFO fairness. n < 1 is floored to 1. A request above
// capacity can never be satisfied by any wait, so it fails immediately rather than
// blocking forever with a [*CapacityError]; callers decide whether to clamp first or
// surface the error. Returns ctx.Err() on cancellation.
func (l *Limiter) AcquireN(ctx context.Context, n int) error {
	if l.sem == nil {
		return nil
	}
	if n < 1 {
		n = 1
	}
	if n > l.cap {
		return &CapacityError{Slots: n, Capacity: l.cap}
	}
	l.queued.Add(int64(n))
	l.fire(PoolEvent{Queued: n})
	defer func() {
		l.queued.Add(-int64(n))
		l.fire(PoolEvent{Queued: -n})
	}()
	start := time.Now()
	if err := l.sem.Acquire(ctx, int64(n)); err != nil {
		return err
	}
	l.running.Add(int64(n))
	l.fire(PoolEvent{Running: n, Wait: time.Since(start)})
	return nil
}

// CapacityError is [Limiter.AcquireN]'s refusal of a request no wait can satisfy.
type CapacityError struct {
	Slots, Capacity int
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf("budget: acquire %d slots exceeds capacity %d", e.Slots, e.Capacity)
}

// Release frees 1 previously acquired slot.
func (l *Limiter) Release() {
	l.ReleaseN(1)
}

// ReleaseN frees n previously acquired slots. n < 1 is floored to 1, as
// [Limiter.AcquireN] floors it. It never frees more slots than are running: the excess is
// left unfreed and reported as the [PoolEvent]'s Shortfall, since freeing a slot nobody
// took would let a peer run beyond the capacity.
func (l *Limiter) ReleaseN(n int) {
	if l.sem == nil {
		return
	}
	n = max(n, 1)
	freed := l.takeRunning(n)
	if freed > 0 {
		l.sem.Release(int64(freed))
	}
	l.fire(PoolEvent{Running: -freed, Shortfall: n - freed})
}

// takeRunning lowers the running count by up to n and returns how many it took.
func (l *Limiter) takeRunning(n int) int {
	for {
		cur := l.running.Load()
		k := min(int64(n), max(cur, 0))
		if l.running.CompareAndSwap(cur, cur-k) {
			return int(k)
		}
	}
}

// Yield releases the caller's slots for the duration of fn and re-acquires them before
// returning. It releases every slot the caller holds ([SlotsHeld], capped at the
// capacity): a weighted step that released only one would pin the rest while fn's own
// AcquireN blocks on them. Re-acquire uses a non-cancellable context so the caller always
// returns holding its slots, and it re-enters the FIFO queue at the back. A failed
// re-acquire is returned joined to fn's error.
//
// ctx must be marked as holding slots ([Limiter.Admit], or [WithSlotsHeld] after a
// [Limiter.Seat] or a raw Acquire). A ctx marked as holding none is refused with a
// [*NoSlotsHeldError] and fn does not run: there is nothing to hand back, and guessing a
// count would release slots another caller holds. Check [SlotHeld] first to run fn
// unchanged instead. The cap keeps a context marked with more slots than the pool has
// from releasing slots nobody took.
//
// Trade-off: the non-cancellable re-acquire can block a returning yield on a saturated
// limiter even after ctx is cancelled, slowing shutdown until peers free the slots.
func (l *Limiter) Yield(ctx context.Context, fn func() error) (err error) {
	n := SlotsHeld(ctx)
	if n < 1 {
		return &NoSlotsHeldError{}
	}
	if l.cap > 0 {
		n = min(n, l.cap)
	}
	// The hold record has to say so too: a step whose slots are back in the pool occupies
	// nothing, and a watch that still counted them would read a pool with room as full.
	hold := seatFromContext(ctx).hold
	defer hold.yield()()
	l.ReleaseN(n)
	defer func() { err = errors.Join(err, l.reacquireYielded(ctx, n)) }()
	return fn()
}

// NoSlotsHeldError is [Limiter.Yield]'s refusal of a context marked as holding no slots.
type NoSlotsHeldError struct{}

func (e *NoSlotsHeldError) Error() string {
	return "budget: cannot yield slots: the context is marked as holding none"
}

// reacquireYielded takes back the slots Yield handed out. Non-cancellable, since the
// caller must return holding them; but VISIBLE to the slot watch, registered as a
// waiter so a pool wedged with only re-acquirers queued still reads as wedged and the
// refusal names them. The refusal cannot end this wait (nothing may return slotless),
// so what ends it is the refusal ending every other waiter, whose steps then unwind and
// release.
func (l *Limiter) reacquireYielded(ctx context.Context, n int) error {
	if w := l.watch; w != nil {
		waiter := w.beginWait(fmt.Sprintf("%s (taking back yielded slots)", seatFromContext(ctx).hold.name()), n, func() {})
		// The refusal it may carry is for the waiters the verdict could end; this one it
		// could not, and the caller returns holding its slots either way.
		defer func() { _ = w.endWait(waiter) }()
	}
	return l.AcquireN(context.WithoutCancel(ctx), n)
}

// LimiterSnapshot is a point-in-time view of the concurrency pool.
type LimiterSnapshot struct {
	Capacity int // total slots; 0 = unlimited
	Running  int // currently acquired slots
	Queued   int // slots currently blocked in Acquire/AcquireN
}

// Capacity returns the limiter's slot capacity. 0 means unlimited.
func (l *Limiter) Capacity() int { return l.cap }

// Snapshot returns a point-in-time view of the limiter. Peers acquire and release while
// it reads, so the counts may never have held at any single instant together.
func (l *Limiter) Snapshot() LimiterSnapshot {
	return LimiterSnapshot{
		Capacity: l.cap,
		Running:  int(l.running.Load()),
		Queued:   int(l.queued.Load()),
	}
}
