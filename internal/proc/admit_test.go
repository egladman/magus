package proc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// TestBudgetRoundTrip is the whole point of the socket: two SEPARATE magus invocations,
// arbitrated by one budget neither of them holds.
func TestBudgetRoundTrip(t *testing.T) {
	budget := cache.NewMachineBudget(10_000, 8)
	srv, err := New(Options{
		Handler:       func(context.Context, []string) error { return nil },
		MachineBudget: budget,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	client := DaemonAdmitter{Addr: srv.Addr()}
	self := os.Getpid()

	first, err := client.Request(t.Context(), "w1", types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Slots: 1, PID: self, Dir: "/tree/a",
	})
	require.NoError(t, err)
	require.True(t, first.Granted)

	second, err := client.Request(t.Context(), "w2", types.MachineClaim{
		Project: "docs", Target: "ci", MemoryMB: 9000, Slots: 1, PID: self,
	})
	require.NoError(t, err)
	assert.False(t, second.Granted, "the second invocation cannot be seated alongside the first")
	require.Len(t, second.Holders, 1, "and it is told who to wait for")
	assert.Equal(t, "/tree/a", second.Holders[0].Dir)

	client.Release(t.Context(), first.ID)
	third, err := client.Request(t.Context(), "w2", types.MachineClaim{
		Project: "docs", Target: "ci", MemoryMB: 9000, Slots: 1, PID: self,
	})
	require.NoError(t, err)
	assert.True(t, third.Granted)
}

// TestBudgetExcusesAnAncestorAcrossTheSocket is the path the CI break took. An SDK
// consumer inside a magus process tree gets the DaemonAdmitter, not the local one - its
// workspace was never handed a budget, so it dials the daemon the parent run started.
// The excusal therefore happens server-side, keyed on the Ancestors the claim carries,
// and it only works if that field survives the wire.
func TestBudgetExcusesAnAncestorAcrossTheSocket(t *testing.T) {
	budget := cache.NewMachineBudget(10_000, 8)
	srv, err := New(Options{
		Handler:       func(context.Context, []string) error { return nil },
		MachineBudget: budget,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	client := DaemonAdmitter{Addr: srv.Addr()}
	self := os.Getpid()

	// The parent run fills the machine, exactly as the shard's own ci step did.
	parent, err := client.Request(t.Context(), "parent", types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 10_000, Slots: 1, PID: self, Invocation: "3217:inv-parent",
	})
	require.NoError(t, err)
	require.True(t, parent.Granted)

	// A stranger is correctly refused: the machine really is full.
	stranger, err := client.Request(t.Context(), "stranger", types.MachineClaim{
		Project: "svc-a", Target: "alpha", MemoryMB: 500, Slots: 1, PID: self,
	})
	require.NoError(t, err)
	assert.False(t, stranger.Granted, "the budget is genuinely spent")

	// The parent's own descendant is not. Without this the pair deadlocks: the parent
	// cannot release until the run it is waiting for finishes.
	child, err := client.Request(t.Context(), "child", types.MachineClaim{
		Project: "svc-a", Target: "alpha", MemoryMB: 500, Slots: 1, PID: self,
		Ancestors: []string{"3217:inv-parent"},
	})
	require.NoError(t, err)
	assert.True(t, child.Granted, "the ancestry must cross the socket and excuse the parent's claim")
	assert.Empty(t, child.Holders, "and the parent is not reported to its own descendant as a peer")
}

// TestBudgetOnAServerWithNoBudget pins the answer a per-process proc server gives. Read
// as a grant it would let a run go unarbitrated while the daemon arbitrates its peers,
// so it must be an error the client can see.
func TestBudgetOnAServerWithNoBudget(t *testing.T) {
	srv, err := New(Options{Handler: func(context.Context, []string) error { return nil }})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	_, err = DaemonAdmitter{Addr: srv.Addr()}.Request(t.Context(), "w1", types.MachineClaim{
		Project: ".", Target: "test", PID: os.Getpid(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not arbitrate the machine budget")
}

// TestBudgetFramesRequireTheMagic pins the guard on the two frames that MUTATE state
// shared by every magus on the machine. A release without it would let anything on the
// socket drop a peer's claim, and the budget would then re-admit work against memory
// that is still held.
func TestBudgetFramesRequireTheMagic(t *testing.T) {
	budget := cache.NewMachineBudget(10_000, 8)
	srv, err := New(Options{
		Handler:       func(context.Context, []string) error { return nil },
		MachineBudget: budget,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	held := budget.Request("held", types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: os.Getpid(),
	})
	require.True(t, held.Granted)

	// An acquire with no magic is answered as unrecognized rather than acted on.
	var reply budgetAcquireReply
	err = DaemonAdmitter{Addr: srv.Addr()}.call(t.Context(), typeBudgetAcquire,
		budgetAcquireRequest{Protocol: protocolV2, Waiter: "w1"}, typeBudgetAcquireReply, &reply)
	require.NoError(t, err, "the server answers rather than hanging up")
	assert.Equal(t, "unrecognized request", reply.Err)

	// A release with no magic leaves the claim alone.
	var rel budgetReleaseReply
	err = DaemonAdmitter{Addr: srv.Addr()}.call(t.Context(), typeBudgetRelease,
		budgetReleaseRequest{Protocol: protocolV2, ID: held.ID}, typeBudgetReleaseReply, &rel)
	require.NoError(t, err)
	assert.Len(t, budget.Snapshot().Holders, 1, "an unauthenticated release must not free a peer's claim")
}

// TestBudgetDropRetiresAWaiter covers the fail-fast teardown: a run that will not queue
// must not leave a place in the queue behind, reserving room for a claim nobody wants.
func TestBudgetDropRetiresAWaiter(t *testing.T) {
	budget := cache.NewMachineBudget(10_000, 8)
	srv, err := New(Options{
		Handler:       func(context.Context, []string) error { return nil },
		MachineBudget: budget,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	client := DaemonAdmitter{Addr: srv.Addr()}
	self := os.Getpid()
	held, err := client.Request(t.Context(), "held", types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: self,
	})
	require.NoError(t, err)
	require.True(t, held.Granted)

	_, err = client.Request(t.Context(), "gone", types.MachineClaim{
		Project: "docs", Target: "ci", MemoryMB: 9000, PID: self,
	})
	require.NoError(t, err)
	client.Drop(t.Context(), "gone")
	client.Release(t.Context(), held.ID)

	assert.Empty(t, budget.Snapshot().Waiters)
	small, err := client.Request(t.Context(), "small", types.MachineClaim{
		Project: "docs", Target: "lint", MemoryMB: 500, PID: self,
	})
	require.NoError(t, err)
	assert.True(t, small.Granted, "nothing is reserving room for the waiter that left")
}

// TestBudgetCallIsBoundedByItsTimeout is B3. A daemon whose accept queue is full is not
// dead - the socket file is there and the connection never completes - so a dial
// outside the bound hangs the caller for as long as the daemon stays sick. For a
// release that runs inside the defer holding a local limiter slot, that is a stalled
// run rather than a slow one.
func TestBudgetCallIsBoundedByItsTimeout(t *testing.T) {
	old := admitTimeout
	admitTimeout = 150 * time.Millisecond
	defer func() { admitTimeout = old }()

	// A listener that never accepts. Once its backlog fills, further dials block in the
	// kernel rather than failing, which is the shape a wedged daemon has.
	dir, err := os.MkdirTemp("", "mgbudget")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(dir) }()
	sock := filepath.Join(dir, "wedged.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	client := DaemonAdmitter{Addr: "unix://" + sock}
	// Enough attempts to outlast any backlog: every one must return within the bound,
	// whether it blocked in the dial or in the read.
	for i := range 24 {
		start := time.Now()
		_, err := client.Request(t.Context(), "w1", types.MachineClaim{
			Project: ".", Target: "test", PID: os.Getpid(),
		})
		elapsed := time.Since(start)
		require.Error(t, err, "attempt %d: a wedged daemon answers nothing", i)
		assert.Less(t, elapsed, 3*time.Second,
			"attempt %d returned after %s; the timeout must cover the dial, not just the exchange", i, elapsed)
	}
}

// TestBudgetReportsTheBudgetInStatus is the observability half: `magus status` reads
// this to show what the machine is doing across worktrees.
func TestBudgetReportsTheBudgetInStatus(t *testing.T) {
	budget := cache.NewMachineBudget(10_000, 8)
	srv, err := New(Options{
		Handler:       func(context.Context, []string) error { return nil },
		MachineBudget: budget,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	_, err = DaemonAdmitter{Addr: srv.Addr()}.Request(t.Context(), "w1", types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Slots: 2, PID: os.Getpid(),
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

// TestIdleForCountsHeldClaimsAsBusy pins what the admission self-exit reads. A daemon
// holding claims is serving runs that are not talking to it - they took their claim,
// went quiet for the length of a build, and will come back to release it - so exiting
// under them would drop every claim on the machine.
func TestIdleForCountsHeldClaimsAsBusy(t *testing.T) {
	budget := cache.NewMachineBudget(10_000, 8)
	srv, err := New(Options{
		Handler:       func(context.Context, []string) error { return nil },
		MachineBudget: budget,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	idle, busy := srv.IdleFor(time.Now().Add(time.Hour))
	assert.False(t, busy, "a daemon nobody is using is not busy")
	assert.Greater(t, idle, 59*time.Minute, "and its idleness is measured from the last client")

	held := budget.Request("w1", types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: os.Getpid(),
	})
	require.True(t, held.Granted)
	_, busy = srv.IdleFor(time.Now().Add(time.Hour))
	assert.True(t, busy, "a held claim is a run in flight, however quiet the socket is")

	budget.Release(held.ID)
	_, busy = srv.IdleFor(time.Now().Add(time.Hour))
	assert.False(t, busy)
}
