package types

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A body's own dependency time is its own: a composed target's ctx.needs must not land on
// the parent's accumulator, which already counts that whole child as one span.
//
// This pins the primitive only. Whether the ENGINE gives every body its own accumulator is
// a question about the install site, held by
// interp.TestDeclaredCeilingIsAPassThroughWithoutATimeout: an uncapped body inheriting its
// nearest ceilinged ancestor's accumulator passes here regardless.
func TestTrackDependencyWaitIsPerBody(t *testing.T) {
	parent := TrackDependencyWait(context.Background())
	AddDependencyWait(parent, time.Minute)
	child := TrackDependencyWait(parent)
	AddDependencyWait(child, 30*time.Second)

	assert.Equal(t, time.Minute, DependencyWait(parent), "a child's dependency time reached its parent twice")
	assert.Equal(t, 30*time.Second, DependencyWait(child))
}

// Outside a target body there is no accumulator, and recording against one must not panic:
// runBuzzDependencies also runs under `magus buzz` and the REPL, where nothing armed a
// ceiling.
func TestAddDependencyWaitOutsideABodyIsANoOp(t *testing.T) {
	assert.NotPanics(t, func() { AddDependencyWait(context.Background(), time.Minute) })
	assert.Zero(t, DependencyWait(context.Background()))
}

// Pool.Dispatch fans a ctx.needs set out concurrently, so several dependencies report
// against one body's accumulator at once. Run under -race, this is what says the counter
// is safe to share.
func TestAddDependencyWaitIsSafeForConcurrentCallers(t *testing.T) {
	ctx := TrackDependencyWait(context.Background())

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			AddDependencyWait(ctx, time.Second)
		}()
	}
	wg.Wait()

	assert.Equal(t, 50*time.Second, DependencyWait(ctx))
}
