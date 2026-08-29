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

	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecRedactsSecretsFromEveryCapturedPath is the regression for two leaks found in
// review AFTER the feature was reported working off a terminal-only check:
//
//   - Capture buffers stdout/stderr through a MultiWriter that never reached the tap,
//     and ExecResult is what a magusfile, MCP, and the output store all read.
//   - Quiet replaces the tap with io.Discard, so a captured-and-quiet exec had no
//     redaction on any path at all.
//
// Both variants run here because Quiet is the one that leaves zero other coverage.
func TestExecRedactsSecretsFromEveryCapturedPath(t *testing.T) {
	ctx := secret.ContextWithResolver(t.Context(), secret.New())
	t.Setenv("MAGUS_TEST_LEAK_TOKEN", "ghp_never_capture_me")
	_, err := secret.ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_LEAK_TOKEN")
	require.NoError(t, err)

	for _, quiet := range []bool{false, true} {
		res, err := Exec(ctx, "sh",
			[]string{"-c", "echo tok=ghp_never_capture_me; echo tok=ghp_never_capture_me >&2"},
			ExecOptions{Dir: ".", Capture: true, Quiet: quiet})
		require.NoError(t, err)

		assert.NotContains(t, res.Stdout, "ghp_never_capture_me", "quiet=%v stdout", quiet)
		assert.NotContains(t, res.Stderr, "ghp_never_capture_me", "quiet=%v stderr", quiet)
		assert.Contains(t, res.Stdout, "tok=***", "quiet=%v", quiet)

		// BuzzObject is the exact value handed back to a magusfile.
		assert.NotContains(t, res.BuzzObject()["stdout"].(string), "ghp_never_capture_me")
	}
}

// TestExecWithoutAResolverIsUnchanged pins that the redaction plumbing is inert on the
// overwhelmingly common path: a run that reads no secret at all.
func TestExecWithoutAResolverIsUnchanged(t *testing.T) {
	res, err := Exec(t.Context(), "sh", []string{"-c", "echo plain output"},
		ExecOptions{Dir: ".", Capture: true, Quiet: true})
	require.NoError(t, err)
	assert.Equal(t, "plain output", strings.TrimSpace(res.Stdout))
}

// TestExecCapturesOutputWithNoTrailingNewline is the regression for a corruption a
// redaction writer introduced: its held-back tail was flushed in a defer that ran AFTER
// the capture buffers were read, so anything past the last newline vanished. It hit every
// run, not only ones touching a secret - `printf` came back empty.
func TestExecCapturesOutputWithNoTrailingNewline(t *testing.T) {
	for _, withResolver := range []bool{false, true} {
		ctx := t.Context()
		if withResolver {
			ctx = secret.ContextWithResolver(ctx, secret.New())
		}
		res, err := Exec(ctx, "printf", []string{"v1.2.3"},
			ExecOptions{Dir: ".", Capture: true, Quiet: true})
		require.NoError(t, err)
		assert.Equal(t, "v1.2.3", res.Stdout, "resolver=%v: unterminated output must survive", withResolver)
	}
}

// TestExecClassifiesAMissingBinary covers Exec specifically, because Exec is the path
// every real caller takes (internal/interp/bindings/command.go's spell-op dispatch,
// std/os.go's proc\exec, std/magus.go, internal/service/journal.go). A missing tool is
// common enough that its classification should be checked directly, not left to the
// real-subprocess coverage below alone.
//
// Both name shapes, because only the bare one is exec.ErrNotFound: a path-form name
// never reaches LookPath and arrives as ENOENT instead, so matching one pattern left
// the other unclassified.
func TestExecClassifiesAMissingBinary(t *testing.T) {
	for _, name := range []string{"magus-no-such-binary-xyzzy", "./magus-no-such-binary-xyzzy"} {
		_, err := Exec(t.Context(), name, nil, ExecOptions{Dir: ".", Quiet: true})

		require.Error(t, err, name)
		assert.ErrorIs(t, err, types.ToolNotOnPath, "%s: classified as MGS3003, matching std/os.go's os\\which()", name)
	}
}

// TestExecMissingBinaryStaysUnwrappable pins wrap transparency: a caller matching the
// stdlib sentinel directly, rather than through types.ToolNotOnPath, must keep working
// through the diagnostic wrap.
func TestExecMissingBinaryStaysUnwrappable(t *testing.T) {
	_, err := Exec(t.Context(), "magus-no-such-binary-xyzzy", nil, ExecOptions{Dir: ".", Quiet: true})

	require.Error(t, err)
	assert.ErrorIs(t, err, exec.ErrNotFound, "the underlying *exec.Error stays reachable")
}

// TestExecConsultsTheStepGate is the regression for --step being a silent no-op. The
// gate is installed by `magus run/x/affected --step` on the run's root ctx, but only
// the old run.Run consulted it, and nothing in production called run.Run - every
// subprocess forks through Exec. So the flag forced concurrency to 1, demanded a TTY,
// and then ran everything without ever prompting.
func TestExecConsultsTheStepGate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		action  StepAction
		wantRun bool
		wantErr error
	}{
		{"step runs the command", StepActionStep, true, nil},
		{"continue runs the command", StepActionContinue, true, nil},
		{"skip does not run it", StepActionSkip, false, nil},
		{"abort does not run it", StepActionAbort, false, ErrAborted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := false
			ctx := WithStepGate(t.Context(), func(_ context.Context, _ string, _ []string, _ string) StepAction {
				seen = true
				return tc.action
			})

			res, err := Exec(ctx, "printf", []string{"ran"}, ExecOptions{Dir: ".", Capture: true, Quiet: true})

			assert.True(t, seen, "the gate must be consulted before forking")
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.wantRun, res.Stdout == "ran", "command ran = %v", tc.wantRun)
		})
	}
}

// TestExecDoesNotClassifyAnExitedProcess guards classifyMissingBinary's `started`
// arm: a tool that RAN and failed for its own reasons must not be reported as a
// missing tool, however its error reads.
func TestExecDoesNotClassifyAnExitedProcess(t *testing.T) {
	_, err := Exec(t.Context(), "sh", []string{"-c", "cat /nonexistent-xyzzy; exit 1"},
		ExecOptions{Dir: ".", Quiet: true})

	require.Error(t, err)
	assert.NotErrorIs(t, err, types.ToolNotOnPath, "sh exists; its own ENOENT is not a missing tool")
}

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

func TestExecWorkdirRespected(t *testing.T) {
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

func TestExecStdoutPassthrough(t *testing.T) {
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skip("'echo' not available")
	}
	read := captureStdout(t)
	_, err := Exec(context.Background(), "echo", []string{"hello"}, ExecOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(read()))
}

func TestExecStderrPassthrough(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	read := captureStderr(t)
	_, err := Exec(context.Background(), "sh", []string{"-c", "echo err 1>&2"}, ExecOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	assert.Equal(t, "err\n", string(read()))
}

func TestExecNonZeroExit(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	_, err := Exec(context.Background(), "sh", []string{"-c", "exit 7"}, ExecOptions{Dir: t.TempDir(), Quiet: true})
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 7, exitErr.ExitCode())
}

func TestExecContextCancelMidRun(t *testing.T) {
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

func TestExecContextDeadline(t *testing.T) {
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

func TestExecArgsVerbatim(t *testing.T) {
	if _, err := exec.LookPath("printf"); err != nil {
		t.Skip("'printf' not available")
	}
	read := captureStdout(t)
	_, err := Exec(context.Background(), "printf", []string{"%s\n", "*"}, ExecOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	assert.Equal(t, "*\n", string(read()), "args may have been shell-expanded")
}
