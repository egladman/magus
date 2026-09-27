package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
