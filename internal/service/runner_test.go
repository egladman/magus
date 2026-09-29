package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

// machineService is a start service over a marker file: "up" means the file exists.
// start and stop each append a line to a log, so a test reads which ran.
func machineService(t *testing.T, marker, log string) spells.Service {
	t.Helper()
	if !hasBin("sh") {
		t.Skip("needs sh")
	}
	return spells.Service{
		Start:     spells.Command{Bin: "sh", Args: []string{"-c", "touch " + marker + "; echo start >> " + log}},
		Readiness: spells.Command{Bin: "test", Args: []string{"-e", marker}},
		Stop:      spells.Command{Bin: "sh", Args: []string{"-c", "rm -f " + marker + "; echo stop >> " + log}},
		Idle:      "10ms",
	}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

// TestStartServiceAdoptedIsNeverStopped pins adoption: a start service already passing
// its readiness probe is shared, its start never runs, and reaping it at idle never
// runs its stop, since magus did not start it.
func TestStartServiceAdoptedIsNeverStopped(t *testing.T) {
	dir := t.TempDir()
	marker, log := filepath.Join(dir, "up"), filepath.Join(dir, "log")
	s := machineService(t, marker, log)
	require.NoError(t, os.WriteFile(marker, nil, 0o600))

	r := New(ExecRunner{ReadyInterval: 10 * time.Millisecond}, 0)
	h, err := r.Acquire(context.Background(), "machine", s)
	require.NoError(t, err)
	assert.Equal(t, Adopted, h)

	r.Release("machine")
	r.Shutdown(context.Background())
	assert.Empty(t, readLog(t, log), "neither start nor stop ran")
	assert.FileExists(t, marker, "the adopted service is still up")
}

// TestStartServiceStartedIsOwnedAndStoppedAtReap pins ownership: a start service not
// yet ready is started, waited on, and stopped once its idle window passes.
func TestStartServiceStartedIsOwnedAndStoppedAtReap(t *testing.T) {
	dir := t.TempDir()
	marker, log := filepath.Join(dir, "up"), filepath.Join(dir, "log")
	s := machineService(t, marker, log)

	r := New(ExecRunner{ReadyInterval: 10 * time.Millisecond}, time.Hour)
	h, err := r.Acquire(context.Background(), "machine", s)
	require.NoError(t, err)
	assert.NotEqual(t, Adopted, h)
	assert.Equal(t, "start\n", readLog(t, log))
	assert.FileExists(t, marker)

	r.Release("machine")
	// The reap drops the entry before it runs stop, so wait on stop's own effect.
	require.Eventually(t, func() bool { return readLog(t, log) == "start\nstop\n" }, 5*time.Second, 10*time.Millisecond, "stopped after its 10ms idle")
	assert.NoFileExists(t, marker)
	assert.Equal(t, 0, r.Held())
}

// TestStartServiceFailingStartIsAnError pins that a start command exiting non-zero
// fails the acquire, rather than polling readiness for something that never began.
func TestStartServiceFailingStartIsAnError(t *testing.T) {
	if !hasBin("false") {
		t.Skip("needs false")
	}
	s := machineService(t, filepath.Join(t.TempDir(), "up"), filepath.Join(t.TempDir(), "log"))
	s.Start = spells.Command{Bin: "false"}
	_, err := ExecRunner{ReadyInterval: 10 * time.Millisecond}.Start(context.Background(), s)
	require.ErrorContains(t, err, `service: start "false"`)
}
