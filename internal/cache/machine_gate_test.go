package cache

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/types"
)

func TestMachineRefusalNamesTheOversizedClaimAndTheWaitForm(t *testing.T) {
	b, _ := testBudget(t, 4000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{
		Project: ".", Target: "ci", DeclaredBy: "test", MemoryMB: 26_000, Slots: 1, PID: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "`magus status --wait` cannot seat it either")
	assert.Contains(t, err.Error(), "25.4 GiB against a budget of 3.9 GiB", "which claim, against which budget")
	assert.Contains(t, err.Error(), "Narrow the run")
}

func snapshotReader(b *MachineBudget) func(context.Context) (types.MachineSnapshot, error) {
	return func(context.Context) (types.MachineSnapshot, error) { return b.Snapshot(), nil }
}

func fastAwait(t *testing.T) {
	t.Helper()
	prev := machineAwaitEvery
	machineAwaitEvery = 10 * time.Millisecond
	t.Cleanup(func() { machineAwaitEvery = prev })
}

func TestMachineGateAwaitReturnsAtOnceWhenTheBudgetSeatsIt(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	b.Assert(types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 4000, PID: 100})

	var said []string
	err := AwaitMachine(t.Context(), snapshotReader(b), []types.MachineClaim{{Project: ".", Target: "test", MemoryMB: 6000}},
		func(msg string) { said = append(said, msg) })
	require.NoError(t, err)
	assert.Empty(t, said, "a claim the budget seats now waits for nothing and says nothing")
}

func TestMachineGateAwaitReturnsWhenTheHolderReleases(t *testing.T) {
	fastAwait(t)
	b, _ := testBudget(t, 10_000, 8)
	id := b.Assert(types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 100})

	var said []string
	done := make(chan error, 1)
	go func() {
		done <- AwaitMachine(t.Context(), snapshotReader(b), []types.MachineClaim{{Project: ".", Target: "test", MemoryMB: 6000}},
			func(msg string) { said = append(said, msg) })
	}()
	select {
	case err := <-done:
		t.Fatalf("returned (%v) while pid 100 held what the claim needs", err)
	case <-time.After(200 * time.Millisecond):
	}
	b.Release(id)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("never returned after the holder released")
	}
	require.Len(t, said, 1, "the wait is announced once, not per poll")
	assert.Contains(t, said[0], "waiting for this machine's build budget to seat (root) test")
	assert.Contains(t, said[0], "pid 100 docs ci")
}

// TestMachineGateAwaitWeighsEachClaimOnItsOwn: a run's steps wait on one another, so
// two claims that only fit one at a time still admit the run.
func TestMachineGateAwaitWeighsEachClaimOnItsOwn(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	claims := []types.MachineClaim{{Project: "a", Target: "test", MemoryMB: 6000}, {Project: "b", Target: "test", MemoryMB: 6000}}
	require.NoError(t, AwaitMachine(t.Context(), snapshotReader(b), claims, nil))
}

func TestMachineGateAwaitRefusesWhatCanNeverFit(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	err := AwaitMachine(t.Context(), snapshotReader(b), []types.MachineClaim{{Project: ".", Target: "test", MemoryMB: 64_000}}, nil)
	require.Error(t, err)
	var stated interface{ ExitCode() int }
	require.ErrorAs(t, err, &stated)
	assert.Equal(t, ExitCodeMachineDeclaration, stated.ExitCode(), "no wait admits it, so it is not EX_TEMPFAIL")
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted))
}

func TestMachineGateAwaitWithoutABrokerWaitsForNothing(t *testing.T) {
	gone := func(context.Context) (types.MachineSnapshot, error) {
		return types.MachineSnapshot{}, errors.New("no broker")
	}
	require.NoError(t, AwaitMachine(t.Context(), gone, []types.MachineClaim{{Slots: 1}}, nil))
}

func TestMachineGateAwaitStopsWithItsContext(t *testing.T) {
	fastAwait(t)
	b, _ := testBudget(t, 0, 1)
	b.Assert(types.MachineClaim{Project: "docs", Target: "ci", PID: 100})

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err := AwaitMachine(ctx, snapshotReader(b), []types.MachineClaim{{Slots: 1}}, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func reserver(b *MachineBudget) func(context.Context, types.MachineClaim, int) (types.MachineVerdict, error) {
	return func(_ context.Context, c types.MachineClaim, reader int) (types.MachineVerdict, error) {
		return b.Reserve(c, reader), nil
	}
}

// TestMachineGateReserveHoldsTheSeatItWaitedFor: once the wait returns, the seat is the
// reader's, so a bare run asking first is refused and the reader's claim is granted.
func TestMachineGateReserveHoldsTheSeatItWaitedFor(t *testing.T) {
	fastAwait(t)
	b, _ := testBudget(t, 0, 4)
	liveReaders(b, 500)
	id := b.Assert(types.MachineClaim{Project: "docs", Target: "ci", Slots: 4, PID: 100})

	done := make(chan error, 1)
	go func() {
		done <- ReserveMachine(t.Context(), reserver(b), []types.MachineClaim{{Project: ".", Target: "go-test", Slots: 4}}, 500, nil)
	}()
	select {
	case err := <-done:
		t.Fatalf("returned (%v) while pid 100 held every slot", err)
	case <-time.After(100 * time.Millisecond):
	}
	b.Release(id)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("never returned after the holder released")
	}
	assert.False(t, b.Request(types.MachineClaim{Target: "test", Slots: 4, PID: 600}).Granted, "the seat is held for the reader")
	assert.True(t, b.Request(types.MachineClaim{Target: "go-test", Slots: 4, PID: 500}).Granted)
}

func TestMachineGateReserveSeatsTheWidestClaim(t *testing.T) {
	seat := widestSeat([]types.MachineClaim{
		{Project: "a", Target: "test", MemoryMB: 6000, Slots: 1},
		{Project: "b", Target: "go-test", MemoryMB: 1000, Slots: 4},
	})
	assert.Equal(t, types.MachineClaim{Project: "a", Target: "test", MemoryMB: 6000, Slots: 4}, seat)
	assert.Equal(t, 1, widestSeat(nil).Slots, "nothing to size still takes a slot")
}

func TestMachineGateReserveRefusesWhatCanNeverFit(t *testing.T) {
	b, _ := testBudget(t, 0, 4)
	liveReaders(b, 500)
	err := ReserveMachine(t.Context(), reserver(b), []types.MachineClaim{{Target: "test", Slots: 8}}, 500, nil)
	var stated interface{ ExitCode() int }
	require.ErrorAs(t, err, &stated)
	assert.Equal(t, ExitCodeMachineDeclaration, stated.ExitCode())
}

// refusingAdmitter answers every request with err.
type refusingAdmitter struct{ err error }

func (a refusingAdmitter) Request(context.Context, types.MachineClaim) (types.MachineVerdict, error) {
	return types.MachineVerdict{}, a.err
}

func (refusingAdmitter) Release(context.Context, string) {}

func (refusingAdmitter) Overrun(context.Context, string) {}

// overrunAdmitter sends each overrun it is told of on overruns.
type overrunAdmitter struct {
	refusingAdmitter
	overruns chan string
}

func (a overrunAdmitter) Overrun(_ context.Context, id string) { a.overruns <- id }

func TestOverrunIsSentOnce(t *testing.T) {
	admit := overrunAdmitter{overruns: make(chan string, 4)}
	profile := types.MemoryProfile{D10Ms: 10_000, D90Ms: 10_000, Runs: 3}
	for i := range profile.Curve {
		profile.Curve[i] = 100
	}
	ctx := types.WithExecCost(t.Context())
	ctx = withOverrunWatch(ctx, admit, func() string { return "claim-1" }, profile)
	watchOverrun(ctx)

	record, end := types.RecordCurve(ctx, time.Now())
	defer end()
	record(types.MemorySample{At: time.Second, Bytes: 50 << 20})
	select {
	case id := <-admit.overruns:
		t.Fatalf("a sample under the curve sent an overrun for %s", id)
	case <-time.After(20 * time.Millisecond):
	}

	record(types.MemorySample{At: 2 * time.Second, Bytes: 200 << 20})
	record(types.MemorySample{At: 3 * time.Second, Bytes: 300 << 20})
	select {
	case id := <-admit.overruns:
		assert.Equal(t, "claim-1", id)
	case <-time.After(5 * time.Second):
		t.Fatal("a sample over the curve sent no overrun")
	}
	select {
	case <-admit.overruns:
		t.Fatal("a second sample over the curve sent a second overrun")
	case <-time.After(50 * time.Millisecond):
	}

	unwatched := withOverrunWatch(types.WithExecCost(t.Context()), admit, func() string { return "claim-2" }, types.MemoryProfile{})
	assert.Nil(t, unwatched.Value(overrunWatchKey{}), "a claim with no profile is charged its peak already")
}

// groupAdmitter is a budget that also holds process groups, recording each hold and
// release in order.
type groupAdmitter struct {
	*budgetAdmitter
	events []string
}

func (a *groupAdmitter) HoldGroup(_ context.Context, claim string, pgid int, command string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, fmt.Sprintf("hold %d (%s) on %s", pgid, command, claim))
	return "hold-" + claim, nil
}

func (a *groupAdmitter) ReleaseHold(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, "release "+id)
	return nil
}

// unansweredHolder is a broker that records each hold on whatever context it was handed
// and answers no release.
type unansweredHolder struct {
	holdErr     error
	holdHasTime bool
}

func (h *unansweredHolder) HoldGroup(ctx context.Context, claim string, _ int, _ string) (string, error) {
	h.holdErr = ctx.Err()
	_, h.holdHasTime = ctx.Deadline()
	return "hold-" + claim, nil
}

func (*unansweredHolder) ReleaseHold(context.Context, string) error {
	return errors.New("connection reset")
}

// A failing sibling cancels the run's context, and a hold made on it then could be one
// the broker records and this process never releases; the hold runs uncancelled but
// bounded. A release the broker does not answer says the group may still be held.
func TestHoldStepGroupOutlivesACancelledRunAndWarnsOnAnUnansweredRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	holder := &unansweredHolder{}

	out := captureLog(t, func() {
		holdStepGroup(ctx, holder, "claim-1", 4242, "sleep 30")()
	})
	require.NoError(t, holder.holdErr, "the hold ran on a context the cancel did not reach")
	assert.True(t, holder.holdHasTime, "the hold is bounded")
	assert.Contains(t, out, "the broker may still hold process group 4242 (sleep 30), which ended")
	assert.Contains(t, out, "connection reset")
}

// A process group a step starts rides the step's own claim, and is let go before it.
func TestAStepGroupRidesItsStepsClaim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows starts a step's group unwatched")
	}
	t.Setenv("MAGUS_LEVEL", "")
	b, _ := testBudget(t, 10_000, 8)
	admit := &groupAdmitter{budgetAdmitter: &budgetAdmitter{budget: b}}
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(admit))
	require.NoError(t, err)

	var pgid string
	_, err = c.RunAll(t.Context(), []Step{{ProjectPath: ".", Target: "go-test"}},
		func(ctx context.Context, _ Step) error {
			res, err := spawn.Exec(ctx, "sh", []string{"-c", "echo $$"}, spawn.ExecOptions{Capture: true, Quiet: true})
			pgid = strings.TrimSpace(res.Stdout)
			return err
		})
	require.NoError(t, err)

	require.Len(t, admit.released, 1)
	claim := admit.released[0]
	assert.Equal(t, []string{
		"hold " + pgid + ` (sh -c "echo $$") on ` + claim,
		"release hold-" + claim,
	}, admit.events)

	admit.events = nil
	_, err = spawn.Exec(t.Context(), "true", nil, spawn.ExecOptions{Quiet: true})
	require.NoError(t, err)
	assert.Empty(t, admit.events, "outside a claimed step there is no claim to ride")
}

// An admitter that refuses with an exit status refused the step: best-effort admits a
// step only when nothing answered.
func TestMachineGateRefusesWhatTheAdmitterRefused(t *testing.T) {
	refusal := types.ExitError{Code: ExitCodeMachineDeclaration, Err: errors.New("jobs.max_validations is -1")}
	g := &machineGate{admit: refusingAdmitter{err: refusal}, log: newLogger("", 0)}
	_, err := g.acquire(t.Context(), types.MachineClaim{Target: "test"})
	require.ErrorIs(t, err, refusal.Err)

	g = &machineGate{admit: refusingAdmitter{err: errors.New("dial: no such file")}, log: newLogger("", 0)}
	release, err := g.acquire(t.Context(), types.MachineClaim{Target: "test"})
	require.NoError(t, err, "an unreachable broker still admits under best-effort")
	release()
}

func TestMachineBusyRefusalNamesTheWaitPipe(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	g, _ := testGate(t, b)
	_, err := g.acquire(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)

	_, err = g.acquire(t.Context(), types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status --wait | ")
	assert.Contains(t, err.Error(), "run <same args>")
}

func TestVerdictSaysItPackedByCurve(t *testing.T) {
	b, now := testBudget(t, 10_000, 8)
	require.True(t, b.Request(curveClaim(100, 7000, 1000, 50_000)).Granted)
	c := curveClaim(200, 1000, 7000, 50_000)
	v := b.Request(c)
	require.False(t, v.Granted)

	assert.Contains(t, machineBusyError(c, v, *now).Error(),
		"The broker packed by curve: the claims pass the 9.8 GiB budget together at 12:00:25, 25s from now.")

	old := v
	old.Packed = false
	assert.Contains(t, machineBusyError(c, old, *now).Error(),
		"The broker packed by peak, predating memory curves: this claimed the 6.8 GiB peak, which passes the budget now.",
		"a broker that predates curves answers Packed false")

	flat := types.MachineClaim{Project: ".", Target: "lint", MemoryMB: 7000, PID: 200}
	assert.Contains(t, machineBusyError(flat, types.MachineVerdict{BudgetMB: 10_000, HeldMB: 7000}, *now).Error(),
		"The broker packed by peak: this claimed the 6.8 GiB peak, which passes the budget now.")
}

// A step's result carries the claim it ran under, what it cost, and how long it queued:
// the budget seats one step at a time, so the second waits out the first.
func TestAStepResultCarriesItsClaimAndItsWait(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	b, _ := testBudget(t, 10_000, 1)
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}))
	require.NoError(t, err)

	const hold = 300 * time.Millisecond
	steps := []Step{
		{ProjectPath: ".", Target: "build", MemoryMB: 512},
		{ProjectPath: ".", Target: "lint", MemoryMB: 512},
	}
	results, err := c.RunAll(t.Context(), steps, func(ctx context.Context, _ Step) error {
		types.RecordCPUMs(ctx, 40)
		types.RecordPeakRSS(ctx, 1<<20)
		time.Sleep(hold)
		return nil
	})
	require.NoError(t, err)

	require.Len(t, results, 2)
	var waits []time.Duration
	for _, r := range results {
		assert.Equal(t, 1, r.Claim.Slots)
		assert.Equal(t, 512, r.Claim.MemoryMB)
		assert.False(t, r.Claim.Started.IsZero())
		assert.Equal(t, int64(40), r.CPUMs)
		assert.Equal(t, int64(1<<20), r.MaxRSSBytes)
		waits = append(waits, r.Claim.Waited)
	}
	slices.Sort(waits)
	assert.GreaterOrEqual(t, waits[1], hold, "the second step queued behind the first")
}

// A replay runs nothing, so its result carries no claim and no cost.
func TestAReplayedStepCarriesNoClaim(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	b, _ := testBudget(t, 10_000, 8)
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}))
	require.NoError(t, err)
	steps := []Step{{ProjectPath: ".", Target: "build", MemoryMB: 512}}
	run := func(ctx context.Context, _ Step) error {
		types.RecordCPUMs(ctx, 40)
		return nil
	}

	first, err := c.RunAll(t.Context(), steps, run)
	require.NoError(t, err)
	require.False(t, first[0].Hit)
	assert.Equal(t, 512, first[0].Claim.MemoryMB)

	replayed, err := c.RunAll(t.Context(), steps, run)
	require.NoError(t, err)
	require.True(t, replayed[0].Hit)
	assert.Zero(t, replayed[0].Claim)
	assert.Zero(t, replayed[0].CPUMs)
	assert.Zero(t, replayed[0].MaxRSSBytes)
}

// A refusal says how the slots were sized as well as the memory, so a reader knows
// whether to look at a slots policy, the run history, or the memory claim.
func TestMachineDeclarationNamesHowSlotsWereSized(t *testing.T) {
	measured := types.SlotSizing{Source: types.SlotsMeasured, Samples: 4}
	assert.Equal(t, "it takes 3 slots (measured over 4 runs)",
		describeMachineDeclaration(types.MachineClaim{Target: "scip", Slots: 3, Sizing: types.MemorySizing{Slots: measured}}))
	assert.Equal(t, "it claims 2.0 GiB (declared) and takes 1 slot (unmeasured)",
		describeMachineDeclaration(types.MachineClaim{Target: "test", MemoryMB: 2048, Slots: 1,
			Sizing: types.MemorySizing{Slots: types.SlotSizing{Source: types.SlotsUnmeasured}}}))
	assert.Equal(t, "it runs test, which claims 6.0 GiB (measured over 5 runs), and takes 6 slots (for its memory)",
		describeMachineDeclaration(types.MachineClaim{Target: "ci", DeclaredBy: "test", MemoryMB: 6144, Slots: 6,
			Sizing: types.MemorySizing{Samples: 5, Slots: types.SlotSizing{Source: types.SlotsForMemory}}}))
}

// composed is a skip_cache composer and the members its one ctx.needs call dispatches; a
// node with no members is a leaf.
type composed struct {
	step    Step
	members []composed
}

// leaves is n's members as leaves.
func leaves(composer Step, members ...Step) composed {
	n := composed{step: composer}
	for _, m := range members {
		n.members = append(n.members, composed{step: m})
	}
	return n
}

// composedRun runs root under a cache admitted by b, and inside each composer its members
// as steps of their own, all at once, as one ctx.needs call's members are dispatched. It
// returns the most leaves that ran at once and the members' joined errors.
func composedRun(t *testing.T, b *MachineBudget, root composed) (int, error) {
	t.Helper()
	t.Setenv("MAGUS_LEVEL", "")
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}))
	require.NoError(t, err)
	lim := WithLimiter(NewLimiter(8))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	running, widest := 0, 0
	leaf := func(context.Context) error {
		mu.Lock()
		running++
		widest = max(widest, running)
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
		return nil
	}
	var membersErr error
	var body func(n composed) func(context.Context) error
	body = func(n composed) func(context.Context) error {
		if len(n.members) == 0 {
			return leaf
		}
		return func(ctx context.Context) error {
			ref := types.TargetRef{Project: n.step.ProjectPath, Target: n.step.Target}
			return ChildrenClaimTheirOwn(ctx, ref, func(ctx context.Context) error {
				errs := make([]error, len(n.members))
				var wg sync.WaitGroup
				for i, m := range n.members {
					wg.Go(func() { _, errs[i] = c.RunAside(ctx, m.step, body(m), lim) })
				}
				wg.Wait()
				mu.Lock()
				membersErr = errors.Join(membersErr, errors.Join(errs...))
				mu.Unlock()
				return nil
			})
		}
	}
	_, err = c.RunAside(ctx, root.step, body(root), lim)
	require.NoError(t, err, "the composer itself fits")
	assert.Equal(t, 0, b.Snapshot().HeldMB, "every claim went back")
	return widest, membersErr
}

// TestChildrenClaimTheirOwnQueueBesideTheirComposer is entry 94's shape: four members of
// one call each fit beside their composer, and together they do not. Each claims for
// itself, so all four run, two at a time.
func TestChildrenClaimTheirOwnQueueBesideTheirComposer(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	var members []Step
	for _, name := range []string{"lint", "build", "test", "security"} {
		members = append(members, Step{ProjectPath: ".", Target: name, MemoryMB: 4000, NoCache: true})
	}

	widest, err := composedRun(t, b, leaves(Step{ProjectPath: ".", Target: "ci", MemoryMB: 1000, NoCache: true}, members...))
	require.NoError(t, err, "16000 summed is over the 10000 budget, but each member waits its turn")
	assert.LessOrEqual(t, widest, 2, "2 x 4000 fits; a third member would not")
}

// TestChildClaimLargerThanTheMachineNamesItself: a member over the whole budget is
// refused at once, by its own name, while its siblings run.
func TestChildClaimLargerThanTheMachineNamesItself(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	_, err := composedRun(t, b, leaves(Step{ProjectPath: ".", Target: "ci", MemoryMB: 1000, NoCache: true},
		Step{ProjectPath: ".", Target: "lint", MemoryMB: 2000, NoCache: true},
		Step{ProjectPath: ".", Target: "test", MemoryMB: 12_000, NoCache: true},
	))
	require.ErrorIs(t, err, types.MachineBudgetExhausted)
	assert.Contains(t, err.Error(), "refusing to start (root) test: it claims 11.7 GiB")
	assert.Contains(t, err.Error(), "does not fit in this machine's whole build budget")
	assert.NotContains(t, err.Error(), "lint", "lint fit and ran")
}

// TestChildClaimRunsInItsWaitingComposersSeat: a member that fits the budget alone but
// not beside its composer runs, because the composer holds nothing while it waits.
func TestChildClaimRunsInItsWaitingComposersSeat(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	_, err := composedRun(t, b, leaves(Step{ProjectPath: ".", Target: "ci", MemoryMB: 3000, NoCache: true},
		Step{ProjectPath: ".", Target: "test", MemoryMB: 8000, NoCache: true},
	))
	require.NoError(t, err)
}

// TestNestedComposersNeverHoldTheSeatsTheirMembersNeed is root generate's shape: every
// member composes generators of its own, and the members alone fill every slot. Each
// composer holds nothing while it waits, so every generator runs.
func TestNestedComposersNeverHoldTheSeatsTheirMembersNeed(t *testing.T) {
	b, _ := testBudget(t, 10_000, 4)
	root := composed{step: Step{ProjectPath: ".", Target: "generate", NoCache: true}}
	for _, name := range []string{"index-generate", "graph-generate", "docs-generate", "job-generate"} {
		root.members = append(root.members, leaves(Step{ProjectPath: ".", Target: name, NoCache: true},
			Step{ProjectPath: ".", Target: name + "-types", NoCache: true},
			Step{ProjectPath: ".", Target: name + "-config", NoCache: true},
		))
	}

	widest, err := composedRun(t, b, root)
	require.NoError(t, err)
	assert.LessOrEqual(t, widest, 4, "the leaves still queue for the 4 slots")
	assert.Equal(t, 0, b.Snapshot().HeldSlots, "every slot went back")
}
