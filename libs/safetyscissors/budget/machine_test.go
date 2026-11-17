package budget

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MachineSuite struct{ suite.Suite }

func TestMachine(t *testing.T) { suite.Run(t, new(MachineSuite)) }

// isWrapper is the suite's wrapper test: an invocation whose id starts "wrap-".
func isWrapper(invocation string) bool {
	_, id, ok := strings.Cut(invocation, ":")
	return ok && strings.HasPrefix(id, "wrap-")
}

// testBudget is a budget with a clock the test controls.
func testBudget(t *testing.T, mb, slots int) (*Machine, *time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	b := NewMachine(mb, slots, WithWrappers(isWrapper), WithClock(func() time.Time { return now }))
	return b, &now
}

func (s *MachineSuite) TestAdmitsUntilFull() {
	t := s.T()
	b, now := testBudget(t, 10_000, 8)

	first := b.Request(Claim{Project: ".", Target: "test", MemoryMB: 8000, Slots: 4, PID: 100}, 0)
	require.True(t, first.Granted, "an empty machine seats the first claim")
	assert.Equal(t, 8000, first.HeldMB)

	// A second invocation, in another worktree, asking for more than what is left.
	second := b.Request(Claim{Project: ".", Target: "ci", MemoryMB: 8000, Slots: 4, PID: 200}, 0)
	assert.False(t, second.Granted, "the machine cannot seat both")
	assert.True(t, second.Fits, "it would fit on an idle machine, so the refusal is temporary")
	assert.False(t, second.OwnRun, "what fills the machine is another process's run")
	assert.Equal(t, []Claimant{
		{Project: ".", Target: "test", PID: 100, MemoryMB: 8000, Slots: 4, Since: *now},
	}, second.Holders, "the refusal names who holds the budget")

	b.Release(first.ID)
	third := b.Request(Claim{Project: ".", Target: "ci", MemoryMB: 8000, Slots: 4, PID: 200}, 0)
	assert.True(t, third.Granted, "the same claim is seated once the holder releases")
}

func (s *MachineSuite) TestRefusesWhatCanNeverFit() {
	t := s.T()
	b, _ := testBudget(t, 4000, 8)

	v := b.Request(Claim{Project: ".", Target: "test", MemoryMB: 64_000, Slots: 1, PID: 100}, 0)
	assert.False(t, v.Granted)
	assert.False(t, v.Fits, "no state of the machine seats a claim larger than the whole budget")
}

func (s *MachineSuite) TestRequestGrantsAnElasticClaimEveryFreeSlotWhenNobodyWaits() {
	t := s.T()
	b, now := testBudget(t, 0, 10)
	vet := b.Request(Claim{Project: ".", Target: "vet", Slots: 4, PID: 100}, 0)
	require.True(t, vet.Granted)

	v := b.Request(Claim{Project: ".", Target: "test", Slots: 8, SlotsFloor: 1, PID: 200}, 0)
	assert.Equal(t, Verdict{
		Granted: true, ID: v.ID, Fits: true, BudgetSlots: 10, HeldSlots: 10, Slots: 6,
		Holders: []Claimant{{Project: ".", Target: "vet", PID: 100, Slots: 4, Since: *now}},
	}, v, "it takes what is free, short of its ceiling")
	assert.Equal(t, []Claimant{
		{Project: ".", Target: "test", PID: 200, Slots: 6, Since: *now},
		{Project: ".", Target: "vet", PID: 100, Slots: 4, Since: *now},
	}, b.Snapshot().Holders, "the claim holds what it was granted")
}

func (s *MachineSuite) TestRequestLeavesEachWaiterAShareOfTheFreeSlots() {
	t := s.T()
	b, _ := testBudget(t, 0, 10)

	v := b.Request(Claim{Project: ".", Target: "test", Slots: 8, SlotsFloor: 1, PID: 200}, 3)
	require.True(t, v.Granted)
	assert.Equal(t, 2, v.Slots, "10 free among itself and 3 waiters")

	narrow := b.Request(Claim{Project: ".", Target: "lint", Slots: 8, SlotsFloor: 1, PID: 300}, 20)
	require.True(t, narrow.Granted)
	assert.Equal(t, 1, narrow.Slots, "a share never falls under one slot")
}

func (s *MachineSuite) TestRequestSeatsAnElasticClaimAtItsFloor() {
	t := s.T()
	b, _ := testBudget(t, 0, 4)
	require.True(t, b.Request(Claim{Project: ".", Target: "vet", Slots: 3, PID: 100}, 0).Granted)

	v := b.Request(Claim{Project: ".", Target: "test", Slots: 8, SlotsFloor: 1, PID: 200}, 0)
	require.True(t, v.Granted, "the floor fits where the ceiling does not")
	assert.Equal(t, 1, v.Slots)

	fixed := b.Request(Claim{Project: ".", Target: "scip", Slots: 4, SlotsFloor: 4, PID: 300}, 0)
	assert.False(t, fixed.Granted, "a declared width is its floor as well as its ceiling")
}

func (s *MachineSuite) TestRequestNamesTheParkedClaimsAheadOfOneItDoesNotSeat() {
	t := s.T()
	b, _ := testBudget(t, 0, 2)
	require.True(t, b.Request(Claim{Project: ".", Target: "vet", Slots: 2, PID: 100}, 0).Granted)

	v := b.Request(Claim{Project: ".", Target: "test", Slots: 1, PID: 200}, 3)
	require.False(t, v.Granted)
	assert.Equal(t, 3, v.Ahead)
	assert.Zero(t, v.Slots, "nothing was granted")
}

func (s *MachineSuite) TestRequestNamesTheComposerAHolderRunsUnder() {
	t := s.T()
	b, _ := testBudget(t, 0, 2)
	require.True(t, b.Request(Claim{Project: ".", Target: "conventions", Under: ".:lint", Slots: 2, PID: 100}, 0).Granted)

	v := b.Request(Claim{Project: ".", Target: "test", Slots: 1, PID: 200}, 0)
	require.Len(t, v.Holders, 1)
	assert.Equal(t, ".:lint", v.Holders[0].Under)
}

// curveClaim is a claim over a 100s run holding early for its first half and late for
// its second, with a duration band from d10Ms to 100s, claiming its peak.
func curveClaim(pid int, early, late uint32, d10Ms int64) Claim {
	p := Profile{D10Ms: d10Ms, D90Ms: 100_000, Runs: 3}
	for i := range p.Curve {
		p.Curve[i] = early
		if i >= CurveBuckets/2 {
			p.Curve[i] = late
		}
	}
	return Claim{Project: ".", Target: "test", MemoryMB: p.Curve.PeakMB(), Profile: p, Slots: 1, PID: pid}
}

func (s *MachineSuite) TestRequestPacksCurvesWhosePeaksDoNotOverlap() {
	t := s.T()
	b, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request(curveClaim(100, 7000, 1000, 100_000), 0).Granted)

	v := b.Request(curveClaim(200, 1000, 7000, 100_000), 0)
	assert.True(t, v.Granted, "an early peak and a late one sum to 8000 at every instant")
	assert.True(t, v.Packed)
	assert.Equal(t, 14_000, v.HeldMB, "what is held is still reported as peaks")
}

func (s *MachineSuite) TestRequestRefusesWhenBandsOverlapAtThePeak() {
	t := s.T()
	b, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request(curveClaim(100, 7000, 1000, 50_000), 0).Granted)

	v := b.Request(curveClaim(200, 1000, 7000, 50_000), 0)
	assert.False(t, v.Granted, "with D10 at half of D90 the first may still be early when the second is late")
	assert.True(t, v.Fits)
	assert.True(t, v.Packed)
}

func (s *MachineSuite) TestOverrunClaimCountsItsPeak() {
	t := s.T()
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

func (s *MachineSuite) TestOverrunCurveCountsItsPeak() {
	t := s.T()
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

func (s *MachineSuite) TestClaimWithoutProfileCountsItsPeak() {
	t := s.T()
	b, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request(curveClaim(100, 1000, 7000, 100_000), 0).Granted)

	flat := Claim{Project: ".", Target: "lint", MemoryMB: 7000, Slots: 1, PID: 200}
	v := b.Request(flat, 0)
	assert.False(t, v.Granted, "a claim with no curve holds 7000 from now, across the first's late 7000")
	assert.True(t, v.Packed, "the held claim is still judged by its curve")

	peaks, _ := testBudget(t, 10_000, 8)
	held := curveClaim(100, 1000, 7000, 100_000)
	held.Profile = Profile{}
	require.True(t, peaks.Request(held, 0).Granted)
	v = peaks.Request(flat, 0)
	assert.False(t, v.Granted, "two peaks held flat sum to 14000")
	assert.False(t, v.Packed)

	early := b.Request(Claim{Project: ".", Target: "vet", MemoryMB: 3000, Slots: 1, PID: 300}, 0)
	assert.True(t, early.Granted, "3000 flat fits beside the first's 7000 peak")
}

// TestMachineBudgetKeepsNothingForARefusedClaim pins that a refusal leaves no trace. A
// refused claim reserving its room would turn away a smaller claim that fits, which is a
// spurious exit 75 for a run that nothing was in the way of.
func (s *MachineSuite) TestKeepsNothingForARefusedClaim() {
	t := s.T()
	b, _ := testBudget(t, 10_000, 8)
	held := b.Request(Claim{Project: ".", Target: "test", MemoryMB: 9000, Slots: 1, PID: 100}, 0)
	require.True(t, held.Granted)
	require.False(t, b.Request(Claim{Project: ".", Target: "ci", MemoryMB: 9000, Slots: 1, PID: 200}, 0).Granted)

	light := b.Request(Claim{Project: "docs", Target: "lint", MemoryMB: 500, Slots: 1, PID: 300}, 0)
	assert.True(t, light.Granted, "the refused claim holds no room")
	assert.Len(t, b.Snapshot().Holders, 2, "only the granted claims are on record")
}

// TestMachineBudgetAssertRecordsWhatDoesNotFit pins the re-assert a holder makes on a new
// broker: its step is already running, so the claim is recorded whatever it does to the
// arithmetic, and the next request sees the memory as spent.
func (s *MachineSuite) TestAssertRecordsWhatDoesNotFit() {
	t := s.T()
	b, _ := testBudget(t, 10_000, 8)
	require.True(t, b.Request(Claim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100}, 0).Granted)

	id := b.Adopt(Claim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.NotEmpty(t, id)
	// An asserted claim counts even over the budget, and takes the one slot every step spends.
	snap := b.Snapshot()
	assert.Equal(t, Snapshot{
		BudgetMB: 10_000, HeldMB: 18_000, BudgetSlots: 8, HeldSlots: 2,
		Holders: snap.Holders, // the two claimants are asserted by the releases below
	}, snap)

	v := b.Request(Claim{Project: "x", Target: "lint", MemoryMB: 500, PID: 300}, 0)
	assert.False(t, v.Granted, "new work is not admitted against memory a running step holds")

	b.Release(id)
	assert.Equal(t, 9000, b.Snapshot().HeldMB)
}

func (s *MachineSuite) TestExcludesAnAncestorsClaim() {
	t := s.T()
	b, _ := testBudget(t, 10_000, 8)
	parent := b.Request(Claim{
		Project: ".", Target: "ci", MemoryMB: 9000, PID: 100, Invocation: "100:aaa",
	}, 0)
	require.True(t, parent.Granted)

	// The suite this claim covers runs `magus run test .`, which is a DESCENDANT of the
	// run already holding the declaration. Counting it would refuse its own child.
	child := b.Request(Claim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: 200, Ancestors: []string{"100:aaa"},
	}, 0)
	assert.True(t, child.Granted)
	assert.Empty(t, child.Holders, "an ancestor is not a peer")
}

// The cross-process half of the in-process fan-out deadlock: four steps of ONE invocation
// each shell out to a magus of their own, and every parent is blocked in exec waiting for
// its child, so a child excused from one parent claim and kept out by the other three is
// waiting on the very steps that are waiting on it.
func (s *MachineSuite) TestSeatsEveryChildOfAFannedOutParent() {
	t := s.T()
	b, _ := testBudget(t, 12_000, 16)
	targets := []string{"build", "test", "lint", "docs"}
	for _, target := range targets {
		require.True(t, b.Request(Claim{
			Project: ".", Target: target, MemoryMB: 3000, Slots: 1, PID: 100, Invocation: "100:aaa",
		}, 0).Granted, target)
	}

	child := func(i int) Claim {
		return Claim{
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
func (s *MachineSuite) TestSeatsAChildOfEachStalledRoot() {
	t := s.T()
	b, now := testBudget(t, 12_000, 16)

	rootA := b.Request(Claim{
		Project: "a", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 100, Invocation: "100:aaa",
	}, 0)
	require.True(t, rootA.Granted)
	rootB := b.Request(Claim{
		Project: "b", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 200, Invocation: "200:bbb",
	}, 0)
	require.True(t, rootB.Granted)

	*now = now.Add(time.Second)
	childA := b.Request(Claim{
		Project: "a", Target: "test", MemoryMB: 7000, Slots: 1, PID: 300,
		Invocation: "300:ccc", Ancestors: []string{"100:aaa"},
	}, 0)
	assert.True(t, childA.Granted, "the bottom of a stalled chain always has a seat")

	// The control. A top-level run has no stalled ancestor to charge a seat to, so this
	// cannot become a way past a full machine.
	*now = now.Add(time.Second)
	stranger := Claim{Project: "c", Target: "build", MemoryMB: 7000, Slots: 1, PID: 400}
	v := b.Request(stranger, 0)
	assert.False(t, v.Granted, "a stranger does not get a seat out of somebody else's stall")
	assert.True(t, v.Fits)
	assert.False(t, v.OwnRun, "and nothing in its way belongs to it, so it is refused")

	*now = now.Add(time.Second)
	childB := b.Request(Claim{
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
func (s *MachineSuite) TestFreeSeatsOneChildPerStalledAncestor() {
	t := s.T()
	b, now := testBudget(t, 12_000, 16)
	stranger := b.Request(Claim{
		Project: "other", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 100,
	}, 0)
	require.True(t, stranger.Granted)
	require.True(t, b.Request(Claim{
		Project: ".", Target: "ci", MemoryMB: 6000, Slots: 1, PID: 200, Invocation: "200:bbb",
	}, 0).Granted)

	child := func(i int) Claim {
		return Claim{
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
func wrapped(pid int, ancestors ...string) Claim {
	return Claim{
		Target: "job exec", Slots: 1, PID: pid, Ancestors: ancestors,
		Invocation: fmt.Sprintf("%d:%s", pid, "wrap-exec"),
	}
}

// A wrapper's child takes the slot the wrapper holds, however little is free.
func (s *MachineSuite) TestSharesAWrappersClaimWithItsChild() {
	t := s.T()
	b, _ := testBudget(t, 0, 1)
	w := wrapped(100)
	b.Adopt(w)
	v := b.Request(Claim{Project: "x", Target: "install", Slots: 1, PID: 200, Ancestors: []string{w.Invocation}}, 0)
	assert.True(t, v.Granted, "the only slot is its wrapper's")

	stranger := b.Request(Claim{Project: "other", Target: "build", Slots: 1, PID: 300}, 0)
	assert.False(t, stranger.Granted, "a stranger gets neither")
}

// A full machine has no stalled step to charge a wrapper's child a seat to.
func (s *MachineSuite) TestGivesAWrappersChildNoSeatOnAFullMachine() {
	t := s.T()
	b, _ := testBudget(t, 1000, 4)
	require.True(t, b.Request(Claim{Project: "other", Target: "test", MemoryMB: 900, Slots: 1, PID: 300}, 0).Granted)
	w := wrapped(100)
	b.Adopt(w)

	v := b.Request(Claim{Project: "x", Target: "test", MemoryMB: 500, Slots: 1, PID: 200, Ancestors: []string{w.Invocation}}, 0)
	assert.False(t, v.Granted, "it would hold 1400 MB of a 1000 MB budget")
	assert.True(t, v.Fits)
	assert.False(t, v.OwnRun, "a stranger keeps it out, so it is refused")
	assert.Equal(t, 900, b.Snapshot().HeldMB)
}

// The two-roots pair under wrappers: each blocked step keeps its child's seat.
func (s *MachineSuite) TestSeatsAChildOfEachStalledRootUnderAWrapper() {
	t := s.T()
	b, _ := testBudget(t, 12_000, 16)
	seat := func(pid int, ancestors ...string) Verdict {
		return b.Request(Claim{
			Target: "test", MemoryMB: 7000, Slots: 1, PID: pid, Invocation: fmt.Sprintf("%d:child", pid), Ancestors: ancestors,
		}, 0)
	}
	wrapA, wrapB := wrapped(100), wrapped(200)
	b.Adopt(wrapA)
	b.Adopt(wrapB)
	require.True(t, b.Request(Claim{Target: "ci", MemoryMB: 6000, Slots: 1, PID: 110, Invocation: "110:a", Ancestors: []string{wrapA.Invocation}}, 0).Granted)
	require.True(t, b.Request(Claim{Target: "ci", MemoryMB: 6000, Slots: 1, PID: 210, Invocation: "210:b", Ancestors: []string{wrapB.Invocation}}, 0).Granted)

	assert.True(t, seat(120, wrapA.Invocation, "110:a").Granted)
	assert.True(t, seat(220, wrapB.Invocation, "210:b").Granted)
}

// A wrapper under a step does not spend that step's seat; what it starts does.
func (s *MachineSuite) TestSeatsAChildThroughAWrapperUnderAStalledStep() {
	t := s.T()
	b, _ := testBudget(t, 1000, 8)
	require.True(t, b.Request(Claim{Target: "other", MemoryMB: 500, Slots: 1, PID: 300}, 0).Granted)
	require.True(t, b.Request(Claim{Target: "ci", MemoryMB: 500, Slots: 1, PID: 100, Invocation: "100:run"}, 0).Granted)
	w := wrapped(200, "100:run")
	b.Adopt(w)

	child := func(pid int) Claim {
		return Claim{
			Target: "test", MemoryMB: 700, Slots: 1, PID: pid,
			Invocation: fmt.Sprintf("%d:child", pid), Ancestors: []string{"100:run", w.Invocation},
		}
	}
	assert.True(t, b.Request(child(210), 0).Granted, "the step above the wrapper is blocked on it")
	assert.False(t, b.Request(child(220), 0).Granted, "and that step has one seat")
}

func (s *MachineSuite) TestFreeSeatIsNotABypassForWhatCanNeverFit() {
	t := s.T()
	b, _ := testBudget(t, 4000, 8)
	require.True(t, b.Request(Claim{
		Project: ".", Target: "ci", MemoryMB: 1000, Slots: 1, PID: 100, Invocation: "100:aaa",
	}, 0).Granted)

	v := b.Request(Claim{
		Project: ".", Target: "test", MemoryMB: 64_000, Slots: 1, PID: 200, Ancestors: []string{"100:aaa"},
	}, 0)
	assert.False(t, v.Granted)
	assert.False(t, v.Fits, "a seat is room for a claim this machine can hold, not a waiver of the budget")
}

func (s *MachineSuite) TestSnapshotReportsHolders() {
	t := s.T()
	b, now := testBudget(t, 10_000, 8)
	require.True(t, b.Request(Claim{
		Project: ".", Target: "test", MemoryMB: 9000, Slots: 2, PID: 100, Dir: "/tree/a",
	}, 0).Granted)

	// A holder names the tree to go and look at.
	assert.Equal(t, Snapshot{
		BudgetMB: 10_000, HeldMB: 9000, BudgetSlots: 8, HeldSlots: 2,
		Holders: []Claimant{
			{Project: ".", Target: "test", PID: 100, MemoryMB: 9000, Slots: 2, Dir: "/tree/a", Since: *now},
		},
	}, b.Snapshot())
}

func (s *MachineSuite) TestNextChangeIsTheEarliestCurveBreakpoint() {
	t := s.T()
	b, now := testBudget(t, 10_000, 8)
	_, ok := b.NextChange()
	assert.False(t, ok, "an empty budget never changes on its own")

	b.Adopt(Claim{Target: "flat", MemoryMB: 1000})
	_, ok = b.NextChange()
	assert.False(t, ok, "a claim held flat changes only when released")

	profile := Profile{D10Ms: 10_000, D90Ms: 10_000, Runs: 3}
	for i := range profile.Curve {
		profile.Curve[i] = 100
	}
	b.Adopt(Claim{Target: "curved", MemoryMB: 100, Profile: profile})
	at, ok := b.NextChange()
	require.True(t, ok)
	step := 10 * time.Second / CurveBuckets
	assert.Equal(t, now.Add(step), at, "a curve's first breakpoint")
}
