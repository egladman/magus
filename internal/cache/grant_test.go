package cache

import (
	"context"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunAllSlotsThrottles verifies that a step whose Slots equals the whole
// slot budget runs alone: while it holds every slot no other step can be in fn,
// yet lighter steps still saturate the budget when the heavy one is idle.
//
// Two light steps in fn together is what releases the rendezvous, so a limiter that
// permits saturation always demonstrates it. Sleeping and hoping two overlapped failed a
// correct limiter on a loaded runner.
//
// Each light step checks the heavy step's in-flight flag rather than sampling a running
// count, which a light step that started and finished inside the sample window hid.
func TestRunAllSlotsThrottles(t *testing.T) {
	root, c := openCache(t)

	var running atomic.Int32
	var lightPeak atomic.Int32       // peak concurrency observed among the light steps
	var heavyInFlight atomic.Bool    // the heavy step is inside fn right now
	var overlappedHeavy atomic.Bool  // some light step was in fn while it was
	saturated := make(chan struct{}) // closed once two light steps are in fn together
	var closeOnce sync.Once

	heavy := depStep(root, "heavy")
	heavy.NoCache = true
	heavy.Slots = 2 // == the WithLimiter budget below, so it holds every slot

	steps := []Step{heavy}
	for _, p := range []string{"l1", "l2", "l3"} {
		s := depStep(root, p)
		s.NoCache = true
		steps = append(steps, s)
	}

	fn := func(_ context.Context, s Step) error {
		cur := running.Add(1)
		defer running.Add(-1)
		if s.ProjectPath == "heavy" {
			heavyInFlight.Store(true)
			defer heavyInFlight.Store(false)
			time.Sleep(30 * time.Millisecond)
			return nil
		}
		if heavyInFlight.Load() {
			overlappedHeavy.Store(true)
		}
		atomicStoreMax(&lightPeak, cur)
		if cur >= 2 {
			closeOnce.Do(func() { close(saturated) })
		}
		select {
		case <-saturated:
		case <-time.After(saturationTimeout):
		}
		return nil
	}

	_, err := c.RunAll(context.Background(), steps, fn, WithLimiter(NewLimiter(2)))
	require.NoError(t, err, "RunAll")

	assert.False(t, overlappedHeavy.Load(), "no step may run while a slots==budget step holds every slot")
	assert.Equal(t, int32(2), lightPeak.Load(), "light steps should saturate the 2-slot budget when the heavy step is idle")
}

// TestRunAllSlotsHandbackNoDeadlock guards against a multi-slot step self-deadlocking
// when its fn hands back its build slot to reserve internally-parallel slots
// (the os.with_slots / archive.* pattern). A step holding N slots must hand back
// *all* N, not one, or an AcquireN inside fn blocks forever on slots the step
// itself is pinning.
func TestRunAllSlotsHandbackNoDeadlock(t *testing.T) {
	root, c := openCache(t)

	heavy := depStep(root, "heavy")
	heavy.NoCache = true
	heavy.Slots = 2 // holds the whole 2-slot budget

	fn := func(ctx context.Context, _ Step) error {
		lim := LimiterFromContext(ctx)
		if lim == nil {
			return nil
		}
		// Mirror OsWithSlots: hand back the slots we hold, then reserve `threads`.
		// With the whole budget held, reserving 2 can only succeed if the handback
		// released both of our slots.
		held := SlotsHeld(ctx)
		if held > 0 {
			lim.ReleaseN(held)
			defer func() { _ = lim.AcquireN(context.WithoutCancel(ctx), held) }()
		}
		if err := lim.AcquireN(ctx, 2); err != nil {
			return err
		}
		defer lim.ReleaseN(2)
		return nil
	}

	done := make(chan error, 1)
	go func() {
		_, err := c.RunAll(context.Background(), []Step{heavy}, fn, WithLimiter(NewLimiter(2)))
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err, "RunAll")
	case <-time.After(5 * time.Second):
		t.Fatal("weighted step deadlocked handing back its slot to reserve more")
	}
}

// TestRunAllSeatsAJobserverOnlyForAMultiSlotStep drives the whole seat: a step granted
// three slots hands its process a live pipe holding two tokens, and a step that
// declared nothing leaves MAKEFLAGS as it found it.
func TestRunAllSeatsAJobserverOnlyForAMultiSlotStep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows has no pipe jobserver: exec.Cmd cannot hand a child extra descriptors there")
	}
	root, c := openCache(t)
	steps := []Step{
		{ProjectPath: "wide", Target: "build", WorkspaceRoot: root, Slots: 3},
		{ProjectPath: "narrow", Target: "build", WorkspaceRoot: root},
	}
	var mu sync.Mutex
	got := map[string][2]string{}
	_, err := c.RunAll(context.Background(), steps, func(ctx context.Context, s Step) error {
		flags, err := spawn.Exec(ctx, "sh", []string{"-c", `printf %s "$CARGO_MAKEFLAGS"`}, spawn.ExecOptions{Capture: true, Quiet: true})
		if err != nil {
			return err
		}
		var tokens spawn.ExecResult
		if s.Slots > 1 {
			if tokens, err = spawn.Exec(ctx, "sh", []string{"-c", "head -c 2 <&3"}, spawn.ExecOptions{Capture: true, Quiet: true}); err != nil {
				return err
			}
		}
		mu.Lock()
		got[s.ProjectPath] = [2]string{flags.Stdout, tokens.Stdout}
		mu.Unlock()
		return nil
	}, WithLimiter(NewLimiter(4)))
	require.NoError(t, err)

	assert.Equal(t, map[string][2]string{
		"wide":   {"-j --jobserver-fds=3,4 --jobserver-auth=3,4", "++"},
		"narrow": {os.Getenv("CARGO_MAKEFLAGS"), ""},
	}, got)
}

// An elastic step granted fewer slots than it asked for runs that narrow:
// its limiter seat, the Go toolchain's width and the slots its outcome records.
func TestGrantedSlotsReachGOMAXPROCSAndTheRecordedWidth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the probe reads the child's environment through sh")
	}
	t.Setenv("MAGUS_LEVEL", "")
	b, _ := testBudget(t, 0, 10)
	require.True(t, b.Request(types.MachineClaim{Project: "other", Target: "test", Slots: 7, PID: os.Getpid() + 1}, 0).Granted)
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}, 0))
	require.NoError(t, err)

	var held int
	var procs string
	step := Step{ProjectPath: ".", Target: "test", WorkspaceRoot: t.TempDir(), Slots: 8, SlotsFloor: 1, NoCache: true}
	_, err = c.RunAll(t.Context(), []Step{step}, func(ctx context.Context, _ Step) error {
		held = SlotsHeld(ctx)
		out, err := spawn.Exec(ctx, "sh", []string{"-c", `printf %s "$GOMAXPROCS"`}, spawn.ExecOptions{Capture: true, Quiet: true})
		procs = out.Stdout
		return err
	}, WithLimiter(NewLimiter(8)))
	require.NoError(t, err)

	assert.Equal(t, [2]any{3, "3"}, [2]any{held, procs}, "the three slots the budget had free")
}

// A step the budget seated at full width keeps it.
func TestGrantedSlotsOfAFixedStepAreItsWidth(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	b, _ := testBudget(t, 0, 10)
	c, err := Open(t.Context(), t.TempDir(), WithMachineAdmission(&budgetAdmitter{budget: b}, 0))
	require.NoError(t, err)

	var held int
	step := Step{ProjectPath: ".", Target: "scip", WorkspaceRoot: t.TempDir(), Slots: 4, NoCache: true}
	_, err = c.RunAll(t.Context(), []Step{step}, func(ctx context.Context, _ Step) error {
		held = SlotsHeld(ctx)
		return nil
	}, WithLimiter(NewLimiter(8)))
	require.NoError(t, err)
	assert.Equal(t, 4, held)
}
