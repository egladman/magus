package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
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

	first := b.Request(types.MachineClaim{Project: ".", Target: "test", MemoryMB: 8000, Slots: 4, PID: 100})
	require.True(t, first.Granted, "an empty machine seats the first claim")
	assert.Equal(t, 8000, first.HeldMB)

	// A second invocation, in another worktree, asking for more than what is left.
	second := b.Request(types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 8000, Slots: 4, PID: 200})
	assert.False(t, second.Granted, "the machine cannot seat both")
	assert.True(t, second.Fits, "it would fit on an idle machine, so the refusal is temporary")
	assert.False(t, second.OwnRun, "what fills the machine is another process's run")
	require.Len(t, second.Holders, 1, "the refusal names who holds the budget")
	assert.Equal(t, 100, second.Holders[0].PID)
	assert.Equal(t, "test", second.Holders[0].Target)

	b.Release(first.ID)
	third := b.Request(types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 8000, Slots: 4, PID: 200})
	assert.True(t, third.Granted, "the same claim is seated once the holder releases")
}

func TestMachineBudgetRefusesWhatCanNeverFit(t *testing.T) {
	b, _, _ := testBudget(t, 4000, 8)

	v := b.Request(types.MachineClaim{Project: ".", Target: "test", MemoryMB: 64_000, Slots: 1, PID: 100})
	assert.False(t, v.Granted)
	assert.False(t, v.Fits, "no state of the machine seats a claim larger than the whole budget")
}

// TestMachineBudgetKeepsNothingForARefusedClaim pins that a refusal leaves no trace. A
// refused claim reserving its room would turn away a smaller claim that fits, which is a
// spurious exit 75 for a run that nothing was in the way of.
func TestMachineBudgetKeepsNothingForARefusedClaim(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	held := b.Request(types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, Slots: 1, PID: 100})
	require.True(t, held.Granted)
	require.False(t, b.Request(types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 9000, Slots: 1, PID: 200}).Granted)

	light := b.Request(types.MachineClaim{Project: "docs", Target: "lint", MemoryMB: 500, Slots: 1, PID: 300})
	assert.True(t, light.Granted, "the refused claim holds no room")
	assert.Len(t, b.Snapshot().Holders, 2, "only the granted claims are on record")
}

func TestMachineBudgetRetiresClaimsOfDeadProcesses(t *testing.T) {
	b, _, alive := testBudget(t, 10_000, 8)
	require.True(t, b.Request(types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100}).Granted)

	// The holder is killed outright, so it never releases. Nothing but liveness can
	// retire the claim, and without that the budget stays spent forever.
	alive[100] = false
	v := b.Request(types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 9000, PID: 200})
	assert.True(t, v.Granted, "a claim whose process is gone stops counting")
	assert.Empty(t, v.Holders)
}

func TestMachineBudgetRetiresClaimsOlderThanAnyHonestBuild(t *testing.T) {
	b, now, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request(types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100}).Granted)

	// A daemon that outlived a reboot holds a pid the kernel has since reused, so
	// liveness alone says yes forever.
	*now = now.Add(machineClaimStaleAfter + time.Minute)
	assert.True(t, b.Request(types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 9000, PID: 200}).Granted)
}

func TestMachineBudgetExcludesAnAncestorsClaim(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	parent := b.Request(types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 9000, PID: 100, Invocation: "100:aaa",
	})
	require.True(t, parent.Granted)

	// The suite this claim covers runs `magus run test .`, which is a DESCENDANT of the
	// run already holding the declaration. Counting it would refuse its own child.
	child := b.Request(types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: 200, Ancestors: []string{"100:aaa"},
	})
	assert.True(t, child.Granted)
	assert.Empty(t, child.Holders, "an ancestor is not a peer")
}

// The cross-process half of the in-process fan-out deadlock: four steps of ONE invocation
// each shell out to a magus of their own, and every parent is blocked in exec waiting for
// its child, so a child excused from one parent claim and kept out by the other three is
// waiting on the very steps that are waiting on it.
func TestMachineBudgetSeatsEveryChildOfAFannedOutParent(t *testing.T) {
	b, _, _ := testBudget(t, 12_000, 16)
	targets := []string{"build", "test", "lint", "docs"}
	for _, target := range targets {
		require.True(t, b.Request(types.MachineClaim{
			Project: ".", Target: target, MemoryMB: 3000, Slots: 1, PID: 100, Invocation: "100:aaa",
		}).Granted, target)
	}

	child := func(i int) types.MachineClaim {
		return types.MachineClaim{
			Project: ".", Target: targets[i], MemoryMB: 4000, Slots: 1, PID: 200 + i,
			Invocation: fmt.Sprintf("%d:bbb", 200+i), Ancestors: []string{"100:aaa"},
		}
	}
	first := ""
	for i := range 3 {
		v := b.Request(child(i))
		require.True(t, v.Granted, targets[i])
		if first == "" {
			first = v.ID
		}
	}

	// Still a budget: the fourth child is a peer of the three now running, not of the
	// parents it is excused from, and 16 GB of concurrent declarations do not fit in 12.
	last := b.Request(child(3))
	assert.False(t, last.Granted, "a fourth concurrent 4 GB child does not fit in 12 GB")
	assert.True(t, last.Fits)
	assert.True(t, last.OwnRun, "what keeps it out is its own run's siblings, so it waits rather than failing the parent")
	assert.Equal(t, 12_000, last.HeldMB, "sibling descendants count; the parents' four claims do not")

	// A wait, not a deadlock: what it waits for is running and not blocked on it.
	b.Release(first)
	assert.True(t, b.Request(child(3)).Granted, "the waiting child is seated once a peer finishes")
}

// The pair no exclusion can reach: two independent roots, each blocked in exec on a child
// that needs more than the other root leaves free. Neither parent can release until its
// child runs, so without make's free slot the machine parks forever.
func TestMachineBudgetSeatsAChildOfEachStalledRoot(t *testing.T) {
	b, now, _ := testBudget(t, 12_000, 16)

	rootA := b.Request(types.MachineClaim{
		Project: "a", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 100, Invocation: "100:aaa",
	})
	require.True(t, rootA.Granted)
	rootB := b.Request(types.MachineClaim{
		Project: "b", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 200, Invocation: "200:bbb",
	})
	require.True(t, rootB.Granted)

	*now = now.Add(time.Second)
	childA := b.Request(types.MachineClaim{
		Project: "a", Target: "test", MemoryMB: 7000, Slots: 1, PID: 300,
		Invocation: "300:ccc", Ancestors: []string{"100:aaa"},
	})
	assert.True(t, childA.Granted, "the bottom of a stalled chain always has a seat")

	// The control. A top-level run has no stalled ancestor to charge a seat to, so this
	// cannot become a way past a full machine.
	*now = now.Add(time.Second)
	stranger := types.MachineClaim{Project: "c", Target: "build", MemoryMB: 7000, Slots: 1, PID: 400}
	v := b.Request(stranger)
	assert.False(t, v.Granted, "a stranger does not get a seat out of somebody else's stall")
	assert.True(t, v.Fits)
	assert.False(t, v.OwnRun, "and nothing in its way belongs to it, so it is refused")

	*now = now.Add(time.Second)
	childB := b.Request(types.MachineClaim{
		Project: "b", Target: "test", MemoryMB: 7000, Slots: 1, PID: 500,
		Invocation: "500:ddd", Ancestors: []string{"200:bbb"},
	})
	assert.True(t, childB.Granted, "the symmetric root is seated too, so both chains finish")

	b.Release(childA.ID)
	b.Release(rootA.ID)
	b.Release(childB.ID)
	b.Release(rootB.ID)
	assert.True(t, b.Request(stranger).Granted, "and the machine drains")
}

// TestMachineBudgetFreeSeatsOneChildPerStalledAncestor is the bound. Make's tokens are
// unit-sized, so a seat per requester costs it little; a claim here is megabytes, and
// four seats under one parent would put 28 GB of children on a 12 GB machine.
func TestMachineBudgetFreeSeatsOneChildPerStalledAncestor(t *testing.T) {
	b, now, _ := testBudget(t, 12_000, 16)
	stranger := b.Request(types.MachineClaim{
		Project: "other", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 100,
	})
	require.True(t, stranger.Granted)
	require.True(t, b.Request(types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 200, Invocation: "200:bbb",
	}).Granted)

	child := func(i int) types.MachineClaim {
		return types.MachineClaim{
			Project: ".", Target: fmt.Sprintf("t%d", i), MemoryMB: 7000, Slots: 1, PID: 300 + i,
			Invocation: fmt.Sprintf("%d:ccc", 300+i), Ancestors: []string{"200:bbb"},
		}
	}
	first := b.Request(child(0))
	require.True(t, first.Granted, "one child of a stalled parent is seated over budget")

	*now = now.Add(time.Second)
	for i := 1; i < 4; i++ {
		v := b.Request(child(i))
		assert.False(t, v.Granted, "the parent's one seat is spent on a child that IS running")
		assert.True(t, v.Fits)
		assert.False(t, v.OwnRun, "the stranger alone leaves too little room, so the child is refused")
	}
	assert.Equal(t, 19_000, b.Snapshot().HeldMB, "one claim over a full machine, which is make's own bound")

	b.Release(stranger.ID)
	b.Release(first.ID)
	assert.True(t, b.Request(child(1)).Granted, "a sibling is seated as peers release")
}

func TestMachineBudgetFreeSeatIsNotABypassForWhatCanNeverFit(t *testing.T) {
	b, _, _ := testBudget(t, 4000, 8)
	require.True(t, b.Request(types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 1000, Slots: 1, PID: 100, Invocation: "100:aaa",
	}).Granted)

	v := b.Request(types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 64_000, Slots: 1, PID: 200, Ancestors: []string{"100:aaa"},
	})
	assert.False(t, v.Granted)
	assert.False(t, v.Fits, "a seat is room for a claim this machine can hold, not a waiver of the budget")
}

func TestMachineSnapshotReportsHolders(t *testing.T) {
	b, _, alive := testBudget(t, 10_000, 8)
	require.True(t, b.Request(types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Slots: 2, PID: 100, Dir: "/tree/a",
	}).Granted)

	snap := b.Snapshot()
	assert.Equal(t, 10_000, snap.BudgetMB)
	assert.Equal(t, 9000, snap.HeldMB)
	assert.Equal(t, 2, snap.HeldSlots)
	require.Len(t, snap.Holders, 1)
	assert.Equal(t, "/tree/a", snap.Holders[0].Dir, "a holder names the tree to go and look at")

	// Snapshot retires nothing, so a corpse is filtered out of the report rather than
	// shown to a reader who would go looking for a process that has gone.
	alive[100] = false
	dead := b.Snapshot()
	assert.Empty(t, dead.Holders, "a dead holder is not reported")
	assert.Zero(t, dead.HeldMB, "and it is not billed either; the ci gate reads these totals as saturation")
	assert.Zero(t, dead.HeldSlots)
}

// fakeAdmitter is a MachineAdmitter whose answers a test writes. It records every
// request so the gate's calls can be observed.
type fakeAdmitter struct {
	mu       sync.Mutex
	budget   *MachineBudget
	requests int
	fail     error
	released []string
}

func (f *fakeAdmitter) Request(_ context.Context, c types.MachineClaim) (types.MachineVerdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if f.fail != nil {
		return types.MachineVerdict{}, f.fail
	}
	return f.budget.Request(c), nil
}

func (f *fakeAdmitter) Release(_ context.Context, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, id)
	f.budget.Release(id)
}

func testGate(t *testing.T, b *MachineBudget) (*machineGate, *fakeAdmitter) {
	t.Helper()
	adm := &fakeAdmitter{budget: b}
	g := &machineGate{admit: adm, log: newLogger("", 0)}
	return g, adm
}

func TestMachineGateFailsFastNamingTheHolder(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)

	_, err = g.acquire(t.Context(), types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted), "the code is what maps to exit 75")
	assert.Contains(t, err.Error(), "this machine's build budget is full")
	assert.Contains(t, err.Error(), "pid 100 (root) test")
	assert.Equal(t, 2, adm.requests, "a refusal is one request, not a poll")
}

// acquireAsync runs g.acquire on its own goroutine, so a test can tell a wait from an
// answer.
func acquireAsync(ctx context.Context, g *machineGate, c types.MachineClaim) <-chan error {
	done := make(chan error, 1)
	go func() {
		release, err := g.acquire(ctx, c)
		if release != nil {
			release()
		}
		done <- err
	}()
	return done
}

// requireWaits asserts the acquire is still pending, then that it is granted once
// free runs.
func requireWaits(t *testing.T, done <-chan error, free func()) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("acquire answered (%v) while the budget was full of its own run's claims; it must wait for them", err)
	case <-time.After(200 * time.Millisecond):
	}
	free()
	select {
	case err := <-done:
		require.NoError(t, err, "granted once its own run's claim is released")
	case <-time.After(5 * time.Second):
		t.Fatal("never granted after the blocking claim was released")
	}
}

// TestMachineGateWaitsOnClaimsOfItsOwnProcess is the daemon shape: two unrelated
// invocations in ONE process, and the budget is full of the first one's claim. Nothing
// here is another magus process, so the second queues rather than exiting 75.
func TestMachineGateWaitsOnClaimsOfItsOwnProcess(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "0")
	b, _, _ := testBudget(t, 10_000, 8)
	g, _ := testGate(t, b)

	release, err := g.acquire(t.Context(), types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: 100, Invocation: "100:inv-a",
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := acquireAsync(ctx, g, types.MachineClaim{
		Project: "docs", Target: "ci", MemoryMB: 9000, PID: 100, Invocation: "100:inv-b",
	})
	requireWaits(t, done, release)
}

// TestMachineGateWaitsOnItsOwnRunsSiblings is a ci target whose parallel steps each shell
// out to a magus of their own: the children are separate processes under one root, and
// the second queues behind the first rather than failing the parent. A stranger asking
// for the same room is still refused.
func TestMachineGateWaitsOnItsOwnRunsSiblings(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "0")
	const root = "100:inv-root"
	b, _, _ := testBudget(t, 10_000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 1000, PID: 100, Invocation: root,
	})
	require.NoError(t, err, "the parent step")
	releaseFirst, err := g.acquire(t.Context(), types.MachineClaim{
		Project: "libs/shared", Target: "build", MemoryMB: 9000, PID: 200,
		Invocation: "200:inv-child-a", Ancestors: []string{root},
	})
	require.NoError(t, err, "the first child is excused from its parent's claim")

	_, err = g.acquire(t.Context(), types.MachineClaim{
		Project: "x", Target: "build", MemoryMB: 9000, PID: 400, Invocation: "400:inv-stranger",
	})
	require.Error(t, err, "a stranger is refused, not queued")
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := acquireAsync(ctx, g, types.MachineClaim{
		Project: "libs/shared", Target: "test", MemoryMB: 9000, PID: 300,
		Invocation: "300:inv-child-b", Ancestors: []string{root},
	})
	requireWaits(t, done, releaseFirst)
}

// TestMachineGateStopsWaitingWithItsContext pins the bound on the own-run wait: the
// caller's context, and nothing left on the budget once it ends.
func TestMachineGateStopsWaitingWithItsContext(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "0")
	b, _, _ := testBudget(t, 10_000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	_, err = g.acquire(ctx, types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 100})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Len(t, b.Snapshot().Holders, 1, "a wait that ended holds nothing")
}

// TestRunAllReportsAMachineRefusal pins that a step the budget refuses is put on the
// record a failed step is: the refusal happens before Run, whose fail is what logs a
// failure and fires the result observers, so skipping that left `magus run` exiting 75
// with nothing printed after the header.
func TestRunAllReportsAMachineRefusal(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	b, _, _ := testBudget(t, 10_000, 8)
	held := b.Request("100.1", types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: 100, Dir: "/elsewhere/checkout",
	})
	require.True(t, held.Granted, "the fixture's holder must own the budget")

	var out bytes.Buffer
	c, err := Open(t.Context(), t.TempDir(),
		WithLogger(slog.New(NewPrettyHandler(&out, slog.LevelInfo))),
		WithMachineAdmission(LocalAdmitter{Budget: b}, true))
	require.NoError(t, err)

	var observed []error
	_, err = c.RunAll(t.Context(), []Step{{ProjectPath: ".", Target: "test", MemoryMB: 9000}},
		func(context.Context, Step) error {
			t.Error("a refused step must not run")
			return nil
		},
		OnResult(func(_ *Step, _ *Result, err error) { observed = append(observed, err) }))

	require.ErrorIs(t, err, types.MachineBudgetExhausted)
	var stated interface{ ExitCode() int }
	require.ErrorAs(t, err, &stated)
	assert.Equal(t, ExitCodeMachineBusy, stated.ExitCode())

	printed := out.String()
	assert.Contains(t, printed, "[fail] workspace test (not started)\n")
	assert.Contains(t, printed, "  cause: [MGS3009] not starting (root) test: this machine's build budget is full")
	assert.Contains(t, printed, "held by pid 100 (root) test (8.8 GiB)")
	assert.NotContains(t, printed, "output:", "nothing ran, so there is no captured output to point at")
	assert.Contains(t, printed, "  reproduce: magus run test .\n")

	require.Len(t, observed, 1, "the result observers feed -o jsonl's run.target.result and run.diagnostic")
	assert.ErrorIs(t, observed[0], types.MachineBudgetExhausted)
	assert.Equal(t, Stats{Error: 1}, c.Stats(), "a refusal counts toward the run's failed total")
}

func TestMachineGateRefusesWhatCanNeverFit(t *testing.T) {
	b, _, _ := testBudget(t, 4000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{
		Project: ".", Target: "ci", DeclaredBy: "test", MemoryMB: 64_000, PID: 100,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted))
	assert.Contains(t, err.Error(), "runs test, which declares 62.5 GiB",
		"a composed target is held over a number a target in its chain wrote")
	assert.Contains(t, err.Error(), "Waiting would not help")
}

// The refusal an author meets with a 26 GiB target on a 32 GiB machine. magus budgets 0.75
// of the machine, so the figure in the message is smaller than the RAM the reader can see,
// and a refusal that does not say so reads as arithmetic magus got wrong.
func TestMachineRefusalNamesTheFractionAndTheDeclarationCheck(t *testing.T) {
	b, _, _ := testBudget(t, 4000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{
		Project: ".", Target: "ci", DeclaredBy: "test", MemoryMB: 26_000, Slots: 1, PID: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "3.9 GiB", "the budget it did not fit in")
	assert.Contains(t, err.Error(), "declares 25.4 GiB", "and the declaration held against it")
	assert.Contains(t, err.Error(), "75% of the memory available here",
		"a budget smaller than the machine reads as a miscount unless the share is named")
	assert.Contains(t, err.Error(), "MGS1030",
		"magus has measured this target's peak, so the author is sent to the check rather than to a guess")
}

// The other axis: a step declaring more slots than the machine has cores is refused by the
// same path, and neither the memory fraction nor a memory check has anything to say about it.
func TestMachineRefusalForTooManySlotsStaysAboutSlots(t *testing.T) {
	b, _, _ := testBudget(t, 32_000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 1000, Slots: 32, PID: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "8 slots in total")
	assert.NotContains(t, err.Error(), "MGS1030", "a core count is not a declaration MGS1030 measures")
	assert.NotContains(t, err.Error(), "75%", "and the memory share is not why this was refused")
}

func TestMachineGateAdmitsWhenTheArbiterIsGone(t *testing.T) {
	b, _, _ := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)
	adm.fail = errors.New("dial: connection refused")

	release, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err, "losing the arbiter must not fail a build that was going to run")
	require.NotNil(t, release)
	release()
}

func TestMachineGateIsInertWithoutAnAdmitter(t *testing.T) {
	var g *machineGate
	release, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000})
	require.NoError(t, err)
	assert.NotNil(t, release, "a library caller with no host to arbitrate behaves as before")
}

// TestMachineGateRefusesANestedRunBlindToItsAncestry is C7. A nested magus that cannot
// name its ancestors cannot be excused from its parent's claim, so admitting it would
// admit a claim indistinguishable from a stranger's against a budget its own parent
// already filled.
func TestMachineGateRefusesANestedRunBlindToItsAncestry(t *testing.T) {
	// Nested, and the ancestry did not survive: a magusfile that cleared the
	// environment. Both variables are set explicitly because the fallback reads the
	// environment, so a test that named only one would be judging the harness's.
	t.Setenv("MAGUS_LEVEL", "1")
	t.Setenv("MAGUS_INVOCATION_ANCESTORS", "")
	b, _, _ := testBudget(t, 10_000, 8)
	g, _ := testGate(t, b)

	held, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)
	defer held()

	// Bounded, so a regression that admits this claim (instead of refusing it) fails in a
	// second rather than hanging the package until the go test timeout.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	// No Ancestors on the claim, none on the context, none in the environment.
	_, err = g.acquire(ctx, types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.Error(t, err, "this claim cannot be excused from its own parent's")
	require.NotErrorIs(t, err, context.DeadlineExceeded, "it must REFUSE immediately, not wait out the ctx")
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted))
	assert.Contains(t, err.Error(), "MAGUS_INVOCATION_ANCESTORS")
}

func TestMachineGateNotBlindWhenNotNested(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "0")
	assert.False(t, blindToOwnAncestry(t.Context()), "a top-level run has no ancestry to lose")
}

// The empty-ctx fallback has no producer on the run path: runResolved's first statement is
// attributeRun, which reads the environment itself and appends this run, so admit always
// sees a stamped ctx. This pins the contract rather than a caller.
func TestAncestryFallsBackToTheEnvironmentForAnUnstampedCaller(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "1")
	t.Setenv("MAGUS_INVOCATION_ANCESTORS", "3217:inv-parent")

	assert.Equal(t, []string{"3217:inv-parent"}, ancestorInvocations(context.Background()),
		"the environment is the only carrier an unstamped caller has")
	assert.False(t, blindToOwnAncestry(context.Background()),
		"a consumer that CAN name its ancestors is not blind, whatever stamped ctx")

	// ctx still wins when something upstream stamped it: under the daemon the process
	// environment belongs to no invocation, and the ancestry on ctx is the run's own.
	ctx := types.WithInvocationAncestors(context.Background(), []string{"55:inv-adopted"})
	assert.Equal(t, []string{"55:inv-adopted"}, ancestorInvocations(ctx),
		"a stamped ctx is authoritative; the environment is the fallback, not an override")
}

// The shape a library run actually arrives in: attributeRun leaves ancestry as
// [parent..., self], and mintedHere strips self, so an SDK consumer is excused from its
// parent's claim by a different branch than the test above.
func TestAncestryStripsThisRunFromWhatAttributeRunStamped(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "1")

	stamped := types.AppendInvocationAncestor(
		types.WithInvocationAncestors(context.Background(), []string{"3217:inv-parent"}),
		os.Getpid(), "inv-self")

	assert.Equal(t, []string{"3217:inv-parent"}, ancestorInvocations(stamped),
		"a run must not count itself among the claims it is excused from")
	assert.Equal(t, fmt.Sprintf("%d:inv-self", os.Getpid()), selfInvocation(stamped))
	assert.False(t, blindToOwnAncestry(stamped),
		"a run that can name its parent is not blind")
}

// The same case end to end through the budget: the parent's claim filled the machine, and
// the in-process run has to be excused from it or the pair deadlocks, since the parent
// cannot release until this run ends.
func TestLibraryCallerIsExcusedFromItsParentsClaim(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "1")
	t.Setenv("MAGUS_INVOCATION_ANCESTORS", "3217:inv-parent")

	b, _, _ := testBudget(t, 10_000, 8)
	parent := b.Request(types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 10_000, Slots: 1, PID: 3217, Invocation: "3217:inv-parent",
	})
	require.True(t, parent.Granted, "the shard's own run fills the machine")

	// The ctx admit actually sees for an SDK consumer: attributeRun adopted the parent
	// from the environment and appended this run, which ancestorInvocations strips back
	// off. A bare context.Background() here is a shape runResolved cannot produce.
	stamped := types.AppendInvocationAncestor(
		types.WithInvocationAncestors(context.Background(), []string{"3217:inv-parent"}),
		os.Getpid(), "inv-self")
	g, _ := testGate(t, b)
	release, err := g.acquire(stamped, types.MachineClaim{
		Project: "svc-a", Target: "alpha", MemoryMB: 500, Slots: 1, PID: 4000,
		Ancestors: ancestorInvocations(stamped),
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
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)
	_, busy := g.acquire(t.Context(), types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.Error(t, busy)

	_, tooBig := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 64_000, PID: 300})
	require.Error(t, tooBig)

	// The code is per REFUSAL, not per package. A busy machine is EX_TEMPFAIL because the
	// same command succeeds later; a declaration that exceeds the whole budget answers the
	// same way forever, and telling a retry wrapper it was temporary loops it.
	for _, tc := range []struct {
		err  error
		want int
	}{
		{busy, ExitCodeMachineBusy},
		{fmt.Errorf("run: %w", busy), ExitCodeMachineBusy},
		{tooBig, ExitCodeMachineDeclaration},
	} {
		var stated interface{ ExitCode() int }
		require.ErrorAs(t, tc.err, &stated, "%v must state its exit code across the socket", tc.err)
		assert.Equal(t, tc.want, stated.ExitCode(), "%v", tc.err)
		assert.True(t, errors.Is(tc.err, types.MachineBudgetExhausted), "and stay matchable by its code")
	}
	assert.NotEqual(t, ExitCodeMachineBusy, ExitCodeMachineDeclaration,
		"a permanent refusal that shares EX_TEMPFAIL is a retry loop")
}

// TestLocalAdmitterWithoutABudgetFailsOpen covers C12: a registry built with no budget
// (every test that does) must not panic, and must not silently grant either.
func TestLocalAdmitterWithoutABudgetFailsOpen(t *testing.T) {
	adm := LocalAdmitter{}
	_, err := adm.Request(t.Context(), types.MachineClaim{Project: ".", Target: "test"})
	require.Error(t, err, "no budget is no arbiter, which the gate reads as fail-open")
	assert.NotPanics(t, func() { adm.Release(t.Context(), "1.1") })
}
