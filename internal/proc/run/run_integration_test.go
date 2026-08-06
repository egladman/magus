//go:build integration

package run

import (
	"bytes"
	"context"
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
	_, err = Exec(context.Background(), "pwd", nil, ExecOptions{Dir: dir})
	require.NoError(t, err)
	got := strings.TrimRight(string(read()), "\n")
	assert.Equal(t, resolved, got, "working dir")
}

func TestIntegrationStdoutPassthrough(t *testing.T) {
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skip("'echo' not available")
	}
	read := captureStdout(t)
	_, err := Exec(context.Background(), "echo", []string{"hello"}, ExecOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(read()))
}

func TestIntegrationStderrPassthrough(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	read := captureStderr(t)
	_, err := Exec(context.Background(), "sh", []string{"-c", "echo err 1>&2"}, ExecOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	assert.Equal(t, "err\n", string(read()))
}

func TestIntegrationNonZeroExit(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	_, err := Exec(context.Background(), "sh", []string{"-c", "exit 7"}, ExecOptions{Dir: t.TempDir(), Quiet: true})
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 7, exitErr.ExitCode())
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
	_, err := Exec(ctx, "sleep", []string{"30"}, ExecOptions{Dir: t.TempDir(), Quiet: true})
	assert.Error(t, err, "want non-nil error after context cancel")
	assert.LessOrEqual(t, time.Since(start), 2*time.Second, "Exec should exit < 2s after cancel")
}

func TestIntegrationContextDeadline(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("'sleep' not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Exec(ctx, "sleep", []string{"30"}, ExecOptions{Dir: t.TempDir(), Quiet: true})
	assert.Error(t, err, "want non-nil error after deadline")
	assert.LessOrEqual(t, time.Since(start), 2*time.Second, "Exec should exit < 2s after deadline")
}

func TestIntegrationArgsVerbatim(t *testing.T) {
	if _, err := exec.LookPath("printf"); err != nil {
		t.Skip("'printf' not available")
	}
	read := captureStdout(t)
	_, err := Exec(context.Background(), "printf", []string{"%s\n", "*"}, ExecOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	assert.Equal(t, "*\n", string(read()), "args may have been shell-expanded")
}
