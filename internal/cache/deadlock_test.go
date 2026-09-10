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
	composer.setYielded(true)
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
