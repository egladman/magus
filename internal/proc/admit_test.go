package proc

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
)

// TestAdmitRoundTrip is the whole point of the socket: two SEPARATE magus invocations,
// arbitrated by one budget neither of them holds.
func TestAdmitRoundTrip(t *testing.T) {
	budget := cache.NewMachineBudget(10_000, 8)
	srv, err := New(Options{
		Handler:       func(context.Context, []string) error { return nil },
		MachineBudget: budget,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	client := MachineAdmitter{Addr: srv.Addr()}
	self := os.Getpid()

	first, err := client.Request(t.Context(), "w1", cache.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Slots: 1, Pid: self, Cwd: "/tree/a",
	})
	require.NoError(t, err)
	require.True(t, first.Granted)

	second, err := client.Request(t.Context(), "w2", cache.MachineClaim{
		Project: "docs", Target: "ci", MemoryMB: 9000, Slots: 1, Pid: self,
	})
	require.NoError(t, err)
	assert.False(t, second.Granted, "the second invocation cannot be seated alongside the first")
	require.Len(t, second.Holders, 1, "and it is told who to wait for")
	assert.Equal(t, "/tree/a", second.Holders[0].Cwd)

	client.Release(t.Context(), first.ID)
	third, err := client.Request(t.Context(), "w2", cache.MachineClaim{
		Project: "docs", Target: "ci", MemoryMB: 9000, Slots: 1, Pid: self,
	})
	require.NoError(t, err)
	assert.True(t, third.Granted)
}

// TestAdmitOnAServerWithNoBudget pins the answer a per-process proc server gives. Read
// as a grant it would let a run go unarbitrated while the daemon arbitrates its peers,
// so it must be an error the client can see.
func TestAdmitOnAServerWithNoBudget(t *testing.T) {
	srv, err := New(Options{Handler: func(context.Context, []string) error { return nil }})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	_, err = MachineAdmitter{Addr: srv.Addr()}.Request(t.Context(), "w1", cache.MachineClaim{
		Project: ".", Target: "test", Pid: os.Getpid(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not arbitrate the machine budget")
}

// TestAdmitDropRetiresAWaiter covers the fail-fast teardown: a run that will not queue
// must not leave a place in the queue behind, reserving room for a claim nobody wants.
func TestAdmitDropRetiresAWaiter(t *testing.T) {
	budget := cache.NewMachineBudget(10_000, 8)
	srv, err := New(Options{
		Handler:       func(context.Context, []string) error { return nil },
		MachineBudget: budget,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	client := MachineAdmitter{Addr: srv.Addr()}
	self := os.Getpid()
	held, err := client.Request(t.Context(), "held", cache.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Pid: self,
	})
	require.NoError(t, err)
	require.True(t, held.Granted)

	_, err = client.Request(t.Context(), "gone", cache.MachineClaim{
		Project: "docs", Target: "ci", MemoryMB: 9000, Pid: self,
	})
	require.NoError(t, err)
	client.Drop(t.Context(), "gone")
	client.Release(t.Context(), held.ID)

	assert.Empty(t, budget.Snapshot().Waiters)
	small, err := client.Request(t.Context(), "small", cache.MachineClaim{
		Project: "docs", Target: "lint", MemoryMB: 500, Pid: self,
	})
	require.NoError(t, err)
	assert.True(t, small.Granted, "nothing is reserving room for the waiter that left")
}

// TestAdmitReportsTheBudgetInStatus is the observability half: `magus status` reads
// this to show what the machine is doing across worktrees.
func TestAdmitReportsTheBudgetInStatus(t *testing.T) {
	budget := cache.NewMachineBudget(10_000, 8)
	srv, err := New(Options{
		Handler:       func(context.Context, []string) error { return nil },
		MachineBudget: budget,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	_, err = MachineAdmitter{Addr: srv.Addr()}.Request(t.Context(), "w1", cache.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Slots: 2, Pid: os.Getpid(),
	})
	require.NoError(t, err)

	st, err := QueryStatus(t.Context(), srv.Addr())
	require.NoError(t, err)
	require.NotNil(t, st.Machine)
	assert.Equal(t, 10_000, st.Machine.BudgetMB)
	assert.Equal(t, 9000, st.Machine.HeldMB)
	require.Len(t, st.Machine.Holders, 1)
	assert.Equal(t, "test", st.Machine.Holders[0].Target)
}

// TestStatusOmitsTheBudgetWithoutOne pins the other half: a per-process proc server
// arbitrates nothing beyond itself, and reporting a budget it does not hold would put a
// second machine budget in front of a reader.
func TestStatusOmitsTheBudgetWithoutOne(t *testing.T) {
	srv, err := New(Options{Handler: func(context.Context, []string) error { return nil }})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	st, err := QueryStatus(t.Context(), srv.Addr())
	require.NoError(t, err)
	assert.Nil(t, st.Machine)
}
