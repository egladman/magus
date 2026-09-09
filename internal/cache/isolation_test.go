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

func TestYieldRunIsolationLetsExclusiveChildRun(t *testing.T) {
	ctx, releaseParent := acquireRunIsolation(WithRunScope(context.Background()), false)
	t.Cleanup(releaseParent)
	parentLease := admissionFrom(ctx).isolation

	childAcquired := make(chan struct{})
	releaseChild := make(chan struct{})
	childDone := make(chan struct{})
	go func() {
		_, release := acquireRunIsolation(ctx, true)
		close(childAcquired)
		<-releaseChild
		release()
		close(childDone)
	}()

	err := YieldRunIsolation(ctx, func(yielded context.Context) error {
		if got := admissionFrom(yielded).isolation; got != nil {
			t.Errorf("yielded context retained lease: %#v", got)
		}
		select {
		case <-childAcquired:
		case <-time.After(time.Second):
			t.Error("exclusive child did not acquire isolation while parent yielded")
		}
		close(releaseChild)
		select {
		case <-childDone:
		case <-time.After(time.Second):
			t.Error("exclusive child did not release isolation")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("YieldRunIsolation: %v", err)
	}
	if got := admissionFrom(ctx).isolation; got != parentLease {
		t.Fatalf("parent lease after yield = %#v, want %#v", got, parentLease)
	}
}

// TestYieldRunIsolationKeepsAnExclusiveWriteLock proves the property without timing, and
// proves it against the half-fix as well as the bug.
//
// Removing the release alone would pass a test that only watches for overlap, then hang in
// production the first time a child tried to take a lease behind the parent's write lock.
// So this admits one INSIDE the yield: it must return immediately, and the write lock must
// still be held when it does.
func TestYieldRunIsolationKeepsAnExclusiveWriteLock(t *testing.T) {
	ctx := WithRunScope(context.Background())
	isolation := isolationFrom(ctx)

	ctx, release := acquireRunIsolation(ctx, true)
	defer release()

	var admitted bool
	require.NoError(t, YieldRunIsolation(ctx, func(child context.Context) error {
		// TryRLock ACQUIRES on success, so a bare assert.False would leak the read lock and
		// hang the deferred re-Lock instead of failing. Give it straight back.
		requireStillWriteLocked(t, isolation, "the write lock was released during the yield, so the step is not exclusive while it fans out")

		// The half-fix hangs here instead of returning.
		_, innerRelease := acquireRunIsolation(child, false)
		innerRelease()
		admitted = true

		// An EXCLUSIVE child is subsumed too, which is Step.Exclusive's own contract: it
		// excludes BATCH peers, and a needs child admitted through RunAside has no batch.
		// The ancestor's write lock already excludes every one of them. Pinned because the
		// alternative reading - nest a fresh region so it excludes its siblings - is the
		// change someone will otherwise make on the way past.
		_, exclusiveRelease := acquireRunIsolation(child, true)
		exclusiveRelease()
		requireStillWriteLocked(t, isolation, "an exclusive child inside the region took a lease of its own")

		requireStillWriteLocked(t, isolation, "a child inside the region took and released a lease of its own")
		return nil
	}))
	assert.True(t, admitted, "the yield never admitted a child")
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

// TestRunAllExclusiveStepQueuesForTheMachineWithoutTheLease pins the acquisition order
// that keeps a saturated machine budget from wedging a batch outright.
//
// The cycle it reproduces, with the budget full and the lease taken first:
//
//  1. the shared step is admitted and holds the read lock and the whole budget;
//  2. its body yields, dropping the read lock so its children can be admitted;
//  3. the exclusive peer takes the write lock, since there are no readers left;
//  4. it queues for the budget the shared step holds, still holding the write lock;
//  5. the shared step returns and parks re-taking its read lock behind that writer.
//
// Neither end can move and neither is anywhere it would see a cancelled context, so the
// batch hangs until the process is killed. Bounded rather than left to the package
// timeout: a deadlock a test can only express as a hang reports as an infrastructure
// failure, in a file nobody attributes to this behavior.
func TestRunAllExclusiveStepQueuesForTheMachineWithoutTheLease(t *testing.T) {
	// Memory-only arbitration (unlimited slots), so the two 9 GB steps are what
	// saturates the budget and the gate step below never competes for a seat.
	adm := &queueingAdmitter{
		budget:  NewMachineBudget(10_000, 0),
		project: "exclusive",
		queued:  make(chan struct{}),
	}
	root, c := openCache(t, WithMachineAdmission(adm, false))

	yielded := make(chan struct{})
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
				return YieldRunIsolation(stepCtx, func(context.Context) error {
					close(yielded)
					select {
					case <-adm.queued:
					case <-stepCtx.Done():
					}
					return nil
				})
			case "gate":
				// The exclusive step is held here rather than raced into place: its
				// write lock is takeable only once every reader is gone, which means
				// after the shared step has yielded and this one has returned.
				select {
				case <-yielded:
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
		// Cancelling frees the exclusive step from the budget, which drops the write
		// lock and lets the parked peer finish, so the batch unwinds before the
		// temp-dir cleanup this failure is about to trigger.
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
		t.Fatal("the batch deadlocked: the exclusive step queued for the machine budget holding the isolation write lock the shared step needs back")
	}
}

// requireStillWriteLocked fails when isolation's write lock is not held, without keeping
// the read lock TryRLock takes on success.
func requireStillWriteLocked(t *testing.T, isolation *runIsolation, msg string) {
	t.Helper()
	if isolation.mu.TryRLock() {
		isolation.mu.RUnlock()
		t.Error(msg)
	}
}
