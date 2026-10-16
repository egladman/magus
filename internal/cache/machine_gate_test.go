package cache

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/types"
)

func snapshotOf(b *MachineBudget) func(context.Context) (types.MachineSnapshot, error) {
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
	err := AwaitMachine(t.Context(), snapshotOf(b), []types.MachineClaim{{Project: ".", Target: "test", MemoryMB: 6000}},
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
		done <- AwaitMachine(t.Context(), snapshotOf(b), []types.MachineClaim{{Project: ".", Target: "test", MemoryMB: 6000}},
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
	require.NoError(t, AwaitMachine(t.Context(), snapshotOf(b), claims, nil))
}

func TestMachineGateAwaitRefusesWhatCanNeverFit(t *testing.T) {
	b, _ := testBudget(t, 10_000, 8)
	err := AwaitMachine(t.Context(), snapshotOf(b), []types.MachineClaim{{Project: ".", Target: "test", MemoryMB: 64_000}}, nil)
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
	err := AwaitMachine(ctx, snapshotOf(b), []types.MachineClaim{{Slots: 1}}, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// refusingAdmitter answers every request with err.
type refusingAdmitter struct{ err error }

func (a refusingAdmitter) Request(context.Context, types.MachineClaim) (types.MachineVerdict, error) {
	return types.MachineVerdict{}, a.err
}

func (refusingAdmitter) Release(context.Context, string) {}

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

func (a *groupAdmitter) ReleaseHold(_ context.Context, id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, "release "+id)
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
