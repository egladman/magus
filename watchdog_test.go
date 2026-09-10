package magus

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file/record"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stalledProgress is a heartbeat that recorded one step and then went quiet, which is the
// shape of the 2026-09-04 wedge: a target started, its log was opened, and nothing ever
// beat again while the run held every project lock.
func stalledProgress() *cache.Progress {
	p := cache.NewProgress()
	p.Record(cache.Mark{
		Project: "docs", Target: "graph-generate", What: "executing",
		Log: "/tmp/.magus/logs/docs/abc123.log",
	})
	return p
}

// TestStallWatchdogAbortsAQuietInvocation fabricates a stall and pins what the abort
// says: the code, the last step that ran, how long it has been quiet, and the log path a
// reader can open. Without those three fields the diagnostic is just another cancellation.
func TestStallWatchdogAbortsAQuietInvocation(t *testing.T) {
	m := &Magus{cfg: config.Config{StallTimeout: 60 * time.Millisecond}}
	prog := stalledProgress()

	ctx, stall := m.watchForStall(t.Context(), prog, nil)
	defer stall.close()

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the watchdog never fired on a heartbeat that stopped beating")
	}

	err := stall.verdict(ctx.Err())
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.InvocationStalled), "the abort carries MGS3012, got %v", err)
	assert.Contains(t, err.Error(), "docs:graph-generate (executing)", "it names the last step that ran")
	assert.Contains(t, err.Error(), "/tmp/.magus/logs/docs/abc123.log", "it names the captured log")
	assert.Contains(t, err.Error(), "stall window: 60ms", "it names the window it measured against")
	assert.Contains(t, err.Error(), "still admitted:", "it says what else was in flight, or that nothing was")
	assert.ErrorIs(t, err, context.Cause(ctx), "the same diagnostic is the context's cause")
	var stated interface{ ExitCode() int }
	require.ErrorAs(t, err, &stated)
	assert.Equal(t, stallExit, stated.ExitCode(), "a stall is a failure and exits like one")
}

// TestStallWatchdogLeavesAProgressingRunAlone is the test that matters. A watchdog that
// fires on healthy work is worse than none: it would abort every legitimately long target
// and teach everyone to turn it off. A run that is slow but talking must survive many
// windows untouched.
func TestStallWatchdogLeavesAProgressingRunAlone(t *testing.T) {
	const window = 500 * time.Millisecond
	m := &Magus{cfg: config.Config{StallTimeout: window}}
	prog := stalledProgress()

	ctx, stall := m.watchForStall(t.Context(), prog, nil)
	defer stall.close()

	// Three windows of a target that never finishes and never starts another, producing
	// only output lines. That is the single-long-target shape a naive watchdog kills.
	deadline := time.Now().Add(3 * window)
	for time.Now().Before(deadline) {
		prog.Beat()
		time.Sleep(window / 20)
		if ctx.Err() != nil {
			break
		}
	}

	assert.NoError(t, ctx.Err(), "a beating heartbeat is not a stall")
	assert.NoError(t, stall.verdict(nil), "and nothing is reported")
}

// TestStallWatchdogOffDoesNotFire is the fails-without-the-fix proof for the test above
// it: the SAME fabricated stall, with the watchdog turned off, is not caught. Every
// assertion in TestStallWatchdogAbortsAQuietInvocation depends on the watchdog running,
// and this is what shows it.
func TestStallWatchdogOffDoesNotFire(t *testing.T) {
	m := &Magus{cfg: config.Config{StallTimeout: -1}}
	prog := stalledProgress()

	ctx, stall := m.watchForStall(t.Context(), prog, nil)
	defer stall.close()

	time.Sleep(300 * time.Millisecond) // five times the window the armed test trips on
	assert.NoError(t, ctx.Err(), "a disabled watchdog cancels nothing")
	assert.NoError(t, stall.verdict(nil), "and reports nothing")
}

// TestStallWatchdogDefaultsToOn keeps the net universal: an unset stall_timeout must arm
// the built-in window, not disable the watchdog the way an unset target_timeout does.
func TestStallWatchdogDefaultsToOn(t *testing.T) {
	m := &Magus{}
	ctx, stall := m.watchForStall(t.Context(), cache.NewProgress(), nil)
	defer stall.close()
	assert.NoError(t, ctx.Err())
	assert.NotEqual(t, t.Context(), ctx, "an armed watchdog hands back its own cancellable context")
	assert.Equal(t, time.Minute, stallPollInterval(defaultStallTimeout), "a long window is polled at the ceiling")
}

// TestStallVerdictKeepsAnOrdinaryError pins that the swap is not a blanket override: a run
// that failed on its own account must keep reporting its own failure.
func TestStallVerdictKeepsAnOrdinaryError(t *testing.T) {
	m := &Magus{cfg: config.Config{StallTimeout: time.Hour}}
	_, stall := m.watchForStall(t.Context(), cache.NewProgress(), nil)
	defer stall.close()

	own := errors.New("lint failed")
	assert.Same(t, own, stall.verdict(own))
}

// The abort has to FREE the locks, not merely ask the run to unwind. Cancelling alone
// returns them when executeStages returns, so a stall that does not answer its context
// holds every project lock for good while the terminal reports it aborted.
func TestStallWatchdogReleasesTheLocksItAborts(t *testing.T) {
	m := &Magus{cfg: config.Config{StallTimeout: 60 * time.Millisecond}}
	released := make(chan struct{})
	var once sync.Once

	ctx, stall := m.watchForStall(t.Context(), stalledProgress(), func() {
		once.Do(func() { close(released) })
	})
	defer stall.close()

	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the watchdog aborted the run without releasing its project locks")
	}
	assert.Error(t, ctx.Err(), "the release happens on the abort path, so the run is already cancelled")
}

// A run that finished on its own must not have its successor's locks freed by a late
// tick, which is what a poller acting on a tick that raced its own stop would do.
func TestStallWatchdogDoesNotReleaseAfterClose(t *testing.T) {
	m := &Magus{cfg: config.Config{StallTimeout: 20 * time.Millisecond}}
	var releases atomic.Int32

	_, stall := m.watchForStall(t.Context(), cache.NewProgress(), func() { releases.Add(1) })
	stall.close()
	time.Sleep(80 * time.Millisecond)

	assert.Zero(t, releases.Load(), "a stopped watchdog must never release")
}

// gateHold builds a hold over one locked project, the shape watchForSupersede polls.
func gateHold(t *testing.T, project string, opts ...lockerOption) *projectHold {
	t.Helper()
	l := newProjectLocker(t.TempDir(), testWorkspaceRoot, false, opts...)
	l.started = time.Now().Add(-time.Minute)
	unlock, err := l.acquire(t.Context(), project)
	require.NoError(t, err)
	return &projectHold{unlock: unlock, locker: l, paths: []string{project}}
}

// deadPID is the pid of a process that has already exited: a real pid the kernel has
// reaped, which is what a marker left by a killed gate carries.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	require.NoError(t, cmd.Run())
	return cmd.Process.Pid
}

// requestYieldFrom writes the request a later gate on the same tree would leave.
func requestYieldFrom(t *testing.T, hold *projectHold, project string) {
	t.Helper()
	later := newProjectLocker("", testWorkspaceRoot, false, asGate())
	require.NoError(t, record.Write(hold.locker.yieldPath(project), later.selfRecord(t.Context(), time.Now())))
}

// TestSupersedeWatchCancelsAndKeepsTheLocks pins what the watch does when the request
// arrives: it cancels with MGS3014 as the cause, and nothing more. The locks stay with
// the run until its own unwind releases them, since a successor taking them while this
// run's subprocesses are still being torn down would have two gates on one tree.
func TestSupersedeWatchCancelsAndKeepsTheLocks(t *testing.T) {
	quickSupersede(t, 5*time.Second)
	hold := gateHold(t, "app", asGate())
	defer hold.release()

	ctx, watch := (&Magus{}).watchForSupersede(t.Context(), hold)
	defer watch.close()
	requestYieldFrom(t, hold, "app")

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the watch never cancelled the run")
	}
	assert.ErrorIs(t, context.Cause(ctx), types.GateSuperseded, "the cause is what tells the run apart from a Ctrl-C")
	assert.ErrorIs(t, watch.verdict(nil), types.GateSuperseded)
	var stated interface{ ExitCode() int }
	require.ErrorAs(t, watch.verdict(nil), &stated)
	assert.Equal(t, supersedeExit, stated.ExitCode())

	other := newProjectLocker("", testWorkspaceRoot, true)
	other.dir = hold.locker.dir
	_, err := other.acquire(t.Context(), "app")
	require.Error(t, err, "the lock is still held: the watch cancelled and released nothing")
}

// TestSupersedeWatchIgnoresANonGate is the containment: only a gate is superseded, so an
// ordinary run holding the same lock must never be aborted by a marker beside it.
func TestSupersedeWatchIgnoresANonGate(t *testing.T) {
	quickSupersede(t, 5*time.Second)
	hold := gateHold(t, "app")
	defer hold.release()

	ctx, watch := (&Magus{}).watchForSupersede(t.Context(), hold)
	defer watch.close()
	requestYieldFrom(t, hold, "app")
	time.Sleep(100 * time.Millisecond)

	assert.NoError(t, ctx.Err(), "a non-gate holder keeps waiting behind it, exactly as before")
	assert.NoError(t, watch.verdict(nil))
}

// TestSupersedeWatchIgnoresADeadRequester: a later gate killed while parked leaves its
// marker behind, and the next gate that started before the marker would otherwise read it
// as live and stop for a successor that is not there.
func TestSupersedeWatchIgnoresADeadRequester(t *testing.T) {
	quickSupersede(t, 5*time.Second)
	hold := gateHold(t, "app", asGate())
	defer hold.release()

	later := newProjectLocker("", testWorkspaceRoot, false, asGate())
	rec := later.selfRecord(t.Context(), time.Now())
	rec.PID = deadPID(t)
	require.NoError(t, record.Write(hold.locker.yieldPath("app"), rec))

	ctx, watch := (&Magus{}).watchForSupersede(t.Context(), hold)
	defer watch.close()
	time.Sleep(100 * time.Millisecond)

	assert.NoError(t, ctx.Err(), "a request nobody is waiting on stops nothing")
	_, err := os.Stat(hold.locker.yieldPath("app"))
	assert.True(t, os.IsNotExist(err), "and is swept, so the next holder does not read it either")
}

// A run that finished on its own must not be cancelled by a tick that raced its own
// stop. Same hazard as the stall watchdog's, same guard.
func TestSupersedeWatchDoesNotCancelAfterClose(t *testing.T) {
	quickSupersede(t, 5*time.Second)
	hold := gateHold(t, "app", asGate())
	defer hold.release()

	ctx, watch := (&Magus{}).watchForSupersede(t.Context(), hold)
	watch.close()
	requestYieldFrom(t, hold, "app")
	time.Sleep(80 * time.Millisecond)

	assert.NoError(t, watch.verdict(nil), "a stopped watch must never trip")
	assert.ErrorIs(t, context.Cause(ctx), context.Canceled, "close cancels plainly, never with a verdict")
}
