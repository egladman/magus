package budget

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// SlotDeadlock is what a limiter saw when it refused a wait: every one of its Capacity
// slots is held by a step that is itself waiting, so no slot can free and nothing queued
// can start. Holding and Queued name each party, sorted. It is the error waiters get
// unless the limiter was built [WithRefusal].
type SlotDeadlock struct {
	Capacity int
	// Holding is "<label> holds <n> and is waiting on <what>" per holder.
	Holding []string
	// Queued is "<label> needs <n>" per waiter.
	Queued []string
}

// Error names every holder and what each is blocked on: a wait a reader cannot attribute
// is a wait they can only interrupt.
func (d SlotDeadlock) Error() string {
	return fmt.Sprintf("budget: refusing to keep waiting for a slot: all %d slots are held by steps that are"+
		" themselves waiting, so no slot can free and nothing queued can start, holding %s, queued %s",
		d.Capacity, strings.Join(d.Holding, ", "), strings.Join(d.Queued, ", "))
}

// slotHold is one admitted step's occupancy of the limiter: the slots it took, and what it
// is waiting on while it holds them.
//
// The blocked field is the whole point. make's jobserver rule is that a slot is held only
// while a target RUNS; a holder parked in a wait is the exception that turns a queue into
// a deadlock, and nothing could see it before. Every field is guarded by the watch's
// mutex, so a verdict reads one consistent picture rather than a set of per-holder
// samples.
type slotHold struct {
	w     *slotWatch
	label string
	slots int
	// blocked names what this step is waiting on, empty while it is running.
	blocked string
	// yields counts the fan-outs the step's slots are handed back for. While it is above
	// zero the step still has a hold record but occupies nothing, so it neither fills the
	// pool nor keeps a verdict from being reached. A count, not a flag: a yield nested in
	// another on the same hold must not end the outer one when it returns.
	yields int
}

// slotWaiter is one queued request for slots. cancel is what a refusal pulls: the waiter
// is parked in the semaphore, and cancelling its context is the only way to hand it an
// answer.
type slotWaiter struct {
	label  string
	n      int
	cancel context.CancelFunc
	err    error
}

// slotWatch is the limiter's view of who holds its slots and who is queued for them, kept
// so an impossible wait becomes an error instead of a hang.
//
// It changes no scheduling: nothing here decides who runs, and a step still yields its
// slots across a fan-out and still waits its turn.
type slotWatch struct {
	mu       sync.Mutex
	capacity int
	grace    time.Duration
	refusal  func(SlotDeadlock) error
	holds    map[*slotHold]struct{}
	waiters  map[*slotWaiter]struct{}
	// wedgedSince is when the pool last entered the wedged shape, zero when it is not in
	// it. A verdict needs the shape to have HELD for the grace, not merely to have been
	// seen twice.
	wedgedSince time.Time
	timer       *time.Timer
}

// newSlotWatch returns the watch for a limiter of capacity n, or nil for an unlimited one:
// with no slot to queue for there is no wait to refuse.
func newSlotWatch(n int, o limiterOptions) *slotWatch {
	if n <= 0 {
		return nil
	}
	return &slotWatch{
		capacity: n,
		grace:    o.grace,
		refusal:  o.refusal,
		holds:    map[*slotHold]struct{}{},
		waiters:  map[*slotWaiter]struct{}{},
	}
}

// Seat takes n slots for the work named label and records the hold, so a later waiter
// can tell a slot that is working from one that is stuck. An n below 1 is a
// [*SlotCountError], as [Limiter.AcquireN] refuses it. It returns ctx
// carrying the hold, which a blocking wait inside the work marks itself on
// ([MarkWaiting]), and release,
// which retires the hold and gives the slots back. Seat does not mark ctx as holding
// slots; [WithSlotsHeld] does, where the work's body starts.
//
// When every slot is held by a step that is itself waiting, the wait is refused with the
// limiter's refusal error once the wedge has held for its grace.
func (l *Limiter) Seat(ctx context.Context, n int, label string) (context.Context, func(), error) {
	if n < 1 {
		return ctx, nil, &SlotCountError{Slots: n}
	}
	hold, err := l.acquireWatched(ctx, n, label)
	if err != nil {
		return ctx, nil, err
	}
	return withSlotHold(ctx, hold), func() {
		// Retire the hold before the slots go back, so a peer waking on them never reads
		// a record that occupies nothing.
		hold.done()
		l.ReleaseN(n)
	}, nil
}

// acquireWatched takes n slots for the named step and records the hold.
func (l *Limiter) acquireWatched(ctx context.Context, n int, label string) (*slotHold, error) {
	w := l.watch
	if w == nil {
		return nil, l.AcquireN(ctx, n)
	}
	// A caller already holding slots is waiting WHILE HOLDING them, which is the
	// hold-and-wait half of the deadlock. Marked here rather than at each call site: this
	// is the only way to reach the queue, so the mark cannot be forgotten.
	unblock := MarkWaiting(ctx, fmt.Sprintf("%d build slot(s)", n))
	defer unblock()

	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	waiter := w.beginWait(label, n, cancel)
	err := l.AcquireN(waitCtx, n)
	refusal := w.endWait(waiter)
	if err != nil {
		if refusal != nil {
			return nil, refusal
		}
		return nil, err
	}
	return w.admit(label, n), nil
}

// Admit takes one slot for work named label. It returns ctx marked as holding the slot,
// so work inside yields it before queueing for another ([Limiter.Yield]) and the slot
// watch sees what it waits on. release gives the slot back. A holder that took its slot
// with a bare Acquire is invisible to the watch, and a pool it wedges hangs instead of
// being refused.
//
// Under an admitted step, a wait message names the work as "<step> > <label>", so label
// need only say what the work is within the step that started it.
func (l *Limiter) Admit(ctx context.Context, label string) (context.Context, func(), error) {
	if parent := seatFromContext(ctx).hold; parent != nil {
		label = fmt.Sprintf("%s > %s", parent.label, label)
	}
	held, release, err := l.Seat(ctx, 1, label)
	if err != nil {
		return ctx, nil, err
	}
	return WithSlotHeld(held), release, nil
}

// admit records that the named step now holds slots.
func (w *slotWatch) admit(label string, slots int) *slotHold {
	h := &slotHold{w: w, label: label, slots: slots}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.holds[h] = struct{}{}
	w.evaluateLocked()
	return h
}

// name is the step the hold was admitted for, as a wait message names it. label never
// changes after admission, so no lock is taken.
func (h *slotHold) name() string {
	if h == nil {
		return "an unmarked step"
	}
	return h.label
}

// done retires the hold. Called before the slots are released, so a peer waking on those
// slots never reads a hold that no longer occupies anything.
func (h *slotHold) done() {
	if h == nil {
		return
	}
	h.w.mu.Lock()
	defer h.w.mu.Unlock()
	delete(h.w.holds, h)
	h.w.evaluateLocked()
}

// block marks the hold as waiting on what until the returned func runs. Nested waits
// restore the outer one, so a mark is never lost by a deeper wait finishing first.
func (h *slotHold) block(what string) func() {
	if h == nil {
		return func() {}
	}
	h.w.mu.Lock()
	prev := h.blocked
	h.blocked = what
	h.w.evaluateLocked()
	h.w.mu.Unlock()
	return func() {
		h.w.mu.Lock()
		defer h.w.mu.Unlock()
		h.blocked = prev
		h.w.evaluateLocked()
	}
}

// yield records that the hold's slots are handed back until the returned func runs, the
// way block hands back its undo.
func (h *slotHold) yield() func() {
	if h == nil {
		return func() {}
	}
	add := func(n int) {
		h.w.mu.Lock()
		defer h.w.mu.Unlock()
		h.yields += n
		h.w.evaluateLocked()
	}
	add(1)
	return func() { add(-1) }
}

func (w *slotWatch) beginWait(label string, n int, cancel context.CancelFunc) *slotWaiter {
	wt := &slotWaiter{label: label, n: n, cancel: cancel}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.waiters[wt] = struct{}{}
	w.evaluateLocked()
	return wt
}

// endWait retires a waiter and returns the refusal it was handed, if any. The refusal is
// read here rather than from the acquire error because a cancelled acquire reports only
// context.Canceled, which says nothing about who cancelled it or why.
func (w *slotWatch) endWait(wt *slotWaiter) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.waiters, wt)
	w.evaluateLocked()
	return wt.err
}

// evaluateLocked arms, disarms or leaves running the grace timer that turns a wedged pool
// into a refusal.
func (w *slotWatch) evaluateLocked() {
	if !w.wedgedLocked() {
		w.wedgedSince = time.Time{}
		if w.timer != nil {
			w.timer.Stop()
			w.timer = nil
		}
		return
	}
	if w.wedgedSince.IsZero() {
		w.wedgedSince = time.Now()
	}
	if w.timer == nil {
		w.armLocked(w.grace)
	}
}

// armLocked starts the grace timer. The timer passes itself to fireVerdict, which acts
// only while it is still the armed one: a timer stopped after it fired would otherwise
// clear, or re-arm over, the timer that replaced it.
func (w *slotWatch) armLocked(d time.Duration) {
	var t *time.Timer
	t = time.AfterFunc(d, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.fireVerdictLocked(t)
	})
	w.timer = t
}

// wedgedLocked reports the shape no wait can end: something is queued for a slot, every
// slot is accounted for by a registered hold, and every one of those holders is itself
// waiting.
//
// Conservative in both directions. A holder that is running can still finish and free its
// slots, so one is enough to answer no. Slots taken outside this bookkeeping (a tool
// reserving them for its own workers) leave the sum short of capacity, which also answers
// no: a wait that MIGHT end is not one to refuse.
func (w *slotWatch) wedgedLocked() bool {
	if len(w.waiters) == 0 {
		return false
	}
	held := 0
	for h := range w.holds {
		if h.yields > 0 {
			continue
		}
		if h.blocked == "" {
			return false
		}
		held += h.slots
	}
	return held >= w.capacity
}

// fireVerdictLocked runs the grace after the pool wedged, and cancels every waiter with
// the refusal when the wedge has held for the whole of it. It re-reads the state rather
// than trusting the timer: the wedge may have broken and re-formed, in which case the
// new one has only the remainder of its own grace to outlive. fired is the timer that
// ran it; one no longer armed does nothing.
func (w *slotWatch) fireVerdictLocked(fired *time.Timer) {
	if w.timer != fired {
		return
	}
	w.timer = nil
	if !w.wedgedLocked() {
		w.wedgedSince = time.Time{}
		return
	}
	if remaining := w.grace - time.Since(w.wedgedSince); remaining > 0 {
		w.armLocked(remaining)
		return
	}
	err := w.refusalLocked()
	for wt := range w.waiters {
		wt.err = err
		wt.cancel()
	}
}

// refusalLocked builds the error every waiter is handed.
func (w *slotWatch) refusalLocked() error {
	d := SlotDeadlock{Capacity: w.capacity}
	for h := range w.holds {
		if h.yields > 0 {
			continue
		}
		d.Holding = append(d.Holding, fmt.Sprintf("%s holds %d and is waiting on %s", h.label, h.slots, h.blocked))
	}
	slices.Sort(d.Holding)
	for wt := range w.waiters {
		d.Queued = append(d.Queued, fmt.Sprintf("%s needs %d", wt.label, wt.n))
	}
	slices.Sort(d.Queued)
	if w.refusal != nil {
		return w.refusal(d)
	}
	return d
}

// MarkWaiting marks the admitted step in ctx as waiting on what until the returned func
// runs, so a wait that fills the pool can be told from work that is progressing.
//
// A no-op outside an admitted step, which is what lets a wait site call it unconditionally
// rather than branching on whether it holds a seat.
func MarkWaiting(ctx context.Context, what string) func() {
	return seatFromContext(ctx).hold.block(what)
}
