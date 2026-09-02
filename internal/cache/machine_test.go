package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// testBudget is a budget with the two readings a test cannot stage pinned: a clock it
// controls and a liveness answer that does not depend on what pids this machine has.
func testBudget(t *testing.T, mb, slots int) (*MachineBudget, *time.Time, map[int]bool) {
	t.Helper()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	alive := map[int]bool{}
	b := NewMachineBudget(mb, slots)
	b.now = func() time.Time { return now }
	b.alive = func(p int) bool {
		live, known := alive[p]
		return !known || live
	}
	return b, &now, alive
}

func TestMachineBudgetAdmitsUntilFull(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)

	first := b.Request("w1", MachineClaim{Project: ".", Target: "test", MemoryMB: 8000, Slots: 4, Pid: 100})
	require.True(t, first.Granted, "an empty machine seats the first claim")
	assert.Equal(t, 8000, first.HeldMB)

	// A second invocation, in another worktree, asking for more than what is left.
	second := b.Request("w2", MachineClaim{Project: ".", Target: "ci", MemoryMB: 8000, Slots: 4, Pid: 200})
	assert.False(t, second.Granted, "the machine cannot seat both")
	assert.True(t, second.Fits, "it would fit on an idle machine, so it queues rather than being refused")
	require.Len(t, second.Holders, 1, "the queue names who it is waiting for")
	assert.Equal(t, 100, second.Holders[0].Pid)
	assert.Equal(t, "test", second.Holders[0].Target)

	b.Release(first.ID)
	third := b.Request("w2", MachineClaim{Project: ".", Target: "ci", MemoryMB: 8000, Slots: 4, Pid: 200})
	assert.True(t, third.Granted, "the waiter is seated once the holder releases")
}

func TestMachineBudgetRefusesWhatCanNeverFit(t *testing.T) {
	b, _, _ := testBudget(t, 4000, 8)

	v := b.Request("w1", MachineClaim{Project: ".", Target: "test", MemoryMB: 64_000, Slots: 1, Pid: 100})
	assert.False(t, v.Granted)
	assert.False(t, v.Fits, "no position in any queue seats a claim larger than the whole budget")
}

func TestMachineBudgetReservesForTheHeadOfTheQueue(t *testing.T) {
	b, now, _ := testBudget(t, 10_000, 8)
	held := b.Request("held", MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, Slots: 1, Pid: 100})
	require.True(t, held.Granted)

	// The heavy waiter queues first.
	heavy := MachineClaim{Project: ".", Target: "ci", MemoryMB: 9000, Slots: 1, Pid: 200}
	require.False(t, b.Request("heavy", heavy).Granted)

	*now = now.Add(time.Second)
	light := MachineClaim{Project: "docs", Target: "lint", MemoryMB: 500, Slots: 1, Pid: 300}
	v := b.Request("light", light)
	assert.False(t, v.Granted, "a small claim must not take the room the head is queued for")
	assert.Equal(t, 1, v.Ahead)

	b.Release(held.ID)
	assert.True(t, b.Request("heavy", heavy).Granted, "the head goes first")
}

func TestMachineBudgetRetiresClaimsOfDeadProcesses(t *testing.T) {
	b, _, alive := testBudget(t, 10_000, 8)
	require.True(t, b.Request("w1", MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, Pid: 100}).Granted)

	// The holder is killed outright, so it never releases. Nothing but liveness can
	// retire the claim, and without that the budget stays spent forever.
	alive[100] = false
	v := b.Request("w2", MachineClaim{Project: ".", Target: "ci", MemoryMB: 9000, Pid: 200})
	assert.True(t, v.Granted, "a claim whose process is gone stops counting")
	assert.Empty(t, v.Holders)
}

func TestMachineBudgetRetiresClaimsOlderThanAnyHonestBuild(t *testing.T) {
	b, now, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request("w1", MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, Pid: 100}).Granted)

	// A daemon that outlived a reboot holds a pid the kernel has since reused, so
	// liveness alone says yes forever.
	*now = now.Add(machineClaimStaleAfter + time.Minute)
	assert.True(t, b.Request("w2", MachineClaim{Project: ".", Target: "ci", MemoryMB: 9000, Pid: 200}).Granted)
}

func TestMachineBudgetDropsWaitersThatStoppedAsking(t *testing.T) {
	b, now, _ := testBudget(t, 10_000, 8)
	held := b.Request("held", MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, Pid: 100})
	require.True(t, held.Granted)
	require.False(t, b.Request("gone", MachineClaim{Project: ".", Target: "ci", MemoryMB: 9000, Pid: 200}).Granted)

	*now = now.Add(machineWaiterStaleAfter + time.Second)
	b.Release(held.ID)
	v := b.Request("live", MachineClaim{Project: "docs", Target: "lint", MemoryMB: 500, Pid: 300})
	assert.True(t, v.Granted, "a corpse at the head of the queue must not reserve room forever")
}

func TestMachineBudgetExcludesAnAncestorsClaim(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	parent := b.Request("parent", MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 9000, Pid: 100, Invocation: "100:aaa",
	})
	require.True(t, parent.Granted)

	// The suite this claim covers runs `magus run test .`, which is a DESCENDANT of the
	// run already holding the declaration. Counting it would refuse its own child.
	child := b.Request("child", MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Pid: 200, Ancestors: []string{"100:aaa"},
	})
	assert.True(t, child.Granted)
	assert.Empty(t, child.Holders, "an ancestor is not a peer")
}

func TestMachineSnapshotReportsHoldersAndWaiters(t *testing.T) {
	b, now, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request("w1", MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Slots: 2, Pid: 100, Cwd: "/tree/a",
	}).Granted)
	*now = now.Add(time.Second)
	require.False(t, b.Request("w2", MachineClaim{
		Project: "docs", Target: "ci", MemoryMB: 9000, Slots: 1, Pid: 200, Cwd: "/tree/b",
	}).Granted)

	snap := b.Snapshot()
	assert.Equal(t, 10_000, snap.BudgetMB)
	assert.Equal(t, 9000, snap.HeldMB)
	assert.Equal(t, 2, snap.HeldSlots)
	require.Len(t, snap.Holders, 1)
	assert.Equal(t, "/tree/a", snap.Holders[0].Cwd)
	require.Len(t, snap.Waiters, 1)
	assert.Equal(t, "/tree/b", snap.Waiters[0].Cwd, "a waiter names the tree to go and look at")
}

// fakeAdmitter is a MachineAdmitter whose answers a test writes. It records every
// request so the gate's polling can be observed.
type fakeAdmitter struct {
	mu       sync.Mutex
	budget   *MachineBudget
	requests int
	fail     error
	released []string
	dropped  []string
}

func (f *fakeAdmitter) Request(_ context.Context, waiter string, c MachineClaim) (MachineVerdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if f.fail != nil {
		return MachineVerdict{}, f.fail
	}
	return f.budget.Request(waiter, c), nil
}

func (f *fakeAdmitter) Release(_ context.Context, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, id)
	f.budget.Release(id)
}

func (f *fakeAdmitter) Drop(_ context.Context, waiter string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, waiter)
	f.budget.Drop(waiter)
}

func testGate(t *testing.T, b *MachineBudget, noWait bool) (*machineGate, *fakeAdmitter, *[]string) {
	t.Helper()
	adm := &fakeAdmitter{budget: b}
	var said []string
	g := &machineGate{
		admit: adm, noWait: noWait, log: newLogger("", 0),
		notify: func(msg string) { said = append(said, msg) },
	}
	return g, adm, &said
}

func TestMachineGateQueuesAndNamesTheHolder(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	g, adm, said := testGate(t, b, false)

	first, err := g.acquire(t.Context(), MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Slots: 1, Pid: 100, Cwd: "/tree/a",
	})
	require.NoError(t, err)

	// The second invocation queues, then is admitted the moment the first releases.
	done := make(chan error, 1)
	go func() {
		release, err := g.acquire(t.Context(), MachineClaim{
			Project: "docs", Target: "ci", MemoryMB: 9000, Slots: 1, Pid: 200,
		})
		if release != nil {
			release()
		}
		done <- err
	}()

	require.Eventually(t, func() bool {
		adm.mu.Lock()
		defer adm.mu.Unlock()
		return adm.requests > 1
	}, 5*time.Second, 10*time.Millisecond, "the queued run keeps asking")
	first()

	require.NoError(t, <-done, "the queued run starts once room frees")
	require.NotEmpty(t, *said)
	assert.Contains(t, (*said)[0], "queued for this machine's build budget")
	assert.Contains(t, (*said)[0], "pid 100 (root) test (8.8GB), in /tree/a",
		"a wait a reader cannot attribute is a wait they can only interrupt")
	assert.Contains(t, (*said)[len(*said)-1], "machine budget freed")
}

func TestMachineGateFailsFastNamingTheHolder(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	g, adm, _ := testGate(t, b, true)

	_, err := g.acquire(t.Context(), MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, Pid: 100})
	require.NoError(t, err)

	_, err = g.acquire(t.Context(), MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, Pid: 200})
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted), "the code is what maps to exit 75")
	assert.Contains(t, err.Error(), "MAGUS_NO_WAIT is set")
	assert.Contains(t, err.Error(), "pid 100 (root) test")
	assert.NotEmpty(t, adm.dropped, "a run that will not wait must not stay in the queue")
}

func TestMachineGateRefusesWhatCanNeverFit(t *testing.T) {
	b, _, _ := testBudget(t, 4000, 8)
	g, _, _ := testGate(t, b, false)

	_, err := g.acquire(t.Context(), MachineClaim{
		Project: ".", Target: "ci", DeclaredBy: "test", MemoryMB: 64_000, Pid: 100,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted))
	assert.Contains(t, err.Error(), "runs test, which declares 62.5GB",
		"a composed target is held over a number a target in its chain wrote")
	assert.Contains(t, err.Error(), "Waiting would not help")
}

func TestMachineGateAdmitsWhenTheArbiterIsGone(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	g, adm, said := testGate(t, b, false)
	adm.fail = errors.New("dial: connection refused")

	release, err := g.acquire(t.Context(), MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, Pid: 100})
	require.NoError(t, err, "losing the arbiter must not fail a build that was going to run")
	require.NotNil(t, release)
	release()
	assert.Empty(t, *said, "the notice is a log line, not a wait")
}

func TestMachineGateFinishesWhenTheDaemonDiesMidWait(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	g, adm, _ := testGate(t, b, false)

	held, err := g.acquire(t.Context(), MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, Pid: 100})
	require.NoError(t, err)
	defer held()

	done := make(chan error, 1)
	go func() {
		_, err := g.acquire(t.Context(), MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, Pid: 200})
		done <- err
	}()
	require.Eventually(t, func() bool {
		adm.mu.Lock()
		defer adm.mu.Unlock()
		return adm.requests > 1
	}, 5*time.Second, 10*time.Millisecond)

	adm.mu.Lock()
	adm.fail = errors.New("dial: connection refused")
	adm.mu.Unlock()
	assert.NoError(t, <-done, "a run that loses its daemon finishes rather than aborting")
}

func TestMachineGateIsInertWithoutAnAdmitter(t *testing.T) {
	var g *machineGate
	release, err := g.acquire(t.Context(), MachineClaim{Project: ".", Target: "test", MemoryMB: 9000})
	require.NoError(t, err)
	assert.NotNil(t, release, "a library caller with no host to arbitrate behaves as before")
}

// TestMachineRefusalStatesItsExitCode pins the method the DAEMON reads. exitCodeOf
// sees the concrete error and could go on matching the diagnostic code; a run the
// daemon executes for an adopted client cannot, because the type does not survive the
// socket. Without the method the refusal exits 75 alone and 1 under a daemon, which is
// the exact split proc.ExitCode exists to close.
func TestMachineRefusalStatesItsExitCode(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	g, _, _ := testGate(t, b, true)

	_, err := g.acquire(t.Context(), MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, Pid: 100})
	require.NoError(t, err)
	_, busy := g.acquire(t.Context(), MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, Pid: 200})
	require.Error(t, busy)

	_, tooBig := g.acquire(t.Context(), MachineClaim{Project: ".", Target: "ci", MemoryMB: 64_000, Pid: 300})
	require.Error(t, tooBig)

	for _, err := range []error{busy, tooBig, fmt.Errorf("run: %w", busy)} {
		var stated interface{ ExitCode() int }
		require.ErrorAs(t, err, &stated, "%v must state its exit code across the socket", err)
		assert.Equal(t, ExitCodeMachineBusy, stated.ExitCode())
		assert.True(t, errors.Is(err, types.MachineBudgetExhausted), "and stay matchable by its code")
	}
}

func TestFormatMB(t *testing.T) {
	assert.Equal(t, "512MB", FormatMB(512))
	assert.Equal(t, "1.0GB", FormatMB(1024))
	assert.Equal(t, "10.0GB", FormatMB(10240))
}

func TestDescribeMachineHoldersBoundsTheList(t *testing.T) {
	holders := make([]MachineHolder, 0, 6)
	for i := range 6 {
		holders = append(holders, MachineHolder{Project: ".", Target: "test", Pid: 100 + i})
	}
	got := describeMachineHolders(holders)
	assert.Equal(t, 4, strings.Count(got, "pid "), "a refusal names a few holders, not every one")
	assert.Contains(t, got, "and 2 more")
	assert.Equal(t, "nothing else holds a claim", describeMachineHolders(nil))
}
