//go:build darwin || linux

package run

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The point of the TTY option is that the child believes it is on a terminal.
// Asserting that directly - by asking the child itself - is the only test that
// cannot pass for the wrong reason: a pipe would make this print "pipe".
func TestExecTTYChildSeesATerminal(t *testing.T) {
	res, err := Exec(context.Background(), "sh",
		[]string{"-c", "if [ -t 1 ]; then echo tty; else echo pipe; fi"},
		ExecOptions{TTY: true, Capture: true, Quiet: true})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got := strings.TrimSpace(res.Stdout); got != "tty" {
		t.Errorf("child reported %q on stdout, want %q", got, "tty")
	}
}

// Without the option nothing changes, which is the half that keeps this from
// being a behaviour change for every existing caller.
func TestExecWithoutTTYChildSeesAPipe(t *testing.T) {
	res, err := Exec(context.Background(), "sh",
		[]string{"-c", "if [ -t 1 ]; then echo tty; else echo pipe; fi"},
		ExecOptions{Capture: true, Quiet: true})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got := strings.TrimSpace(res.Stdout); got != "pipe" {
		t.Errorf("child reported %q on stdout, want %q", got, "pipe")
	}
}

// A terminal is one stream, so stderr arrives interleaved into stdout rather than
// separately. Documented on the option; asserted here so the documentation cannot
// quietly stop being true.
func TestExecTTYMergesStderrIntoStdout(t *testing.T) {
	res, err := Exec(context.Background(), "sh",
		[]string{"-c", "printf out; printf err 1>&2"},
		ExecOptions{TTY: true, Capture: true, Quiet: true})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(res.Stdout, "out") || !strings.Contains(res.Stdout, "err") {
		t.Errorf("Stdout = %q, want both streams merged into it", res.Stdout)
	}
	if res.Stderr != "" {
		t.Errorf("Stderr = %q, want empty (a tty has one stream)", res.Stderr)
	}
}

// The master read is what the pty branch cannot bound with WaitDelay, because
// the copy is ours rather than exec.Cmd's. A reader that never ends must cost two
// drain delays and then be cut loose - without the bound this test does not fail,
// it hangs until the suite's timeout, which is the shape the fix exists to
// prevent.
func TestDrainPTYBoundsACopyThatNeverEnds(t *testing.T) {
	restore := ptyDrainDelay
	ptyDrainDelay = 20 * time.Millisecond
	t.Cleanup(func() { ptyDrainDelay = restore })

	var captured strings.Builder
	dst := &detachableWriter{w: &captured}
	// Never written to: the copy this stands in for is blocked in a read that the
	// kernel will not end while a grandchild holds the slave.
	stuck := make(chan error)

	if err := drainPTY(context.Background(), exec.Command("true"), stuck, dst); err != nil {
		t.Errorf("drainPTY: %v, want nil once it gives up on the copy", err)
	}

	// Detached, so the abandoned copy cannot write into the buffer Exec reads the
	// moment runOnPTY returns.
	if _, err := dst.Write([]byte("late")); err != nil {
		t.Fatalf("Write after detach: %v", err)
	}
	if got := captured.String(); got != "" {
		t.Errorf("captured %q, want nothing after detach", got)
	}
}

// A copy that finishes on its own is returned as-is, error and all: the bound
// above must not swallow the read error the caller reports.
func TestDrainPTYReturnsTheCopyResult(t *testing.T) {
	done := make(chan error, 1)
	sentinel := errors.New("read failed")
	done <- sentinel

	if err := drainPTY(context.Background(), exec.Command("true"), done, &detachableWriter{w: io.Discard}); !errors.Is(err, sentinel) {
		t.Errorf("drainPTY = %v, want the copy's own error", err)
	}
}

// Stdin is replayed through the master by a goroutine, so the child must still
// read it as typed input - and that goroutine must be done before the master is
// closed underneath it.
func TestExecTTYReplaysStdinThroughTheMaster(t *testing.T) {
	res, err := Exec(context.Background(), "sh", []string{"-c", "read line; echo got:$line"},
		ExecOptions{TTY: true, Stdin: "hello\n", Capture: true, Quiet: true})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(res.Stdout, "got:hello") {
		t.Errorf("Stdout = %q, want the replayed stdin echoed back", res.Stdout)
	}
}

// The exit code must survive the Start/copy/Wait sequence the pty branch uses in
// place of cmd.Run().
func TestExecTTYPropagatesExitCode(t *testing.T) {
	// Exec surfaces a non-zero exit as an error AND in Code; the contract under a
	// pty must match the contract without one, so only Code is asserted here.
	res, _ := Exec(context.Background(), "sh", []string{"-c", "exit 3"},
		ExecOptions{TTY: true, Quiet: true})
	if res.Code != 3 {
		t.Errorf("Code = %d, want 3", res.Code)
	}
}
