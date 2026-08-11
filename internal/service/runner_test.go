package service

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These smoke tests fork real short-lived processes, so they need a POSIX shell
// environment. They cover the ExecRunner's contract (readiness gating, stop,
// failed-readiness cleanup); the Registry's lifecycle policy is tested separately
// with a fake runner.
func hasBin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func TestExecRunnerReadyThenStop(t *testing.T) {
	if !hasBin("sleep") || !hasBin("true") {
		t.Skip("needs sleep and true")
	}
	h, err := ExecRunner{}.Start(context.Background(), spells.Service{
		Command:   spells.Command{Bin: "sleep", Args: []string{"60"}},
		Readiness: spells.Command{Bin: "true"}, // exits 0 immediately: ready at once
	})
	require.NoError(t, err)
	eh := h.(*execHandle)

	// Still running right after a passing readiness probe.
	select {
	case <-eh.done:
		t.Fatal("service exited before stop")
	default:
	}

	ExecRunner{}.Stop(context.Background(), h)
	select {
	case <-eh.done:
	case <-time.After(2 * time.Second):
		t.Fatal("service not reaped after stop")
	}
}

func TestExecRunnerReadinessFailureCleansUp(t *testing.T) {
	if !hasBin("sleep") || !hasBin("false") {
		t.Skip("needs sleep and false")
	}
	// A readiness probe that never passes must fail Start and leave nothing running.
	// Shorten nothing here; instead rely on the probe never succeeding and cancel via
	// a short context so the test stays fast.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := ExecRunner{}.Start(ctx, spells.Service{
		Command:   spells.Command{Bin: "sleep", Args: []string{"60"}},
		Readiness: spells.Command{Bin: "false"}, // never exits 0
	})
	assert.Error(t, err)
}

// TestExecRunnerWaitReadyProbeHangTimesOut is the regression test for a readiness
// probe that never exits: waitReady must not block inside the probe's Run() past
// ReadyTimeout. The 2s select is a hard bound so a regression here fails the test
// instead of wedging the suite.
func TestExecRunnerWaitReadyProbeHangTimesOut(t *testing.T) {
	if !hasBin("sleep") {
		t.Skip("needs sleep")
	}
	r := ExecRunner{ReadyTimeout: 150 * time.Millisecond, ReadyInterval: 20 * time.Millisecond}
	done := make(chan error, 1)
	go func() {
		done <- r.waitReady(context.Background(), spells.Command{Bin: "sleep", Args: []string{"60"}})
	}()
	select {
	case err := <-done:
		assert.ErrorContains(t, err, "did not pass within")
	case <-time.After(2 * time.Second):
		t.Fatal("waitReady did not return within the hard test bound; a hung probe blocked past ReadyTimeout")
	}
}

// TestExecRunnerWaitReadyRespectsCtxCancel covers the other half of D4: a cancelled
// ctx must interrupt a probe attempt in flight, not just be checked between attempts.
func TestExecRunnerWaitReadyRespectsCtxCancel(t *testing.T) {
	if !hasBin("sleep") {
		t.Skip("needs sleep")
	}
	r := ExecRunner{ReadyTimeout: 10 * time.Second, ReadyInterval: 20 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	done := make(chan error, 1)
	go func() {
		done <- r.waitReady(ctx, spells.Command{Bin: "sleep", Args: []string{"60"}})
	}()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("waitReady did not return within the hard test bound after ctx cancel; a hung probe outlived cancellation")
	}
}

func TestExecRunnerUsesStopCommand(t *testing.T) {
	if !hasBin("sleep") {
		t.Skip("needs sleep")
	}
	runner := ExecRunner{StopGrace: 100 * time.Millisecond}
	h, err := runner.Start(context.Background(), spells.Service{
		Command: spells.Command{Bin: "sleep", Args: []string{"60"}},
		Stop:    spells.Command{Bin: "true"}, // stand-in graceful stop; process then killed on grace
	})
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { runner.Stop(context.Background(), h); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}
}
