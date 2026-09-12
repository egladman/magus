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

	parent, releaseParent, err := acquireRunIsolation(scope, false, "parent run")
	require.NoError(t, err)

	queued := make(chan struct{})
	go func() {
		// Never satisfiable while the parent holds a seat, which is the point: it only
		// has to be QUEUED for the child behind it to be parked.
		_, release, err := acquireRunIsolation(scope, true, "exclusive peer")
		if err == nil {
			release()
		}
		close(queued)
	}()
	waitForIsolationWaiters(t, isolation, 1)

	childDone := make(chan error, 1)
	go func() {
		_, release, err := acquireRunIsolation(parent, false, "needs child")
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

			ancestor, releaseAncestor, err := acquireRunIsolation(scope, tc.ancestorExclusive, "ancestor")
			require.NoError(t, err)
			defer releaseAncestor()
			require.Equal(t, 1, isolationHolders(isolation), "the ancestor's own acquisition")

			child, releaseChild, err := acquireRunIsolation(ancestor, tc.childExclusive, "child")
			require.NoError(t, err)
			assert.Equal(t, 1, isolationHolders(isolation), "the child took a second hold on the gate")
			assert.Same(t, admissionFrom(ancestor).isolation, admissionFrom(child).isolation,
				"the child runs under a lease that is not its ancestor's")

			// Releasing an inherited lease must hand nothing back: the ancestor is still
			// inside its own region, and an exclusive one is still excluding everyone.
			releaseChild()
			assert.Equal(t, 1, isolationHolders(isolation), "releasing the inherited lease retired the ancestor's hold")
			if tc.ancestorExclusive {
				assert.False(t, isolation.gate.TryAcquire(1), "an exclusive ancestor stopped excluding once its child released")
				return
			}
			require.True(t, isolation.gate.TryAcquire(1), "a shared ancestor's seat is the only one taken")
			isolation.gate.Release(1)
		})
	}
}

// A wedge the engine cannot unwind has to end in a verdict rather than in a hang, and the
// verdict has to say who was holding and who was queued: the 19-minute stall was
// unattributable from the outside, with `magus status` reporting nothing running.
func TestRunIsolationWedgeIsRefusedAndNamesTheWaiters(t *testing.T) {
	restore := isolationWedgeGrace
	isolationWedgeGrace = 50 * time.Millisecond
	t.Cleanup(func() { isolationWedgeGrace = restore })

	root, c := openCache(t)
	// The exclusive step is held behind a gate step rather than raced into place: it can
	// only queue once the holder has taken its seat.
	steps := []Step{
		{ProjectPath: "gate", WorkspaceRoot: root, Target: "run"},
		{ProjectPath: "holder", WorkspaceRoot: root, Target: "run"},
		{ProjectPath: "queued", WorkspaceRoot: root, Target: "run", Exclusive: true, DependsOn: []string{"gate"}},
	}

	// The heartbeat has to be installed and quiet: a wedge is only a wedge when nothing
	// in the invocation is moving, and a run nobody watches is never refused.
	ctx, cancel := context.WithCancel(ContextWithProgress(context.Background(), NewProgress()))
	defer cancel()
	blocked := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		// The budget is what turns the refusal into an unwind: the holder is parked on a
		// wait only this batch's cancellation can end, which is the wedge's own shape.
		_, err := c.RunAll(ctx, steps, func(stepCtx context.Context, s Step) error {
			switch s.ProjectPath {
			case "holder":
				unblock := BlockedOn(stepCtx, "a wait only this test can end")
				defer unblock()
				close(blocked)
				<-stepCtx.Done()
			case "gate":
				select {
				case <-blocked:
				case <-stepCtx.Done():
				}
			}
			return nil
		}, WithLimiter(NewLimiter(4)), WithMaxFailures(1))
		done <- err
	}()

	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		cancel()
		<-done
		t.Fatal("the wedged gate was never refused")
	}

	require.ErrorIs(t, err, types.RunIsolationWedged)
	assert.ErrorContains(t, err, "holder run holds the shared side and is waiting on a wait only this test can end")
	assert.ErrorContains(t, err, "queued run needs the exclusive side")
	var exit types.ExitError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, ExitCodeRunIsolationWedged, exit.Code)
}

// A holder that is still working answers "not wedged" however long it takes, which is
// what keeps a silent-but-busy run (a long link, a test package that prints only when it
// finishes) from being refused out from under a legitimately queued exclusive step.
func TestRunIsolationDoesNotRefuseWhileAHolderIsWorking(t *testing.T) {
	restore := isolationWedgeGrace
	isolationWedgeGrace = 50 * time.Millisecond
	t.Cleanup(func() { isolationWedgeGrace = restore })

	root, c := openCache(t)
	steps := []Step{
		{ProjectPath: "gate", WorkspaceRoot: root, Target: "run"},
		{ProjectPath: "holder", WorkspaceRoot: root, Target: "run"},
		{ProjectPath: "queued", WorkspaceRoot: root, Target: "run", Exclusive: true, DependsOn: []string{"gate"}},
	}

	ctx, cancel := context.WithCancel(ContextWithProgress(context.Background(), NewProgress()))
	defer cancel()
	working := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := c.RunAll(ctx, steps, func(stepCtx context.Context, s Step) error {
			switch s.ProjectPath {
			case "holder":
				close(working)
				time.Sleep(10 * isolationWedgeGrace)
			case "gate":
				select {
				case <-working:
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
		t.Fatal("the batch never finished")
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

// The acquisition order keeps a saturated machine budget from wedging a batch outright.
// The cycle it reproduces, with the budget full and the gate taken first:
//
//  1. the shared step is admitted and holds a seat on the gate and the whole budget;
//  2. the exclusive peer needs every seat, so it waits for the shared step to finish;
//  3. the shared step is waiting for that peer to reach the budget it holds;
//  4. neither can move, and neither is anywhere it would see a cancelled context.
//
// Bounded here rather than left to the package timeout, which reports a hang as an
// infrastructure failure nobody attributes to this.
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

// isolationHolders counts the leases currently holding the gate, which is the direct
// reading of "acquired at most once per path".
func isolationHolders(r *runIsolation) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.holders)
}

// waitForIsolationWaiters blocks until n requests are queued for the gate, so a test can
// order a step against a queue rather than against a sleep.
func waitForIsolationWaiters(t *testing.T, r *runIsolation, n int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		r.mu.Lock()
		got := len(r.waiters)
		r.mu.Unlock()
		if got >= n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("waiters on the isolation gate = %d, want %d", got, n)
		case <-time.After(time.Millisecond):
		}
	}
}
