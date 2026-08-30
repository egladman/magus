package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runConfirm drives confirmInterrupts on its own goroutine and reports
// whether the context was cancelled within a short grace period.
func runConfirm(t *testing.T, interactive bool, window time.Duration, send ...os.Signal) (cancelled bool, out string) {
	t.Helper()
	cancelled, out, _ = runConfirmRecording(t, interactive, window, send...)
	return cancelled, out
}

// runConfirmRecording is runConfirm plus the recorded signal.
func runConfirmRecording(t *testing.T, interactive bool, window time.Duration, send ...os.Signal) (cancelled bool, out string, recorded syscall.Signal) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sigs := make(chan os.Signal, len(send))
	var buf bytes.Buffer
	var got atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		confirmInterrupts(ctx, sigs, cancel, &buf, interactive, window,
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
	assert.Contains(t, out, interruptMessage, "the user must be told how to actually stop")
}

func TestSecondInterruptStopsTheRun(t *testing.T) {
	t.Parallel()
	cancelled, out := runConfirm(t, true, time.Minute, syscall.SIGINT, syscall.SIGINT)
	assert.True(t, cancelled, "a confirmed interrupt stops the run")
	assert.Contains(t, out, interruptMessage)
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
		confirmInterrupts(ctx, sigs, cancel, &buf, true, 20*time.Millisecond, nil, nil)
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

	assert.Equal(t, 2, bytes.Count(buf.Bytes(), []byte(interruptMessage)),
		"each lapsed press gets its own warning")
}

// TestSigtermStopsImmediately keeps a supervisor's shutdown honest: it is
// not a fingertip and must not need confirming.
func TestSigtermStopsImmediately(t *testing.T) {
	t.Parallel()
	cancelled, out := runConfirm(t, true, time.Minute, syscall.SIGTERM)
	assert.True(t, cancelled, "SIGTERM stops the run at once")
	assert.Empty(t, out, "a supervisor is not prompted")
}

// TestNonInteractiveStopsImmediately is what keeps CI working: a runner
// sending one SIGINT must not have to send a second.
func TestNonInteractiveStopsImmediately(t *testing.T) {
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
		cancelled, _, got := runConfirmRecording(t, true, time.Minute, syscall.SIGTERM)
		require.True(t, cancelled)
		assert.Equal(t, syscall.SIGTERM, got, "the signal that stopped the run is recorded")
	})

	// Off a terminal is the case that matters: CI and agents read the exit code
	// and never see [fail] on a screen.
	t.Run("sigint off a terminal", func(t *testing.T) {
		t.Parallel()
		cancelled, _, got := runConfirmRecording(t, false, time.Minute, syscall.SIGINT)
		require.True(t, cancelled)
		assert.Equal(t, syscall.SIGINT, got)
	})

	t.Run("confirmed sigint at a terminal", func(t *testing.T) {
		t.Parallel()
		cancelled, _, got := runConfirmRecording(t, true, time.Minute, syscall.SIGINT, syscall.SIGINT)
		require.True(t, cancelled)
		assert.Equal(t, syscall.SIGINT, got)
	})

	t.Run("unconfirmed first press records nothing", func(t *testing.T) {
		t.Parallel()
		cancelled, _, got := runConfirmRecording(t, true, time.Minute, syscall.SIGINT)
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
	// returns ctx.Err()) reached exitCodeOf as a generic failure and reported 1, which
	// says the work failed about a run the user stopped.
	cancelled := fmt.Errorf("--wait: %w", context.Canceled)
	assert.Equal(t, 130, withInterrupt(1, cancelled, intr), "a surfaced cancellation is the signal")
	assert.Equal(t, 1, withInterrupt(1, cancelled, none),
		"without a signal it is an ordinary cancellation and keeps its code")
}
