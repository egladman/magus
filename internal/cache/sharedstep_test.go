package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ceilingKey stands in for any value a parent target puts on the context it dispatches
// with; the shared step must still see it.
type ceilingKey struct{}

// TestSharedStepSurvivesTheDeadlineOfTheParentThatAskedFirst is the 2026-09-10 stall in
// miniature. Two parents need one composed step; the step is dispatched once, on the
// context of whichever parent got there first, and that parent declared a timeout. When
// the ceiling expired the shared step died under it, and every sibling waiting on the one
// execution failed with a deadline that was never theirs.
//
// Deterministic, with no sleep on the assertion path: the step's body blocks until the
// timed parent's context is DONE, so by the time it checks its own context the ceiling has
// certainly fired. It must still be live.
func TestSharedStepSurvivesTheDeadlineOfTheParentThatAskedFirst(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main\nfunc main() {}\n")
	s := makeStep(root)
	s.Target = "generate"
	s.NoCache = true

	scheduled := WithSharedStepBase(t.Context())
	timed, cancel := context.WithTimeout(scheduled, 20*time.Millisecond)
	defer cancel()
	timed = context.WithValue(timed, ceilingKey{}, "security")

	var (
		sawValue any
		sawErr   error
	)
	_, err := c.RunAside(SharedStepContext(timed), s, func(stepCtx context.Context) error {
		<-timed.Done() // the parent's ceiling has now expired
		sawValue = stepCtx.Value(ceilingKey{})
		sawErr = stepCtx.Err()
		return nil
	}, WithLimiter(NewLimiter(2)))

	require.NoError(t, err, "the shared step outlives one parent's ceiling")
	assert.NoError(t, sawErr, "the parent's expired ceiling is not the step's cancellation")
	assert.Equal(t, "security", sawValue, "the step still reads the dispatching parent's values")

	_, hasDeadline := SharedStepContext(timed).Deadline()
	assert.False(t, hasDeadline, "the parent's ceiling is not reported as the shared step's own")
}

// TestSharedStepStillDiesWithTheScheduledUnit pins the other half: dropping the
// dispatching parent's deadline must not make a composed step unkillable. Ctrl-C, the
// stall watchdog and the ceiling of the unit the user actually scheduled all sit above the
// base, so all three still reach it.
func TestSharedStepStillDiesWithTheScheduledUnit(t *testing.T) {
	scheduled, abort := context.WithCancel(context.Background())
	defer abort()
	parent, cancel := context.WithTimeout(WithSharedStepBase(scheduled), time.Hour)
	defer cancel()

	shared := SharedStepContext(parent)
	require.NoError(t, shared.Err())

	abort()
	assert.True(t, errors.Is(shared.Err(), context.Canceled), "the scheduled unit's abort reaches the shared step")
	select {
	case <-shared.Done():
	default:
		t.Fatal("the shared step's Done never closed on the scheduled unit's abort")
	}
}

// TestSharedStepContextIsAPassThroughOutsideAScheduledTarget keeps the seam honest for the
// entry points that establish no base (a bare Cache.Run in a test, an embedder calling in):
// with nothing above the caller to borrow a cancellation from, inventing one would make
// such a step unkillable.
func TestSharedStepContextIsAPassThroughOutsideAScheduledTarget(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	shared := SharedStepContext(parent)
	cancel()
	assert.True(t, errors.Is(shared.Err(), context.Canceled), "with no base the caller still cancels the step")
}
