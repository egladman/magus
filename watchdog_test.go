package magus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
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
	assert.Same(t, err, context.Cause(ctx), "the same diagnostic is the context's cause")
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
