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

	first := b.Request("w1", types.MachineClaim{Project: ".", Target: "test", MemoryMB: 8000, Slots: 4, PID: 100})
	require.True(t, first.Granted, "an empty machine seats the first claim")
	assert.Equal(t, 8000, first.HeldMB)

	// A second invocation, in another worktree, asking for more than what is left.
	second := b.Request("w2", types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 8000, Slots: 4, PID: 200})
	assert.False(t, second.Granted, "the machine cannot seat both")
	assert.True(t, second.Fits, "it would fit on an idle machine, so it queues rather than being refused")
	require.Len(t, second.Holders, 1, "the queue names who it is waiting for")
	assert.Equal(t, 100, second.Holders[0].PID)
	assert.Equal(t, "test", second.Holders[0].Target)

	b.Release(first.ID)
	third := b.Request("w2", types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 8000, Slots: 4, PID: 200})
	assert.True(t, third.Granted, "the waiter is seated once the holder releases")
}

func TestMachineBudgetRefusesWhatCanNeverFit(t *testing.T) {
	b, _, _ := testBudget(t, 4000, 8)

	v := b.Request("w1", types.MachineClaim{Project: ".", Target: "test", MemoryMB: 64_000, Slots: 1, PID: 100})
	assert.False(t, v.Granted)
	assert.False(t, v.Fits, "no position in any queue seats a claim larger than the whole budget")
}

func TestMachineBudgetReservesForTheHeadOfTheQueue(t *testing.T) {
	b, now, _ := testBudget(t, 10_000, 8)
	held := b.Request("held", types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, Slots: 1, PID: 100})
	require.True(t, held.Granted)

	// The heavy waiter queues first.
	heavy := types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 9000, Slots: 1, PID: 200}
	require.False(t, b.Request("heavy", heavy).Granted)

	*now = now.Add(time.Second)
	light := types.MachineClaim{Project: "docs", Target: "lint", MemoryMB: 500, Slots: 1, PID: 300}
	v := b.Request("light", light)
	assert.False(t, v.Granted, "a small claim must not take the room the head is queued for")
	assert.Equal(t, 1, v.Ahead)

	b.Release(held.ID)
	assert.True(t, b.Request("heavy", heavy).Granted, "the head goes first")
}

// TestMachineBudgetDoesNotReserveForADescendantsAncestor is the deadlock B1 names. The
// parent step is blocked in exec waiting for this very child, so it can never reach the
// front of the queue; reserving its room against the child leaves both parked forever.
func TestMachineBudgetDoesNotReserveForAnAncestor(t *testing.T) {
	b, now, _ := testBudget(t, 10_000, 8)

	// A stranger holds most of the machine, so the parent cannot be seated and queues.
	stranger := b.Request("stranger", types.MachineClaim{
		Project: "other", Target: "test", MemoryMB: 7000, Slots: 1, PID: 100, Invocation: "100:aaa",
	})
	require.True(t, stranger.Granted)

	*now = now.Add(time.Second)
	parent := types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 4000, Slots: 1, PID: 200, Invocation: "200:bbb"}
	require.False(t, b.Request("parent", parent).Granted, "the parent is the head of the queue")

	// Its own nested magus asks for room that only exists if the parent's reservation is
	// excused. Reserving it parks the pair: the parent is blocked in exec waiting for
	// this child, so it can never reach the front and free anything.
	*now = now.Add(time.Second)
	child := types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 3000, Slots: 1, PID: 300, Ancestors: []string{"200:bbb"},
	}
	assert.True(t, b.Request("child", child).Granted,
		"a child must not queue behind the parent that is waiting for it")

	// The control, and what keeps the assertion above from passing for the wrong reason:
	// the SAME request from a stranger is refused, because the parent's reservation
	// still applies to everyone who is not its descendant.
	stranger2 := types.MachineClaim{Project: "x", Target: "build", MemoryMB: 3000, Slots: 1, PID: 400}
	assert.False(t, b.Request("stranger2", stranger2).Granted,
		"the exclusion is for ancestors only; a stranger still reserves for the head")
}

func TestMachineBudgetRetiresClaimsOfDeadProcesses(t *testing.T) {
	b, _, alive := testBudget(t, 10_000, 8)
	require.True(t, b.Request("w1", types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100}).Granted)

	// The holder is killed outright, so it never releases. Nothing but liveness can
	// retire the claim, and without that the budget stays spent forever.
	alive[100] = false
	v := b.Request("w2", types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 9000, PID: 200})
	assert.True(t, v.Granted, "a claim whose process is gone stops counting")
	assert.Empty(t, v.Holders)
}

func TestMachineBudgetRetiresClaimsOlderThanAnyHonestBuild(t *testing.T) {
	b, now, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request("w1", types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100}).Granted)

	// A daemon that outlived a reboot holds a pid the kernel has since reused, so
	// liveness alone says yes forever.
	*now = now.Add(machineClaimStaleAfter + time.Minute)
	assert.True(t, b.Request("w2", types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 9000, PID: 200}).Granted)
}

// TestMachineBudgetDropsWaitersThatStoppedAsking fails with the waiter reap deleted:
// the abandoned head's reservation is sized so the live claim cannot fit around it.
func TestMachineBudgetDropsWaitersThatStoppedAsking(t *testing.T) {
	b, now, _ := testBudget(t, 10_000, 8)
	held := b.Request("held", types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9600, PID: 100})
	require.True(t, held.Granted)
	require.False(t, b.Request("gone", types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 9600, PID: 200}).Granted)

	*now = now.Add(machineWaiterStaleAfter + time.Second)
	b.Release(held.ID)
	v := b.Request("live", types.MachineClaim{Project: "docs", Target: "lint", MemoryMB: 500, PID: 300})
	assert.True(t, v.Granted, "a corpse at the head of the queue must not reserve room forever")
	assert.Empty(t, b.Snapshot().Waiters, "and it is gone from the registry, not merely ignored")
}

func TestMachineBudgetExcludesAnAncestorsClaim(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	parent := b.Request("parent", types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 9000, PID: 100, Invocation: "100:aaa",
	})
	require.True(t, parent.Granted)

	// The suite this claim covers runs `magus run test .`, which is a DESCENDANT of the
	// run already holding the declaration. Counting it would refuse its own child.
	child := b.Request("child", types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: 200, Ancestors: []string{"100:aaa"},
	})
	assert.True(t, child.Granted)
	assert.Empty(t, child.Holders, "an ancestor is not a peer")
}

// TestMachineBudgetExcusesOneClaimPerAncestor is C6: an invocation is not a step. A
// parent running several steps at once holds several claims, and excusing all of them
// would make its child blind to genuine peers and re-admit the oversubscription this
// whole mechanism exists to stop.
func TestMachineBudgetExcusesOneClaimPerAncestor(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	for _, target := range []string{"test", "lint", "docs"} {
		v := b.Request("parent-"+target, types.MachineClaim{
			Project: ".", Target: target, MemoryMB: 3000, Slots: 1, PID: 100, Invocation: "100:aaa",
		})
		require.True(t, v.Granted, target)
	}

	// One of those three spawned this child. Excusing all three would show it an empty
	// machine and seat it; excusing one leaves 6000 held, so its 5000 does not fit.
	child := types.MachineClaim{
		Project: ".", Target: "build", MemoryMB: 5000, Slots: 1, PID: 200, Ancestors: []string{"100:aaa"},
	}
	v := b.Request("child", child)
	assert.False(t, v.Granted, "only the parent STEP is excused, not every step its invocation runs")
	assert.Equal(t, 6000, v.HeldMB, "two of the parent's three claims still count")
	assert.Len(t, v.Holders, 2)
}

func TestMachineSnapshotReportsHoldersAndWaiters(t *testing.T) {
	b, now, alive := testBudget(t, 10_000, 8)
	require.True(t, b.Request("w1", types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Slots: 2, PID: 100, Dir: "/tree/a",
	}).Granted)
	*now = now.Add(time.Second)
	require.False(t, b.Request("w2", types.MachineClaim{
		Project: "docs", Target: "ci", MemoryMB: 9000, Slots: 1, PID: 200, Dir: "/tree/b",
	}).Granted)

	snap := b.Snapshot()
	assert.Equal(t, 10_000, snap.BudgetMB)
	assert.Equal(t, 9000, snap.HeldMB)
	assert.Equal(t, 2, snap.HeldSlots)
	require.Len(t, snap.Holders, 1)
	assert.Equal(t, "/tree/a", snap.Holders[0].Dir)
	require.Len(t, snap.Waiters, 1)
	assert.Equal(t, "/tree/b", snap.Waiters[0].Dir, "a waiter names the tree to go and look at")

	// Snapshot retires nothing, so a corpse is filtered out of the report rather than
	// shown to a reader who would go looking for a process that has gone.
	alive[100] = false
	assert.Empty(t, b.Snapshot().Holders, "a dead holder is not reported")
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

func (f *fakeAdmitter) Request(_ context.Context, waiter string, c types.MachineClaim) (types.MachineVerdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if f.fail != nil {
		return types.MachineVerdict{}, f.fail
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

func (f *fakeAdmitter) dropCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dropped)
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

	first, err := g.acquire(t.Context(), types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Slots: 1, PID: 100, Dir: "/tree/a",
	})
	require.NoError(t, err)

	// The second invocation queues, then is admitted the moment the first releases.
	done := make(chan error, 1)
	go func() {
		release, err := g.acquire(t.Context(), types.MachineClaim{
			Project: "docs", Target: "ci", MemoryMB: 9000, Slots: 1, PID: 200,
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
	assert.Contains(t, (*said)[0], "pid 100 (root) test (8.8 GiB), in /tree/a",
		"a wait a reader cannot attribute is a wait they can only interrupt")
	assert.Contains(t, (*said)[len(*said)-1], "machine budget freed")
}

// TestMachineGateHeartbeatsWhileQueued covers the line whose absence makes a queued run
// indistinguishable from a hung one. The cadence is a var so this costs milliseconds
// rather than the real fifteen seconds.
func TestMachineGateHeartbeatsWhileQueued(t *testing.T) {
	defer swapMachineWaitTimings(20*time.Millisecond, 30*time.Millisecond)()
	b, _, _ := testBudget(t, 10_000, 8)
	g, _, said := testGate(t, b, false)

	held, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	beats := make(chan struct{}, 1)
	g.notify = func(msg string) {
		*said = append(*said, msg)
		if strings.Contains(msg, "still queued") {
			select {
			case beats <- struct{}{}:
			default:
			}
		}
	}
	go func() {
		_, _ = g.acquire(ctx, types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	}()

	select {
	case <-beats:
	case <-time.After(5 * time.Second):
		t.Fatal("a queued run went silent; without the heartbeat it reads as hung")
	}
	cancel()
	held()
}

// swapMachineWaitTimings shortens the wait cadence for a test and returns the restore.
func swapMachineWaitTimings(poll, beat time.Duration) func() {
	oldPoll, oldBeat := machinePollEvery, machineWaitHeartbeat
	machinePollEvery, machineWaitHeartbeat = poll, beat
	return func() { machinePollEvery, machineWaitHeartbeat = oldPoll, oldBeat }
}

func TestMachineGateFailsFastNamingTheHolder(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	g, adm, _ := testGate(t, b, true)

	_, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)

	_, err = g.acquire(t.Context(), types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted), "the code is what maps to exit 75")
	assert.Contains(t, err.Error(), "MAGUS_NO_WAIT is set")
	assert.Contains(t, err.Error(), "pid 100 (root) test")
	assert.NotEmpty(t, adm.dropped, "a run that will not wait must not stay in the queue")
}

func TestMachineGateRefusesWhatCanNeverFit(t *testing.T) {
	b, _, _ := testBudget(t, 4000, 8)
	g, _, _ := testGate(t, b, false)

	_, err := g.acquire(t.Context(), types.MachineClaim{
		Project: ".", Target: "ci", DeclaredBy: "test", MemoryMB: 64_000, PID: 100,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted))
	assert.Contains(t, err.Error(), "runs test, which declares 62.5 GiB",
		"a composed target is held over a number a target in its chain wrote")
	assert.Contains(t, err.Error(), "Waiting would not help")
}

func TestMachineGateAdmitsWhenTheArbiterIsGone(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	g, adm, said := testGate(t, b, false)
	adm.fail = errors.New("dial: connection refused")

	release, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err, "losing the arbiter must not fail a build that was going to run")
	require.NotNil(t, release)
	release()
	assert.Empty(t, *said, "the notice is a log line, not a wait")
}

func TestMachineGateFinishesWhenTheDaemonDiesMidWait(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	g, adm, _ := testGate(t, b, false)

	held, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)
	defer held()

	done := make(chan error, 1)
	go func() {
		_, err := g.acquire(t.Context(), types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
		done <- err
	}()
	require.Eventually(t, func() bool {
		adm.mu.Lock()
		defer adm.mu.Unlock()
		return adm.requests > 1
	}, 5*time.Second, 10*time.Millisecond)

	before := adm.dropCount()
	adm.mu.Lock()
	adm.fail = errors.New("dial: connection refused")
	adm.mu.Unlock()
	assert.NoError(t, <-done, "a run that loses its daemon finishes rather than aborting")
	assert.Greater(t, adm.dropCount(), before,
		"and it leaves the queue on the way out; the head's whole claim is reserved until it does")
}

func TestMachineGateIsInertWithoutAnAdmitter(t *testing.T) {
	var g *machineGate
	release, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000})
	require.NoError(t, err)
	assert.NotNil(t, release, "a library caller with no host to arbitrate behaves as before")
}

// TestMachineWaiterIDsAreUniqueAcrossCaches is B2. The daemon holds one Cache per
// workspace and every one reports the daemon's pid, so a per-Cache counter handed two
// workspaces the same id and their queue entries overwrote each other.
func TestMachineWaiterIDsAreUniqueAcrossCaches(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	adm := &fakeAdmitter{budget: b}
	newGate := func() *machineGate {
		return &machineGate{admit: adm, log: newLogger("", 0), notify: func(string) {}}
	}
	held, err := newGate().acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)
	defer held()

	// Two workspaces in ONE process: same pid, different Cache, both queue.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for _, project := range []string{"docs", "console"} {
		go func() {
			_, _ = newGate().acquire(ctx, types.MachineClaim{
				Project: project, Target: "ci", MemoryMB: 9000, PID: 100,
			})
		}()
	}
	require.Eventually(t, func() bool { return len(b.Snapshot().Waiters) == 2 }, 5*time.Second, 10*time.Millisecond,
		"both workspaces must hold their own place in the queue")
}

// TestMachineGateRefusesRatherThanQueueWhenBlindToItsAncestry is C7. A nested magus
// that cannot name its ancestors cannot be excused from its parent's claim, so queueing
// is queueing behind a step that is blocked waiting for this very process.
func TestMachineGateRefusesRatherThanQueueWhenBlindToItsAncestry(t *testing.T) {
	// Nested, and the ancestry did not survive: a magusfile that cleared the
	// environment. Both variables are set explicitly because the fallback reads the
	// environment, so a test that named only one would be judging the harness's.
	t.Setenv("MAGUS_LEVEL", "1")
	t.Setenv("MAGUS_INVOCATION_ANCESTORS", "")
	b, _, _ := testBudget(t, 10_000, 8)
	g, adm, _ := testGate(t, b, false)

	held, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)
	defer held()

	// Bounded, so a regression that queues fails in a second instead of hanging the
	// package until the go test timeout - which is how this test first went wrong.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	// No Ancestors on the claim, none on the context, none in the environment.
	_, err = g.acquire(ctx, types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.Error(t, err, "queueing here would hang forever while the heartbeat says otherwise")
	require.NotErrorIs(t, err, context.DeadlineExceeded, "it must REFUSE, not queue until the caller gives up")
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted))
	assert.Contains(t, err.Error(), "MAGUS_INVOCATION_ANCESTORS")
	assert.NotEmpty(t, adm.dropped)
}

func TestMachineGateQueuesNormallyWhenNotNested(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "0")
	assert.False(t, blindToOwnAncestry(t.Context()), "a top-level run has no ancestry to lose")
}

// TestAncestryFallsBackToTheEnvironmentForALibraryCaller is the SDK case, and the one
// that broke CI. A Go test driving magus in-process passes a plain context.Background():
// only the CLI and the daemon stamp ancestry onto ctx, so reading ctx alone made every
// library consumer inside a magus process tree look like a nested run that had lost its
// ancestry - refused against the very claim its own parent was holding, with the
// variable sitting in the process environment the whole time.
func TestAncestryFallsBackToTheEnvironmentForALibraryCaller(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "1")
	t.Setenv("MAGUS_INVOCATION_ANCESTORS", "3217:inv-parent")

	assert.Equal(t, []string{"3217:inv-parent"}, ancestorInvocations(context.Background()),
		"the environment is the only carrier an SDK consumer has")
	assert.False(t, blindToOwnAncestry(context.Background()),
		"a consumer that CAN name its ancestors is not blind, whatever stamped ctx")

	// ctx still wins when something upstream stamped it: under the daemon the process
	// environment belongs to no invocation, and the ancestry on ctx is the run's own.
	ctx := types.WithInvocationAncestors(context.Background(), []string{"55:inv-adopted"})
	assert.Equal(t, []string{"55:inv-adopted"}, ancestorInvocations(ctx),
		"a stamped ctx is authoritative; the environment is the fallback, not an override")
}

// TestLibraryCallerIsExcusedFromItsParentsClaim is the same case end to end through the
// budget: the parent's claim filled the machine, and the in-process run has to be
// excused from it or the pair deadlocks - the parent cannot release until this run ends.
func TestLibraryCallerIsExcusedFromItsParentsClaim(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "1")
	t.Setenv("MAGUS_INVOCATION_ANCESTORS", "3217:inv-parent")

	b, _, _ := testBudget(t, 10_000, 8)
	parent := b.Request("parent", types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 10_000, Slots: 1, PID: 3217, Invocation: "3217:inv-parent",
	})
	require.True(t, parent.Granted, "the shard's own run fills the machine")

	// What RunAll builds for an SDK consumer: no ancestry on ctx, resolved from the env.
	g, _, _ := testGate(t, b, false)
	release, err := g.acquire(context.Background(), types.MachineClaim{
		Project: "svc-a", Target: "alpha", MemoryMB: 500, Slots: 1, PID: 4000,
		Ancestors: ancestorInvocations(context.Background()),
	})
	require.NoError(t, err, "an in-process run must be excused from the claim its own parent holds")
	require.NotNil(t, release)
	release()
}

func TestFormatMB(t *testing.T) {
	assert.Equal(t, "512 MiB", FormatMB(512))
	assert.Equal(t, "1.0 GiB", FormatMB(1024))
	assert.Equal(t, "10.0 GiB", FormatMB(10240))
}

func TestDescribeMachineHoldersBoundsTheList(t *testing.T) {
	holders := make([]types.MachineClaimant, 0, 6)
	for i := range 6 {
		holders = append(holders, types.MachineClaimant{Project: ".", Target: "test", PID: 100 + i})
	}
	got := describeMachineHolders(holders)
	assert.Equal(t, 4, strings.Count(got, "pid "), "a refusal names a few holders, not every one")
	assert.Contains(t, got, "and 2 more")
	assert.Equal(t, "nothing else holds a claim", describeMachineHolders(nil))
}

// TestMachineRefusalStatesItsExitCode pins the method the DAEMON reads. exitCodeOf
// sees the concrete error and could go on matching the diagnostic code; a run the
// daemon executes for an adopted client cannot, because the type does not survive the
// socket. Without the method the refusal exits 75 alone and 1 under a daemon, which is
// the exact split proc.ExitCode exists to close.
func TestMachineRefusalStatesItsExitCode(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	g, _, _ := testGate(t, b, true)

	_, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)
	_, busy := g.acquire(t.Context(), types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.Error(t, busy)

	_, tooBig := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 64_000, PID: 300})
	require.Error(t, tooBig)

	for _, err := range []error{busy, tooBig, fmt.Errorf("run: %w", busy)} {
		var stated interface{ ExitCode() int }
		require.ErrorAs(t, err, &stated, "%v must state its exit code across the socket", err)
		assert.Equal(t, ExitCodeMachineBusy, stated.ExitCode())
		assert.True(t, errors.Is(err, types.MachineBudgetExhausted), "and stay matchable by its code")
	}
}

// TestLocalAdmitterWithoutABudgetFailsOpen covers C12: a registry built with no budget
// (every test that does) must not panic, and must not silently grant either.
func TestLocalAdmitterWithoutABudgetFailsOpen(t *testing.T) {
	adm := LocalAdmitter{}
	_, err := adm.Request(t.Context(), "w1", types.MachineClaim{Project: ".", Target: "test"})
	require.Error(t, err, "no budget is no arbiter, which the gate reads as fail-open")
	assert.NotPanics(t, func() {
		adm.Release(t.Context(), "1.1")
		adm.Drop(t.Context(), "w1")
	})
}
