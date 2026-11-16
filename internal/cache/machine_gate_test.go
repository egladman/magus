package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// standInReads counts reads of the healthy stand-in every test here starts on.
// A test machine truly short of memory must not fail this package.
var standInReads atomic.Int64

func init() {
	readPressure = func(context.Context) (string, error) {
		standInReads.Add(1)
		return "", nil
	}
}

type MemoryPressureSuite struct {
	suite.Suite
	reads int
}

func TestMemoryPressureSuite(t *testing.T) {
	suite.Run(t, new(MemoryPressureSuite))
}

// Neither variable may say this test binary sits under a parent magus.
func (s *MemoryPressureSuite) SetupTest() {
	s.T().Setenv("MAGUS_LEVEL", "0")
	s.T().Setenv(spawn.AncestorsEnvVar, "")
}

func (s *MemoryPressureSuite) reading(reason string, err error) {
	saved := readPressure
	s.T().Cleanup(func() { readPressure = saved })
	s.reads = 0
	readPressure = func(context.Context) (string, error) {
		s.reads++
		return reason, err
	}
}

const badMemory = "the memory compressor holds 97% of its segment limit"

func (s *MemoryPressureSuite) gate() (*machineGate, *budgetAdmitter) {
	b := testBudget(s.T(), 10_000, 8)
	return testGate(s.T(), b)
}

func (s *MemoryPressureSuite) TestABadReadingRefusesBeforeTheBudgetIsAsked() {
	s.reading(badMemory, nil)
	g, adm := s.gate()
	_, err := g.acquire(s.T().Context(), budget.Claim{Project: "docs", Target: "test", PID: 100})
	s.Require().ErrorIs(err, types.MemoryPressureHigh)
	var exit types.ExitError
	s.Require().ErrorAs(err, &exit)
	s.Equal(ExitCodeMachineBusy, exit.Code)
	s.Contains(err.Error(), "not starting docs test")
	s.Contains(err.Error(), badMemory)
	s.Contains(err.Error(), "Running steps keep going")
	s.Zero(adm.requests, "the refusal reaches no budget, so no claim waits, a gate's included")
}

func (s *MemoryPressureSuite) TestARefusalOffersNoWaitForASeat() {
	s.reading(badMemory, nil)
	g, _ := s.gate()
	_, err := g.acquire(s.T().Context(), budget.Claim{Target: "test"})
	s.NotContains(err.Error(), "--wait")
}

func (s *MemoryPressureSuite) TestARefusalToALeasedWorkerNamesTheOwedFiling() {
	s.T().Setenv("XDG_STATE_HOME", s.T().TempDir())
	s.reading(badMemory, nil)
	g, _ := s.gate()
	_, err := g.acquire(job.WithLease(s.T().Context(), "wave/api"), budget.Claim{Target: "test"})
	s.Require().ErrorIs(err, types.MemoryPressureHigh)
	s.Contains(err.Error(), LeasedPressureNext("wave/api"))
	s.NotContains(err.Error(), "never waits for a seat", "a seat is not what this lacks")
}

// owedGate refuses owed from an integration, so the refusal cannot stop there.
func (s *MemoryPressureSuite) TestAnIntegrationIsToldToFreeMemory() {
	s.Contains(LeasedPressureNext("integrate"), "An integration owns its checks, so free memory and run this again")
}

func (s *MemoryPressureSuite) TestAClaimHeldIntoBadMemoryHandsItsSeatBack() {
	s.reading("", nil)
	b := testBudget(s.T(), 10_000, 8)
	adm := &turningAdmitter{MachineAdmitter: &budgetAdmitter{budget: b}, turn: func() { s.reading(badMemory, nil) }}
	g := &machineGate{admit: adm, log: newLogger("", 0)}
	_, err := g.acquire(s.T().Context(), budget.Claim{Project: ".", Target: "ci", PID: 100})
	s.Require().ErrorIs(err, types.MemoryPressureHigh)
	s.Empty(b.Snapshot().Holders, "the seat went back")
}

func (s *MemoryPressureSuite) TestANestedRunPassesUnderItsRunningParent() {
	s.reading(badMemory, nil)
	g, _ := s.gate()
	release, err := g.acquire(s.T().Context(), budget.Claim{Target: "test", PID: 100, Ancestors: []string{"1:parent"}})
	s.Require().NoError(err)
	release()
	s.Zero(s.reads)
}

var underWrapper = "1:" + types.WrapperInvocation("exec")

func (s *MemoryPressureSuite) TestARunUnderOnlyAWrapperIsRefused() {
	s.reading(badMemory, nil)
	g, adm := s.gate()
	_, err := g.acquire(s.T().Context(), budget.Claim{Target: "test", PID: 100, Ancestors: []string{underWrapper}})
	s.Require().ErrorIs(err, types.MemoryPressureHigh)
	s.Zero(adm.requests, "no running step sits above it")
}

func (s *MemoryPressureSuite) TestARunUnderAStepUnderAWrapperPasses() {
	s.reading(badMemory, nil)
	g, _ := s.gate()
	release, err := g.acquire(s.T().Context(), budget.Claim{Target: "test", PID: 100, Ancestors: []string{underWrapper, "2:parent"}})
	s.Require().NoError(err)
	release()
	s.Zero(s.reads)
}

func (s *MemoryPressureSuite) TestARunUnderAWrapperUnderAStepPasses() {
	s.T().Setenv("MAGUS_LEVEL", "1")
	s.reading(badMemory, nil)
	g, _ := s.gate()
	release, err := g.acquire(s.T().Context(), budget.Claim{Target: "test", PID: 100, Ancestors: []string{underWrapper}})
	s.Require().NoError(err)
	release()
}

func (s *MemoryPressureSuite) TestANestedRunThatLostItsAncestryPassesToo() {
	s.T().Setenv("MAGUS_LEVEL", "1")
	s.reading(badMemory, nil)
	g, _ := s.gate()
	release, err := g.acquire(s.T().Context(), budget.Claim{Target: "test", PID: 100})
	s.Require().NoError(err)
	release()
}

func (s *MemoryPressureSuite) TestAPartialReadingSaysSoOnceAndStillRefuses() {
	s.reading(badMemory, errors.New("compressor segments are unread"))
	g, _ := s.gate()
	var logged bytes.Buffer
	g.log = slog.New(slog.NewTextHandler(&logged, nil))
	for range 2 {
		_, err := g.acquire(s.T().Context(), budget.Claim{Target: "test", PID: 100})
		s.Require().ErrorIs(err, types.MemoryPressureHigh)
	}
	s.Equal(1, strings.Count(logged.String(), "compressor segments are unread"))
}

func (s *MemoryPressureSuite) TestATestGateReadsTheStandIn() {
	before := standInReads.Load()
	g, _ := s.gate()
	release, err := g.acquire(s.T().Context(), budget.Claim{Target: "test", PID: 100})
	s.Require().NoError(err)
	release()
	s.Greater(standInReads.Load(), before)
}

func (s *MemoryPressureSuite) TestAHealthyReadingSeatsTheClaim() {
	s.reading("", nil)
	g, adm := s.gate()
	release, err := g.acquire(s.T().Context(), budget.Claim{Target: "test", PID: 100})
	s.Require().NoError(err)
	release()
	s.Equal(1, adm.requests)
}

func (s *MemoryPressureSuite) TestAnUnreadableHostSaysSoOnceAndAdmits() {
	s.reading("", errors.New("this platform has no memory-pressure reading"))
	g, adm := s.gate()
	var logged bytes.Buffer
	g.log = slog.New(slog.NewTextHandler(&logged, nil))
	for range 2 {
		release, err := g.acquire(s.T().Context(), budget.Claim{Target: "test", PID: 100})
		s.Require().NoError(err)
		release()
	}
	s.Equal(2, adm.requests)
	s.Equal(1, strings.Count(logged.String(), "memory reading is missing or partial"))
	s.Contains(logged.String(), "this platform has no memory-pressure reading")
}

// A composer waiting on its members is a running step. Memory going bad while
// it waited must not fail its retake.
func (s *MemoryPressureSuite) TestARunningStepRetakesItsClaimUnderPressure() {
	s.reading("", nil)
	g, _ := s.gate()
	claim := budget.Claim{Target: "ci", PID: 100}
	seat, err := g.claim(s.T().Context(), claim)
	s.Require().NoError(err)
	h := &machineHold{gate: g, claim: claim, id: seat.id, release: seat.release, slots: seat.slots}
	h.lend()
	s.Empty(h.current())

	s.reading(badMemory, nil)
	s.Require().NoError(h.reclaim(s.T().Context()))
	s.NotEmpty(h.current())
	s.Zero(s.reads, "a retake reads no memory")
	h.close()
}

func (s *MemoryPressureSuite) TestAdmitMachineRefusesHostWorkToo() {
	s.reading(badMemory, nil)
	_, adm := s.gate()
	_, err := AdmitMachine(s.T().Context(), adm, budget.Claim{Project: ".", Target: "session-brief"}, false, 0)
	s.Require().ErrorIs(err, types.MemoryPressureHigh)
}

func (s *MemoryPressureSuite) TestNoAdmitterReadsNoMemory() {
	s.reading(badMemory, nil)
	release, err := AdmitMachine(s.T().Context(), nil, budget.Claim{Target: "test"}, false, 0)
	s.Require().NoError(err, "broker: off wires no admitter, and is the one way out of a machine stuck bad")
	release()
	s.Zero(s.reads)
}

// turningAdmitter runs turn during its Claim, as a machine going bad during a
// held wait does.
type turningAdmitter struct {
	MachineAdmitter
	turn func()
}

func (a *turningAdmitter) Claim(ctx context.Context, c budget.Claim, parked func(budget.Verdict)) (budget.Verdict, error) {
	a.turn()
	return a.MachineAdmitter.Claim(ctx, c, parked)
}

func TestMachineRefusalNamesTheOversizedClaimAndThatNoWaitSeatsIt(t *testing.T) {
	b := testBudget(t, 4000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), budget.Claim{
		Project: ".", Target: "ci", DeclaredBy: "test", MemoryMB: 26_000, Slots: 1, PID: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no wait seats it")
	assert.Contains(t, err.Error(), "25.4 GiB against a budget of 3.9 GiB", "which claim, against which budget")
	assert.Contains(t, err.Error(), "Narrow the run")
}

// refusingAdmitter answers every request with err.
type refusingAdmitter struct{ err error }

func (a refusingAdmitter) Claim(context.Context, budget.Claim, func(budget.Verdict)) (budget.Verdict, error) {
	return budget.Verdict{}, a.err
}

func (a refusingAdmitter) Request(context.Context, budget.Claim) (budget.Verdict, error) {
	return budget.Verdict{}, a.err
}

func (refusingAdmitter) Release(context.Context, string) {}

func (refusingAdmitter) Overrun(context.Context, string) {}

func (a refusingAdmitter) HoldGroup(context.Context, string, int, string) (string, error) {
	return "", a.err
}

func (refusingAdmitter) ReleaseHold(context.Context, string) error { return nil }

// overrunAdmitter sends each overrun it is told of on overruns.
type overrunAdmitter struct {
	refusingAdmitter
	overruns chan string
}

func (a overrunAdmitter) Overrun(_ context.Context, id string) { a.overruns <- id }

func TestOverrunIsSentOnce(t *testing.T) {
	admit := overrunAdmitter{overruns: make(chan string, 4)}
	profile := budget.Profile{D10Ms: 10_000, D90Ms: 10_000, Runs: 3}
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

	unwatched := withOverrunWatch(types.WithExecCost(t.Context()), admit, func() string { return "claim-2" }, budget.Profile{})
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
	MachineAdmitter
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

// refusingHolder is a broker that refuses every hold and records the claim each named.
type refusingHolder struct {
	MachineAdmitter
	mu     sync.Mutex
	claims []string
}

func (h *refusingHolder) HoldGroup(_ context.Context, claim string, _ int, _ string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.claims = append(h.claims, claim)
	return "", errors.New("no such claim")
}

func (*refusingHolder) ReleaseHold(context.Context, string) error { return nil }

func spawnChildren(t *testing.T, ctx context.Context, n int) {
	t.Helper()
	for range n {
		_, err := spawn.Exec(ctx, "true", nil, spawn.ExecOptions{Quiet: true})
		require.NoError(t, err)
	}
}

// One refusal line per step, however many children it starts.
func TestAStepGroupRefusalWarnsOnceForManyChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows starts a step's group unwatched")
	}
	logs := captureLogs(t)
	holder := &refusingHolder{}
	hold := &machineHold{claim: budget.Claim{Project: "libs/xdg", Target: "go-test"}, id: "claim-1"}
	ctx := withMachineHold(t.Context(), hold)
	ctx = withStepGroups(ctx, holder, hold.current)

	spawnChildren(t, ctx, 3)

	assert.Equal(t, []string{"claim-1", "claim-1", "claim-1"}, holder.claims, "every child is offered to the broker")
	warnings := warnMessages(logs)
	require.Len(t, warnings, 1)
	assert.Regexp(t, `^child processes of libs/xdg:go-test are not tied to its build claim, so they keep running if magus exits abnormally: the broker refused the hold \(no such claim\)\. First child: pid \d+ \(true\)\.$`, warnings[0])
}

func warnMessages(h *recordingHandler) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range h.records {
		if r.Level >= slog.LevelWarn {
			out = append(out, r.Message)
		}
	}
	return out
}

// A claim lent to members reads "" when a child starts: expected, so nothing reaches the
// broker and nothing is said.
func TestAStepGroupStartedWhileTheClaimIsLentWarnsOfNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows starts a step's group unwatched")
	}
	logs := captureLogs(t)
	holder := &refusingHolder{}
	hold := &machineHold{claim: budget.Claim{Project: "libs/xdg", Target: "go-test"}, id: "claim-1", release: func() {}}
	ctx := withMachineHold(t.Context(), hold)
	ctx = withStepGroups(ctx, holder, hold.current)

	require.NoError(t, LendMachineClaim(ctx, func(ctx context.Context) error {
		spawnChildren(t, ctx, 3)
		return nil
	}))

	assert.Empty(t, holder.claims, "an empty claim id never reaches the broker")
	assert.Empty(t, warnMessages(logs))
}

// A failing sibling cancels the run's context, and a hold made on it then could be one
// the broker records and this process never releases; the hold runs uncancelled but
// bounded. A release the broker does not answer says the group may still be held.
func TestHoldStepGroupOutlivesACancelledRunAndWarnsOnAnUnansweredRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	holder := &unansweredHolder{}

	out := captureLog(t, func() {
		holdStepGroup(ctx, holder, "claim-1", 4242, "sleep 30", func(int, string, error) {})()
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
	b := testBudget(t, 10_000, 8)
	admit := &groupAdmitter{budgetAdmitter: &budgetAdmitter{budget: b}}
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(admit, 0))
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
		fmt.Sprintf("hold %s (sh -c \"echo $$\") on %s", pgid, claim),
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
	_, err := g.acquire(t.Context(), budget.Claim{Target: "test"})
	require.ErrorIs(t, err, refusal.Err)

	g = &machineGate{admit: refusingAdmitter{err: errors.New("dial: no such file")}, log: newLogger("", 0)}
	release, err := g.acquire(t.Context(), budget.Claim{Target: "test"})
	require.NoError(t, err, "an unreachable broker still admits under best-effort")
	release()
}

func TestVerdictSaysItPackedByCurve(t *testing.T) {
	now := testBudgetNow
	b := testBudget(t, 10_000, 8)
	require.True(t, b.Request(curveClaim(100, 7000, 1000, 50_000), 0).Granted)
	c := curveClaim(200, 1000, 7000, 50_000)
	v := b.Request(c, 0)
	require.False(t, v.Granted)

	assert.Contains(t, machineBusyError(c, v, now, "", 0, waitBound{}).Error(),
		"The broker packed by curve: the claims pass the 9.8 GiB budget together at 12:00:25, 25s from now.")

	old := v
	old.Packed = false
	assert.Contains(t, machineBusyError(c, old, now, "", 0, waitBound{}).Error(),
		"The broker packed by peak, predating memory curves: this claimed the 6.8 GiB peak, which passes the budget now.",
		"a broker that predates curves answers Packed false")

	flat := budget.Claim{Project: ".", Target: "lint", MemoryMB: 7000, PID: 200}
	assert.Contains(t, machineBusyError(flat, budget.Verdict{BudgetMB: 10_000, HeldMB: 7000}, now, "", 0, waitBound{}).Error(),
		"The broker packed by peak: this claimed the 6.8 GiB peak, which passes the budget now.")
}

// A step's result carries the claim it ran under, what it cost, and how long it queued:
// the budget seats one step at a time, so the second waits out the first.
func TestAStepResultCarriesItsClaimAndItsWait(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	b := testBudget(t, 10_000, 1)
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}, 0))
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
	b := testBudget(t, 10_000, 8)
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}, 0))
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
	measured := budget.SlotSizing{Source: budget.SlotsMeasured, Samples: 4}
	assert.Equal(t, "it takes 3 slots (measured over 4 runs)",
		describeMachineDeclaration(budget.Claim{Target: "scip", Slots: 3, Sizing: budget.Sizing{Slots: measured}}))
	assert.Equal(t, "it claims 2.0 GiB (declared) and takes 1 slot (unmeasured)",
		describeMachineDeclaration(budget.Claim{Target: "test", MemoryMB: 2048, Slots: 1,
			Sizing: budget.Sizing{Slots: budget.SlotSizing{Source: budget.SlotsUnmeasured}}}))
	assert.Equal(t, "it runs test, which claims 6.0 GiB (measured over 5 runs), and takes 6 slots (for its memory)",
		describeMachineDeclaration(budget.Claim{Target: "ci", DeclaredBy: "test", MemoryMB: 6144, Slots: 6,
			Sizing: budget.Sizing{Samples: 5, Slots: budget.SlotSizing{Source: budget.SlotsForMemory}}}))
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
func composedRun(t *testing.T, b *budget.Machine, root composed) (int, error) {
	t.Helper()
	t.Setenv("MAGUS_LEVEL", "")
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}, 0))
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
			return LendMachineClaim(ctx, func(ctx context.Context) error {
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
	b := testBudget(t, 10_000, 8)
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
	b := testBudget(t, 10_000, 8)
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
	b := testBudget(t, 10_000, 8)
	_, err := composedRun(t, b, leaves(Step{ProjectPath: ".", Target: "ci", MemoryMB: 3000, NoCache: true},
		Step{ProjectPath: ".", Target: "test", MemoryMB: 8000, NoCache: true},
	))
	require.NoError(t, err)
}

// TestNestedComposersNeverHoldTheSeatsTheirMembersNeed is root generate's shape: every
// member composes generators of its own, and the members alone fill every slot. Each
// composer holds nothing while it waits, so every generator runs.
func TestNestedComposersNeverHoldTheSeatsTheirMembersNeed(t *testing.T) {
	b := testBudget(t, 10_000, 4)
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

// TestMembersWaitingOnASharedNeedLendTheirSeats is the 2026-10-21 `vcs resolve` hang:
// generate's members alone fill every slot, and each needs types-generate, which
// generate dispatched too and which needs a seat of its own. Each member lends its claim
// while it waits, so the shared need runs and every member completes.
func TestMembersWaitingOnASharedNeedLendTheirSeats(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	const members = 4
	b := testBudget(t, 10_000, members)
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}, 0))
	require.NoError(t, err)
	lim := WithLimiter(NewLimiter(8))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	waiting := make(chan struct{}, members)
	sharedDone := make(chan struct{})
	member := func(ctx context.Context) error {
		waiting <- struct{}{}
		return LendMachineClaim(ctx, func(ctx context.Context) error {
			select {
			case <-sharedDone:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}
	shared := Step{ProjectPath: ".", Target: "types-generate", NoCache: true}
	_, err = c.RunAside(ctx, Step{ProjectPath: ".", Target: "generate", NoCache: true}, func(ctx context.Context) error {
		return LendMachineClaim(ctx, func(ctx context.Context) error {
			errs := make([]error, members+1)
			var wg sync.WaitGroup
			for i := range members {
				m := Step{ProjectPath: ".", Target: fmt.Sprintf("gen-%d", i), NoCache: true}
				wg.Go(func() { _, errs[i] = c.RunAside(ctx, m, member, lim) })
			}
			for range members {
				<-waiting
			}
			wg.Go(func() {
				defer close(sharedDone)
				_, errs[members] = c.RunAside(ctx, shared, func(context.Context) error { return nil }, lim)
			})
			wg.Wait()
			return errors.Join(errs...)
		})
	}, lim)
	require.NoError(t, err, "every member held a seat while the need they share queued for one")
	assert.Equal(t, 0, b.Snapshot().HeldSlots, "every slot went back")
}

// BudgetWaitSuite pins how long a claim waits for room other invocations
// hold, and what it says while it does.
type BudgetWaitSuite struct {
	suite.Suite
	b      *budget.Machine
	adm    *budgetAdmitter
	gate   *machineGate
	logged *lockedLog
	// holder is pid 100's claim filling 9000 of 10000 MB.
	holder string
}

func TestBudgetWaitSuite(t *testing.T) { suite.Run(t, new(BudgetWaitSuite)) }

func (s *BudgetWaitSuite) SetupTest() {
	s.T().Setenv("MAGUS_LEVEL", "0")
	s.T().Setenv(spawn.AncestorsEnvVar, "")
	s.T().Setenv("XDG_STATE_HOME", s.T().TempDir())
	s.b = testBudget(s.T(), 10_000, 8)
	s.adm = &budgetAdmitter{budget: s.b}
	s.logged = &lockedLog{}
	s.gate = &machineGate{admit: s.adm, wait: time.Minute, log: slog.New(slog.NewTextHandler(s.logged, nil))}
	v := s.b.Request(budget.Claim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100}, 0)
	s.Require().True(v.Granted)
	s.holder = v.ID
}

// stranger is pid 200's claim, which the holder keeps out.
var stranger = budget.Claim{Project: "docs", Target: "ci", MemoryMB: 5000, PID: 200}

func (s *BudgetWaitSuite) said(text string) bool { return strings.Contains(s.logged.String(), text) }

func (s *BudgetWaitSuite) TestAStrangerHeldClaimWaitsAndIsSeatedOnRelease() {
	done := acquireAsync(s.T().Context(), s.gate, stranger)
	s.Eventually(func() bool { return s.said("waiting for a seat for docs ci") }, 5*time.Second, 5*time.Millisecond)
	s.True(s.said("pid 100 (root) test"))
	s.True(s.said("It gives up after 1m0s (the bound " + BudgetWaitSource + " sets)"))

	s.adm.Release(s.T().Context(), s.holder)
	s.Require().NoError(<-done)
	s.True(s.said("seated docs ci after"))
}

func (s *BudgetWaitSuite) TestAtTheBoundItFailsWithMGS3009NamingTheHoldersAndTheSource() {
	s.gate.wait = 100 * time.Millisecond
	_, err := s.gate.acquire(s.T().Context(), stranger)

	s.Require().ErrorIs(err, types.MachineBudgetExhausted)
	var exit types.ExitError
	s.Require().ErrorAs(err, &exit)
	s.Equal(ExitCodeMachineBusy, exit.Code)
	s.Contains(err.Error(), "That is the bound "+BudgetWaitSource+" sets")
	s.Contains(err.Error(), "held by pid 100 (root) test")
	s.Contains(err.Error(), "Raise "+BudgetWaitSource+", or free a holder")
}

func (s *BudgetWaitSuite) TestABudgetWaitOfZeroRefusesAtOnce() {
	s.gate.wait = 0
	_, err := s.gate.acquire(s.T().Context(), stranger)

	s.Require().ErrorIs(err, types.MachineBudgetExhausted)
	s.Contains(err.Error(), "It did not wait, since "+BudgetWaitSource+" is 0")
	s.Contains(err.Error(), "run the same command again with --budget-wait 30m")
	s.False(s.said("waiting for a seat"), "a refusal at once announces no wait")
}

func (s *BudgetWaitSuite) TestTheBoundIsTheSoonerOfBudgetWaitAndTheDeadline() {
	s.Equal(waitBound{d: time.Minute, source: "the bound " + BudgetWaitSource + " sets"}, budgetBound(s.T().Context(), time.Minute))

	ctx, cancel := context.WithTimeout(s.T().Context(), time.Second)
	defer cancel()
	b := budgetBound(ctx, time.Minute)
	s.Equal("this run's deadline", b.source)
	s.LessOrEqual(b.d, time.Second)
}

func (s *BudgetWaitSuite) TestAHolderChangeSaysSoOnce() {
	done := acquireAsync(s.T().Context(), s.gate, stranger)
	s.Eventually(func() bool { return s.said("waiting for a seat") }, 5*time.Second, 5*time.Millisecond)

	s.Require().True(s.b.Request(budget.Claim{Project: "web", Target: "lint", MemoryMB: 500, PID: 300}, 0).Granted)
	s.adm.wake()
	s.Eventually(func() bool { return s.said("still waiting for a seat for docs ci") }, 5*time.Second, 5*time.Millisecond)
	s.True(s.said("arrived pid 300 web lint"))
	s.False(strings.Contains(s.logged.String()[strings.Index(s.logged.String(), "still waiting"):], "pid 100"),
		"the progress line does not repeat the holders already listed")
	s.adm.wake()
	s.Never(func() bool { return strings.Count(s.logged.String(), "still waiting") > 1 }, 100*time.Millisecond, 10*time.Millisecond,
		"no change says nothing")

	s.adm.Release(s.T().Context(), s.holder)
	s.Require().NoError(<-done)
}

func (s *BudgetWaitSuite) TestAFinalAskAtTheBoundThatFitsIsSeated() {
	s.gate.admit = &roomAtTheBound{budgetAdmitter: s.adm}
	s.gate.wait = 50 * time.Millisecond
	release, err := s.gate.acquire(s.T().Context(), stranger)

	s.Require().NoError(err, "the unparked ask at the bound found room")
	release()
}

func (s *BudgetWaitSuite) TestANestedChildUnderAHolderIsFreeSeatedNotQueued() {
	parent := s.b.Request(budget.Claim{Project: ".", Target: "ci", MemoryMB: 500, PID: 400, Invocation: "400:root"}, 0)
	s.Require().True(parent.Granted)
	release, err := s.gate.acquire(s.T().Context(), budget.Claim{
		Project: ".", Target: "go-test", MemoryMB: 4000, PID: 401, Invocation: "401:child", Ancestors: []string{"400:root"},
	})

	s.Require().NoError(err)
	release()
	s.False(s.said("waiting for a seat"), "its stalled parent's free seat takes it")
}

func (s *BudgetWaitSuite) TestAComposersRetakeWaitsInsteadOfFailing() {
	s.adm.Release(s.T().Context(), s.holder)
	claim := budget.Claim{Project: ".", Target: "ci", MemoryMB: 6000, PID: 500}
	seat, err := s.gate.claim(s.T().Context(), claim)
	s.Require().NoError(err)
	h := &machineHold{gate: s.gate, claim: claim, id: seat.id, release: seat.release, slots: seat.slots}
	h.lend()
	member := s.b.Request(budget.Claim{Project: "x", Target: "build", MemoryMB: 9000, PID: 600}, 0)
	s.Require().True(member.Granted)

	retaken := make(chan error, 1)
	go func() { retaken <- h.reclaim(s.T().Context()) }()
	s.Eventually(func() bool { return s.said("waiting for a seat for (root) ci") }, 5*time.Second, 5*time.Millisecond)
	s.adm.Release(s.T().Context(), member.ID)

	s.Require().NoError(<-retaken, "the retake waits for room rather than failing after its members ran")
	s.NotEmpty(h.current())
	h.close()
}

func (s *BudgetWaitSuite) TestALeasedWorkerWaitsItsBoundThenNamesTheOwedFiling() {
	s.gate.wait = 50 * time.Millisecond
	_, err := s.gate.acquire(job.WithLease(s.T().Context(), "wave/api"), stranger)

	s.Require().ErrorIs(err, types.MachineBudgetExhausted)
	s.Contains(err.Error(), fmt.Sprintf(
		"Job wave/api got no seat: file this check as owed, not as a ref, with `%s --stdin` and owed %q on its gate evidence",
		hint.JobExit.With("wave/api"), job.OwedNoSeat))
}

// roomAtTheBound parks every claim and seats the one unparked ask.
type roomAtTheBound struct{ *budgetAdmitter }

func (a *roomAtTheBound) Claim(ctx context.Context, _ budget.Claim, parked func(budget.Verdict)) (budget.Verdict, error) {
	parked(budget.Verdict{Fits: true, Holders: []budget.Claimant{{Project: ".", Target: "test", PID: 100}}})
	<-ctx.Done()
	return budget.Verdict{}, ctx.Err()
}

func (a *roomAtTheBound) Request(context.Context, budget.Claim) (budget.Verdict, error) {
	return budget.Verdict{Granted: true, Fits: true, ID: "at-the-bound"}, nil
}

// lockedLog is a log the gate writes from its own goroutines.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (s *BudgetWaitSuite) timedWait() (context.Context, *budgetWait) {
	g := &machineGate{wait: 50 * time.Millisecond, log: s.gate.log}
	wctx, w := g.newBudgetWait(s.T().Context(), budget.Claim{Project: ".", Target: "test"})
	s.T().Cleanup(w.stop)
	return wctx, w
}

var (
	strangerHolds = budget.Verdict{Fits: true, Holders: []budget.Claimant{{PID: 7, Project: ".", Target: "lint"}}}
	ownRunHolds   = budget.Verdict{Fits: true, OwnRun: true}
)

func (s *BudgetWaitSuite) TestAWaitThatTurnsToItsOwnRunNeverExpires() {
	wctx, w := s.timedWait()
	w.parked(s.T().Context(), strangerHolds)
	w.parked(s.T().Context(), ownRunHolds)

	s.Never(w.expired, 300*time.Millisecond, 10*time.Millisecond)
	s.NoError(wctx.Err())
}

func (s *BudgetWaitSuite) TestAWaitThatTurnsToStrangersAgainStartsItsBoundAgain() {
	wctx, w := s.timedWait()
	w.parked(s.T().Context(), strangerHolds)
	w.parked(s.T().Context(), ownRunHolds)
	w.parked(s.T().Context(), strangerHolds)

	s.Eventually(w.expired, 5*time.Second, 10*time.Millisecond)
	s.Error(wctx.Err())
}

func TestBudgetWaitLineNamesEachHoldersBreakdownAndTheSoonestEndAsAtLeast(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	v := budget.Verdict{Holders: []budget.Claimant{
		{Project: ".", Target: "lint", PID: 10, MemoryMB: 4915, Sizing: budget.Sizing{Samples: 48}, Dir: "/a",
			Since: now.Add(-time.Minute), Profile: budget.Profile{Runs: 48, D10Ms: 90_000, D90Ms: 300_000}},
		{Project: ".", Target: "conventions", Under: ".:lint", PID: 10, MemoryMB: 4300, Sizing: budget.Sizing{Samples: 12}},
		{Project: ".", Target: "vet", PID: 11},
	}}
	assert.Equal(t, "held by pid 10 (root) lint 4.8 GiB: own peak over 48 runs, in /a; "+
		"pid 10 (root) conventions 4.2 GiB under .:lint: own peak over 12 runs; pid 11 (root) vet; "+
		"the first holder ends in at least 30s", describeMachineWait(v, now))
}

func TestGrantedSlotsFromABrokerWithoutGrantsAreTheFullWidthSaidOnce(t *testing.T) {
	var logged bytes.Buffer
	g := &machineGate{log: slog.New(slog.NewTextHandler(&logged, nil))}
	elastic := budget.Claim{Project: ".", Target: "test", Slots: 6, SlotsFloor: 1}

	first := g.granted(t.Context(), elastic, budget.Verdict{Granted: true, ID: "1.1"})
	second := g.granted(t.Context(), elastic, budget.Verdict{Granted: true, ID: "1.2"})
	assert.Equal(t, [2]int{6, 6}, [2]int{first.slots, second.slots})
	assert.Equal(t, 1, strings.Count(logged.String(), "the broker grants no elastic slots"))
	assert.Equal(t, 2, g.granted(t.Context(), elastic, budget.Verdict{Granted: true, ID: "1.3", Slots: 2}).slots)
}

func TestAComposersRetakeAsksTheWidthItRunsWith(t *testing.T) {
	h := &machineHold{claim: budget.Claim{Target: "lint", Slots: 8, SlotsFloor: 1}, slots: 3}
	assert.Equal(t, budget.Claim{Target: "lint", Slots: 3, SlotsFloor: 3}, h.retake(),
		"its jobserver and GOMAXPROCS were fixed at three")
}

func TestBudgetWaitLineSaysItsPlaceInTheQueue(t *testing.T) {
	assert.Empty(t, describeMachineQueue(budget.Verdict{}), "a broker without positions says nothing")
	assert.Equal(t, "; 1 claim is ahead of it in the queue", describeMachineQueue(budget.Verdict{Ahead: 1}))
	assert.Equal(t, "; 3 claims are ahead of it in the queue", describeMachineQueue(budget.Verdict{Ahead: 3}))
}

// curveClaim is a claim over a 100s run holding early for its first half and late for
// its second, with a duration band from d10Ms to 100s, claiming its peak.
func curveClaim(pid int, early, late uint32, d10Ms int64) budget.Claim {
	p := budget.Profile{D10Ms: d10Ms, D90Ms: 100_000, Runs: 3}
	for i := range p.Curve {
		p.Curve[i] = early
		if i >= budget.CurveBuckets/2 {
			p.Curve[i] = late
		}
	}
	return budget.Claim{Project: ".", Target: "test", MemoryMB: p.Curve.PeakMB(), Profile: p, Slots: 1, PID: pid}
}

// testBudgetNow is the instant every testBudget's clock stands at.
var testBudgetNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

// testBudget is a machine budget whose clock stands at testBudgetNow and that knows
// magus's wrapper invocations, as the broker's does.
func testBudget(t *testing.T, mb, slots int) *budget.Machine {
	t.Helper()
	return budget.NewMachine(mb, slots, budget.WithWrappers(types.IsWrapperInvocation),
		budget.WithClock(func() time.Time { return testBudgetNow }))
}

// budgetAdmitter is a MachineAdmitter answering from a budget a test
// writes. Like the broker, it queues a claim that can fit until a release
// seats it. It records every request so the gate's calls can be observed.
type budgetAdmitter struct {
	mu       sync.Mutex
	freed    sync.Cond
	budget   *budget.Machine
	requests int
	fail     error
	released []string
	overruns []string
}

func (*budgetAdmitter) HoldGroup(context.Context, string, int, string) (string, error) {
	return "", nil
}

func (*budgetAdmitter) ReleaseHold(context.Context, string) error { return nil }

func (f *budgetAdmitter) Claim(ctx context.Context, c budget.Claim, parked func(budget.Verdict)) (budget.Verdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if f.fail != nil {
		return budget.Verdict{}, f.fail
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
			return budget.Verdict{}, ctx.Err()
		}
		f.freed.Wait()
	}
}

func (f *budgetAdmitter) Request(_ context.Context, c budget.Claim) (budget.Verdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return budget.Verdict{}, f.fail
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

func testGate(t *testing.T, b *budget.Machine) (*machineGate, *budgetAdmitter) {
	t.Helper()
	adm := &budgetAdmitter{budget: b}
	g := &machineGate{admit: adm, log: newLogger("", 0)}
	return g, adm
}

func TestAdmitMachineClaimsAndReleasesNonRunWork(t *testing.T) {
	b := testBudget(t, 1000, 2)
	adm := &budgetAdmitter{budget: b}
	release, err := AdmitMachine(t.Context(), adm, budget.Claim{
		Project: ".", Target: "session-brief", MemoryMB: 512,
	}, false, 0)
	require.NoError(t, err)

	holders := b.Snapshot().Holders
	require.Len(t, holders, 1)
	assert.NotEmpty(t, holders[0].Dir)
	assert.Equal(t, budget.Claimant{
		Project: ".", Target: "session-brief", PID: os.Getpid(), MemoryMB: 512, Slots: 1,
		Dir:     holders[0].Dir,     // where the test binary runs
		Command: holders[0].Command, // the test binary's argv
		Since:   testBudgetNow,
	}, holders[0])

	other := b.Request(budget.Claim{Project: ".", Target: "build", MemoryMB: 600, PID: os.Getpid() + 1}, 0)
	assert.False(t, other.Granted, "the brief's claim must count against another invocation")
	release()
	assert.Empty(t, b.Snapshot().Holders)
}

func TestABudgetWaitOfZeroRefusesAtOnceNamingTheHolder(t *testing.T) {
	b := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)

	_, err := g.acquire(t.Context(), budget.Claim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)

	_, err = g.acquire(t.Context(), budget.Claim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted), "the code is what maps to exit 75")
	assert.Contains(t, err.Error(), "It did not wait")
	assert.Contains(t, err.Error(), "pid 100 (root) test")
	assert.Equal(t, 2, adm.requests, "a refusal is one request, not a poll")
}

// acquireAsync runs g.acquire on its own goroutine, so a test can tell a wait from an
// answer.
func acquireAsync(ctx context.Context, g *machineGate, c budget.Claim) <-chan error {
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
	b := testBudget(t, 10_000, 8)
	g, _ := testGate(t, b)

	release, err := g.acquire(t.Context(), budget.Claim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: 100, Invocation: "100:inv-a",
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := acquireAsync(ctx, g, budget.Claim{
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
	b := testBudget(t, 10_000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), budget.Claim{
		Project: ".", Target: "ci", MemoryMB: 1000, PID: 100, Invocation: root,
	})
	require.NoError(t, err, "the parent step")
	releaseFirst, err := g.acquire(t.Context(), budget.Claim{
		Project: "libs/shared", Target: "build", MemoryMB: 9000, PID: 200,
		Invocation: "200:inv-child-a", Ancestors: []string{root},
	})
	require.NoError(t, err, "the first child is excused from its parent's claim")

	_, err = g.acquire(t.Context(), budget.Claim{
		Project: "x", Target: "build", MemoryMB: 9000, PID: 400, Invocation: "400:inv-stranger",
	})
	require.Error(t, err, "under budget_wait 0 a stranger is refused, not queued")
	assert.True(t, errors.Is(err, types.MachineBudgetExhausted))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := acquireAsync(ctx, g, budget.Claim{
		Project: "libs/shared", Target: "test", MemoryMB: 9000, PID: 300,
		Invocation: "300:inv-child-b", Ancestors: []string{root},
	})
	requireWaits(t, done, releaseFirst)
}

// TestMachineGateStopsWaitingWithItsContext pins the bound on the own-run wait: the
// caller's context, and nothing left on the budget once it ends.
func TestMachineGateStopsWaitingWithItsContext(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "0")
	b := testBudget(t, 10_000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), budget.Claim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	_, err = g.acquire(ctx, budget.Claim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 100})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Len(t, b.Snapshot().Holders, 1, "a wait that ended holds nothing")
}

// TestRunAllReportsAMachineRefusal pins that a step the budget refuses is put on the
// record a failed step is: the refusal happens before Run, whose fail is what logs a
// failure and fires the result observers, so skipping that left `magus run` exiting 75
// with nothing printed after the header.
func TestRunAllReportsAMachineRefusal(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	b := testBudget(t, 10_000, 8)
	held := b.Request(budget.Claim{
		Project: ".", Target: "test", MemoryMB: 9000, PID: 100, Dir: "/elsewhere/checkout",
	}, 0)
	require.True(t, held.Granted, "the fixture's holder must own the budget")

	var out bytes.Buffer
	c, err := Open(t.Context(), t.TempDir(),
		WithLogger(slog.New(prettyHandler(&out, slog.LevelInfo))),
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
	b := testBudget(t, 10_000, 8)
	held := b.Request(budget.Claim{
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

	b := testBudget(t, 10_000, 8)
	held := b.Request(budget.Claim{
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
	b := testBudget(t, 4000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), budget.Claim{
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
	c := budget.Claim{Target: "test", MemoryMB: 5880, Slots: 1,
		Sizing: budget.Sizing{Samples: 53, AnyShape: true}}
	assert.Equal(t, "it claims 5.7 GiB (measured over 53 runs of any shape) and takes 1 slot",
		describeMachineDeclaration(c))
}

func TestMachineRefusalExplainsTheGapAndTheDeclarationCheck(t *testing.T) {
	b := testBudget(t, 4000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), budget.Claim{
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
	b := testBudget(t, 32_000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), budget.Claim{
		Project: ".", Target: "test", MemoryMB: 1000, Slots: 32, PID: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "8 slots in total")
	assert.NotContains(t, err.Error(), "MGS1030", "a core count is not a declaration MGS1030 measures")
	assert.NotContains(t, err.Error(), "75%", "and the memory share is not why this was refused")
}

func TestMachineGateAdmitsWhenTheArbiterIsGone(t *testing.T) {
	b := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)
	adm.fail = errors.New("dial: connection refused")

	release, err := g.acquire(t.Context(), budget.Claim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err, "losing the arbiter must not fail a build that was going to run")
	require.NotNil(t, release)
	release()
}

// TestMachineGateRequiredRefusesWhenTheArbiterIsGone is broker: required. The step does
// not start, and the refusal exits 69 rather than 75: no arbiter is not a busy host.
func TestMachineGateRequiredRefusesWhenTheArbiterIsGone(t *testing.T) {
	b := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)
	g.required = true
	adm.fail = errors.New("dial: connection refused")

	release, err := g.acquire(t.Context(), budget.Claim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
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
	b := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)
	var logged bytes.Buffer
	g.log = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	adm.fail = types.WrapDiagnostic(types.PeerFromAnotherBuild, errors.New("broker: expected a hello speaking protocol 2"), "hello")

	release, err := g.acquire(t.Context(), budget.Claim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err, "best-effort runs on without the broker")
	release()
	assert.Contains(t, logged.String(), string(types.PeerFromAnotherBuild))
	assert.Contains(t, logged.String(), "a broker from another magus build answered")
	assert.NotContains(t, logged.String(), "no broker answered")
}

func TestMachineGateRequiredRefusesABrokerFromAnotherBuild(t *testing.T) {
	b := testBudget(t, 10_000, 8)
	g, adm := testGate(t, b)
	g.required = true
	adm.fail = types.WrapDiagnostic(types.PeerFromAnotherBuild, errors.New("broker: expected a hello speaking protocol 2"), "hello")

	_, err := g.acquire(t.Context(), budget.Claim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.ErrorIs(t, err, types.PeerFromAnotherBuild)
	var stated interface{ ExitCode() int }
	require.ErrorAs(t, err, &stated)
	assert.Equal(t, ExitCodeBrokerUnavailable, stated.ExitCode())
}

func TestMachineGateIsInertWithoutAnAdmitter(t *testing.T) {
	var g *machineGate
	release, err := g.acquire(t.Context(), budget.Claim{Project: ".", Target: "test", MemoryMB: 9000})
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
	b := testBudget(t, 10_000, 8)
	g, _ := testGate(t, b)

	held, err := g.acquire(t.Context(), budget.Claim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)
	defer held()

	// Bounded, so a regression that admits this claim (instead of refusing it) fails in a
	// second rather than hanging the package until the go test timeout.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	// No Ancestors on the claim, none on the context, none in the environment.
	_, err = g.acquire(ctx, budget.Claim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
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

	b := testBudget(t, 10_000, 8)
	parent := b.Request(budget.Claim{
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
	release, err := g.acquire(stamped, budget.Claim{
		Project: "svc-a", Target: "alpha", MemoryMB: 500, Slots: 1, PID: 4000,
		Ancestors: ancestorInvocations(stamped),
	})
	require.NoError(t, err, "an in-process run must be excused from the claim its own parent holds")
	require.NotNil(t, release)
	release()
}

func TestDescribeMachineHoldersBoundsTheList(t *testing.T) {
	holders := make([]budget.Claimant, 0, 6)
	for i := range 6 {
		holders = append(holders, budget.Claimant{Project: ".", Target: "test", PID: 100 + i})
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
	b := testBudget(t, 10_000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), budget.Claim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	require.NoError(t, err)
	_, busy := g.acquire(t.Context(), budget.Claim{Project: "docs", Target: "ci", MemoryMB: 9000, PID: 200})
	require.Error(t, busy)

	_, tooBig := g.acquire(t.Context(), budget.Claim{Project: ".", Target: "ci", MemoryMB: 64_000, PID: 300})
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
