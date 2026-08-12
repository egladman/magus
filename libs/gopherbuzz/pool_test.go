package buzz

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDispatchRejectsCycleWithMemo is the regression for the cyclic-magusfile
// deadlock: with a TargetMemo active (as on every `magus run`), a target that
// dispatches one of its own ancestors must error promptly rather than subscribe
// to the ancestor's in-flight result and deadlock. The factory must never run —
// the cycle is caught before any session is checked out.
func TestDispatchRejectsCycleWithMemo(t *testing.T) {
	p := newPool(func(context.Context) (*WorkerSession, error) {
		t.Fatal("factory ran; the cycle should be caught before execution")
		return nil, nil
	}, nil, 1)
	defer func() { _ = p.Close() }()

	ctx := WithTargetMemo(context.Background(), NewTargetMemo())

	done := make(chan error, 1)
	go func() {
		// "a" dispatched while "a" is already an ancestor — a self-cycle.
		done <- p.Dispatch(ctx, []string{"a"}, []string{"a"})
	}()

	select {
	case err := <-done:
		require.Error(t, err, "want cycle error")
		assert.Contains(t, err.Error(), "cycle detected", "want cycle error")
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch deadlocked on a cycle instead of erroring")
	}
}

// TestDispatchKeepsSiblingAncestorsPrivate is the true-negative twin of
// TestDispatchRejectsCycleWithMemo: an ACYCLIC fan-out must not be reported as a
// cycle. Only asserting the true positive is what let the aliasing bug ship —
// dispatchInner hands every concurrently-submitted sibling the SAME ancestors slice
// header, so when that header has spare capacity (make([]string, 3, 4), exactly the
// shape a depth-3 dispatch produces under append's 0->1->2->4 growth) each sibling's
// append wrote its own name into the caller's backing array. Every sibling then read
// back whichever one won, and a nested need of a sibling reported a cycle for a graph
// that has none. The three assertions below are each independently fatal to that bug,
// and the sibling appends race under -race.
func TestDispatchKeepsSiblingAncestorsPrivate(t *testing.T) {
	const siblings = 4
	names := make([]string, siblings)
	for i := range names {
		names[i] = fmt.Sprintf("s%d", i)
	}
	leader := names[0]

	// Hold every sibling inside its target body until all of them have entered, so
	// their appends overlap in time (the write/write race) and so the ancestors each
	// one reads back are read after every sibling has appended.
	var entered sync.WaitGroup
	entered.Add(siblings)

	var mu sync.Mutex
	seen := map[string][]string{} // sibling name -> the ancestor stack it observed

	var p *Pool
	p = newPool(func(ctx context.Context) (*WorkerSession, error) {
		targets := make(map[string]vmpackage.Callable, siblings)
		for _, name := range names {
			targets[name] = func(ctx context.Context, _ []vmpackage.Value) (vmpackage.Value, error) {
				entered.Done()
				entered.Wait()
				mu.Lock()
				seen[name] = AncestorsFromContext(ctx)
				mu.Unlock()
				if name != leader {
					return vmpackage.Null, nil
				}
				// The leader needs its siblings; nothing needs the leader, so this is
				// acyclic. Reached through AncestorsFromContext exactly as the ctx.needs
				// binding (bindings/target.go buzzDispatchViaPool) does.
				return vmpackage.Null, p.Dispatch(ctx, names[1:], AncestorsFromContext(ctx))
			}
		}
		return &WorkerSession{Session: NewSession(ctx), Targets: targets}, nil
	}, nil, siblings)
	defer func() { _ = p.Close() }()

	ancestors := make([]string, 3, 4)
	copy(ancestors, []string{"ci", "lint", "vet"})
	ctx := WithTargetMemo(context.Background(), NewTargetMemo())

	done := make(chan error, 1)
	go func() { done <- p.Dispatch(ctx, names, ancestors) }()
	select {
	case err := <-done:
		require.NoError(t, err, "an acyclic fan-out must not report a cycle")
	case <-time.After(10 * time.Second):
		t.Fatal("Dispatch hung on an acyclic fan-out")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, name := range names {
		require.Contains(t, seen, name, "sibling %q did not run", name)
		assert.Equal(t, append([]string{"ci", "lint", "vet"}, name), seen[name],
			"sibling %q must see ITSELF as its last ancestor, not a sibling's name", name)
	}
	assert.Empty(t, ancestors[:cap(ancestors)][3], "a child appended into the caller's backing array")
}

// TestMemoSubscribersShareOneRun exercises the memo's in-flight subscribe/resolve path
// under real contention: every caller of one target either starts it or parks on its
// entry, so the target runs once and every caller sees that run's result. The existing
// memo coverage dispatches sequentially, where the second call always resolves an
// already-CLOSED entry - the cheap half of the path, and the half a cache hit would
// hide.
func TestMemoSubscribersShareOneRun(t *testing.T) {
	const callers = 8
	var runs atomic.Int32
	wantErr := errors.New("kaboom")
	p := newPool(func(ctx context.Context) (*WorkerSession, error) {
		targets := map[string]vmpackage.Callable{
			"slow": func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
				runs.Add(1)
				time.Sleep(10 * time.Millisecond) // widen the in-flight window
				return vmpackage.Null, wantErr
			},
		}
		return &WorkerSession{Session: NewSession(ctx), Targets: targets}, nil
	}, nil, callers)
	defer func() { _ = p.Close() }()

	ctx := WithTargetMemo(context.Background(), NewTargetMemo())
	var start, finished sync.WaitGroup
	start.Add(1)
	errs := make([]error, callers)
	for i := range errs {
		finished.Add(1)
		go func() {
			defer finished.Done()
			start.Wait()
			errs[i] = p.Dispatch(ctx, []string{"slow"}, nil)
		}()
	}
	start.Done()
	finished.Wait()

	assert.Equal(t, int32(1), runs.Load(), "the memo must collapse concurrent needs into one run")
	for i, err := range errs {
		assert.ErrorIs(t, err, wantErr, "caller %d saw a different result than the single run", i)
	}
}

// TestDispatchSiblingFailureLetsPeersFinish covers the error path of a fan-out: one
// sibling failing must not abandon the peers already in flight, and must still record
// its outcome in the memo. A Complete skipped on the error path leaves a permanently
// in-flight entry, which is silent until some later need subscribes to it and hangs -
// so the second Dispatch below, not the first, is the assertion that matters.
func TestDispatchSiblingFailureLetsPeersFinish(t *testing.T) {
	var entered sync.WaitGroup
	entered.Add(2)
	var peerFinished atomic.Bool
	wantErr := errors.New("kaboom")
	p := newPool(func(ctx context.Context) (*WorkerSession, error) {
		targets := map[string]vmpackage.Callable{
			"boom": func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
				// Both siblings park here, so the failure lands while the peer is
				// genuinely in flight. It doubles as the re-run detector: a second
				// execution would drive this WaitGroup negative and panic.
				entered.Done()
				entered.Wait()
				return vmpackage.Null, wantErr
			},
			"peer": func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
				entered.Done()
				entered.Wait()
				peerFinished.Store(true)
				return vmpackage.Null, nil
			},
		}
		return &WorkerSession{Session: NewSession(ctx), Targets: targets}, nil
	}, nil, 2)
	defer func() { _ = p.Close() }()

	ctx := WithTargetMemo(context.Background(), NewTargetMemo())
	err := p.Dispatch(ctx, []string{"boom", "peer"}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.Contains(t, err.Error(), "boom: ", "the failing sibling's name is the error's prefix")
	assert.True(t, peerFinished.Load(), "a sibling failure abandoned a peer mid-flight")

	done := make(chan error, 1)
	go func() { done <- p.Dispatch(ctx, []string{"boom", "peer"}, nil) }()
	select {
	case second := <-done:
		assert.ErrorIs(t, second, wantErr, "the memo must replay the failure, not re-run it")
	case <-time.After(10 * time.Second):
		t.Fatal("a later need of a failed target hung; its memo entry was never completed")
	}
}

// TestTargetMemoWaiterUnblocksOnCtxCancel is the regression for waitFn having no
// ctx escape: before, it was a bare `<-e.done`, so cancelling a run could never
// unblock a caller subscribed to a target that will now never complete (e.g. its
// runner crashed or was itself cancelled without reaching Complete). waitFn must
// return promptly on ctx cancellation instead of blocking forever.
func TestTargetMemoWaiterUnblocksOnCtxCancel(t *testing.T) {
	m := NewTargetMemo()
	isNew, _ := m.TryRun("", "slow")
	require.True(t, isNew, "first TryRun for a name must be new")
	// Deliberately never call m.Complete("slow", ...): the entry stays in-flight
	// forever, standing in for a runner that never finishes.

	_, waitFn := m.TryRun("", "slow")
	require.NotNil(t, waitFn, "second TryRun for an in-flight name must return a waitFn")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- waitFn(ctx) }()

	select {
	case err := <-done:
		require.Error(t, err, "want the ctx deadline to unblock the waiter")
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("waitFn did not return within bound: ctx cancellation did not unblock it")
	}
}

// TestDispatchDetectsSiblingWaitCycle is the regression for the deadlock the
// static ancestor check cannot see: two in-flight SIBLINGS that depend on each
// other. A dispatches [b, c]; b then needs c (ancestors [a, b], c not among
// them — the ancestor check passes) and c needs b (ancestors [a, c], symmetric).
// Before TargetMemo tracked the dynamic wait-for graph, both TryRun calls
// returned a waitFn subscribing to the other's still-running memoEntry, and
// neither entry could ever complete — a permanent hang. Both dispatches must
// now report a cycle within the bound instead.
func TestDispatchDetectsSiblingWaitCycle(t *testing.T) {
	var entered sync.WaitGroup
	entered.Add(2)

	var p *Pool
	p = newPool(func(ctx context.Context) (*WorkerSession, error) {
		targets := map[string]vmpackage.Callable{
			"b": func(ctx context.Context, _ []vmpackage.Value) (vmpackage.Value, error) {
				entered.Done()
				entered.Wait() // widen the window: both siblings are genuinely in-flight
				return vmpackage.Null, p.Dispatch(ctx, []string{"c"}, AncestorsFromContext(ctx))
			},
			"c": func(ctx context.Context, _ []vmpackage.Value) (vmpackage.Value, error) {
				entered.Done()
				entered.Wait()
				return vmpackage.Null, p.Dispatch(ctx, []string{"b"}, AncestorsFromContext(ctx))
			},
		}
		return &WorkerSession{Session: NewSession(ctx), Targets: targets}, nil
	}, nil, 2)
	defer func() { _ = p.Close() }()

	ctx := WithTargetMemo(context.Background(), NewTargetMemo())
	done := make(chan error, 1)
	go func() { done <- p.Dispatch(ctx, []string{"b", "c"}, []string{"a"}) }()

	select {
	case err := <-done:
		require.Error(t, err, "want cycle error")
		assert.Contains(t, err.Error(), "cycle detected", "want cycle error")
	case <-time.After(5 * time.Second):
		t.Fatal("Dispatch deadlocked on a sibling wait-for cycle instead of erroring")
	}
}

// TestDispatchCancelledContextRunsNothing verifies a cancelled run stops at the pool
// boundary: no session is warmed and no target body runs, and every name reports the
// cancellation rather than a nil error that would read as success.
func TestDispatchCancelledContextRunsNothing(t *testing.T) {
	p := newPool(func(context.Context) (*WorkerSession, error) {
		t.Error("a session was warmed for a cancelled dispatch")
		return nil, errors.New("unreachable")
	}, nil, 2)
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithCancel(WithTargetMemo(context.Background(), NewTargetMemo()))
	cancel()
	err := p.Dispatch(ctx, []string{"a", "b"}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// chanSemaphore is a capacity-bounded Semaphore standing in for cache.Limiter, with
// the same contract: Yield releases the caller's slot for the duration of fn and
// re-acquires it with a non-cancellable context, so the caller always returns holding
// what it held.
type chanSemaphore struct {
	slots  chan struct{}
	yields atomic.Int32
}

func (s *chanSemaphore) Acquire(ctx context.Context) error {
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *chanSemaphore) Release() { <-s.slots }

func (s *chanSemaphore) Yield(ctx context.Context, fn func() error) error {
	s.yields.Add(1)
	s.Release()
	defer func() { _ = s.Acquire(context.WithoutCancel(ctx)) }()
	return fn()
}

// TestDispatchNestedYieldsSlotAtCapacityOne is the deadlock-freedom property the Pool
// doc comment claims, at the setting that can actually break it: with one slot, a
// parent holding it must yield before waiting on a child, or the child blocks in
// Acquire on a slot only the parent can free. MAGUS_CONCURRENCY=1 is a supported
// setting and a debugging reflex, so a regression here strands exactly the run someone
// reached for to diagnose something else.
func TestDispatchNestedYieldsSlotAtCapacityOne(t *testing.T) {
	sem := &chanSemaphore{slots: make(chan struct{}, 1)}
	var childRan atomic.Bool
	var p *Pool
	p = newPool(func(ctx context.Context) (*WorkerSession, error) {
		targets := map[string]vmpackage.Callable{
			"parent": func(ctx context.Context, _ []vmpackage.Value) (vmpackage.Value, error) {
				return vmpackage.Null, p.Dispatch(ctx, []string{"child"}, AncestorsFromContext(ctx))
			},
			"child": func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
				childRan.Store(true)
				return vmpackage.Null, nil
			},
		}
		return &WorkerSession{Session: NewSession(ctx), Targets: targets}, nil
	}, func(context.Context) Semaphore { return sem }, 2)
	defer func() { _ = p.Close() }()

	ctx := WithTargetMemo(context.Background(), NewTargetMemo())
	done := make(chan error, 1)
	go func() { done <- p.Dispatch(ctx, []string{"parent"}, nil) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("nested dispatch deadlocked at capacity 1; the parent did not yield its slot")
	}

	assert.True(t, childRan.Load(), "the nested target never ran")
	assert.Equal(t, int32(1), sem.yields.Load(), "the nested dispatch should yield exactly once")
	assert.Empty(t, sem.slots, "every acquired slot was released")
}
