package budget

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// testGrace is the deadlock grace the suite's limiters wait out, far below the default
// so a refusal does not cost every test three seconds.
const testGrace = 50 * time.Millisecond

func newTestLimiter(t *testing.T, n int) *Limiter {
	t.Helper()
	return NewLimiter(n, withDeadlockGrace(testGrace))
}

// withDeadlockGrace sets how long the pool must stay wedged before a wait is refused, so
// the suite need not wait out [DefaultDeadlockGrace].
func withDeadlockGrace(d time.Duration) LimiterOption {
	return func(o *limiterOptions) { o.grace = d }
}

func requireDeadlock(t *testing.T, err error) {
	t.Helper()
	var d SlotDeadlock
	require.True(t, errors.As(err, &d), "want a SlotDeadlock, got %v", err)
}

type WatchSuite struct{ suite.Suite }

func TestWatch(t *testing.T) { suite.Run(t, new(WatchSuite)) }

// wedgedPool fills a two-slot limiter with two steps and parks both, which is the shape
// the 2026-09-10 gate reached with eight: every seat taken by something that cannot
// proceed until a seat frees.
func wedgedPool(t *testing.T) *Limiter {
	t.Helper()
	lim := newTestLimiter(t, 2)
	build, err := lim.acquireWatched(context.Background(), 1, ". build")
	require.NoError(t, err)
	test, err := lim.acquireWatched(context.Background(), 1, ". test")
	require.NoError(t, err)
	build.block("the cache lock for 4f1ac2be, held by . generate")
	test.block("1 build slot(s)")
	return lim
}

func (s *WatchSuite) TestSlotWatchRefusesAWaitNothingCanEnd() {
	t := s.T()
	lim := wedgedPool(t)

	start := time.Now()
	_, err := lim.acquireWatched(t.Context(), 1, ". mocks-generate")
	elapsed := time.Since(start)

	require.Error(t, err)
	requireDeadlock(t, err)
	assert.Less(t, elapsed, 2*time.Second, "the refusal lands on the grace, not on a timeout")
	assert.GreaterOrEqual(t, elapsed, testGrace, "and never before it: a hand-off is not a deadlock")

	msg := err.Error()
	for _, want := range []string{
		". build holds 1 and is waiting on the cache lock for 4f1ac2be, held by . generate",
		". test holds 1 and is waiting on 1 build slot(s)",
		". mocks-generate needs 1",
		"all 2 slots",
	} {
		assert.Contains(t, msg, want)
	}
}

func (s *WatchSuite) TestSlotWatchLeavesABusyPoolAlone() {
	t := s.T()
	lim := newTestLimiter(s.T(), 2)
	working, err := lim.acquireWatched(context.Background(), 1, ". build")
	require.NoError(t, err)
	parked, err := lim.acquireWatched(context.Background(), 1, ". test")
	require.NoError(t, err)
	parked.block("the cache lock for 4f1ac2be, held by . generate")

	// One holder is doing work, so the queue can still drain and there is nothing to
	// refuse. Queueing behind a busy pool is the ordinary case.
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err = lim.acquireWatched(ctx, 1, ". mocks-generate")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorAs(t, err, new(SlotDeadlock))

	// And once that holder finishes, the waiter is admitted rather than refused.
	working.done()
	lim.Release()
	held, err := lim.acquireWatched(t.Context(), 1, ". mocks-generate")
	require.NoError(t, err)
	held.done()
	lim.Release()
}

func (s *WatchSuite) TestSlotWatchIgnoresAYieldedHold() {
	t := s.T()
	lim := newTestLimiter(s.T(), 2)
	composer, err := lim.acquireWatched(context.Background(), 1, ". ci")
	require.NoError(t, err)
	blocked, err := lim.acquireWatched(context.Background(), 1, ". test")
	require.NoError(t, err)
	blocked.block("the cache lock for 4f1ac2be, held by . generate")

	// A composer that yielded for its ctx.needs fan-out occupies nothing, so the pool it
	// left has room and its record must not read as a seat that cannot free.
	defer composer.yield()()
	lim.Release()

	held, err := lim.acquireWatched(t.Context(), 1, ". mocks-generate")
	require.NoError(t, err)
	held.done()
	lim.Release()
}

func (s *WatchSuite) TestMarkWaitingIsANoOpOutsideAnAdmittedStep() {
	t := s.T()
	// A bare Run in a test, a spell reaching a wait with no seat of its own: the mark has
	// nowhere to land and the call site must not have to know that.
	assert.NotPanics(t, func() { MarkWaiting(context.Background(), "the cache lock")() })
}

func (s *WatchSuite) TestAdmitMarksTheStepBlockedWhileItWaitsForMoreSlots() {
	t := s.T()
	lim := newTestLimiter(s.T(), 2)
	parent, err := lim.acquireWatched(context.Background(), 1, ". ci")
	require.NoError(t, err)
	other, err := lim.acquireWatched(context.Background(), 1, ". test")
	require.NoError(t, err)
	other.block("the cache lock for 4f1ac2be, held by . generate")

	// The parent still holds its seat and queues for a second one: hold-and-wait, which
	// the acquire path marks on its own so no wait site has to remember to.
	ctx := withSlotHold(WithSlotsHeld(context.Background(), 1), parent)
	_, err = lim.acquireWatched(ctx, 1, ". ci child")
	requireDeadlock(t, err)
	assert.Contains(t, err.Error(), ". ci holds 1 and is waiting on 1 build slot(s)")
}

// TestSlotWatchSeesAYieldReacquire: a step taking back the slots it yielded waits like
// any other, so a pool wedged with it queued is a wedge the watch has to see, and the
// refusal a regular waiter receives has to name it. The re-acquire itself is not ended
// by the refusal (its caller must return holding), which is why the holders are freed
// by hand at the end.
func (s *WatchSuite) TestSlotWatchSeesAYieldReacquire() {
	t := s.T()
	lim := newTestLimiter(s.T(), 2)
	build, err := lim.acquireWatched(context.Background(), 1, ". build")
	require.NoError(t, err)
	composer, err := lim.acquireWatched(context.Background(), 1, ". ci")
	require.NoError(t, err)
	build.block("the cache lock for 4f1ac2be, held by . generate")
	ctx := seat{hold: composer}.on(context.Background())

	// The composer yields for a fan-out; a peer takes its seat and parks; the composer
	// then wants its seat back, still occupying nothing until it has it.
	restore := composer.yield()
	lim.Release()
	peer, err := lim.acquireWatched(context.Background(), 1, ". test")
	require.NoError(t, err)
	peer.block("1 build slot(s)")
	back := make(chan struct{})
	go func() {
		defer close(back)
		_ = lim.reacquireYielded(ctx, 1)
	}()

	_, err = lim.acquireWatched(t.Context(), 1, ". mocks-generate")
	requireDeadlock(t, err)
	assert.Contains(t, err.Error(), ". ci (taking back yielded slots) needs 1")

	build.done()
	lim.Release()
	peer.done()
	lim.Release()
	<-back
	restore()
	composer.done()
	lim.Release()
}

// TestAdmitShowsItsHolderToTheSlotWatch: work admitted outside a cache step (a pooled
// Buzz target reached through ctx.needs) that then queues for a step of its own, without
// yielding, is the width-one hang. Its holder has to be on the watch, named under the
// step that needed it, or the run waits out --target-timeout instead of being refused.
func (s *WatchSuite) TestAdmitShowsItsHolderToTheSlotWatch() {
	t := s.T()
	lim := newTestLimiter(s.T(), 1)
	parent, err := lim.acquireWatched(context.Background(), 1, "libs/textsearch test")
	require.NoError(t, err)
	ctx := withSlotHold(WithSlotHeld(context.Background()), parent)

	err = lim.Yield(ctx, func() error {
		held, release, err := lim.Admit(WithoutSlotHeld(ctx), "install")
		require.NoError(t, err)
		defer release()
		assert.Equal(t, 1, SlotsHeld(held), "the admitted work runs marked as holding its slot")

		_, err = lim.acquireWatched(held, 1, "libs/textsearch install")
		return err
	})
	requireDeadlock(t, err)
	assert.Contains(t, err.Error(), "libs/textsearch test > install holds 1 and is waiting on 1 build slot(s)")

	parent.done()
	lim.Release()
	assert.Equal(t, LimiterSnapshot{Capacity: 1}, lim.Snapshot(), "Admit's release gave its slot back")
}

// TestSlotWatchKeepsANestedYieldUntilTheOuterEnds: a yield nested inside another on the
// same hold (a spell's install inside the spell fan-out that yielded for it) returns
// first. The hold still occupies nothing until the outer one returns, so the watch must
// not read it as holding the slot someone else took in the meantime.
func (s *WatchSuite) TestSlotWatchKeepsANestedYieldUntilTheOuterEnds() {
	t := s.T()
	lim := newTestLimiter(s.T(), 1)
	composer, err := lim.acquireWatched(context.Background(), 1, ". test")
	require.NoError(t, err)
	composer.block("its spells")

	outer := composer.yield()
	lim.Release()
	// A tool's own worker reservation takes the freed slot, the kind of holder the watch
	// never counts.
	require.NoError(t, lim.AcquireN(context.Background(), 1))
	composer.yield()()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err = lim.acquireWatched(ctx, 1, ". mocks-generate")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorAs(t, err, new(SlotDeadlock), "the composer's slot is still handed back")

	lim.Release()
	outer()
	composer.done()
}

// A limiter built WithRefusal hands every waiter the caller's error, built from what the
// watch saw.
func (s *WatchSuite) TestRefusalIsTheCallersError() {
	t := s.T()
	coded := errors.New("coded refusal")
	var saw SlotDeadlock
	lim := NewLimiter(2, withDeadlockGrace(testGrace), WithRefusal(func(d SlotDeadlock) error {
		saw = d
		return coded
	}))
	build, err := lim.acquireWatched(context.Background(), 1, ". build")
	require.NoError(t, err)
	test, err := lim.acquireWatched(context.Background(), 1, ". test")
	require.NoError(t, err)
	build.block("a lock")
	test.block("a slot")

	_, err = lim.acquireWatched(t.Context(), 1, ". lint")
	require.ErrorIs(t, err, coded)
	assert.Equal(t, SlotDeadlock{
		Capacity: 2,
		Holding:  []string{". build holds 1 and is waiting on a lock", ". test holds 1 and is waiting on a slot"},
		Queued:   []string{". lint needs 1"},
	}, saw)
}

// Seat's release retires the hold and gives the slots back.
func (s *WatchSuite) TestSeatReleaseGivesTheSlotsBack() {
	t := s.T()
	lim := newTestLimiter(s.T(), 3)
	held, release, err := lim.Seat(t.Context(), 2, ". build")
	require.NoError(t, err)
	assert.Equal(t, 0, SlotsHeld(held), "Seat leaves marking the slots held to the body")
	assert.Equal(t, 2, lim.Snapshot().Running)
	release()
	assert.Equal(t, LimiterSnapshot{Capacity: 3}, lim.Snapshot())
}

// A seat of fewer than one slot is refused, as AcquireN refuses it, and takes nothing.
func (s *WatchSuite) TestSeatRefusesACountBelowOne() {
	lim := newTestLimiter(s.T(), 1)
	_, release, err := lim.Seat(s.T().Context(), 0, ". build")
	var refused *SlotCountError
	s.Require().ErrorAs(err, &refused)
	s.Nil(release)
	s.Equal(LimiterSnapshot{Capacity: 1}, lim.Snapshot())
}

// A timer stopped after it fired still runs its verdict; it must leave the timer armed
// since alone, or that one fires unwatched.
func (s *WatchSuite) TestAStaleTimerLeavesTheArmedOneAlone() {
	w := newTestLimiter(s.T(), 1).watch
	stale := time.NewTimer(time.Hour)
	defer stale.Stop()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.armLocked(time.Hour)
	armed := w.timer
	defer armed.Stop()
	w.fireVerdictLocked(stale)
	s.Same(armed, w.timer)
}
