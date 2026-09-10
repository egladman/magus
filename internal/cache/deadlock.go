package cache

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/types"
)

// ExitCodeSlotDeadlock is what a deadlocked-slot refusal asks for: 78, EX_CONFIG. The
// same invocation will do the same thing on the next machine and the next run, so it is a
// configuration answer rather than a timing one, and a wrapper retrying on EX_TEMPFAIL
// must not retry this.
//
// It picks the number ExitCodeMachineDeclaration picks, for the reason that constant
// gives about the workspace lock: two independent decisions that agree, not one shared
// setting, because coupling them would move both the day either changes.
const ExitCodeSlotDeadlock = 78

// slotDeadlockGrace is how long the pool must stay wedged before the wait is called
// impossible. A hand-off is not instantaneous: a step releases its slots in a defer while
// the peer that wanted them is already queued, so a sample taken in that window shows
// every slot held with nobody running. The grace outlives that window and nothing else.
//
// A var, not a const, for the reason lock.go gives about its own wait timings: a test that
// has to spend the real cadence either sleeps for it or does not cover it.
var slotDeadlockGrace = 3 * time.Second

// slotHold is one admitted step's occupancy of the limiter: the slots it took, and what it
// is waiting on while it holds them.
//
// The blocked field is the whole point. make's jobserver rule, which this engine adopts on
// purpose, is that a slot is held only while a recipe RUNS; a holder parked in a wait is
// the exception that turns a queue into a deadlock, and nothing could see it before.
// Every field is guarded by the watch's mutex, so a verdict reads one consistent picture
// rather than a set of per-holder samples.
type slotHold struct {
	w     *slotWatch
	label string
	slots int
	// blocked names what this step is waiting on, empty while it is running.
	blocked string
	// yielded marks slots handed back for a ctx.needs fan-out. The step still exists and
	// still has a hold record, but it is occupying nothing, so it neither fills the pool
	// nor keeps a verdict from being reached.
	yielded bool
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
	mu      sync.Mutex
	cap     int
	holds   map[*slotHold]struct{}
	waiters map[*slotWaiter]struct{}
	// wedgedSince is when the pool last entered the wedged shape, zero when it is not in
	// it. A verdict needs the shape to have HELD for the grace, not merely to have been
	// seen twice.
	wedgedSince time.Time
	timer       *time.Timer
}

// newSlotWatch returns the watch for a limiter of capacity n, or nil for an unlimited one:
// with no slot to queue for there is no wait to refuse.
func newSlotWatch(n int) *slotWatch {
	if n <= 0 {
		return nil
	}
	return &slotWatch{
		cap:     n,
		holds:   map[*slotHold]struct{}{},
		waiters: map[*slotWaiter]struct{}{},
	}
}

// acquireWatched takes n slots for the named step and records the hold, so a later waiter
// can tell a slot that is working from one that is stuck. It returns the hold, or the
// MGS3013 refusal when every slot is held by a step that is itself waiting.
func (l *Limiter) acquireWatched(ctx context.Context, n int, label string) (*slotHold, error) {
	w := l.watch
	if w == nil {
		return nil, l.AcquireN(ctx, n)
	}
	// A caller already holding slots is waiting WHILE HOLDING them, which is the
	// hold-and-wait half of the deadlock. Marked here rather than at each call site: this
	// is the only way to reach the queue, so the mark cannot be forgotten.
	unblock := BlockedOn(ctx, fmt.Sprintf("%d build slot(s)", n))
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
	return w.hold(label, n), nil
}

func (w *slotWatch) hold(label string, slots int) *slotHold {
	h := &slotHold{w: w, label: label, slots: slots}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.holds[h] = struct{}{}
	w.evaluateLocked()
	return h
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

// setYielded records whether the hold's slots are currently handed back.
func (h *slotHold) setYielded(y bool) {
	if h == nil {
		return
	}
	h.w.mu.Lock()
	defer h.w.mu.Unlock()
	h.yielded = y
	h.w.evaluateLocked()
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
		w.timer = time.AfterFunc(slotDeadlockGrace, w.verdict)
	}
}

// wedgedLocked reports the shape no wait can end: something is queued for a slot, every
// slot is accounted for by a registered hold, and every one of those holders is itself
// waiting.
//
// Conservative in both directions. A holder that is running can still finish and free its
// slots, so one is enough to answer no. Slots taken outside this bookkeeping (a spell
// reserving them for a tool's own workers) leave the sum short of capacity, which also
// answers no: a wait that MIGHT end is not one to refuse.
func (w *slotWatch) wedgedLocked() bool {
	if len(w.waiters) == 0 {
		return false
	}
	held := 0
	for h := range w.holds {
		if h.yielded {
			continue
		}
		if h.blocked == "" {
			return false
		}
		held += h.slots
	}
	return held >= w.cap
}

// verdict fires the grace after the pool wedged. It re-reads the state rather than
// trusting the timer: the wedge may have broken and re-formed, in which case the new one
// has not yet outlived the grace and deserves its own.
func (w *slotWatch) verdict() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timer = nil
	if !w.wedgedLocked() {
		w.wedgedSince = time.Time{}
		return
	}
	if since := w.wedgedSince; since.IsZero() || time.Since(since) < slotDeadlockGrace {
		w.timer = time.AfterFunc(slotDeadlockGrace, w.verdict)
		return
	}
	err := w.refusalLocked()
	for wt := range w.waiters {
		wt.err = err
		wt.cancel()
	}
}

// refusalLocked builds the MGS3013 error. It names every holder and what each is blocked
// on, in the shape a machine-budget refusal names its holders: a wait a reader cannot
// attribute is a wait they can only interrupt.
func (w *slotWatch) refusalLocked() error {
	holders := make([]string, 0, len(w.holds))
	for h := range w.holds {
		if h.yielded {
			continue
		}
		holders = append(holders, fmt.Sprintf("%s holds %d and is waiting on %s", h.label, h.slots, h.blocked))
	}
	slices.Sort(holders)
	queued := make([]string, 0, len(w.waiters))
	for wt := range w.waiters {
		queued = append(queued, fmt.Sprintf("%s needs %d", wt.label, wt.n))
	}
	slices.Sort(queued)
	return slotRefusal{exit: ExitCodeSlotDeadlock, error: types.DiagnosticErrorf(types.BuildSlotsDeadlocked,
		"refusing to keep waiting for a build slot: all %d of this run's slots are held by steps that are"+
			" themselves waiting, so no slot can free and nothing queued can start. Holding: %s. Queued: %s."+
			" The usual cause is a target reading what a target beside it writes with no ctx.needs between"+
			" them (see %s); order the reader after the writer. Raising concurrency widens the window rather"+
			" than closing it.",
		w.cap, strings.Join(holders, "; "), strings.Join(queued, "; "),
		types.CodeURL(types.UnorderedSameStepWrite))}
}

// slotRefusal states the process status this refusal asks for, the way machineRefusal
// does and for the same reason: a step the daemon runs for an adopted client crosses a
// socket that erases the Go type, and the daemon reads the code off the error.
type slotRefusal struct {
	error
	exit int
}

func (e slotRefusal) ExitCode() int { return e.exit }

func (e slotRefusal) Unwrap() error { return e.error }

// BlockedOn marks the admitted step in ctx as waiting on what until the returned func
// runs, so a wait that fills the pool can be told from work that is progressing.
//
// A no-op outside an admitted step, which is what lets a wait site call it unconditionally
// rather than branching on whether it holds a seat.
func BlockedOn(ctx context.Context, what string) func() {
	return admissionFrom(ctx).hold.block(what)
}
