package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	runPkg "github.com/egladman/magus/internal/proc/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collapseCache builds a Cache in collapse-on-success mode at default verbosity.
func collapseCache(t *testing.T) *Cache {
	t.Helper()
	return &Cache{dir: t.TempDir(), log: slog.Default(), logLevel: slog.LevelInfo, collapse: true}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

// TestCaptureRunCollapseSuppressesOutputOnSuccess verifies a passing project's
// subprocess output is withheld rather than streamed to stderr.
func TestCaptureRunCollapseSuppressesOutputOnSuccess(t *testing.T) {
	c := collapseCache(t)
	lp := c.logPath("svc/api", "deadbeef")

	out := captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintln(stdout, "compiling lots of noisy output...")
			return nil
		})
		require.NoError(t, err)
	})

	assert.Empty(t, out, "collapse mode should withhold subprocess output on success")
}

// TestCaptureRunCollapseReplaysOnFailure verifies that on failure the withheld
// output is replayed raw on stdout (copy/paste friendly), with an attributing
// header on the stderr live view, so the error is not lost to the collapse.
func TestCaptureRunCollapseReplaysOnFailure(t *testing.T) {
	c := collapseCache(t)
	lp := c.logPath("svc/api", "cafef00d")
	want := errors.New("boom")

	var stdoutBuf string
	stderrOut := captureStderr(t, func() {
		stdoutBuf = captureStdout(t, func() {
			_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
				stdout, _ := runPkg.OutputWriters(ctx)
				fmt.Fprintln(stdout, "lint: undefined symbol foo")
				return want
			})
			require.ErrorIs(t, err, want)
		})
	})

	// Header (attribution) on the stderr live view; raw body on stdout.
	assert.Contains(t, stderrOut, "-- svc/api (failed) --")
	assert.Contains(t, stdoutBuf, "lint: undefined symbol foo")
	assert.NotContains(t, stdoutBuf, "-- svc/api (failed) --", "stdout body must stay raw (no header)")
	// The failure log is retained (Run persists it to the output store under a ref
	// so `magus query ref...` can replay a failing target's exact output).
	data, statErr := os.ReadFile(lp)
	require.NoError(t, statErr, "collapse failure log should be retained after replay")
	assert.Contains(t, string(data), "lint: undefined symbol foo")
}
