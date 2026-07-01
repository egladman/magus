package buzz

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	vmpackage "github.com/egladman/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// observerStub records the TargetEnd notifications it receives.
type observerStub struct {
	mu   sync.Mutex
	ends map[string]error
}

func newObserverStub() *observerStub { return &observerStub{ends: map[string]error{}} }

func (o *observerStub) TargetEnd(_ context.Context, name string, _ time.Duration, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ends[name] = err
}

func (o *observerStub) snapshot() map[string]error {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make(map[string]error, len(o.ends))
	for k, v := range o.ends {
		out[k] = v
	}
	return out
}

// observerTestPool builds a pool whose targets are trivial Go callables, so the test
// exercises the observer hook without compiling a magusfile.
func observerTestPool(t *testing.T) *Pool {
	t.Helper()
	factory := func(ctx context.Context) (*Session, map[string]vmpackage.Callable, error) {
		targets := map[string]vmpackage.Callable{
			"ok": func(context.Context, []vmpackage.Value) (vmpackage.Value, error) { return vmpackage.Null, nil },
			"boom": func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
				return vmpackage.Null, errors.New("kaboom")
			},
		}
		return NewSession(ctx), targets, nil
	}
	return newPool(factory, nil, 2)
}

// TestPoolObserverFiresPerTarget verifies the pool notifies an attached observer once
// per executed target, with the target's outcome (nil for success, the error for failure).
func TestPoolObserverFiresPerTarget(t *testing.T) {
	p := observerTestPool(t)
	defer func() { _ = p.Close() }()

	obs := newObserverStub()
	ctx := WithObserver(WithTargetMemo(context.Background(), NewTargetMemo()), obs)

	require.NoError(t, p.Dispatch(ctx, []string{"ok"}, nil))
	require.Error(t, p.Dispatch(ctx, []string{"boom"}, nil))

	got := obs.snapshot()
	require.Len(t, got, 2, "observer should see both targets")
	assert.NoError(t, got["ok"], "successful target reports nil error")
	require.Error(t, got["boom"], "failed target reports its error")
	assert.Contains(t, got["boom"].Error(), "kaboom")
}

// TestPoolObserverDedupedByMemo verifies a target needed twice under one memo runs —
// and is observed — exactly once.
func TestPoolObserverDedupedByMemo(t *testing.T) {
	p := observerTestPool(t)
	defer func() { _ = p.Close() }()

	obs := &countingObserver{}
	ctx := WithObserver(WithTargetMemo(context.Background(), NewTargetMemo()), obs)

	require.NoError(t, p.Dispatch(ctx, []string{"ok"}, nil))
	require.NoError(t, p.Dispatch(ctx, []string{"ok"}, nil)) // second need: memo serves the cached result

	assert.Equal(t, 1, obs.count(), "a memo-deduped target is observed exactly once")
}

type countingObserver struct {
	mu sync.Mutex
	n  int
}

func (o *countingObserver) TargetEnd(context.Context, string, time.Duration, error) {
	o.mu.Lock()
	o.n++
	o.mu.Unlock()
}

func (o *countingObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n
}
