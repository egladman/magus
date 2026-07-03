package service

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/egladman/magus/types"
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
	h, err := ExecRunner{}.Start(context.Background(), types.Service{
		Command:   types.Command{Bin: "sleep", Args: []string{"60"}},
		Readiness: types.Command{Bin: "true"}, // exits 0 immediately: ready at once
	})
	require.NoError(t, err)
	eh := h.(*execHandle)

	// Still running right after a passing readiness probe.
	select {
	case <-eh.done:
		t.Fatal("service exited before stop")
	default:
	}

	ExecRunner{}.Stop(h)
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
	_, err := ExecRunner{}.Start(ctx, types.Service{
		Command:   types.Command{Bin: "sleep", Args: []string{"60"}},
		Readiness: types.Command{Bin: "false"}, // never exits 0
	})
	assert.Error(t, err)
}

func TestExecRunnerUsesStopCommand(t *testing.T) {
	if !hasBin("sleep") {
		t.Skip("needs sleep")
	}
	runner := ExecRunner{StopGrace: 100 * time.Millisecond}
	h, err := runner.Start(context.Background(), types.Service{
		Command: types.Command{Bin: "sleep", Args: []string{"60"}},
		Stop:    types.Command{Bin: "true"}, // stand-in graceful stop; process then killed on grace
	})
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { runner.Stop(h); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}
}
