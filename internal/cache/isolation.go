package cache

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/egladman/magus/types"
)

// isolationSeats is the gate's width: a shared request takes one seat, an exclusive one
// takes every seat. Wide enough that the shared side is never the scarce resource, since
// bounding concurrency is the limiter's job and not this gate's.
const isolationSeats = 1 << 20

// isolationWedgeGrace is how long the wedged shape must hold, with the whole invocation
// silent, before the wait is called impossible. Two of the 15s wait heartbeats every
// other in-process wait uses, so a run that beats at all outlives it comfortably.
//
// A var, not a const, for the reason lock.go gives about its own wait timings: a test
// that has to spend the real cadence either sleeps for it or does not cover it.
var isolationWedgeGrace = 30 * time.Second

// ExitCodeRunIsolationWedged is what a wedged-gate refusal asks for: 70, EX_SOFTWARE.
// Unlike the slot deadlock, which a magusfile causes and a magusfile fixes, this shape is
// magus arranging its own waits into a cycle, so the honest status is an internal fault
// and a wrapper must not retry it.
const ExitCodeRunIsolationWedged = 70

// runIsolation serializes Step.Exclusive steps against the rest of one invocation: an
// exclusive step takes the whole gate, every other step takes one seat.
//
// ONE ACQUISITION PER PATH is the invariant that keeps it deadlock-free.
// [acquireRunIsolation] inherits the lease the context already carries, for either kind
// of request under either kind of lease, so work dispatched beneath an admitted step
// never queues for a gate its own ancestor is holding. Without that rule a ctx.needs
// child asking for the exclusive side waited on its parent's shared seat, and a queued
// exclusive request parks every later shared one behind it, so the whole invocation
// stopped: 19 minutes of it on 2026-09-11, with every project lock held and `magus
// status` reporting nothing running.
//
// What inheriting costs is nothing the policy promised. Step.Exclusive is a BATCH claim,
// "RunAll only ... no other batch step runs concurrently", and dynamically dispatched work
// arrives through RunAside, outside any batch. Whatever lease the ancestor holds already
// excludes every batch peer that declared exclusivity, which is the whole of it.
//
// # The other waits in this package
//
// Each is listed with who takes it, whether a dispatched child can meet it a second time,
// and whether waiting on it feeds the stall watchdog. The shape to watch for is the one
// above: a wait a step's own descendant can queue for.
//
//	gate (this type)   | RunAll and RunAside, per step | nested requests INHERIT, so a
//	                   | descendant never queues       | descendant cannot starve it;
//	                   |                               | a wait here does NOT beat
//	slots (limiter)    | Cache.admit, per step         | nested admits take their own,
//	                   | and hand them back across a   | and MGS3013 refuses the wedge;
//	                   | ctx.needs fan-out             | a wait here does NOT beat
//	keyed cache lock   | Cache.Run, per cache key      | a descendant keyed the same way
//	                   |                               | would queue, and the holder may
//	                   |                               | be another PROCESS: it beats
//	machine gate       | RunAll and RunAside, per step | cross-process budget, and an
//	                   | before the gate above         | ancestry-blind run is refused
//	                   |                               | rather than queued: it beats
//	dep barrier        | RunAll, before any admission  | in-process only, and acyclicity
//	                   |                               | is checked before launch: it
//	                   |                               | does NOT beat (see barrier.go)
//
// A wait that beats is one whose subject is OUTSIDE this invocation, where nothing else
// would report liveness. A wait on this run's own work must not beat: if that work is
// moving it beats for itself, and if it is not, the beat is the invocation telling the
// watchdog it is fine while nothing happens.
type runIsolation struct {
	gate *semaphore.Weighted

	mu      sync.Mutex
	holders map[*runIsolationLease]struct{}
	waiters map[*isolationWaiter]struct{}
	// prog is the invocation's heartbeat, captured from the first context to queue. Nil
	// when no heartbeat is installed, which makes the wedge verdict unreachable: Idle
	// reports zero on a nil Progress, so a run nobody watches is never refused.
	prog *Progress
	// wedgedSince is when the gate last entered the wedged shape, zero when it is not in
	// it. A verdict needs the shape to have HELD for the grace, not merely to have been
	// seen twice.
	wedgedSince time.Time
	timer       *time.Timer
}

func newRunIsolation() *runIsolation {
	return &runIsolation{
		gate:    semaphore.NewWeighted(isolationSeats),
		holders: map[*runIsolationLease]struct{}{},
		waiters: map[*isolationWaiter]struct{}{},
	}
}

type runIsolationKey struct{}

// runIsolationLease is one path's hold on the gate: what it took, who took it, and the
// slot record that says whether it is still working. A context carrying one is INSIDE the
// region, whether it acquired the lease itself or inherited it from an ancestor.
type runIsolationLease struct {
	isolation *runIsolation
	exclusive bool
	holder    string
	// hold is the holder's slot-watch record, attached by [Cache.admit] once the step has
	// a seat and nil until then. It is how the wedge verdict tells a holder that is
	// working from one that is parked; a holder without one is read as working, so an
	// unlimited limiter (which keeps no records) can never produce a verdict.
	hold *slotHold
}

// isolationWaiter is one queued request. cancel is what a refusal pulls: the waiter is
// parked in the semaphore, and cancelling its context is the only way to hand it an
// answer.
type isolationWaiter struct {
	label     string
	exclusive bool
	cancel    context.CancelFunc
	err       error
}

// WithRunScope attaches the isolation gate shared by a top-level invocation and
// its off-batch cache work. Reusing an existing scope makes nested entry points
// join the same gate instead of serializing against an unrelated one.
func WithRunScope(ctx context.Context) context.Context {
	if isolationFrom(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, runIsolationKey{}, newRunIsolation())
}

func isolationFrom(ctx context.Context) *runIsolation {
	isolation, _ := ctx.Value(runIsolationKey{}).(*runIsolation)
	return isolation
}

// acquireRunIsolation admits one step to the invocation's isolation gate and returns the
// context it runs under plus the release for what it took. label names the step in a
// refusal.
//
// A context that already carries a lease inherits it: nothing is taken, the release is a
// no-op, and an exclusive request under a shared ancestor is answered by the ancestor's
// admission rather than by upgrading it. See runIsolation for why that is the policy and
// not a shortcut.
//
// The wait is cancellable, so a sibling's failure, a Ctrl-C or the wedge verdict all
// reach a parked step; it returns that cause unchanged, and MGS3015 when the gate refused
// the wait outright.
func acquireRunIsolation(ctx context.Context, exclusive bool, label string) (context.Context, func(), error) {
	ctx = WithRunScope(ctx)
	if lease := admissionFrom(ctx).isolation; lease != nil {
		return ctx, func() {}, nil
	}
	isolation := isolationFrom(ctx)
	lease, release, err := isolation.acquire(ctx, exclusive, label)
	if err != nil {
		return ctx, func() {}, err
	}
	held := admissionFrom(ctx)
	held.isolation = lease
	return held.on(ctx), release, nil
}

func (r *runIsolation) acquire(ctx context.Context, exclusive bool, label string) (*runIsolationLease, func(), error) {
	weight := int64(1)
	if exclusive {
		weight = isolationSeats
	}
	// Marked as a blocking hold for the same reason acquireWatched marks its own: a step
	// already holding slots that parks here is the hold-and-wait half of a deadlock, and
	// this is the only way into the queue, so the mark cannot be forgotten.
	unblock := BlockedOn(ctx, "the run-isolation gate ("+isolationSide(exclusive)+")")
	defer unblock()

	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := r.beginWait(ctx, label, exclusive, cancel)
	err := r.gate.Acquire(waitCtx, weight)
	refusal := r.endWait(w)
	if err != nil {
		if refusal != nil {
			return nil, nil, refusal
		}
		return nil, nil, err
	}
	lease := &runIsolationLease{isolation: r, exclusive: exclusive, holder: label}
	r.mu.Lock()
	r.holders[lease] = struct{}{}
	r.evaluateLocked()
	r.mu.Unlock()
	return lease, func() {
		r.mu.Lock()
		delete(r.holders, lease)
		r.evaluateLocked()
		r.mu.Unlock()
		r.gate.Release(weight)
	}, nil
}

// attach records the holder's slot-watch record on the lease, so the wedge verdict can
// read whether this holder is working or parked. A no-op outside an admitted step.
func (l *runIsolationLease) attach(h *slotHold) {
	if l == nil || h == nil {
		return
	}
	l.isolation.mu.Lock()
	defer l.isolation.mu.Unlock()
	l.hold = h
	l.isolation.evaluateLocked()
}

func (r *runIsolation) beginWait(ctx context.Context, label string, exclusive bool, cancel context.CancelFunc) *isolationWaiter {
	w := &isolationWaiter{label: label, exclusive: exclusive, cancel: cancel}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prog == nil {
		r.prog = ProgressFromContext(ctx)
	}
	r.waiters[w] = struct{}{}
	r.evaluateLocked()
	return w
}

// endWait retires a waiter and returns the refusal it was handed, if any. Read here
// rather than from the acquire error because a cancelled acquire reports only
// context.Canceled, which says nothing about who cancelled it or why.
func (r *runIsolation) endWait(w *isolationWaiter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.waiters, w)
	r.evaluateLocked()
	return w.err
}

// evaluateLocked arms or disarms the timer that turns a wedged gate into a refusal. It
// runs while anything is queued rather than only while the wedged shape is showing,
// because the shape forms without the gate being told: a holder becomes parked by
// blocking somewhere else entirely, and that edge is the slot watch's, not this one's.
func (r *runIsolation) evaluateLocked() {
	if len(r.waiters) == 0 {
		r.wedgedSince = time.Time{}
		if r.timer != nil {
			r.timer.Stop()
			r.timer = nil
		}
		return
	}
	if r.wedgedLocked() && r.wedgedSince.IsZero() {
		r.wedgedSince = time.Now()
	}
	if r.timer == nil {
		r.timer = time.AfterFunc(isolationWedgeGrace, r.fireVerdict)
	}
}

// wedgedLocked reports the shape no wait can end: something is queued for the gate and
// every step holding it is itself parked.
//
// Conservative in both directions, like the slot watch's own verdict. One holder that is
// still working answers no, because it can still finish and release. The invocation's
// silence is checked separately, when the grace expires: the shape can form legitimately
// for as long as a holder is waiting on another process, and only a run where nothing
// started, finished or printed a line for the whole grace is wedged rather than slow.
func (r *runIsolation) wedgedLocked() bool {
	if len(r.waiters) == 0 {
		return false
	}
	for l := range r.holders {
		if !l.hold.stalled() {
			return false
		}
	}
	return true
}

// fireVerdict runs the grace after the gate wedged, and cancels every waiter with the
// refusal when the wedge has held for the whole of it with nothing else moving. It
// re-reads the state rather than trusting the timer: the wedge may have broken and
// re-formed, in which case the new one has only the remainder of its own grace to outlive.
func (r *runIsolation) fireVerdict() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timer = nil
	if len(r.waiters) == 0 {
		r.wedgedSince = time.Time{}
		return
	}
	defer func() {
		if r.timer == nil {
			r.timer = time.AfterFunc(isolationWedgeGrace, r.fireVerdict)
		}
	}()
	if !r.wedgedLocked() {
		r.wedgedSince = time.Time{}
		return
	}
	if r.wedgedSince.IsZero() {
		r.wedgedSince = time.Now()
		return
	}
	if remaining := isolationWedgeGrace - time.Since(r.wedgedSince); remaining > 0 {
		r.timer = time.AfterFunc(remaining, r.fireVerdict)
		return
	}
	idle := r.prog.Idle()
	if idle < isolationWedgeGrace {
		r.timer = time.AfterFunc(isolationWedgeGrace-idle, r.fireVerdict)
		return
	}
	err := r.refusalLocked(idle)
	for w := range r.waiters {
		w.err = err
		w.cancel()
	}
}

// refusalLocked builds the MGS3015 error. It names every holder, what each is parked on,
// and everything queued behind them, in the shape MGS3013 names its own: a wait a reader
// cannot attribute is a wait they can only interrupt.
func (r *runIsolation) refusalLocked(idle time.Duration) error {
	holders := make([]string, 0, len(r.holders))
	for l := range r.holders {
		holders = append(holders, fmt.Sprintf("%s holds the %s side and is waiting on %s",
			l.holder, isolationSide(l.exclusive), l.hold.waitingOn()))
	}
	slices.Sort(holders)
	queued := make([]string, 0, len(r.waiters))
	for w := range r.waiters {
		queued = append(queued, fmt.Sprintf("%s needs the %s side", w.label, isolationSide(w.exclusive)))
	}
	slices.Sort(queued)
	// Carries its exit status the way the slot refusal does and for the same reason: a
	// step the daemon runs for an adopted client crosses a socket that erases the Go
	// type, and the daemon reads the code off the error.
	return types.ExitError{Code: ExitCodeRunIsolationWedged, Err: types.DiagnosticErrorf(types.RunIsolationWedged,
		"refusing to keep waiting for this run's isolation gate: every step holding it is itself waiting,"+
			" nothing in this run has started, finished or printed a line for %s, and what is queued cannot"+
			" start until a holder finishes. Holding: %s. Queued: %s."+
			" A step waiting for the gate its own ancestor holds is the shape this catches; report it with"+
			" the run's captured log, since no magusfile can produce it.",
		idle.Round(time.Second), strings.Join(holders, "; "), strings.Join(queued, "; "))}
}

// isolationSide names which half of the gate a request is for, as a message spells it.
func isolationSide(exclusive bool) string {
	if exclusive {
		return "exclusive"
	}
	return "shared"
}
