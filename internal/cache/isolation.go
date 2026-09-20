package cache

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// isolationSeats is the gate's width: a shared request takes one seat, an exclusive one
// takes every seat. Wide enough that the shared side is never the scarce resource, since
// bounding concurrency is the limiter's job and not this gate's.
const isolationSeats = 1 << 20

// runIsolation serializes Step.Exclusive steps against the rest of one invocation: an
// exclusive step takes the whole gate, every other step takes one seat.
//
// ONE ACQUISITION PER PATH keeps it deadlock-free: [acquireRunIsolation] inherits whatever
// lease the context carries, so dispatched work never queues for a gate its own ancestor
// holds. Without that, a ctx.needs child wanting the exclusive side waited on its parent's
// shared seat and parked every later request behind it, wedging a run for 19 minutes.
//
// Inheriting gives up nothing: Exclusive is a BATCH claim, dispatched work arrives through
// RunAside outside any batch, and the ancestor's lease already excludes every batch peer.
//
// This gate deliberately carries no wedge detector. One lived here and answered about a
// stranger, because a composite step's lease points at whichever descendant admitted last;
// it also refused healthy fan-outs. The inheritance rule above prevents the shape it looked
// for, TestRunIsolationNeedsChildInheritsUnderAFanOut pins that, and MGS3012 catches a hang
// that escapes it. docs/decisions/0001 carries the full reasoning and what replaces it.
type runIsolation struct {
	gate *semaphore.Weighted
}

func newRunIsolation() *runIsolation {
	return &runIsolation{gate: semaphore.NewWeighted(isolationSeats)}
}

type runIsolationKey struct{}

// runIsolationLease is one path's hold on the gate. A context carrying one is INSIDE the
// region, acquired or inherited; [acquireRunIsolation] reads its presence and nothing more.
// Anything else hung on it aliases across a fan-out, since siblings share their ancestor's.
type runIsolationLease struct {
	isolation *runIsolation
	exclusive bool
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
// context it runs under plus the release for what it took.
//
// A context that already carries a lease inherits it: nothing is taken, the release is a
// no-op, and an exclusive request under a shared ancestor is answered by the ancestor's
// admission rather than by upgrading it. See runIsolation for why that is the policy and
// not a shortcut.
//
// The wait is cancellable, so a sibling's failure or a Ctrl-C reaches a parked step, and
// it returns that cause unchanged.
func acquireRunIsolation(ctx context.Context, exclusive bool) (context.Context, func(), error) {
	ctx = WithRunScope(ctx)
	if lease := admissionFrom(ctx).isolation; lease != nil {
		return ctx, func() {}, nil
	}
	isolation := isolationFrom(ctx)
	lease, release, err := isolation.acquire(ctx, exclusive)
	if err != nil {
		return ctx, func() {}, err
	}
	held := admissionFrom(ctx)
	held.isolation = lease
	return held.on(ctx), release, nil
}

func (r *runIsolation) acquire(ctx context.Context, exclusive bool) (*runIsolationLease, func(), error) {
	weight := int64(1)
	if exclusive {
		weight = isolationSeats
	}
	// Marked as a blocking hold for the same reason acquireWatched marks its own: a step
	// already holding slots that parks here is the hold-and-wait half of a deadlock, and
	// this is the only way into the queue, so the mark cannot be forgotten. The SLOT
	// watch is what reads that mark (MGS3013); this gate reads nothing.
	unblock := BlockedOn(ctx, "the run-isolation gate ("+isolationSide(exclusive)+")")
	defer unblock()

	if err := r.gate.Acquire(ctx, weight); err != nil {
		return nil, nil, err
	}
	return &runIsolationLease{isolation: r, exclusive: exclusive}, func() { r.gate.Release(weight) }, nil
}

// isolationSide names which half of the gate a request is for, as a message spells it.
func isolationSide(exclusive bool) string {
	if exclusive {
		return "exclusive"
	}
	return "shared"
}
