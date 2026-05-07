//go:build integration

package run_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/run"
)

// captureStdout redirects os.Stdout to a pipe and returns a function that
// closes the write end, drains the pipe, and returns the captured bytes.
// The original os.Stdout is restored via t.Cleanup. Do not call t.Parallel
// in tests that use this helper.
func captureStdout(t *testing.T) func() []byte {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = orig })
	return func() []byte {
		w.Close()
		var buf bytes.Buffer
		io.Copy(&buf, r)
		r.Close()
		return buf.Bytes()
	}
}

// captureStderr is the stderr equivalent of captureStdout.
func captureStderr(t *testing.T) func() []byte {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })
	return func() []byte {
		w.Close()
		var buf bytes.Buffer
		io.Copy(&buf, r)
		r.Close()
		return buf.Bytes()
	}
}

func TestIntegrationWorkdirRespected(t *testing.T) {
	if _, err := exec.LookPath("pwd"); err != nil {
		t.Skip("'pwd' not available")
	}
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	read := captureStdout(t)
	if err := run.Run(context.Background(), dir, "pwd"); err != nil {
		t.Fatalf("Run('pwd') = %v, want nil", err)
	}
	got := strings.TrimRight(string(read()), "\n")
	if got != resolved {
		t.Errorf("working dir = %q, want %q", got, resolved)
	}
}

func TestIntegrationStdoutPassthrough(t *testing.T) {
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skip("'echo' not available")
	}
	read := captureStdout(t)
	if err := run.Run(context.Background(), t.TempDir(), "echo", "hello"); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if got := string(read()); got != "hello\n" {
		t.Errorf("stdout = %q, want %q", got, "hello\n")
	}
}

func TestIntegrationStderrPassthrough(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	read := captureStderr(t)
	if err := run.Run(context.Background(), t.TempDir(), "sh", "-c", "echo err 1>&2"); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if got := string(read()); got != "err\n" {
		t.Errorf("stderr = %q, want %q", got, "err\n")
	}
}

func TestIntegrationNonZeroExit(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	err := run.Run(context.Background(), t.TempDir(), "sh", "-c", "exit 7")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("want *exec.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 7 {
		t.Errorf("exit code = %d, want 7", exitErr.ExitCode())
	}
}

func TestIntegrationMissingBinary(t *testing.T) {
	t.Parallel()
	err := run.Run(context.Background(), t.TempDir(), "magus-no-such-binary-xyzzy")
	if err == nil {
		t.Fatal("want error for missing binary, got nil")
	}
	if !errors.Is(err, exec.ErrNotFound) &&
		!strings.Contains(err.Error(), "executable file not found") &&
		!strings.Contains(err.Error(), "no such file") {
		t.Errorf("unexpected error kind %T: %v", err, err)
	}
}

func TestIntegrationContextCancelMidRun(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("'sleep' not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := run.Run(ctx, t.TempDir(), "sleep", "30")
	if err == nil {
		t.Error("want non-nil error after context cancel, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Run took %v after cancel, want < 2s", elapsed)
	}
}

func TestIntegrationContextDeadline(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("'sleep' not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := run.Run(ctx, t.TempDir(), "sleep", "30")
	if err == nil {
		t.Error("want non-nil error after deadline, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Run took %v after deadline, want < 2s", elapsed)
	}
}

func TestIntegrationArgsVerbatim(t *testing.T) {
	if _, err := exec.LookPath("printf"); err != nil {
		t.Skip("'printf' not available")
	}
	read := captureStdout(t)
	if err := run.Run(context.Background(), t.TempDir(), "printf", "%s\n", "*"); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if got := string(read()); got != "*\n" {
		t.Errorf("output = %q, want %q (args may have been shell-expanded)", got, "*\n")
	}
}
