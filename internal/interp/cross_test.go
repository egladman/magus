package interp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
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
			if err := cd.Dispatch(context.Background(), "/ws/gopherbuzz", "build"); !errors.Is(err, wantErr) {
				t.Errorf("Dispatch err = %v, want %v", err, wantErr)
			}
		}()
	}
	wg.Wait()
	if got := runs.Load(); got != 1 {
		t.Errorf("target ran %d times, want 1 (run-once)", got)
	}
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
	if err == nil {
		t.Fatal("expected a cross-project cycle error, got nil")
	}
}

// TestCrossDispatchDistinct verifies different (dir, target) keys each run.
func TestCrossDispatchDistinct(t *testing.T) {
	cd := NewCrossDispatch()
	var runs atomic.Int32
	cd.run = func(_ context.Context, _, _ string) error { runs.Add(1); return nil }
	_ = cd.Dispatch(context.Background(), "/ws/a", "build")
	_ = cd.Dispatch(context.Background(), "/ws/b", "build")
	_ = cd.Dispatch(context.Background(), "/ws/a", "test")
	if got := runs.Load(); got != 3 {
		t.Errorf("distinct keys ran %d times, want 3", got)
	}
}
