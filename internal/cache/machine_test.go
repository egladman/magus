package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/types"
)

// testBudget is a budget with a clock the test controls.
func testBudget(t *testing.T, mb, slots int) (*MachineBudget, *time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	b := NewMachineBudget(mb, slots)
	b.now = func() time.Time { return now }
	return b, &now
}

func TestMachineBudgetAdmitsUntilFull(t *testing.T) {
	b, now := testBudget(t, 10_000, 8)

	first := b.Request(types.MachineClaim{Project: ".", Target: "test", MemoryMB: 8000, Slots: 4, PID: 100}, 0)
	require.True(t, first.Granted, "an empty machine seats the first claim")
	assert.Equal(t, 8000, first.HeldMB)

	// A second invocation, in another worktree, asking for more than what is left.
	second := b.Request(types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 8000, Slots: 4, PID: 200}, 0)
	assert.False(t, second.Granted, "the machine cannot seat both")
	assert.True(t, second.Fits, "it would fit on an idle machine, so the refusal is temporary")
	assert.False(t, second.OwnRun, "what fills the machine is another process's run")
	assert.Equal(t, []types.MachineClaimant{
		{Project: ".", Target: "test", PID: 100, MemoryMB: 8000, Slots: 4, Since: *now},
	}, second.Holders, "the refusal names who holds the budget")

	b.Release(first.ID)
	third := b.Request(types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 8000, Slots: 4, PID: 200}, 0)
	assert.True(t, third.Granted, "the same claim is seated once the holder releases")
}

func TestMachineBudgetRefusesWhatCanNeverFit(t *testing.T) {
	b, _ := testBudget(t, 4000, 8)

	v := b.Request(types.MachineClaim{Project: ".", Target: "test", MemoryMB: 64_000, Slots: 1, PID: 100}, 0)
	assert.False(t, v.Granted)
	assert.False(t, v.Fits, "no state of the machine seats a claim larger than the whole budget")
}

func TestRequestGrantsAnElasticClaimEveryFreeSlotWhenNobodyWaits(t *testing.T) {
	b, now := testBudget(t, 0, 10)
	vet := b.Request(types.MachineClaim{Project: ".", Target: "vet", Slots: 4, PID: 100}, 0)
	require.True(t, vet.Granted)

	v := b.Request(types.MachineClaim{Project: ".", Target: "test", Slots: 8, SlotsFloor: 1, PID: 200}, 0)
	assert.Equal(t, types.MachineVerdict{
		Granted: true, ID: v.ID, Fits: true, BudgetSlots: 10, HeldSlots: 10, Slots: 6,
		Holders: []types.MachineClaimant{{Project: ".", Target: "vet", PID: 100, Slots: 4, Since: *now}},
	}, v, "it takes what is free, short of its ceiling")
	assert.Equal(t, []types.MachineClaimant{
		{Project: ".", Target: "test", PID: 200, Slots: 6, Since: *now},
		{Project: ".", Target: "vet", PID: 100, Slots: 4, Since: *now},
	}, b.Snapshot().Holders, "the claim holds what it was granted")
}

func TestRequestLeavesEachWaiterAShareOfTheFreeSlots(t *testing.T) {
	b, _ := testBudget(t, 0, 10)

	v := b.Request(types.MachineClaim{Project: ".", Target: "test", Slots: 8, SlotsFloor: 1, PID: 200}, 3)
	require.True(t, v.Granted)
	assert.Equal(t, 2, v.Slots, "10 free among itself and 3 waiters")

	narrow := b.Request(types.MachineClaim{Project: ".", Target: "lint", Slots: 8, SlotsFloor: 1, PID: 300}, 20)
	require.True(t, narrow.Granted)
	assert.Equal(t, 1, narrow.Slots, "a share never falls under one slot")
}

func TestRequestSeatsAnElasticClaimAtItsFloor(t *testing.T) {
	b, _ := testBudget(t, 0, 4)
	require.True(t, b.Request(types.MachineClaim{Project: ".", Target: "vet", Slots: 3, PID: 100}, 0).Granted)

	v := b.Request(types.MachineClaim{Project: ".", Target: "test", Slots: 8, SlotsFloor: 1, PID: 200}, 0)
	require.True(t, v.Granted, "the floor fits where the ceiling does not")
	assert.Equal(t, 1, v.Slots)

	fixed := b.Request(types.MachineClaim{Project: ".", Target: "scip", Slots: 4, SlotsFloor: 4, PID: 300}, 0)
	assert.False(t, fixed.Granted, "a declared width is its floor as well as its ceiling")
}

func TestRequestNamesTheParkedClaimsAheadOfOneItDoesNotSeat(t *testing.T) {
	b, _ := testBudget(t, 0, 2)
	require.True(t, b.Request(types.MachineClaim{Project: ".", Target: "vet", Slots: 2, PID: 100}, 0).Granted)

	v := b.Request(types.MachineClaim{Project: ".", Target: "test", Slots: 1, PID: 200}, 3)
	require.False(t, v.Granted)
	assert.Equal(t, 3, v.Ahead)
	assert.Zero(t, v.Slots, "nothing was granted")
}

func TestRequestNamesTheComposerAHolderRunsUnder(t *testing.T) {
	b, _ := testBudget(t, 0, 2)
	require.True(t, b.Request(types.MachineClaim{Project: ".", Target: "conventions", Under: ".:lint", Slots: 2, PID: 100}, 0).Granted)

	v := b.Request(types.MachineClaim{Project: ".", Target: "test", Slots: 1, PID: 200}, 0)
	require.Len(t, v.Holders, 1)
	assert.Equal(t, ".:lint", v.Holders[0].Under)
}

// curveClaim is a claim over a 100s run holding early for its first half and late for
// its second, with a duration band from d10Ms to 100s, claiming its peak.
func curveClaim(pid int, early, late uint32, d10Ms int64) types.MachineClaim {
	p := types.MemoryProfile{D10Ms: d10Ms, D90Ms: 100_000, Runs: 3}
	for i := range p.Curve {
		p.Curve[i] = early
		if i >= types.MemoryCurveBuckets/2 {
			p.Curve[i] = late
		}
	}
	return types.MachineClaim{Project: ".", Target: "test", MemoryMB: p.Curve.PeakMB(), Profile: p, Slots: 1, PID: pid}
}

func TestRequestPacksCurvesWhosePeaksDoNotOverlap(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request(curveClaim(100, 7000, 1000, 100_000), 0).Granted)

	v := b.Request(curveClaim(200, 1000, 7000, 100_000), 0)
	assert.True(t, v.Granted, "an early peak and a late one sum to 8000 at every instant")
	assert.True(t, v.Packed)
	assert.Equal(t, 14_000, v.HeldMB, "what is held is still reported as peaks")
}

func TestRequestRefusesWhenBandsOverlapAtThePeak(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request(curveClaim(100, 7000, 1000, 50_000), 0).Granted)

	v := b.Request(curveClaim(200, 1000, 7000, 50_000), 0)
	assert.False(t, v.Granted, "with D10 at half of D90 the first may still be early when the second is late")
	assert.True(t, v.Fits)
	assert.True(t, v.Packed)
}

func TestOverrunClaimCountsItsPeak(t *testing.T) {
	b, now := testBudget(t, 10_000, 8)
	require.True(t, b.Request(curveClaim(100, 7000, 1000, 100_000), 0).Granted)

	*now = now.Add(60 * time.Second)
	within := b.Request(curveClaim(200, 1000, 7000, 100_000), 0)
	require.True(t, within.Granted, "60s in, the first holds its late 1000 and is expected released at 100s")
	b.Release(within.ID)

	*now = now.Add(41 * time.Second)
	v := b.Request(curveClaim(200, 1000, 7000, 100_000), 0)
	assert.False(t, v.Granted, "past its D90 the first is charged its 7000 peak until released")
	assert.True(t, v.Fits)
}

func TestOverrunCurveCountsItsPeak(t *testing.T) {
	b, now := testBudget(t, 10_000, 8)
	first := b.Request(curveClaim(100, 7000, 1000, 100_000), 0)
	require.True(t, first.Granted)
	*now = now.Add(60 * time.Second)

	b.Overrun(first.ID)
	b.Overrun("unknown")
	v := b.Request(curveClaim(200, 1000, 7000, 100_000), 0)
	assert.False(t, v.Granted, "an overrun claim is charged its 7000 peak, not its late 1000")
	assert.True(t, v.Fits)

	b.Release(first.ID)
	assert.True(t, b.Request(curveClaim(200, 1000, 7000, 100_000), 0).Granted, "released, it holds nothing")
}

func TestClaimWithoutProfileCountsItsPeak(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request(curveClaim(100, 1000, 7000, 100_000), 0).Granted)

	flat := types.MachineClaim{Project: ".", Target: "lint", MemoryMB: 7000, Slots: 1, PID: 200}
	v := b.Request(flat, 0)
	assert.False(t, v.Granted, "a claim with no curve holds 7000 from now, across the first's late 7000")
	assert.True(t, v.Packed, "the held claim is still judged by its curve")

	peaks, _ := testBudget(t, 10_000, 8)
	held := curveClaim(100, 1000, 7000, 100_000)
	held.Profile = types.MemoryProfile{}
	require.True(t, peaks.Request(held, 0).Granted)
	v = peaks.Request(flat, 0)
	assert.False(t, v.Granted, "two peaks held flat sum to 14000")
	assert.False(t, v.Packed)

	early := b.Request(types.MachineClaim{Project: ".", Target: "vet", MemoryMB: 3000, Slots: 1, PID: 300}, 0)
	assert.True(t, early.Granted, "3000 flat fits beside the first's 7000 peak")
}

// TestMachineBudgetKeepsNothingForARefusedClaim pins that a refusal leaves no trace. A
// refused claim reserving its room would turn away a smaller claim that fits, which is a
// spurious exit 75 for a run that nothing was in the way of.
func TestMachineBudgetKeepsNothingForARefusedClaim(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	held := b.Request(types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, Slots: 1, PID: 100}, 0)
	require.True(t, held.Granted)
	require.False(t, b.Request(types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 9000, Slots: 1, PID: 200}, 0).Granted)

	light := b.Request(types.MachineClaim{Project: "docs", Target: "lint", MemoryMB: 500, Slots: 1, PID: 300}, 0)
	assert.True(t, light.Granted, "the refused claim holds no room")
	assert.Len(t, b.Snapshot().Holders, 2, "only the granted claims are on record")
}

// TestMachineBudgetAssertRecordsWhatDoesNotFit pins the re-assert a holder makes on a new
// broker: its step is already running, so the claim is recorded whatever it does to the
// arithmetic, and the next request sees the memory as spent.
func TestMachineBudgetAssertRecordsWhatDoesNotFit(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request(types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100}, 0).Granted)

	id := b.Assert(types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.NotEmpty(t, id)
	// An asserted claim counts even over the budget, and takes the one slot every step spends.
	snap := b.Snapshot()
	assert.Equal(t, types.MachineSnapshot{
		BudgetMB: 10_000, HeldMB: 18_000, BudgetSlots: 8, HeldSlots: 2,
		Holders: snap.Holders, // the two claimants are asserted by the releases below
	}, snap)

	v := b.Request(types.MachineClaim{Project: "x", Target: "lint", MemoryMB: 500, PID: 300}, 0)
	assert.False(t, v.Granted, "new work is not admitted against memory a running step holds")

	b.Release(id)
	assert.Equal(t, 9000, b.Snapshot().HeldMB)
}

func TestMachineBudgetExcludesAnAncestorsClaim(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	parent := b.Request(types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 9000, PID: 100, Invocation: "100:aaa",
	}, 0)
	require.True(t, parent.Granted)

	// The suite this claim covers runs `magus run test .`, which is a DESCENDANT of the
	// run already holding the declaration. Counting it would refuse its own child.
	child := b.Request(types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: 200, Ancestors: []string{"100:aaa"},
	}, 0)
	assert.True(t, child.Granted)
	assert.Empty(t, child.Holders, "an ancestor is not a peer")
}

// The cross-process half of the in-process fan-out deadlock: four steps of ONE invocation
// each shell out to a magus of their own, and every parent is blocked in exec waiting for
// its child, so a child excused from one parent claim and kept out by the other three is
// waiting on the very steps that are waiting on it.
func TestMachineBudgetSeatsEveryChildOfAFannedOutParent(t *testing.T) {
	b, _ := testBudget(t, 12_000, 16)
	targets := []string{"build", "test", "lint", "docs"}
	for _, target := range targets {
		require.True(t, b.Request(types.MachineClaim{
			Project: ".", Target: target, MemoryMB: 3000, Slots: 1, PID: 100, Invocation: "100:aaa",
		}, 0).Granted, target)
	}

	child := func(i int) types.MachineClaim {
		return types.MachineClaim{
			Project: ".", Target: targets[i], MemoryMB: 4000, Slots: 1, PID: 200 + i,
			Invocation: fmt.Sprintf("%d:bbb", 200+i), Ancestors: []string{"100:aaa"},
		}
	}
	first := ""
	for i := range 3 {
		v := b.Request(child(i), 0)
		require.True(t, v.Granted, targets[i])
		if first == "" {
			first = v.ID
		}
	}

	// Still a budget: the fourth child is a peer of the three now running, not of the
	// parents it is excused from, and 16 GB of concurrent declarations do not fit in 12.
	last := b.Request(child(3), 0)
	assert.False(t, last.Granted, "a fourth concurrent 4 GB child does not fit in 12 GB")
	assert.True(t, last.Fits)
	assert.True(t, last.OwnRun, "what keeps it out is its own run's siblings, so it waits rather than failing the parent")
	assert.Equal(t, 12_000, last.HeldMB, "sibling descendants count; the parents' four claims do not")

	// A wait, not a deadlock: what it waits for is running and not blocked on it.
	b.Release(first)
	assert.True(t, b.Request(child(3), 0).Granted, "the waiting child is seated once a peer finishes")
}

// The pair no exclusion can reach: two independent roots, each blocked in exec on a child
// that needs more than the other root leaves free. Neither parent can release until its
// child runs, so without make's free slot the machine parks forever.
func TestMachineBudgetSeatsAChildOfEachStalledRoot(t *testing.T) {
	b, now := testBudget(t, 12_000, 16)

	rootA := b.Request(types.MachineClaim{
		Project: "a", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 100, Invocation: "100:aaa",
	}, 0)
	require.True(t, rootA.Granted)
	rootB := b.Request(types.MachineClaim{
		Project: "b", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 200, Invocation: "200:bbb",
	}, 0)
	require.True(t, rootB.Granted)

	*now = now.Add(time.Second)
	childA := b.Request(types.MachineClaim{
		Project: "a", Target: "test", MemoryMB: 7000, Slots: 1, PID: 300,
		Invocation: "300:ccc", Ancestors: []string{"100:aaa"},
	}, 0)
	assert.True(t, childA.Granted, "the bottom of a stalled chain always has a seat")

	// The control. A top-level run has no stalled ancestor to charge a seat to, so this
	// cannot become a way past a full machine.
	*now = now.Add(time.Second)
	stranger := types.MachineClaim{Project: "c", Target: "build", MemoryMB: 7000, Slots: 1, PID: 400}
	v := b.Request(stranger, 0)
	assert.False(t, v.Granted, "a stranger does not get a seat out of somebody else's stall")
	assert.True(t, v.Fits)
	assert.False(t, v.OwnRun, "and nothing in its way belongs to it, so it is refused")

	*now = now.Add(time.Second)
	childB := b.Request(types.MachineClaim{
		Project: "b", Target: "test", MemoryMB: 7000, Slots: 1, PID: 500,
		Invocation: "500:ddd", Ancestors: []string{"200:bbb"},
	}, 0)
	assert.True(t, childB.Granted, "the symmetric root is seated too, so both chains finish")

	b.Release(childA.ID)
	b.Release(rootA.ID)
	b.Release(childB.ID)
	b.Release(rootB.ID)
	assert.True(t, b.Request(stranger, 0).Granted, "and the machine drains")
}

// TestMachineBudgetFreeSeatsOneChildPerStalledAncestor is the bound. Make's tokens are
// unit-sized, so a seat per requester costs it little; a claim here is megabytes, and
// four seats under one parent would put 28 GB of children on a 12 GB machine.
func TestMachineBudgetFreeSeatsOneChildPerStalledAncestor(t *testing.T) {
	b, now := testBudget(t, 12_000, 16)
	stranger := b.Request(types.MachineClaim{
		Project: "other", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 100,
	}, 0)
	require.True(t, stranger.Granted)
	require.True(t, b.Request(types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 200, Invocation: "200:bbb",
	}, 0).Granted)

	child := func(i int) types.MachineClaim {
		return types.MachineClaim{
			Project: ".", Target: fmt.Sprintf("t%d", i), MemoryMB: 7000, Slots: 1, PID: 300 + i,
			Invocation: fmt.Sprintf("%d:ccc", 300+i), Ancestors: []string{"200:bbb"},
		}
	}
	first := b.Request(child(0), 0)
	require.True(t, first.Granted, "one child of a stalled parent is seated over budget")

	*now = now.Add(time.Second)
	for i := 1; i < 4; i++ {
		v := b.Request(child(i), 0)
		assert.False(t, v.Granted, "the parent's one seat is spent on a child that IS running")
		assert.True(t, v.Fits)
		assert.False(t, v.OwnRun, "the stranger alone leaves too little room, so the child is refused")
	}
	assert.Equal(t, 19_000, b.Snapshot().HeldMB, "one claim over a full machine, which is make's own bound")

	b.Release(stranger.ID)
	b.Release(first.ID)
	assert.True(t, b.Request(child(1), 0).Granted, "a sibling is seated as peers release")
}

// wrapped is a `magus job exec --timeout` hold, as the broker asserts it.
func wrapped(pid int, ancestors ...string) types.MachineClaim {
	return types.MachineClaim{
		Target: "job exec", Slots: 1, PID: pid, Ancestors: ancestors,
		Invocation: fmt.Sprintf("%d:%s", pid, types.WrapperInvocation("exec")),
	}
}

// A wrapper's child takes the slot the wrapper holds, however little is free.
func TestMachineBudgetSharesAWrappersClaimWithItsChild(t *testing.T) {
	b, _ := testBudget(t, 0, 1)
	w := wrapped(100)
	b.Assert(w)
	v := b.Request(types.MachineClaim{Project: "x", Target: "install", Slots: 1, PID: 200, Ancestors: []string{w.Invocation}}, 0)
	assert.True(t, v.Granted, "the only slot is its wrapper's")

	stranger := b.Request(types.MachineClaim{Project: "other", Target: "build", Slots: 1, PID: 300}, 0)
	assert.False(t, stranger.Granted, "a stranger gets neither")
}

// A full machine has no stalled step to charge a wrapper's child a seat to.
func TestMachineBudgetGivesAWrappersChildNoSeatOnAFullMachine(t *testing.T) {
	b, _ := testBudget(t, 1000, 4)
	require.True(t, b.Request(types.MachineClaim{Project: "other", Target: "test", MemoryMB: 900, Slots: 1, PID: 300}, 0).Granted)
	w := wrapped(100)
	b.Assert(w)

	v := b.Request(types.MachineClaim{Project: "x", Target: "test", MemoryMB: 500, Slots: 1, PID: 200, Ancestors: []string{w.Invocation}}, 0)
	assert.False(t, v.Granted, "it would hold 1400 MB of a 1000 MB budget")
	assert.True(t, v.Fits)
	assert.False(t, v.OwnRun, "a stranger keeps it out, so it is refused")
	assert.Equal(t, 900, b.Snapshot().HeldMB)
}

// The two-roots pair under wrappers: each blocked step keeps its child's seat.
func TestMachineBudgetSeatsAChildOfEachStalledRootUnderAWrapper(t *testing.T) {
	b, _ := testBudget(t, 12_000, 16)
	seat := func(pid int, ancestors ...string) types.MachineVerdict {
		return b.Request(types.MachineClaim{
			Target: "test", MemoryMB: 7000, Slots: 1, PID: pid, Invocation: fmt.Sprintf("%d:child", pid), Ancestors: ancestors,
		}, 0)
	}
	wrapA, wrapB := wrapped(100), wrapped(200)
	b.Assert(wrapA)
	b.Assert(wrapB)
	require.True(t, b.Request(types.MachineClaim{Target: "ci", MemoryMB: 6000, Slots: 1, PID: 110, Invocation: "110:a", Ancestors: []string{wrapA.Invocation}}, 0).Granted)
	require.True(t, b.Request(types.MachineClaim{Target: "ci", MemoryMB: 6000, Slots: 1, PID: 210, Invocation: "210:b", Ancestors: []string{wrapB.Invocation}}, 0).Granted)

	assert.True(t, seat(120, wrapA.Invocation, "110:a").Granted)
	assert.True(t, seat(220, wrapB.Invocation, "210:b").Granted)
}

// A wrapper under a step does not spend that step's seat; what it starts does.
func TestMachineBudgetSeatsAChildThroughAWrapperUnderAStalledStep(t *testing.T) {
	b, _ := testBudget(t, 1000, 8)
	require.True(t, b.Request(types.MachineClaim{Target: "other", MemoryMB: 500, Slots: 1, PID: 300}, 0).Granted)
	require.True(t, b.Request(types.MachineClaim{Target: "ci", MemoryMB: 500, Slots: 1, PID: 100, Invocation: "100:run"}, 0).Granted)
	w := wrapped(200, "100:run")
	b.Assert(w)

	child := func(pid int) types.MachineClaim {
		return types.MachineClaim{
			Target: "test", MemoryMB: 700, Slots: 1, PID: pid,
			Invocation: fmt.Sprintf("%d:child", pid), Ancestors: []string{"100:run", w.Invocation},
		}
	}
	assert.True(t, b.Request(child(210), 0).Granted, "the step above the wrapper is blocked on it")
	assert.False(t, b.Request(child(220), 0).Granted, "and that step has one seat")
}

func TestMachineBudgetFreeSeatIsNotABypassForWhatCanNeverFit(t *testing.T) {
	b, _ := testBudget(t, 4000, 8)
	require.True(t, b.Request(types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 1000, Slots: 1, PID: 100, Invocation: "100:aaa",
	}, 0).Granted)

	v := b.Request(types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 64_000, Slots: 1, PID: 200, Ancestors: []string{"100:aaa"},
	}, 0)
	assert.False(t, v.Granted)
	assert.False(t, v.Fits, "a seat is room for a claim this machine can hold, not a waiver of the budget")
}

func TestMachineSnapshotReportsHolders(t *testing.T) {
	b, now := testBudget(t, 10_000, 8)
	require.True(t, b.Request(types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, Slots: 2, PID: 100, Dir: "/tree/a",
	}, 0).Granted)

	// A holder names the tree to go and look at.
	assert.Equal(t, types.MachineSnapshot{
		BudgetMB: 10_000, HeldMB: 9000, BudgetSlots: 8, HeldSlots: 2,
		Holders: []types.MachineClaimant{
			{Project: ".", Target: "test", PID: 100, MemoryMB: 9000, Slots: 2, Dir: "/tree/a", Since: *now},
		},
	}, b.Snapshot())
}

// budgetAdmitter is a MachineAdmitter answering from a budget a test
// writes. Like the broker, it queues a claim that can fit until a release
// seats it. It records every request so the gate's calls can be observed.
type budgetAdmitter struct {
	mu       sync.Mutex
	freed    sync.Cond
	budget   *MachineBudget
	requests int
	fail     error
	released []string
	overruns []string
}

func (*budgetAdmitter) HoldGroup(context.Context, string, int, string) (string, error) {
	return "", nil
}

func (*budgetAdmitter) ReleaseHold(context.Context, string) error { return nil }

func (f *budgetAdmitter) Claim(ctx context.Context, c types.MachineClaim, parked func(types.MachineVerdict)) (types.MachineVerdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if f.fail != nil {
		return types.MachineVerdict{}, f.fail
	}
	f.freed.L = &f.mu
	stop := context.AfterFunc(ctx, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.freed.Broadcast()
	})
	defer stop()
	sent := ""
	for {
		v := f.budget.Request(c, 0)
		if v.Granted || !v.Fits {
			return v, nil
		}
		if k := fmt.Sprint(v.OwnRun, v.Holders); k != sent && parked != nil {
			sent = k
			f.mu.Unlock()
			parked(v)
			f.mu.Lock()
		}
		if ctx.Err() != nil {
			return types.MachineVerdict{}, ctx.Err()
		}
		f.freed.Wait()
	}
}

func (f *budgetAdmitter) Request(_ context.Context, c types.MachineClaim) (types.MachineVerdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return types.MachineVerdict{}, f.fail
	}
	return f.budget.Request(c, 0), nil
}

// wake re-judges every parked claim, as a broker does when a holder joins.
func (f *budgetAdmitter) wake() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.freed.L = &f.mu
	f.freed.Broadcast()
}

func (f *budgetAdmitter) Release(_ context.Context, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, id)
	f.budget.Release(id)
	f.freed.L = &f.mu
	f.freed.Broadcast()
}

func (f *budgetAdmitter) Overrun(_ context.Context, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overruns = append(f.overruns, id)
	f.budget.Overrun(id)
}

func testGate(t *testing.T, b *MachineBudget) (*machineGate, *budgetAdmitter) {
	t.Helper()
	adm := &budgetAdmitter{budget: b}
	g := &machineGate{admit: adm, log: newLogger("", 0)}
	return g, adm
}

func TestAdmitMachineClaimsAndReleasesNonRunWork(t *testing.T) {
	budget, now := testBudget(t, 1000, 2)
	adm := &budgetAdmitter{budget: budget}
	release, err := AdmitMachine(t.Context(), adm, types.MachineClaim{
		Project: ".", Target: "session-brief", MemoryMB: 512,
	}, false, 0)
	require.NoError(t, err)

	holders := budget.Snapshot().Holders
	require.Len(t, holders, 1)
	assert.NotEmpty(t, holders[0].Dir)
	assert.Equal(t, types.MachineClaimant{
		Project: ".", Target: "session-brief", PID: os.Getpid(), MemoryMB: 512, Slots: 1,
		Dir:     holders[0].Dir,     // where the test binary runs
		Command: holders[0].Command, // the test binary's argv
		Since:   *now,
	}, holders[0])

	other := budget.Request(types.MachineClaim{Project: ".", Target: "build", MemoryMB: 600, PID: os.Getpid() + 1}, 0)
	assert.False(t, other.Granted, "the brief's claim must count against another invocation")
	release()
	assert.Empty(t, budget.Snapshot().Holders)
}

func TestABudgetWaitOfZeroRefusesAtOnceNamingTheHolder(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)

	_, err = g.acquire(t.Context(), types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted), "the code is what maps to exit 75")
	assert.Contains(t, err.Error(), "It did not wait")
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

// TestMachineGateWaitsOnClaimsOfItsOwnProcess is the server shape: two unrelated
// invocations in ONE process, and the budget is full of the first one's claim. Nothing
// here is another magus process, so the second queues rather than exiting 75.
func TestMachineGateWaitsOnClaimsOfItsOwnProcess(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "0")
	b, _ := testBudget(t, 10_000, 8)
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
	b, _ := testBudget(t, 10_000, 8)
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
	require.Error(t, err, "under budget_wait 0 a stranger is refused, not queued")
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
	b, _ := testBudget(t, 10_000, 8)
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
	b, _ := testBudget(t, 10_000, 8)
	held := b.Request(types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: 100, Dir: "/elsewhere/checkout",
	}, 0)
	require.True(t, held.Granted, "the fixture's holder must own the budget")

	var out bytes.Buffer
	c, err := Open(t.Context(), t.TempDir(),
		WithLogger(slog.New(NewPrettyHandler(&out, slog.LevelInfo))),
		WithMachineAdmission(&budgetAdmitter{budget: b}, 0))
	require.NoError(t, err)

	sink := &eventSink{}
	ctx := journal.WithLogger(t.Context(), journal.NewLogger(sink))
	ctx = journal.WithInvocationID(ctx, "invrefused")

	var observed []error
	_, err = c.RunAll(ctx, []Step{{ProjectPath: ".", Target: "test", MemoryMB: 9000}},
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
	assert.Contains(t, printed, "  cause: [MGS3009] not starting (root) test: no seat is free")
	assert.Contains(t, printed, "held by pid 100 (root) test (8.8 GiB)")
	assert.NotContains(t, printed, "output:", "nothing ran, so there is no captured output to point at")
	assert.Contains(t, printed, "  reproduce: magus run test .\n")

	require.Len(t, observed, 1, "the result observers feed -o jsonl's run.target.result and run.diagnostic")
	assert.ErrorIs(t, observed[0], types.MachineBudgetExhausted)
	assert.Equal(t, Stats{Error: 1}, c.Stats(), "a refusal counts toward the run's failed total")

	// The session store and `magus session --brief` read failures off result events, so a
	// refusal that emitted none left them showing a run that never happened.
	events := sink.snapshot()
	require.Len(t, events, 1)
	assert.Equal(t, refusalEvent("invrefused"), normalizeRefusal(t, events[0]),
		"nothing ran, so nothing was captured and there is no ref")
}

// refusalEvent is the result event a machine refusal of `.`'s test journals, with the
// volatile fields normalizeRefusal clears.
func refusalEvent(inv string) journal.Event {
	return journal.Event{
		Inv: inv, Project: ".", Target: "test", Kind: journal.KindResult, Level: "error",
		Status: journal.StatusFail, DiagnosticCode: string(types.MachineBudgetExhausted),
	}
}

// normalizeRefusal clears a refusal event's timestamp and its cause text, which names the
// holder's figures, once the text is checked to give the cause.
func normalizeRefusal(t *testing.T, e journal.Event) journal.Event {
	t.Helper()
	assert.Contains(t, e.Text, "no seat is free")
	e.Ts, e.Text = 0, ""
	return e
}

// RunAside has no batch to report the refusal to, so its caller sees only the returned
// error; the journal is where the record of a run that did nothing lives.
func TestRunAsideJournalsAMachineRefusal(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	b, _ := testBudget(t, 10_000, 8)
	held := b.Request(types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: 100, Dir: "/elsewhere/checkout",
	}, 0)
	require.True(t, held.Granted, "the fixture's holder must own the budget")

	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}, 0))
	require.NoError(t, err)

	sink := &eventSink{}
	ctx := journal.WithLogger(t.Context(), journal.NewLogger(sink))
	_, err = c.RunAside(ctx, Step{ProjectPath: ".", Target: "test", MemoryMB: 9000},
		func(context.Context) error {
			t.Error("a refused step must not run")
			return nil
		})

	require.ErrorIs(t, err, types.MachineBudgetExhausted)
	events := sink.snapshot()
	require.Len(t, events, 1)
	assert.Equal(t, refusalEvent(events[0].Inv), normalizeRefusal(t, events[0]))

	// A claim that ends because the run was cancelled is not a refusal to put on record.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = c.RunAside(cancelled, Step{ProjectPath: ".", Target: "test", MemoryMB: 9000},
		func(context.Context) error {
			t.Error("a cancelled step must not run")
			return nil
		})
	require.Error(t, err)
	assert.Len(t, sink.snapshot(), 1, "a cancelled claim journals nothing")
}

// cachedUnderFullBudget warms a local entry for step with no admitter, then reopens the
// same cache under an admitter whose budget another checkout's claim has filled.
func cachedUnderFullBudget(t *testing.T, step Step) (*Cache, *budgetAdmitter) {
	t.Helper()
	t.Setenv("MAGUS_LEVEL", "")
	cdir := t.TempDir()
	warm, err := Open(t.Context(), cdir, WithLocalWrite(true))
	require.NoError(t, err)
	_, err = warm.RunAll(t.Context(), []Step{step}, func(context.Context, Step) error { return nil })
	require.NoError(t, err, "warming the entry")

	b, _ := testBudget(t, 10_000, 8)
	held := b.Request(types.MachineClaim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: 100, Dir: "/elsewhere/checkout",
	}, 0)
	require.True(t, held.Granted, "the fixture's holder must own the budget")
	adm := &budgetAdmitter{budget: b}
	c, err := Open(t.Context(), cdir, WithLocalWrite(true), WithMachineAdmission(adm, 0))
	require.NoError(t, err)
	return c, adm
}

// TestRunAllReplaysALocalHitWithoutAMachineClaim is two fully cached `magus run test .`
// in different checkouts: the second must replay, not exit 75 for memory it never uses.
func TestRunAllReplaysALocalHitWithoutAMachineClaim(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("v1"), 0o644))
	step := Step{ProjectPath: ".", WorkspaceRoot: root, Target: "test", Sources: types.MustParseGlobs("a.txt"), MemoryMB: 9000}
	c, adm := cachedUnderFullBudget(t, step)
	noRun := func(context.Context) error {
		t.Error("a cached step must replay, not run")
		return nil
	}

	results, err := c.RunAll(t.Context(), []Step{step}, func(ctx context.Context, _ Step) error { return noRun(ctx) })
	require.NoError(t, err)
	assert.True(t, results[0].Hit)

	r, err := c.RunAside(t.Context(), step, noRun)
	require.NoError(t, err)
	assert.True(t, r.Hit)

	assert.Zero(t, adm.requests, "a local hit asks the machine budget for nothing")
}

func TestRunAllClaimsForAStepThatWillExecute(t *testing.T) {
	cases := []struct {
		name string
		edit func(root string, s *Step)
	}{
		{"source changed", func(root string, _ *Step) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("v2-different-length"), 0o644))
		}},
		{"no cache", func(_ string, s *Step) { s.NoCache = true }},
		{"skip replay", func(_ string, s *Step) { s.SkipReplay = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("v1"), 0o644))
			step := Step{ProjectPath: ".", WorkspaceRoot: root, Target: "test", Sources: types.MustParseGlobs("a.txt"), MemoryMB: 9000}
			c, adm := cachedUnderFullBudget(t, step)
			tc.edit(root, &step)

			_, err := c.RunAll(t.Context(), []Step{step}, func(context.Context, Step) error {
				t.Error("a refused step must not run")
				return nil
			})
			require.ErrorIs(t, err, types.MachineBudgetExhausted)
			assert.Equal(t, 1, adm.requests)
		})
	}
}

// TestRunRefusesToExecuteAGoneLocalHit pins what keeps an evicted entry from running
// unclaimed: under replayLocal, Run neither falls through to fn nor to another tier.
func TestRunRefusesToExecuteAGoneLocalHit(t *testing.T) {
	c, err := Open(t.Context(), t.TempDir(), WithLocalWrite(true))
	require.NoError(t, err)
	_, err = c.Run(t.Context(), Step{ProjectPath: ".", Target: "test"}, func(context.Context) error {
		t.Error("a step admitted as a local hit must not execute")
		return nil
	}, replayLocal(strings.Repeat("0", 64), 0))
	require.ErrorIs(t, err, errLocalEntryGone)
}

func TestMachineGateRefusesWhatCanNeverFit(t *testing.T) {
	b, _ := testBudget(t, 4000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{
		Project: ".", Target: "ci", DeclaredBy: "test", MemoryMB: 64_000, PID: 100,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted))
	assert.Contains(t, err.Error(), "runs test, which claims 62.5 GiB (declared)",
		"a composed target is held over a number a target in its chain wrote")
	assert.Contains(t, err.Error(), "Waiting would not help")
}

// The refusal an author meets with a 26 GiB target on a 32 GiB machine. magus budgets less
// than the whole machine (a quarter under balanced/conservative, all but a small floor
// under aggressive), so the figure in the message is smaller than the RAM the reader can
// see, and a refusal that does not say so reads as arithmetic magus got wrong. It names no
// fixed percentage: which reservation applied is not something this budget records, and a
// number right for one profile is a lie for the other.
func TestMachineDeclarationNamesAMeasuredFigure(t *testing.T) {
	c := types.MachineClaim{Target: "test", MemoryMB: 5880, Slots: 1,
		Sizing: types.MemorySizing{Samples: 53, AnyShape: true}}
	assert.Equal(t, "it claims 5.7 GiB (measured over 53 runs of any shape) and takes 1 slot",
		describeMachineDeclaration(c))
}

func TestMachineRefusalExplainsTheGapAndTheDeclarationCheck(t *testing.T) {
	b, _ := testBudget(t, 4000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{
		Project: ".", Target: "ci", DeclaredBy: "test", MemoryMB: 26_000, Slots: 1, PID: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "3.9 GiB", "the budget it did not fit in")
	assert.Contains(t, err.Error(), "claims 25.4 GiB (declared)", "and the declaration held against it")
	assert.Contains(t, err.Error(), "not the whole machine",
		"a budget smaller than the machine reads as a miscount unless the gap is explained")
	assert.Contains(t, err.Error(), "MGS1030",
		"magus has measured this target's peak, so the author is sent to the check rather than to a guess")
}

// The other axis: a step declaring more slots than the machine has cores is refused by the
// same path, and neither the memory fraction nor a memory check has anything to say about it.
func TestMachineRefusalForTooManySlotsStaysAboutSlots(t *testing.T) {
	b, _ := testBudget(t, 32_000, 8)
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
	b, _ := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)
	adm.fail = errors.New("dial: connection refused")

	release, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err, "losing the arbiter must not fail a build that was going to run")
	require.NotNil(t, release)
	release()
}

// TestMachineGateRequiredRefusesWhenTheArbiterIsGone is broker: required. The step does
// not start, and the refusal exits 69 rather than 75: no arbiter is not a busy host.
func TestMachineGateRequiredRefusesWhenTheArbiterIsGone(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)
	g.required = true
	adm.fail = errors.New("dial: connection refused")

	release, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.Error(t, err)
	assert.Nil(t, release)
	assert.ErrorIs(t, err, types.BrokerUnavailable)
	var stated interface{ ExitCode() int }
	require.ErrorAs(t, err, &stated)
	assert.Equal(t, ExitCodeBrokerUnavailable, stated.ExitCode())
	assert.Contains(t, err.Error(), "connection refused", "the cause stays on the error")
}

// A broker from another build is not reported as no broker.
func TestMachineGateNamesABrokerFromAnotherBuild(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)
	var logged bytes.Buffer
	g.log = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	adm.fail = types.WrapDiagnostic(types.PeerFromAnotherBuild, errors.New("broker: expected a hello speaking protocol 2"), "hello")

	release, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err, "best-effort runs on without the broker")
	release()
	assert.Contains(t, logged.String(), string(types.PeerFromAnotherBuild))
	assert.Contains(t, logged.String(), "a broker from another magus build answered")
	assert.NotContains(t, logged.String(), "no broker answered")
}

func TestMachineGateRequiredRefusesABrokerFromAnotherBuild(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)
	g.required = true
	adm.fail = types.WrapDiagnostic(types.PeerFromAnotherBuild, errors.New("broker: expected a hello speaking protocol 2"), "hello")

	_, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.ErrorIs(t, err, types.PeerFromAnotherBuild)
	var stated interface{ ExitCode() int }
	require.ErrorAs(t, err, &stated)
	assert.Equal(t, ExitCodeBrokerUnavailable, stated.ExitCode())
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
	b, _ := testBudget(t, 10_000, 8)
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

	// ctx still wins when something upstream stamped it: under the server the process
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

	b, _ := testBudget(t, 10_000, 8)
	parent := b.Request(types.MachineClaim{
		Project: ".", Target: "ci", MemoryMB: 10_000, Slots: 1, PID: 3217, Invocation: "3217:inv-parent",
	}, 0)
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

// TestMachineRefusalStatesItsExitCode pins the method the SERVER reads. mapExitCode
// sees the concrete error and could go on matching the diagnostic code; a run the
// server executes for an adopted client cannot, because the type does not survive the
// socket. Without the method the refusal exits 75 alone and 1 under a server, which is
// the exact split exit.Code exists to close.
func TestMachineRefusalStatesItsExitCode(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
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

func TestNextChangeIsTheEarliestCurveBreakpoint(t *testing.T) {
	b, now := testBudget(t, 10_000, 8)
	_, ok := b.NextChange()
	assert.False(t, ok, "an empty budget never changes on its own")

	b.Assert(types.MachineClaim{Target: "flat", MemoryMB: 1000})
	_, ok = b.NextChange()
	assert.False(t, ok, "a claim held flat changes only when released")

	profile := types.MemoryProfile{D10Ms: 10_000, D90Ms: 10_000, Runs: 3}
	for i := range profile.Curve {
		profile.Curve[i] = 100
	}
	b.Assert(types.MachineClaim{Target: "curved", MemoryMB: 100, Profile: profile})
	at, ok := b.NextChange()
	require.True(t, ok)
	step := 10 * time.Second / types.MemoryCurveBuckets
	assert.Equal(t, now.Add(step), at, "a curve's first breakpoint")
}
