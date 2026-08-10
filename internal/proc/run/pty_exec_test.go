//go:build darwin || linux

package run

import (
	"context"
	"strings"
	"testing"
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
