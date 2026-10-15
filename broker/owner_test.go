package broker

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// leasedClient is a client acting under lease in a tree whose runs the broker seats
// maxValidations at a time.
func leasedClient(t *testing.T, addr, lease, tree string, maxValidations int) *Client {
	t.Helper()
	c := NewClient(addr)
	c.hello.Lease, c.hello.Tree, c.hello.MaxValidations = lease, tree, maxValidations
	c.scopeOnce.Do(func() {})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestAJobTreeRunsAsManyValidationsAsItsCap(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 10))
	first := leasedClient(t, addr, "plan/a", "plan", 1)
	second := leasedClient(t, addr, "plan/b", "plan", 1)
	nested := leasedClient(t, addr, "plan/a", "plan", 1)
	root := NewClient(addr)
	t.Cleanup(func() { _ = root.Close() })

	v, err := first.Request(t.Context(), types.MachineClaim{Target: "go-test", Invocation: "1:a"})
	require.NoError(t, err)
	require.True(t, v.Granted)

	v, err = second.Request(t.Context(), types.MachineClaim{Target: "go-test", Invocation: "2:b"})
	require.NoError(t, err)
	assert.False(t, v.Granted, "a second run of the tree waits for the seat")
	assert.True(t, v.OwnRun, "and waits rather than refusing")
	assert.True(t, second.seatWaiting.Load(), "the wait is said")

	v, err = nested.Request(t.Context(), types.MachineClaim{Target: "build", Invocation: "3:c", Ancestors: []string{"1:a"}})
	require.NoError(t, err)
	assert.True(t, v.Granted, "a magus nested under the seated run shares its seat")

	v, err = root.Request(t.Context(), types.MachineClaim{Target: "test", Invocation: "4:d"})
	require.NoError(t, err)
	assert.True(t, v.Granted, "a run nobody leased is never counted")

	require.NoError(t, first.Close())
	require.NoError(t, nested.Close())
	assert.Eventually(t, func() bool {
		v, err := second.Request(t.Context(), types.MachineClaim{Target: "go-test", Invocation: "2:b"})
		return err == nil && v.Granted
	}, 2*time.Second, 10*time.Millisecond, "the seat passes on once its run's connections close")
	assert.False(t, second.seatWaiting.Load())
}

func TestASeatWaitNamesTheRunHoldingIt(t *testing.T) {
	wait := SeatWait{Tree: "plan", Max: 1, Holders: []Work{{Kind: WorkRun, Lease: "plan/a", PID: 42, Command: "magus run go-test .", Dir: "/w"}}}
	assert.Equal(t, "waiting for job tree plan's validation seat: jobs.max_validations is 1, and it is held by plan/a (pid 42, magus run go-test ., in /w)", wait.String())
}

func TestWorkNamesEachRunsOwner(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 10))
	worker := leasedClient(t, addr, "plan/a", "plan", 0)
	_, err := worker.Request(t.Context(), types.MachineClaim{Target: "go-test", Slots: 4})
	require.NoError(t, err)

	r, err := QueryWork(t.Context(), addr)
	require.NoError(t, err)
	require.Len(t, r.Running, 1)
	assert.Equal(t, "plan/a", r.Running[0].Owner())
	got := r.Running[0]
	got.Dir, got.Command, got.Since = "", "", time.Time{}
	assert.Equal(t, Work{Kind: WorkRun, Lease: "plan/a", PID: os.Getpid(), Slots: 4}, got)
}

func TestValidationScopeReadsTheWorkspacesCap(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("jobs:\n  max_validations: 2\n"), 0o644))
	sub := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	tree, n, err := validationScope(sub, "plan/a")
	require.NoError(t, err)
	assert.Equal(t, "plan/a", tree, "a lease no row declares is its own tree")
	assert.Equal(t, 2, n)

	tree, n, err = validationScope(t.TempDir(), "w")
	require.NoError(t, err)
	assert.Equal(t, "w", tree)
	assert.Equal(t, 1, n, "outside a workspace the default holds")

	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("jobs:\n  max_validations: -1\n"), 0o644))
	_, _, err = validationScope(sub, "plan/a")
	require.ErrorContains(t, err, "jobs.max_validations is -1")
}
