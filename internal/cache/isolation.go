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
//
// # Why there is no verdict here any more
//
// This type used to carry a wedge detector (MGS3015, exit 70) that refused a run when every
// gate holder looked parked. It was deleted on 2026-09-19 because it could not answer
// correctly, in three independent ways:
//
//   - It read the WRONG RECORD. [Cache.admit] attached each admitted step's slot record to
//     the lease its context carried, and a ctx.needs member inherits its ANCESTOR's lease,
//     so a composite step's lease pointed at whichever descendant admitted last and then at
//     a retired record whose blocked field stays empty forever. Measured with a probe: the
//     parent read blocked="1 build slot(s)" while the gate saw an empty string.
//   - For a LEAF step it could not fire at all. The lease has no slot record until admit
//     returns, and the only other waits it could observe are the cache lock, which beats the
//     invocation heartbeat every 15s so the silence condition never opens, and the slot
//     queue, whose own verdict (MGS3013) fires at 3s against this one's 30s.
//   - What it did fire on was healthy. A holder mid-fan-out has handed its slots back, which
//     the pool counts as stalled, so one generator silent for the grace refused a whole run:
//     that is the CI failure this branch started from.
//
// The 2026-09-11 shape it was built for is prevented upstream by the inheritance rule
// above, not detected here, and TestRunIsolationNeedsChildInheritsUnderAFanOut pins that
// rule directly. A hang that escapes it is caught by the stall watchdog (MGS3012), whose
// subject is the whole invocation rather than this gate's shape. Detecting a cycle rather
// than guessing at one needs a wait-for graph over every resource in this package, which is
// a different design than three timers with three graces.
// A semaphore and nothing else. The bookkeeping that used to sit beside it (holders,
// waiters, a heartbeat, a captured context, a grace timer) existed only to feed the verdict
// described above, and went with it.
type runIsolation struct {
	gate *semaphore.Weighted
}

func newRunIsolation() *runIsolation {
	return &runIsolation{gate: semaphore.NewWeighted(isolationSeats)}
}

type runIsolationKey struct{}

// runIsolationLease is one path's hold on the gate. A context carrying one is INSIDE the
// region, whether it acquired the lease itself or inherited it from an ancestor, and that
// is the whole of what a lease is for: [acquireRunIsolation] reads its presence and takes
// nothing more.
//
// It no longer carries the holder's slot record. Attaching one aliased every composite
// step's lease to whichever descendant admitted last, which is the defect that made the
// deleted verdict answer about a stranger.
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
// context it runs under plus the release for what it took. label names the step in a
// refusal.
//
// A context that already carries a lease inherits it: nothing is taken, the release is a
// no-op, and an exclusive request under a shared ancestor is answered by the ancestor's
// admission rather than by upgrading it. See runIsolation for why that is the policy and
// not a shortcut.
//
// The wait is cancellable, so a sibling's failure or a Ctrl-C reaches a parked step, and
// it returns that cause unchanged.
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
	// this is the only way into the queue, so the mark cannot be forgotten. The SLOT
	// watch reads that mark (MGS3013); this gate no longer reads anything.
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
