package interp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCrossDispatchRunOnce verifies a (dir, target) runs once even when many
// callers race for it, and that they all observe the same result.
func TestCrossDispatchRunOnce(t *testing.T) {
	cd := NewCrossDispatch()
	var runs atomic.Int32
	wantErr := errors.New("boom")
	cd.run = func(_ context.Context, _, _ string) error {
		runs.Add(1)
		return wantErr
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := cd.Dispatch(context.Background(), "/ws/gopherbuzz", "build")
			assert.ErrorIs(t, err, wantErr)
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), runs.Load(), "target should run once (run-once)")
}

// TestCrossDispatchCycle verifies a cross-project dependency cycle errors rather
// than deadlocking: the runner re-dispatches the same (dir, target) it was given,
// which must be caught as an ancestor.
func TestCrossDispatchCycle(t *testing.T) {
	cd := NewCrossDispatch()
	cd.run = func(ctx context.Context, dir, target string) error {
		// Simulate the remote target depending back on itself across the boundary.
		return cd.Dispatch(ctx, dir, target)
	}
	err := cd.Dispatch(context.Background(), "/ws/a", "build")
	assert.Error(t, err, "expected a cross-project cycle error")
}

// TestCrossDispatchDistinct verifies different (dir, target) keys each run.
func TestCrossDispatchDistinct(t *testing.T) {
	cd := NewCrossDispatch()
	var runs atomic.Int32
	cd.run = func(_ context.Context, _, _ string) error { runs.Add(1); return nil }
	_ = cd.Dispatch(context.Background(), "/ws/a", "build")
	_ = cd.Dispatch(context.Background(), "/ws/b", "build")
	_ = cd.Dispatch(context.Background(), "/ws/a", "test")
	assert.Equal(t, int32(3), runs.Load(), "distinct keys should each run")
}
