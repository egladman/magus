package main

import (
	"bytes"
	"context"
	"os"
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

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sigs := make(chan os.Signal, len(send))
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		confirmInterrupts(ctx, sigs, cancel, &buf, interactive, window)
	}()

	for _, s := range send {
		sigs <- s
	}

	select {
	case <-ctx.Done():
		<-done
		return true, buf.String()
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-done
		return false, buf.String()
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
		confirmInterrupts(ctx, sigs, cancel, &buf, true, 20*time.Millisecond)
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
	ctx, stop := watchInterrupts(t.Context())
	stop()
	require.Error(t, ctx.Err(), "stop cancels the returned context")
	assert.NotPanics(t, stop, "stop is safe to call twice")
}
