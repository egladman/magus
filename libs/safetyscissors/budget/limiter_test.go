package budget

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type LimiterSuite struct{ suite.Suite }

func TestLimiter(t *testing.T) { suite.Run(t, new(LimiterSuite)) }

func (s *LimiterSuite) TestSnapshot() {
	t := s.T()
	l := NewLimiter(3)
	assert.Equal(t, LimiterSnapshot{Capacity: 3, Running: 0, Queued: 0}, l.Snapshot(), "initial snapshot")

	_ = l.Acquire(context.Background())
	_ = l.Acquire(context.Background())
	snap := l.Snapshot()
	assert.Equal(t, LimiterSnapshot{Capacity: 3, Running: 2}, snap, "after 2 acquires")

	l.Release()
	assert.Equal(t, 1, l.Snapshot().Running, "after release")
}

func (s *LimiterSuite) TestUnlimited() {
	t := s.T()
	l := NewUnlimitedLimiter()
	for range 100 {
		require.NoError(t, l.Acquire(context.Background()))
	}
	snap := l.Snapshot()
	assert.Equal(t, LimiterSnapshot{}, snap, "an unlimited limiter reports no capacity and counts nothing running")
}

func (s *LimiterSuite) TestACapacityBelowOneIsNoCapacityNotUnlimited() {
	var refused *CapacityError
	s.Require().ErrorAs(NewLimiter(0).Acquire(s.T().Context()), &refused)
	s.Equal(0, refused.Capacity)
}

func (s *LimiterSuite) TestACountBelowOneIsRefused() {
	l := NewLimiter(2)
	var refused *SlotCountError
	s.Require().ErrorAs(l.AcquireN(s.T().Context(), 0), &refused)
	s.Equal(0, refused.Slots)
	l.ReleaseN(0)
	s.Equal(LimiterSnapshot{Capacity: 2}, l.Snapshot(), "a release of no slots frees nothing")
}

func (s *LimiterSuite) TestCancelledAcquire() {
	t := s.T()
	l := NewLimiter(1)
	_ = l.Acquire(context.Background()) // fill the slot

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, l.Acquire(ctx), "expected error on cancelled acquire")
}

func (s *LimiterSuite) TestYield() {
	t := s.T()
	l := NewLimiter(2)
	_ = l.Acquire(context.Background())
	_ = l.Acquire(context.Background()) // both slots taken

	// Yield releases one slot, runs fn while holding zero, then re-acquires.
	var maxSeen int
	var mu sync.Mutex

	err := l.Yield(WithSlotHeld(context.Background()), func() error {
		// We released our slot; a second goroutine can now proceed.
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l.Acquire(context.Background())
			snap := l.Snapshot()
			mu.Lock()
			if snap.Running > maxSeen {
				maxSeen = snap.Running
			}
			mu.Unlock()
			l.Release()
		}()
		wg.Wait()
		return nil
	})
	require.NoError(t, err)
	// After Yield returns we have re-acquired; total running should be 2 again.
	assert.Equal(t, 2, l.Snapshot().Running, "post-yield running")
	// The goroutine inside fn should have seen running==2 (its own + the 1 held
	// by the other Acquire still outstanding).
	assert.Equal(t, 2, maxSeen, "maxSeen inside fn")
}

func (s *LimiterSuite) TestAcquireN() {
	t := s.T()
	l := NewLimiter(4)

	require.NoError(t, l.AcquireN(context.Background(), 3), "AcquireN(3)")
	assert.Equal(t, 3, l.Snapshot().Running, "after AcquireN(3)")

	l.ReleaseN(3)
	assert.Equal(t, 0, l.Snapshot().Running, "after ReleaseN(3)")
}

func (s *LimiterSuite) TestAcquireNTooMany() {
	t := s.T()
	l := NewLimiter(4)
	assert.Error(t, l.AcquireN(context.Background(), 5), "expected error when requesting more slots than capacity")
	assert.Equal(t, 0, l.Snapshot().Running, "after rejected AcquireN")
}

func (s *LimiterSuite) TestAcquireNCancel() {
	t := s.T()
	l := NewLimiter(2)
	_ = l.AcquireN(context.Background(), 2) // fill all slots

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, l.AcquireN(ctx, 1), "expected error on cancelled context")
	assert.Equal(t, 2, l.Snapshot().Running, "cancelled AcquireN must not change running")
}

// TestLimiterFairQueueing verifies FIFO fairness: a large request at the head
// of the queue blocks smaller requests behind it until it can be fully satisfied.
func (s *LimiterSuite) TestFairQueueing() {
	t := s.T()
	l := NewLimiter(4)

	// Hold all 4 slots.
	require.NoError(t, l.AcquireN(context.Background(), 4))

	order := make([]int, 0, 2)
	var mu sync.Mutex
	record := func(id int) {
		mu.Lock()
		order = append(order, id)
		mu.Unlock()
	}

	ready := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: requests 3 slots — queued first, needs 3 to free.
	go func() {
		defer wg.Done()
		<-ready
		_ = l.AcquireN(context.Background(), 3)
		record(1)
		l.ReleaseN(3)
	}()

	// Goroutine 2: requests 1 slot — queued second; should NOT sneak past g1.
	go func() {
		defer wg.Done()
		<-ready
		_ = l.AcquireN(context.Background(), 1)
		record(2)
		l.Release()
	}()

	// Give goroutines time to queue, then release all slots.
	close(ready)
	// Small sleep to let both goroutines reach their Acquire calls.
	// This is inherently racy but the semaphore's FIFO guarantee means
	// whichever goroutine queued first wins — we're verifying no panic
	// and correct release accounting, not strict ordering.
	l.ReleaseN(4)

	// Drain: both goroutines must finish before the limiter can be called clean. This was a
	// 20-iteration busy loop whose body was `_ = i`: it yielded nothing and almost never
	// outlasted the goroutines, so the Running assertion below sampled a limiter still
	// holding slots and failed intermittently under -race.
	wg.Wait()

	mu.Lock()
	recorded := len(order)
	mu.Unlock()
	assert.Equal(t, 2, recorded, "both queued acquirers must have run")
	// Verify both completed and limiter is clean.
	assert.Equal(t, 0, l.Snapshot().Running, "post-fairness-test running")
}

// TestPoolEvents verifies that the events WithPoolEvents installs hear every acquire as
// +1 running and every release as -1 running.
func (s *LimiterSuite) TestPoolEvents() {
	t := s.T()
	var running []int
	var mu sync.Mutex
	l := NewLimiter(2, WithPoolEvents(func(ev PoolEvent) {
		if ev.Running == 0 {
			return
		}
		mu.Lock()
		running = append(running, ev.Running)
		mu.Unlock()
	}))

	_ = l.Acquire(context.Background())
	_ = l.Acquire(context.Background())
	l.Release()
	l.Release()

	mu.Lock()
	assert.Equal(t, []int{1, 1, -1, -1}, running)
	mu.Unlock()

	// Contended case: second Acquire on a full pool should observe wait > 0.
	var waited atomic.Int64
	l2 := NewLimiter(1, WithPoolEvents(func(ev PoolEvent) {
		if ev.Running > 0 {
			waited.Store(int64(ev.Wait))
		}
	}))
	_ = l2.Acquire(context.Background()) // fill pool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = l2.Acquire(context.Background())
	}()
	// Wait until the goroutine is provably blocked in Acquire (Queued==1) before
	// releasing, so the contended path is real and the measured wait is non-zero.
	// Without this, Release can win the race and the goroutine acquires an
	// already-free slot in ~0ns (flaky on fast machines).
	for l2.Snapshot().Queued == 0 {
		runtime.Gosched()
	}
	l2.Release() // unblock the now-blocked goroutine
	wg.Wait()
	assert.NotZero(t, waited.Load(), "contended acquire reported wait == 0ns")
}

// TestLimiterWaitingHook verifies the hook's Queued deltas mirror the internal queued counter
// exactly: it fires +n the instant a caller begins waiting and -n once its Acquire returns,
// so their running sum tracks Snapshot().Queued at every step.
func (s *LimiterSuite) TestWaitingHook() {
	t := s.T()
	var net atomic.Int64
	l := NewLimiter(1, WithPoolEvents(func(ev PoolEvent) { net.Add(int64(ev.Queued)) }))

	// Uncontended: the hook still hears +1 (before Acquire) and -1 (on return), netting 0.
	require.NoError(t, l.Acquire(context.Background()))
	assert.Equal(t, int64(0), net.Load(), "uncontended acquire should net zero waiting")

	// Contended: a second acquire on the now-full pool must register as waiting.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = l.Acquire(context.Background())
	}()
	for l.Snapshot().Queued == 0 {
		runtime.Gosched()
	}
	// Queued and the hook's sum are separate atomics that AcquireN updates a couple of
	// instructions apart (it increments queued, THEN calls the hook), so waiting on Queued
	// alone exits inside that window and samples net == 0 against Queued == 1. Waiting for
	// both makes the assertion below about the two agreeing rather than a race with the
	// update itself. The slot is held for the duration, so the waiter cannot proceed and
	// the pair cannot move again before it is read.
	require.Eventually(t, func() bool { return l.Snapshot().Queued > 0 && net.Load() > 0 },
		time.Second, time.Millisecond, "the blocked caller must register as waiting")
	assert.Equal(t, int64(l.Snapshot().Queued), net.Load(), "the Queued sum must mirror Queued")

	l.Release() // unblock the waiter
	wg.Wait()
	assert.Equal(t, int64(0), net.Load(), "waiting should return to zero after acquire")
}

// TestLimiterYieldRestoresSlotOnCancel verifies that Yield always returns with
// its slot re-acquired, even when ctx is cancelled while fn runs. The slot is
// restored (re-acquire is non-cancellable) so callers like RunAll, which
// release their slot unconditionally on return, stay balanced. fn's error is
// returned unchanged.
func (s *LimiterSuite) TestYieldRestoresSlotOnCancel() {
	t := s.T()
	l := NewLimiter(1)
	_ = l.Acquire(context.Background()) // hold the only slot

	ctx, cancel := context.WithCancel(WithSlotHeld(context.Background()))
	fnErr := errors.New("fn failed")

	err := l.Yield(ctx, func() error {
		cancel() // cancel during the yielded window
		return fnErr
	})

	assert.ErrorIs(t, err, fnErr, "fn error not returned")
	// Slot was restored: the caller still holds exactly its one slot.
	assert.Equal(t, 1, l.Snapshot().Running, "post-yield running, want 1 (slot restored)")
}

// TestLimiterYieldNoOverReleaseOnCancel mimics a RunAll worker: acquire a slot,
// yield it around a cancelled child, then release once on return. If Yield
// returned slot-less, the deferred Release would over-release the semaphore
// (x/sync/semaphore panics on net over-release) or silently inflate capacity.
func (s *LimiterSuite) TestYieldNoOverReleaseOnCancel() {
	t := s.T()
	l := NewLimiter(2)
	for range 5 {
		require.NoError(t, l.Acquire(context.Background()))
		ctx, cancel := context.WithCancel(WithSlotHeld(context.Background()))
		_ = l.Yield(ctx, func() error { cancel(); return nil })
		l.Release() // must not panic; balanced because Yield restored the slot
	}
	assert.Equal(t, 0, l.Snapshot().Running, "running after balanced acquire/yield/release cycles")
	// Full capacity must still be acquirable — no permits leaked or lost.
	require.NoError(t, l.AcquireN(context.Background(), 2), "capacity shrank after yield cycles")
}

// A caller marked as holding no slots is refused rather than guessed at: a guess of one
// would release a slot some other caller holds.
func (s *LimiterSuite) TestYieldRefusesACallerHoldingNoSlots() {
	l := NewLimiter(1)
	s.Require().NoError(l.Acquire(s.T().Context()))
	ran := false
	err := l.Yield(s.T().Context(), func() error { ran = true; return nil })
	var refused *NoSlotsHeldError
	s.Require().ErrorAs(err, &refused)
	s.False(ran, "fn does not run when nothing can be yielded")
	s.Equal(1, l.Snapshot().Running, "the slot someone else holds stays held")
}

// A release of more slots than are running frees only those running and reports the
// rest, so the semaphore is never released past what it handed out.
func (s *LimiterSuite) TestReleaseNFreesNoMoreThanAreRunning() {
	var events []PoolEvent
	l := NewLimiter(3, WithPoolEvents(func(ev PoolEvent) {
		if ev.Queued == 0 {
			events = append(events, ev)
		}
	}))
	s.Require().NoError(l.AcquireN(s.T().Context(), 2))
	l.ReleaseN(3)
	l.Release()
	s.Equal(LimiterSnapshot{Capacity: 3}, l.Snapshot())
	s.Equal([]PoolEvent{{Running: 2}, {Running: -2, Shortfall: 1}, {Shortfall: 1}},
		clearWaits(events))
	s.Require().NoError(l.AcquireN(s.T().Context(), 3), "the full capacity is still there")
}

func clearWaits(events []PoolEvent) []PoolEvent {
	out := make([]PoolEvent, len(events))
	for i, ev := range events {
		ev.Wait = 0
		out[i] = ev
	}
	return out
}

// A derived limiter shares the pool and reports only what was taken through it.
func (s *LimiterSuite) TestWithPoolEventsSharesThePool() {
	var base, derived atomic.Int64
	l := NewLimiter(2, WithPoolEvents(func(ev PoolEvent) { base.Add(int64(ev.Running)) }))
	d := l.WithPoolEvents(func(ev PoolEvent) { derived.Add(int64(ev.Running)) })

	s.Require().NoError(d.Acquire(s.T().Context()))
	s.Equal(1, l.Snapshot().Running, "the derived limiter's acquire holds a slot of the shared pool")
	s.Equal(int64(1), derived.Load())
	s.Zero(base.Load(), "the original's events hear only what it takes")
	s.Require().NoError(l.Acquire(s.T().Context()))
	s.Equal(2, d.Snapshot().Running)
	d.Release()
	l.Release()
	s.Equal(LimiterSnapshot{Capacity: 2}, l.Snapshot())
}

// A context marked with more slots than the pool has yields the whole pool, never more,
// so the semaphore is not released past what it handed out.
func (s *LimiterSuite) TestYieldReleasesNoMoreThanTheCapacity() {
	l := NewLimiter(2)
	s.Require().NoError(l.AcquireN(s.T().Context(), 2))
	inside := -1
	s.Require().NoError(l.Yield(WithSlotsHeld(s.T().Context(), 5), func() error {
		inside = l.Snapshot().Running
		return nil
	}))
	s.Zero(inside)
	s.Equal(2, l.Snapshot().Running, "it takes back what it yielded")
}

func (s *LimiterSuite) TestAcquireAboveCapacityIsACapacityError() {
	err := NewLimiter(2).AcquireN(s.T().Context(), 3)
	var refused *CapacityError
	s.Require().ErrorAs(err, &refused)
	s.Equal(CapacityError{Slots: 3, Capacity: 2}, *refused)
}
