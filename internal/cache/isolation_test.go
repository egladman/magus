package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// The wedge that held the gate for 19 minutes on 2026-09-11, at its smallest: a composed
// target's skip-cache gate is dispatched through RunAside from inside a step that already
// holds the shared side, and asks for the exclusive one. It waited on its own ancestor,
// and every later shared request parked behind it.
//
// Driven under a deadline because the failure mode is a hang: without the deadline this
// reports as a test binary timeout, which reads as infrastructure rather than as this.
func TestRunAsideExclusiveChildUnderASharedStepCompletes(t *testing.T) {
	root, c := openCache(t)

	// The peer holds the shared side for the whole of the child's acquisition, so the
	// wedge is the real one: an exclusive request that can be satisfied by no drain.
	peerAdmitted := make(chan struct{})
	childDone := make(chan struct{})
	steps := []Step{
		{ProjectPath: "composer", WorkspaceRoot: root, Target: "run"},
		{ProjectPath: "peer", WorkspaceRoot: root, Target: "run"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.RunAll(ctx, steps, func(stepCtx context.Context, s Step) error {
			if s.ProjectPath == "peer" {
				close(peerAdmitted)
				select {
				case <-childDone:
				case <-stepCtx.Done():
				}
				return nil
			}
			select {
			case <-peerAdmitted:
			case <-stepCtx.Done():
			}
			gate := Step{ProjectPath: "composer", WorkspaceRoot: root, Target: "generate", Exclusive: true}
			_, err := c.RunAside(stepCtx, gate, func(context.Context) error { return nil })
			close(childDone)
			return err
		}, WithLimiter(NewLimiter(4)))
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		t.Fatal("the run deadlocked: a needs child asked for the exclusive side of the isolation gate its own ancestor holds")
	}
}

// The other half of the same shape, and the reason a HALF fix is not one: a shared child
// under a shared ancestor re-acquires cheaply until an exclusive request is queued, and
// Go's semaphore parks every later request behind that one. The child then waits for a
// gate its ancestor will not release until the child returns.
func TestRunIsolationSharedChildAdmittedBehindAQueuedExclusiveRequest(t *testing.T) {
	scope := WithRunScope(context.Background())
	isolation := isolationFrom(scope)

	parent, releaseParent, err := acquireRunIsolation(scope, false)
	require.NoError(t, err)

	queued := make(chan struct{})
	go func() {
		// Never satisfiable while the parent holds a seat, which is the point: it only
		// has to be QUEUED for the child behind it to be parked.
		_, release, err := acquireRunIsolation(scope, true)
		if err == nil {
			release()
		}
		close(queued)
	}()
	waitForGateWaiter(t, isolation)

	childDone := make(chan error, 1)
	go func() {
		_, release, err := acquireRunIsolation(parent, false)
		if err == nil {
			release()
		}
		childDone <- err
	}()

	select {
	case err := <-childDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("a shared child under a shared ancestor parked behind a queued exclusive request")
	}
	releaseParent()
	<-queued
}

// The invariant, stated as a table: a run scope takes the gate at most once per path, and
// a nested request of either kind under either kind of lease inherits. Pinned over all
// four combinations because the bug was one cell of it (exclusive under shared) and an
// earlier fix covered only another (anything under exclusive).
func TestRunIsolationNestedRequestsInheritTheAncestorLease(t *testing.T) {
	for _, tc := range []struct {
		name              string
		ancestorExclusive bool
		childExclusive    bool
	}{
		{"shared child under a shared ancestor", false, false},
		{"exclusive child under a shared ancestor", false, true},
		{"shared child under an exclusive ancestor", true, false},
		{"exclusive child under an exclusive ancestor", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := WithRunScope(context.Background())
			isolation := isolationFrom(scope)

			ancestor, releaseAncestor, err := acquireRunIsolation(scope, tc.ancestorExclusive)
			require.NoError(t, err)
			defer releaseAncestor()
			wantSeats := int64(1)
			if tc.ancestorExclusive {
				wantSeats = -1
			}
			require.Equal(t, wantSeats, heldSeats(t, isolation), "the ancestor's own acquisition")

			child, releaseChild, err := acquireRunIsolation(ancestor, tc.childExclusive)
			require.NoError(t, err)
			assert.Equal(t, wantSeats, heldSeats(t, isolation), "the child took a seat of its own")
			assert.Same(t, admissionFrom(ancestor).isolation, admissionFrom(child).isolation,
				"the child runs under a lease that is not its ancestor's")

			// Releasing an inherited lease must hand nothing back: the ancestor is still
			// inside its own region, and an exclusive one is still excluding everyone.
			releaseChild()
			assert.Equal(t, wantSeats, heldSeats(t, isolation), "releasing the inherited lease handed back a seat")
			if tc.ancestorExclusive {
				assert.False(t, isolation.gate.TryAcquire(1), "an exclusive ancestor stopped excluding once its child released")
				return
			}
			require.True(t, isolation.gate.TryAcquire(1), "a shared ancestor's seat is the only one taken")
			isolation.gate.Release(1)
		})
	}
}

// queueingAdmitter answers from an in-process budget and reports the first request for
// one project that the budget does not grant, which is the moment that project's step
// has begun waiting for the machine.
type queueingAdmitter struct {
	budget  *MachineBudget
	project string
	once    sync.Once
	queued  chan struct{}
}

func (a *queueingAdmitter) Request(_ context.Context, waiter string, c types.MachineClaim) (types.MachineVerdict, error) {
	v := a.budget.Request(waiter, c)
	if !v.Granted && c.Project == a.project {
		a.once.Do(func() { close(a.queued) })
	}
	return v, nil
}

func (a *queueingAdmitter) Release(_ context.Context, id string)  { a.budget.Release(id) }
func (a *queueingAdmitter) Drop(_ context.Context, waiter string) { a.budget.Drop(waiter) }

// The acquisition order keeps a saturated machine budget from wedging a batch outright:
// the machine claim is taken BEFORE the gate, so a step queued for the budget is holding
// no seat anybody needs. Bounded here rather than left to the package timeout, which
// reports a hang as an infrastructure failure nobody attributes to this.
func TestRunAllExclusiveStepQueuesForTheMachineWithoutTheLease(t *testing.T) {
	// Memory-only arbitration (unlimited slots), so the two 9 GB steps are what
	// saturates the budget and the gate step below never competes for a seat.
	adm := &queueingAdmitter{
		budget:  NewMachineBudget(10_000, 0),
		project: "exclusive",
		queued:  make(chan struct{}),
	}
	root, c := openCache(t, WithMachineAdmission(adm, false))

	started := make(chan struct{})
	steps := []Step{
		{ProjectPath: "gate", WorkspaceRoot: root, Target: "run"},
		{ProjectPath: "shared", WorkspaceRoot: root, Target: "run", MemoryMB: 9_000},
		{
			ProjectPath: "exclusive", WorkspaceRoot: root, Target: "run",
			MemoryMB: 9_000, Exclusive: true, DependsOn: []string{"gate"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.RunAll(ctx, steps, func(stepCtx context.Context, s Step) error {
			switch s.ProjectPath {
			case "shared":
				close(started)
				select {
				case <-adm.queued:
				case <-stepCtx.Done():
				}
			case "gate":
				// The exclusive step is held here rather than raced into place: it
				// needs every seat on the gate, which means after the shared step has
				// taken its own and this one has returned.
				select {
				case <-started:
				case <-stepCtx.Done():
				}
			}
			return nil
		}, WithLimiter(NewLimiter(4)))
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		// Cancelling frees the exclusive step from the budget, which lets the shared
		// step's wait end, so the batch unwinds before the temp-dir cleanup this failure
		// is about to trigger.
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
		t.Fatal("the batch deadlocked: the exclusive step queued for the machine budget holding the isolation gate the shared step needs")
	}
}

// waitForGateWaiter blocks until a request is queued for the gate, so a test can order a
// step against a queue rather than against a sleep.
//
// Read off the semaphore itself, because the gate keeps no waiter bookkeeping any more:
// Go's TryAcquire refuses while anything is queued, so with seats plentiful a refused
// single-seat request means exactly one thing, that somebody is waiting.
func waitForGateWaiter(t *testing.T, r *runIsolation) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if r.gate.TryAcquire(1) {
			r.gate.Release(1)
		} else {
			return
		}
		select {
		case <-deadline:
			t.Fatal("no request ever queued for the isolation gate")
		case <-time.After(time.Millisecond):
		}
	}
}

// heldSeats reports how many seats the gate currently has out, by taking every remaining
// one. It replaces a direct read of a holders map: the gate keeps no bookkeeping beyond
// the semaphore now, and the semaphore is the thing the invariant is actually about.
//
// Returns -1 when the gate is held exclusively, since no seat is free to count against.
func heldSeats(t *testing.T, r *runIsolation) int64 {
	t.Helper()
	for n := int64(0); n <= 4; n++ {
		if r.gate.TryAcquire(isolationSeats - n) {
			r.gate.Release(isolationSeats - n)
			return n
		}
	}
	return -1
}

// This is load-bearing in a way the sibling inheritance test is not. With the wedge verdict
// deleted, inheritance is the ONLY thing preventing the 2026-09-11 shape: a needs child
// asking for the exclusive side while its own parent holds the shared one. If
// SharedStepContext ever stopped carrying the parent's admission, that child would queue
// behind its own parent and the run would hang until the 15-minute stall watchdog, with
// nothing naming the gate.
func TestRunIsolationNeedsChildInheritsUnderAFanOut(t *testing.T) {
	lim := NewLimiter(4)
	scope := WithRunScope(context.Background())
	isolation := isolationFrom(scope)

	parent, releaseParent, err := acquireRunIsolation(scope, false)
	require.NoError(t, err)
	defer releaseParent()
	// Really taken from the limiter, because Yield hands back exactly what the context
	// says is held; a marker without the acquisition releases a slot that was never taken.
	require.NoError(t, lim.AcquireN(context.Background(), 1))
	parent = withSlotHold(WithSlotsHeld(parent, 1), lim.watch.admit("parent", 1))
	require.Equal(t, int64(1), heldSeats(t, isolation))

	// The base a dispatched member's context is spliced onto, as RunAll installs it.
	base, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	parent = context.WithValue(parent, sharedStepBaseKey{}, base)

	require.NoError(t, lim.Yield(parent, func() error {
		// Mid-fan-out, with the parent's slots handed back: the shape that used to be
		// refused, and the one a dispatched member really runs in.
		member := SharedStepContext(parent)
		child, releaseChild, err := acquireRunIsolation(member, true)
		require.NoError(t, err, "an exclusive member queued instead of inheriting")
		defer releaseChild()
		assert.Equal(t, int64(1), heldSeats(t, isolation), "the member took a seat of its own")
		assert.Same(t, admissionFrom(parent).isolation, admissionFrom(child).isolation,
			"the member runs under a lease that is not its parent's")
		return nil
	}))
}
