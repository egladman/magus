package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	"github.com/egladman/magus/internal/proc/spawn"
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
	b, _ := testBudget(s.T(), 10_000, 8)
	return testGate(s.T(), b)
}

func (s *MemoryPressureSuite) TestABadReadingRefusesBeforeTheBudgetIsAsked() {
	s.reading(badMemory, nil)
	g, adm := s.gate()
	_, err := g.acquire(s.T().Context(), types.MachineClaim{Project: "docs", Target: "test", PID: 100})
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
	_, err := g.acquire(s.T().Context(), types.MachineClaim{Target: "test"})
	s.NotContains(err.Error(), "--wait")
}

func (s *MemoryPressureSuite) TestARefusalToALeasedWorkerNamesTheOwedFiling() {
	s.T().Setenv("XDG_STATE_HOME", s.T().TempDir())
	s.reading(badMemory, nil)
	g, _ := s.gate()
	_, err := g.acquire(types.WithJobLease(s.T().Context(), "wave/api"), types.MachineClaim{Target: "test"})
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
	b, _ := testBudget(s.T(), 10_000, 8)
	adm := &turningAdmitter{MachineAdmitter: &budgetAdmitter{budget: b}, turn: func() { s.reading(badMemory, nil) }}
	g := &machineGate{admit: adm, log: newLogger("", 0)}
	_, err := g.acquire(s.T().Context(), types.MachineClaim{Project: ".", Target: "ci", PID: 100})
	s.Require().ErrorIs(err, types.MemoryPressureHigh)
	s.Empty(b.Snapshot().Holders, "the seat went back")
}

func (s *MemoryPressureSuite) TestANestedRunPassesUnderItsRunningParent() {
	s.reading(badMemory, nil)
	g, _ := s.gate()
	release, err := g.acquire(s.T().Context(), types.MachineClaim{Target: "test", PID: 100, Ancestors: []string{"1:parent"}})
	s.Require().NoError(err)
	release()
	s.Zero(s.reads)
}

var underWrapper = "1:" + types.WrapperInvocation("exec")

func (s *MemoryPressureSuite) TestARunUnderOnlyAWrapperIsRefused() {
	s.reading(badMemory, nil)
	g, adm := s.gate()
	_, err := g.acquire(s.T().Context(), types.MachineClaim{Target: "test", PID: 100, Ancestors: []string{underWrapper}})
	s.Require().ErrorIs(err, types.MemoryPressureHigh)
	s.Zero(adm.requests, "no running step sits above it")
}

func (s *MemoryPressureSuite) TestARunUnderAStepUnderAWrapperPasses() {
	s.reading(badMemory, nil)
	g, _ := s.gate()
	release, err := g.acquire(s.T().Context(), types.MachineClaim{Target: "test", PID: 100, Ancestors: []string{underWrapper, "2:parent"}})
	s.Require().NoError(err)
	release()
	s.Zero(s.reads)
}

func (s *MemoryPressureSuite) TestARunUnderAWrapperUnderAStepPasses() {
	s.T().Setenv("MAGUS_LEVEL", "1")
	s.reading(badMemory, nil)
	g, _ := s.gate()
	release, err := g.acquire(s.T().Context(), types.MachineClaim{Target: "test", PID: 100, Ancestors: []string{underWrapper}})
	s.Require().NoError(err)
	release()
}

func (s *MemoryPressureSuite) TestANestedRunThatLostItsAncestryPassesToo() {
	s.T().Setenv("MAGUS_LEVEL", "1")
	s.reading(badMemory, nil)
	g, _ := s.gate()
	release, err := g.acquire(s.T().Context(), types.MachineClaim{Target: "test", PID: 100})
	s.Require().NoError(err)
	release()
}

func (s *MemoryPressureSuite) TestAPartialReadingSaysSoOnceAndStillRefuses() {
	s.reading(badMemory, errors.New("compressor segments are unread"))
	g, _ := s.gate()
	var logged bytes.Buffer
	g.log = slog.New(slog.NewTextHandler(&logged, nil))
	for range 2 {
		_, err := g.acquire(s.T().Context(), types.MachineClaim{Target: "test", PID: 100})
		s.Require().ErrorIs(err, types.MemoryPressureHigh)
	}
	s.Equal(1, strings.Count(logged.String(), "compressor segments are unread"))
}

func (s *MemoryPressureSuite) TestATestGateReadsTheStandIn() {
	before := standInReads.Load()
	g, _ := s.gate()
	release, err := g.acquire(s.T().Context(), types.MachineClaim{Target: "test", PID: 100})
	s.Require().NoError(err)
	release()
	s.Greater(standInReads.Load(), before)
}

func (s *MemoryPressureSuite) TestAHealthyReadingSeatsTheClaim() {
	s.reading("", nil)
	g, adm := s.gate()
	release, err := g.acquire(s.T().Context(), types.MachineClaim{Target: "test", PID: 100})
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
		release, err := g.acquire(s.T().Context(), types.MachineClaim{Target: "test", PID: 100})
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
	claim := types.MachineClaim{Target: "ci", PID: 100}
	id, release, err := g.claim(s.T().Context(), claim)
	s.Require().NoError(err)
	h := &machineHold{gate: g, claim: claim, id: id, release: release}
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
	_, err := AdmitMachine(s.T().Context(), adm, types.MachineClaim{Project: ".", Target: "session-brief"}, false, 0)
	s.Require().ErrorIs(err, types.MemoryPressureHigh)
}

func (s *MemoryPressureSuite) TestNoAdmitterReadsNoMemory() {
	s.reading(badMemory, nil)
	release, err := AdmitMachine(s.T().Context(), nil, types.MachineClaim{Target: "test"}, false, 0)
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

func (a *turningAdmitter) Claim(ctx context.Context, c types.MachineClaim, parked func(types.MachineVerdict)) (types.MachineVerdict, error) {
	a.turn()
	return a.MachineAdmitter.Claim(ctx, c, parked)
}

func TestMachineRefusalNamesTheOversizedClaimAndThatNoWaitSeatsIt(t *testing.T) {
	b, _ := testBudget(t, 4000, 8)
	g, _ := testGate(t, b)

	_, err := g.acquire(t.Context(), types.MachineClaim{
		Project: ".", Target: "ci", DeclaredBy: "test", MemoryMB: 26_000, Slots: 1, PID: 100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no wait seats it")
	assert.Contains(t, err.Error(), "25.4 GiB against a budget of 3.9 GiB", "which claim, against which budget")
	assert.Contains(t, err.Error(), "Narrow the run")
}

// refusingAdmitter answers every request with err.
type refusingAdmitter struct{ err error }

func (a refusingAdmitter) Claim(context.Context, types.MachineClaim, func(types.MachineVerdict)) (types.MachineVerdict, error) {
	return types.MachineVerdict{}, a.err
}

func (a refusingAdmitter) Request(context.Context, types.MachineClaim) (types.MachineVerdict, error) {
	return types.MachineVerdict{}, a.err
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
	hold := &machineHold{claim: types.MachineClaim{Project: "libs/xdg", Target: "go-test"}, id: "claim-1"}
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
	hold := &machineHold{claim: types.MachineClaim{Project: "libs/xdg", Target: "go-test"}, id: "claim-1", release: func() {}}
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
	b, _ := testBudget(t, 10_000, 8)
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
	_, err := g.acquire(t.Context(), types.MachineClaim{Target: "test"})
	require.ErrorIs(t, err, refusal.Err)

	g = &machineGate{admit: refusingAdmitter{err: errors.New("dial: no such file")}, log: newLogger("", 0)}
	release, err := g.acquire(t.Context(), types.MachineClaim{Target: "test"})
	require.NoError(t, err, "an unreachable broker still admits under best-effort")
	release()
}

func TestVerdictSaysItPackedByCurve(t *testing.T) {
	b, now := testBudget(t, 10_000, 8)
	require.True(t, b.Request(curveClaim(100, 7000, 1000, 50_000)).Granted)
	c := curveClaim(200, 1000, 7000, 50_000)
	v := b.Request(c)
	require.False(t, v.Granted)

	assert.Contains(t, machineBusyError(c, v, *now, "", 0, waitBound{}).Error(),
		"The broker packed by curve: the claims pass the 9.8 GiB budget together at 12:00:25, 25s from now.")

	old := v
	old.Packed = false
	assert.Contains(t, machineBusyError(c, old, *now, "", 0, waitBound{}).Error(),
		"The broker packed by peak, predating memory curves: this claimed the 6.8 GiB peak, which passes the budget now.",
		"a broker that predates curves answers Packed false")

	flat := types.MachineClaim{Project: ".", Target: "lint", MemoryMB: 7000, PID: 200}
	assert.Contains(t, machineBusyError(flat, types.MachineVerdict{BudgetMB: 10_000, HeldMB: 7000}, *now, "", 0, waitBound{}).Error(),
		"The broker packed by peak: this claimed the 6.8 GiB peak, which passes the budget now.")
}

// A step's result carries the claim it ran under, what it cost, and how long it queued:
// the budget seats one step at a time, so the second waits out the first.
func TestAStepResultCarriesItsClaimAndItsWait(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	b, _ := testBudget(t, 10_000, 1)
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
	b, _ := testBudget(t, 10_000, 8)
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

// TestMembersWaitingOnASharedNeedLendTheirSeats is the 2026-10-21 `vcs resolve` hang:
// generate's members alone fill every slot, and each needs types-generate, which
// generate dispatched too and which needs a seat of its own. Each member lends its claim
// while it waits, so the shared need runs and every member completes.
func TestMembersWaitingOnASharedNeedLendTheirSeats(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	const members = 4
	b, _ := testBudget(t, 10_000, members)
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
	b      *MachineBudget
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
	s.b, _ = testBudget(s.T(), 10_000, 8)
	s.adm = &budgetAdmitter{budget: s.b}
	s.logged = &lockedLog{}
	s.gate = &machineGate{admit: s.adm, wait: time.Minute, log: slog.New(slog.NewTextHandler(s.logged, nil))}
	v := s.b.Request(types.MachineClaim{Project: ".", Target: "test", MemoryMB: 9000, PID: 100})
	s.Require().True(v.Granted)
	s.holder = v.ID
}

// stranger is pid 200's claim, which the holder keeps out.
var stranger = types.MachineClaim{Project: "docs", Target: "ci", MemoryMB: 5000, PID: 200}

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

	s.Require().True(s.b.Request(types.MachineClaim{Project: "web", Target: "lint", MemoryMB: 500, PID: 300}).Granted)
	s.adm.wake()
	s.Eventually(func() bool { return s.said("still waiting for a seat for docs ci") }, 5*time.Second, 5*time.Millisecond)
	s.True(s.said("pid 300 web lint"))
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
	parent := s.b.Request(types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 500, PID: 400, Invocation: "400:root"})
	s.Require().True(parent.Granted)
	release, err := s.gate.acquire(s.T().Context(), types.MachineClaim{
		Project: ".", Target: "go-test", MemoryMB: 4000, PID: 401, Invocation: "401:child", Ancestors: []string{"400:root"},
	})

	s.Require().NoError(err)
	release()
	s.False(s.said("waiting for a seat"), "its stalled parent's free seat takes it")
}

func (s *BudgetWaitSuite) TestAComposersRetakeWaitsInsteadOfFailing() {
	s.adm.Release(s.T().Context(), s.holder)
	claim := types.MachineClaim{Project: ".", Target: "ci", MemoryMB: 6000, PID: 500}
	id, release, err := s.gate.claim(s.T().Context(), claim)
	s.Require().NoError(err)
	h := &machineHold{gate: s.gate, claim: claim, id: id, release: release}
	h.lend()
	member := s.b.Request(types.MachineClaim{Project: "x", Target: "build", MemoryMB: 9000, PID: 600})
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
	_, err := s.gate.acquire(types.WithJobLease(s.T().Context(), "wave/api"), stranger)

	s.Require().ErrorIs(err, types.MachineBudgetExhausted)
	s.Contains(err.Error(), fmt.Sprintf(
		"Job wave/api got no seat: file this check as owed, not as a ref, with `%s --stdin` and owed %q on its gate evidence",
		hint.JobExit.With("wave/api"), types.OwedNoSeat))
}

// roomAtTheBound parks every claim and seats the one unparked ask.
type roomAtTheBound struct{ *budgetAdmitter }

func (a *roomAtTheBound) Claim(ctx context.Context, _ types.MachineClaim, parked func(types.MachineVerdict)) (types.MachineVerdict, error) {
	parked(types.MachineVerdict{Fits: true, Holders: []types.MachineClaimant{{Project: ".", Target: "test", PID: 100}}})
	<-ctx.Done()
	return types.MachineVerdict{}, ctx.Err()
}

func (a *roomAtTheBound) Request(context.Context, types.MachineClaim) (types.MachineVerdict, error) {
	return types.MachineVerdict{Granted: true, Fits: true, ID: "at-the-bound"}, nil
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
	wctx, w := g.newBudgetWait(s.T().Context(), types.MachineClaim{Project: ".", Target: "test"})
	s.T().Cleanup(w.stop)
	return wctx, w
}

var (
	strangerHolds = types.MachineVerdict{Fits: true, Holders: []types.MachineClaimant{{PID: 7, Project: ".", Target: "lint"}}}
	ownRunHolds   = types.MachineVerdict{Fits: true, OwnRun: true}
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
