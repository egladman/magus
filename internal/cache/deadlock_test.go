package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withShortDeadlockGrace shrinks the grace for the duration of one test. Not parallel:
// the grace is package state, and a test that has to spend the real one either sleeps for
// three seconds or does not cover it.
func withShortDeadlockGrace(t *testing.T, d time.Duration) {
	t.Helper()
	prev := slotDeadlockGrace
	slotDeadlockGrace = d
	t.Cleanup(func() { slotDeadlockGrace = prev })
}

// wedgedPool fills a two-slot limiter with two steps and parks both, which is the shape
// the 2026-09-10 gate reached with eight: every seat taken by something that cannot
// proceed until a seat frees.
func wedgedPool(t *testing.T) *Limiter {
	t.Helper()
	lim := NewLimiter(2)
	build, err := lim.acquireWatched(context.Background(), 1, ". build")
	require.NoError(t, err)
	test, err := lim.acquireWatched(context.Background(), 1, ". test")
	require.NoError(t, err)
	build.block("the cache lock for 4f1ac2be, held by . generate")
	test.block("1 build slot(s)")
	return lim
}

func TestSlotWatchRefusesAWaitNothingCanEnd(t *testing.T) {
	withShortDeadlockGrace(t, 50*time.Millisecond)
	lim := wedgedPool(t)

	start := time.Now()
	_, err := lim.acquireWatched(t.Context(), 1, ". mocks-generate")
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, types.BuildSlotsDeadlocked)
	assert.Less(t, elapsed, 2*time.Second, "the refusal lands on the grace, not on a timeout")
	assert.GreaterOrEqual(t, elapsed, slotDeadlockGrace, "and never before it: a hand-off is not a deadlock")

	msg := err.Error()
	for _, want := range []string{
		". build holds 1 and is waiting on the cache lock for 4f1ac2be, held by . generate",
		". test holds 1 and is waiting on 1 build slot(s)",
		". mocks-generate needs 1",
		"all 2 of this run's slots",
	} {
		assert.Contains(t, msg, want)
	}

	var coded interface{ ExitCode() int }
	require.True(t, errors.As(err, &coded))
	assert.Equal(t, ExitCodeSlotDeadlock, coded.ExitCode(), "waiting cannot fix it, so it is a config status, not EX_TEMPFAIL")
}

func TestSlotWatchLeavesABusyPoolAlone(t *testing.T) {
	withShortDeadlockGrace(t, 50*time.Millisecond)
	lim := NewLimiter(2)
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
	assert.NotErrorIs(t, err, types.BuildSlotsDeadlocked)

	// And once that holder finishes, the waiter is admitted rather than refused.
	working.done()
	lim.Release()
	held, err := lim.acquireWatched(t.Context(), 1, ". mocks-generate")
	require.NoError(t, err)
	held.done()
	lim.Release()
}

func TestSlotWatchIgnoresAYieldedHold(t *testing.T) {
	withShortDeadlockGrace(t, 50*time.Millisecond)
	lim := NewLimiter(2)
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

func TestBlockedOnIsANoOpOutsideAnAdmittedStep(t *testing.T) {
	t.Parallel()
	// A bare Run in a test, a spell reaching a wait with no seat of its own: the mark has
	// nowhere to land and the call site must not have to know that.
	assert.NotPanics(t, func() { BlockedOn(context.Background(), "the cache lock")() })
}

func TestAdmitMarksTheStepBlockedWhileItWaitsForMoreSlots(t *testing.T) {
	withShortDeadlockGrace(t, 50*time.Millisecond)
	lim := NewLimiter(2)
	parent, err := lim.acquireWatched(context.Background(), 1, ". ci")
	require.NoError(t, err)
	other, err := lim.acquireWatched(context.Background(), 1, ". test")
	require.NoError(t, err)
	other.block("the cache lock for 4f1ac2be, held by . generate")

	// The parent still holds its seat and queues for a second one: hold-and-wait, which
	// the acquire path marks on its own so no wait site has to remember to.
	ctx := withSlotHold(WithSlotsHeld(context.Background(), 1), parent)
	_, err = lim.acquireWatched(ctx, 1, ". ci child")
	require.ErrorIs(t, err, types.BuildSlotsDeadlocked)
	assert.Contains(t, err.Error(), ". ci holds 1 and is waiting on 1 build slot(s)")
}

// TestSlotWatchSeesAYieldReacquire: a step taking back the slots it yielded waits like
// any other, so a pool wedged with it queued is a wedge the watch has to see, and the
// refusal a regular waiter receives has to name it. The re-acquire itself is not ended
// by the refusal (its caller must return holding), which is why the holders are freed
// by hand at the end.
func TestSlotWatchSeesAYieldReacquire(t *testing.T) {
	withShortDeadlockGrace(t, 50*time.Millisecond)
	lim := NewLimiter(2)
	build, err := lim.acquireWatched(context.Background(), 1, ". build")
	require.NoError(t, err)
	composer, err := lim.acquireWatched(context.Background(), 1, ". ci")
	require.NoError(t, err)
	build.block("the cache lock for 4f1ac2be, held by . generate")
	ctx := admission{hold: composer}.on(context.Background())

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
		lim.reacquireYielded(ctx, 1)
	}()

	_, err = lim.acquireWatched(t.Context(), 1, ". mocks-generate")
	require.ErrorIs(t, err, types.BuildSlotsDeadlocked)
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
func TestAdmitShowsItsHolderToTheSlotWatch(t *testing.T) {
	withShortDeadlockGrace(t, 50*time.Millisecond)
	lim := NewLimiter(1)
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
	require.ErrorIs(t, err, types.BuildSlotsDeadlocked)
	assert.Contains(t, err.Error(), "libs/textsearch test > install holds 1 and is waiting on 1 build slot(s)")

	parent.done()
	lim.Release()
	assert.Equal(t, LimiterStats{Capacity: 1}, lim.Snapshot(), "Admit's release gave its slot back")
}

// TestSlotWatchKeepsANestedYieldUntilTheOuterEnds: a yield nested inside another on the
// same hold (a spell's install inside the spell fan-out that yielded for it) returns
// first. The hold still occupies nothing until the outer one returns, so the watch must
// not read it as holding the slot someone else took in the meantime.
func TestSlotWatchKeepsANestedYieldUntilTheOuterEnds(t *testing.T) {
	withShortDeadlockGrace(t, 50*time.Millisecond)
	lim := NewLimiter(1)
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
	assert.NotErrorIs(t, err, types.BuildSlotsDeadlocked, "the composer's slot is still handed back")

	lim.Release()
	outer()
	composer.done()
}
