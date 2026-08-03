package interp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.ErrorIs(t, err, types.TargetDependencyCycle, "a cycle carries the MGS1007 diagnostic code")
}

// TestCrossDispatchPanicUnblocksWaiters verifies that a panic in the runner is
// converted to an error and re-raised, and — critically — that e.done is still
// closed so concurrent waiters on the same key unblock instead of hanging forever.
func TestCrossDispatchPanicUnblocksWaiters(t *testing.T) {
	cd := NewCrossDispatch()
	var started sync.WaitGroup
	started.Add(1)
	release := make(chan struct{})
	cd.run = func(_ context.Context, _, _ string) error {
		started.Done()
		<-release // hold so a second caller parks on e.done before we panic
		panic("kaboom")
	}

	// First caller drives the run and must recover the re-raised panic.
	go func() {
		defer func() {
			assert.NotNil(t, recover(), "panic should propagate to the running caller")
		}()
		_ = cd.Dispatch(context.Background(), "/ws/a", "build")
	}()

	// Second caller parks on e.done; it must unblock with the converted error
	// rather than hang.
	started.Wait()
	done := make(chan error, 1)
	go func() { done <- cd.Dispatch(context.Background(), "/ws/a", "build") }()
	close(release)

	select {
	case err := <-done:
		assert.ErrorContains(t, err, "cross-dispatch panic")
	case <-time.After(2 * time.Second):
		t.Fatal("waiter hung after runner panicked (e.done was not closed)")
	}
}

// TestCrossDispatchResetsBuzzAncestors verifies the buzz dispatch ancestor stack does
// not cross a project boundary. Those entries are bare target names, and a name only
// means something within one project: a sub-project target that happens to share a
// name with any of the caller's ancestors was reported as a cycle, deterministically,
// for a graph that has none ("lint" needs the sibling project, whose own "lint" then
// looks like it is already on the stack). Cross-project cycles are caught by the
// (dir, target) ancestor key this coordinator keeps, which is why clearing the
// per-project stack loses no detection.
func TestCrossDispatchResetsBuzzAncestors(t *testing.T) {
	var ran atomic.Bool
	reg := buzz.NewPoolRegistry(nil, 1)
	defer func() { _ = reg.Close() }()
	pool := reg.Get("/ws/sub\x00buzz", func(ctx context.Context) (*buzz.WorkerSession, error) {
		targets := map[string]vm.Callable{
			"lint": func(context.Context, []vm.Value) (vm.Value, error) {
				ran.Store(true)
				return vm.Null, nil
			},
		}
		return &buzz.WorkerSession{Session: buzz.NewSession(ctx), Targets: targets}, nil
	})

	cd := NewCrossDispatch()
	cd.run = func(ctx context.Context, _, _ string) error {
		// Stands in for the sub-project's own ctx.needs(lint): the same read of the
		// inherited stack that bindings.buzzDispatchViaPool performs.
		return pool.Dispatch(ctx, []string{"lint"}, buzz.AncestorsFromContext(ctx))
	}

	// The caller is mid-run inside the parent project, whose "lint" target is what
	// reached across the boundary.
	ctx := buzz.WithAncestors(context.Background(), []string{"lint"})
	require.NoError(t, cd.Dispatch(ctx, "/ws/sub", "build"),
		"a sub-project target sharing a name with a caller's ancestor is not a cycle")
	assert.True(t, ran.Load(), "the sub-project's target never ran")
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
