package buzz

import (
	"context"
	"testing"
	"time"

	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDispatchRejectsCycleWithMemo is the regression for the cyclic-magusfile
// deadlock: with a TargetMemo active (as on every `magus run`), a target that
// dispatches one of its own ancestors must error promptly rather than subscribe
// to the ancestor's in-flight result and deadlock. The factory must never run —
// the cycle is caught before any session is checked out.
func TestDispatchRejectsCycleWithMemo(t *testing.T) {
	p := newPool(func(context.Context) (*Session, map[string]vmpackage.Callable, error) {
		t.Fatal("factory ran; the cycle should be caught before execution")
		return nil, nil, nil
	}, nil, 1)
	defer func() { _ = p.Close() }()

	ctx := WithTargetMemo(context.Background(), NewTargetMemo())

	done := make(chan error, 1)
	go func() {
		// "a" dispatched while "a" is already an ancestor — a self-cycle.
		done <- p.Dispatch(ctx, []string{"a"}, []string{"a"})
	}()

	select {
	case err := <-done:
		require.Error(t, err, "want cycle error")
		assert.Contains(t, err.Error(), "cycle detected", "want cycle error")
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch deadlocked on a cycle instead of erroring")
	}
}
