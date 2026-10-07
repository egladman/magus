package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// building is a process four minutes into one target.
func building() []cache.RunningTarget {
	return []cache.RunningTarget{{Project: ".", Target: "go-build", Started: time.Now().Add(-4 * time.Minute)}}
}

// idle is a process with nothing executing: a read, a follow stream, or a run
// between targets.
func idle() []cache.RunningTarget { return nil }

// runConfirm drives confirmInterrupts on its own goroutine, with a target in
// flight, and reports whether the context was cancelled within a short grace
// period.
func runConfirm(t *testing.T, interactive bool, window time.Duration, send ...os.Signal) (cancelled bool, out string) {
	t.Helper()
	cancelled, out, _ = runConfirmRecording(t, interactive, window, building, send...)
	return cancelled, out
}

// runConfirmRecording is runConfirm with the running set chosen, plus the
// recorded signal.
func runConfirmRecording(t *testing.T, interactive bool, window time.Duration, inFlight func() []cache.RunningTarget, send ...os.Signal) (cancelled bool, out string, recorded syscall.Signal) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sigs := make(chan os.Signal, len(send))
	var buf bytes.Buffer
	var got atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		confirmInterrupts(ctx, sigs, cancel, &buf, interactive, window, inFlight,
			func(s syscall.Signal) { got.Store(int32(s)) }, nil)
	}()

	for _, s := range send {
		sigs <- s
	}

	select {
	case <-ctx.Done():
		<-done
		return true, buf.String(), syscall.Signal(got.Load())
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-done
		return false, buf.String(), syscall.Signal(got.Load())
	}
}

// TestFirstInterruptOnlyWarns is the whole point of the confirmation: a
// single fingertip must not discard a long run.
func TestFirstInterruptOnlyWarns(t *testing.T) {
	t.Parallel()
	cancelled, out := runConfirm(t, true, time.Minute, syscall.SIGINT)
	assert.False(t, cancelled, "one Ctrl+C must not stop the run")
	assert.Equal(t, "\ninterrupt: 1 target running (. go-build), 4m0s in; Ctrl+C again stops it\n", out,
		"the warning names what a stop would discard and how to confirm it")
}

func TestSecondInterruptStopsTheRun(t *testing.T) {
	t.Parallel()
	cancelled, out := runConfirm(t, true, time.Minute, syscall.SIGINT, syscall.SIGINT)
	assert.True(t, cancelled, "a confirmed interrupt stops the run")
	assert.Contains(t, out, "Ctrl+C again stops it")
}

// TestInterruptWithNothingRunningStopsAtOnce is the other half of the window:
// a read or a follow stream (events -f, status -W, watch) has nothing a stop
// would discard, so one press ends it and nothing claims a run is stopping.
func TestInterruptWithNothingRunningStopsAtOnce(t *testing.T) {
	t.Parallel()
	for name, inFlight := range map[string]func() []cache.RunningTarget{
		"empty set": idle,
		"no source": nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cancelled, out, got := runConfirmRecording(t, true, time.Minute, inFlight, syscall.SIGINT)
			assert.True(t, cancelled, "one Ctrl+C stops a command with nothing in flight")
			assert.Empty(t, out, "no warning when nothing would be lost")
			assert.Equal(t, syscall.SIGINT, got, "the stop still reports 130")
		})
	}
}

// TestInterruptReadsTheRunningSetAtThePress pins that the window arms on what
// is executing when the key is pressed, not on the verb: a run between targets
// stops on one press, and the same run mid-target warns.
func TestInterruptReadsTheRunningSetAtThePress(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var executing atomic.Bool
	executing.Store(true)
	inFlight := func() []cache.RunningTarget {
		if executing.Load() {
			return building()
		}
		return nil
	}
	sigs := make(chan os.Signal, 2)
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		confirmInterrupts(ctx, sigs, cancel, &buf, true, 20*time.Millisecond, inFlight, nil, nil)
	}()

	sigs <- syscall.SIGINT
	time.Sleep(120 * time.Millisecond) // let the window lapse
	executing.Store(false)
	sigs <- syscall.SIGINT

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("a press with nothing executing must stop at once")
	}
	<-done
	assert.Equal(t, 1, strings.Count(buf.String(), "interrupt:"), "only the press mid-target warned")
}

// TestInterruptWarningNamesWhatItDiscards pins the first-press text: the
// count, the first few targets, and the age of the oldest.
func TestInterruptWarningNamesWhatItDiscards(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) time.Time { return now.Add(-ago) }

	for name, tc := range map[string]struct {
		running []cache.RunningTarget
		want    string
	}{
		"one": {
			running: []cache.RunningTarget{{Project: "cmd/magus", Target: "test", Started: at(90 * time.Second)}},
			want:    "interrupt: 1 target running (cmd/magus test), 1m30s in; Ctrl+C again stops it",
		},
		"oldest sets the age": {
			running: []cache.RunningTarget{
				{Project: ".", Target: "go-build", Started: at(4*time.Minute + 12*time.Second)},
				{Project: "cmd/magus", Target: "test", Started: at(time.Minute)},
			},
			want: "interrupt: 2 targets running (. go-build, cmd/magus test), 4m12s in; Ctrl+C again stops them",
		},
		"past three are counted": {
			running: []cache.RunningTarget{
				{Project: "a", Target: "lint", Started: at(time.Second)},
				{Project: "b", Target: "lint", Started: at(time.Second)},
				{Project: "c", Target: "lint", Started: at(time.Second)},
				{Project: "d", Target: "lint", Started: at(time.Second)},
				{Project: "e", Target: "lint", Started: at(2 * time.Second)},
			},
			want: "interrupt: 5 targets running (a lint, b lint, c lint and 2 more), 2s in; Ctrl+C again stops them",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, discardWarning(tc.running, now))
		})
	}
}

// TestInterruptRearmsAfterTheWindow guards the accident the window exists
// to prevent: a stray Ctrl+C long ago must not make a later single press
// fatal without its own warning.
func TestInterruptRearmsAfterTheWindow(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sigs := make(chan os.Signal, 2)
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		confirmInterrupts(ctx, sigs, cancel, &buf, true, 20*time.Millisecond, building, nil, nil)
	}()

	sigs <- syscall.SIGINT
	time.Sleep(120 * time.Millisecond) // let the window lapse
	sigs <- syscall.SIGINT

	select {
	case <-ctx.Done():
		t.Fatal("a press after the window lapsed must warn again, not stop the run")
	case <-time.After(150 * time.Millisecond):
	}
	cancel()
	<-done

	assert.Equal(t, 2, strings.Count(buf.String(), "Ctrl+C again stops it"),
		"each lapsed press gets its own warning")
}

// TestInterruptBySigtermStopsImmediately keeps a supervisor's shutdown honest:
// it is not a fingertip and must not need confirming.
func TestInterruptBySigtermStopsImmediately(t *testing.T) {
	t.Parallel()
	cancelled, out := runConfirm(t, true, time.Minute, syscall.SIGTERM)
	assert.True(t, cancelled, "SIGTERM stops the run at once")
	assert.Empty(t, out, "a supervisor is not prompted")
}

// TestInterruptOffATerminalStopsImmediately is what keeps CI working: a
// runner sending one SIGINT must not have to send a second.
func TestInterruptOffATerminalStopsImmediately(t *testing.T) {
	t.Parallel()
	cancelled, out := runConfirm(t, false, time.Minute, syscall.SIGINT)
	assert.True(t, cancelled, "off a terminal, one interrupt stops the run")
	assert.Empty(t, out, "nobody is there to read a prompt")
}

func TestWatchInterruptsStopReleasesTheHandler(t *testing.T) {
	t.Parallel()
	ctx, stop, interrupted := watchInterrupts(t.Context())
	stop()
	_, wasInterrupted := interrupted()
	assert.False(t, wasInterrupted, "stop() is not an interrupt")
	require.Error(t, ctx.Err(), "stop cancels the returned context")
	assert.NotPanics(t, stop, "stop is safe to call twice")
}

// TestInterruptIsRecordedForTheExitCode is the regression for a run that
// printed [fail] and exited 0, which made `magus run test . && deploy` deploy
// after a Ctrl+C.
func TestInterruptIsRecordedForTheExitCode(t *testing.T) {
	t.Parallel()

	t.Run("sigterm", func(t *testing.T) {
		t.Parallel()
		cancelled, _, got := runConfirmRecording(t, true, time.Minute, building, syscall.SIGTERM)
		require.True(t, cancelled)
		assert.Equal(t, syscall.SIGTERM, got, "the signal that stopped the run is recorded")
	})

	// Off a terminal is the case that matters: CI and agents read the exit code
	// and never see [fail] on a screen.
	t.Run("sigint off a terminal", func(t *testing.T) {
		t.Parallel()
		cancelled, _, got := runConfirmRecording(t, false, time.Minute, building, syscall.SIGINT)
		require.True(t, cancelled)
		assert.Equal(t, syscall.SIGINT, got)
	})

	t.Run("confirmed sigint at a terminal", func(t *testing.T) {
		t.Parallel()
		cancelled, _, got := runConfirmRecording(t, true, time.Minute, building, syscall.SIGINT, syscall.SIGINT)
		require.True(t, cancelled)
		assert.Equal(t, syscall.SIGINT, got)
	})

	t.Run("unconfirmed first press records nothing", func(t *testing.T) {
		t.Parallel()
		cancelled, _, got := runConfirmRecording(t, true, time.Minute, building, syscall.SIGINT)
		require.False(t, cancelled)
		assert.Zero(t, got, "a warned-but-continuing run is not interrupted")
	})
}

// TestWithInterruptPrefersASpecificCode keeps 128+N from flattening a code the
// command already chose.
func TestWithInterruptPrefersASpecificCode(t *testing.T) {
	t.Parallel()

	none := func() (syscall.Signal, bool) { return 0, false }
	term := func() (syscall.Signal, bool) { return syscall.SIGTERM, true }
	intr := func() (syscall.Signal, bool) { return syscall.SIGINT, true }

	assert.Equal(t, 0, withInterrupt(0, nil, none), "an uninterrupted success stays 0")
	assert.Equal(t, 1, withInterrupt(1, nil, none), "an ordinary failure is untouched")
	assert.Equal(t, 130, withInterrupt(0, nil, intr), "SIGINT reports 128+2")
	assert.Equal(t, 143, withInterrupt(0, nil, term), "SIGTERM reports 128+15")
	assert.Equal(t, exitUsage, withInterrupt(exitUsage, nil, term),
		"a usage error keeps its own code rather than being flattened to 143")

	// A command that RETURNS the cancellation instead of swallowing it (awaitInvocation
	// returns ctx.Err()) reached mapExitCode as a generic failure and reported 1, which
	// says the work failed about a run the user stopped.
	cancelled := fmt.Errorf("--wait: %w", context.Canceled)
	assert.Equal(t, 130, withInterrupt(1, cancelled, intr), "a surfaced cancellation is the signal")
	assert.Equal(t, 1, withInterrupt(1, cancelled, none),
		"without a signal it is an ordinary cancellation and keeps its code")
}
