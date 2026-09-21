package types

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A body's own dependency time is its own: a composed target's ctx.needs must not land on
// the parent's accumulator, which already counts that whole child as one span.
//
// This pins the primitive only. Whether the ENGINE gives every body its own accumulator is
// a question about the install site, held by
// interp.TestDeclaredCeilingIsAPassThroughWithoutATimeout: an uncapped body inheriting its
// nearest ceilinged ancestor's accumulator passes here regardless.
func TestDependencyWaitIsPerBody(t *testing.T) {
	parent := WithDependencyWait(context.Background())
	DependencyWaitFromContext(parent).Add(time.Minute)
	child := WithDependencyWait(parent)
	DependencyWaitFromContext(child).Add(30 * time.Second)

	assert.Equal(t, time.Minute, DependencyWaitFromContext(parent).Elapsed(),
		"a child's dependency time reached its parent twice")
	assert.Equal(t, 30*time.Second, DependencyWaitFromContext(child).Elapsed())
}

// Nested derives a child's accumulator from its parent's, and derives a FRESH one: the
// scoping is the guarantee, so a parent that already counts the whole child as one span
// never also receives the child's own breakdown.
func TestDependencyWaitNestedIsFresh(t *testing.T) {
	parent := &DependencyWait{}
	parent.Add(time.Minute)

	child := parent.Nested()
	child.Add(30 * time.Second)

	assert.Equal(t, time.Minute, parent.Elapsed())
	assert.Equal(t, 30*time.Second, child.Elapsed())
}

// Do is the only way to run requested work, which is what makes the measurement
// unforgettable: a caller cannot run a dependency and omit the timing.
func TestDependencyWaitDoTimesTheWorkItRuns(t *testing.T) {
	w := &DependencyWait{}

	did, err := w.Do(context.Background())
	assert.False(t, did, "nothing requested")
	assert.NoError(t, err)
	assert.Zero(t, w.Elapsed())

	w.Request(func(context.Context) error {
		time.Sleep(5 * time.Millisecond)
		return nil
	})
	did, err = w.Do(context.Background())
	assert.True(t, did)
	assert.NoError(t, err)
	assert.Positive(t, w.Elapsed(), "Do must book the time it spent")

	// The request is consumed, so a second Do finds nothing rather than re-running it.
	did, _ = w.Do(context.Background())
	assert.False(t, did, "a completed request must not run twice")
}

// Outside a target body there is no accumulator, and recording against one must not
// panic: runBuzzDependencies also runs under `magus buzz` and the REPL, where nothing
// armed a ceiling.
//
// The nil receiver is what makes that work: DependencyWaitFromContext returns nil
// outside a body, and every method tolerates it, so no caller has to check.
func TestDependencyWaitOutsideABodyIsANoOp(t *testing.T) {
	outside := DependencyWaitFromContext(context.Background())
	require.Nil(t, outside)
	assert.NotPanics(t, func() { outside.Add(time.Minute) })
	assert.Zero(t, outside.Elapsed())
}

// Pool.Dispatch fans a ctx.needs set out concurrently, so several dependencies report
// against one body's accumulator at once. Run under -race, this is what says the counter
// is safe to share.
func TestDependencyWaitIsSafeForConcurrentCallers(t *testing.T) {
	w := DependencyWaitFromContext(WithDependencyWait(context.Background()))

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Add(time.Second)
		}()
	}
	wg.Wait()

	assert.Equal(t, 50*time.Second, w.Elapsed())
}
