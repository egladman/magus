//go:build integration

package run

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStdout redirects os.Stdout to a pipe and returns a function that
// closes the write end, drains the pipe, and returns the captured bytes.
// The original os.Stdout is restored via t.Cleanup. Do not call t.Parallel
// in tests that use this helper.
func captureStdout(t *testing.T) func() []byte {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
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
	require.NoError(t, err)
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
	require.NoError(t, err)
	read := captureStdout(t)
	require.NoError(t, Run(context.Background(), dir, "pwd"))
	got := strings.TrimRight(string(read()), "\n")
	assert.Equal(t, resolved, got, "working dir")
}

func TestIntegrationStdoutPassthrough(t *testing.T) {
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skip("'echo' not available")
	}
	read := captureStdout(t)
	require.NoError(t, Run(context.Background(), t.TempDir(), "echo", "hello"))
	assert.Equal(t, "hello\n", string(read()))
}

func TestIntegrationStderrPassthrough(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	read := captureStderr(t)
	require.NoError(t, Run(context.Background(), t.TempDir(), "sh", "-c", "echo err 1>&2"))
	assert.Equal(t, "err\n", string(read()))
}

func TestIntegrationNonZeroExit(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	err := Run(context.Background(), t.TempDir(), "sh", "-c", "exit 7")
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 7, exitErr.ExitCode())
}

func TestIntegrationMissingBinary(t *testing.T) {
	t.Parallel()
	err := Run(context.Background(), t.TempDir(), "magus-no-such-binary-xyzzy")
	require.Error(t, err, "want error for missing binary")
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
	err := Run(ctx, t.TempDir(), "sleep", "30")
	assert.Error(t, err, "want non-nil error after context cancel")
	assert.LessOrEqual(t, time.Since(start), 2*time.Second, "Run should exit < 2s after cancel")
}

func TestIntegrationContextDeadline(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("'sleep' not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Run(ctx, t.TempDir(), "sleep", "30")
	assert.Error(t, err, "want non-nil error after deadline")
	assert.LessOrEqual(t, time.Since(start), 2*time.Second, "Run should exit < 2s after deadline")
}

func TestIntegrationArgsVerbatim(t *testing.T) {
	if _, err := exec.LookPath("printf"); err != nil {
		t.Skip("'printf' not available")
	}
	read := captureStdout(t)
	require.NoError(t, Run(context.Background(), t.TempDir(), "printf", "%s\n", "*"))
	assert.Equal(t, "*\n", string(read()), "args may have been shell-expanded")
}
