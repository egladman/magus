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

// captureStderr redirects os.Stderr for the duration of fn and returns what was written.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

// silentCache builds a Cache in silent mode with a log dir under t.TempDir.
func silentCache(t *testing.T) *Cache {
	t.Helper()
	c := &Cache{dir: t.TempDir(), log: slog.Default(), logLevel: slog.LevelError, silent: true}
	return c
}

func TestCaptureRunSilentBubblesNotices(t *testing.T) {
	c := silentCache(t)
	lp := c.logPath("svc/api", "deadbeef")

	out := captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintln(stdout, "compiling...")
			fmt.Fprintln(stdout, "magus:notice: deployed api v1.2.3")
			return nil
		})
		require.NoError(t, err)
	})

	assert.Equal(t, "notice: svc/api: deployed api v1.2.3\n", out)
	// Successful-run log is retained (replayable).
	_, statErr := os.Stat(lp)
	assert.NoError(t, statErr)
}

func TestCaptureRunSilentBoundsFailureAndKeepsLog(t *testing.T) {
	c := silentCache(t)
	lp := c.logPath("svc/api", "cafef00d")
	want := errors.New("boom")

	out := captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			for i := 0; i < maxFailTailLines+10; i++ {
				fmt.Fprintf(stdout, "line %d\n", i)
			}
			return want
		})
		require.ErrorIs(t, err, want)
	})

	assert.Contains(t, out, "-- svc/api (failed) --")
	assert.Contains(t, out, "earlier line(s) omitted; full log: "+lp)
	assert.Contains(t, out, fmt.Sprintf("line %d", maxFailTailLines+9)) // last line present
	assert.NotContains(t, out, "line 0\n")                              // earliest line trimmed
	// Failure log is retained in silent mode so the printed path resolves.
	_, statErr := os.Stat(lp)
	assert.NoError(t, statErr)
}

// In quiet-but-not-silent mode the failure output is fully dumped to stderr; the
// log is now retained (not removed) so Run can persist it under a target-output ref.
func TestCaptureRunQuietRetainsFailureLog(t *testing.T) {
	c := &Cache{dir: t.TempDir(), log: slog.Default(), logLevel: slog.LevelError}
	lp := c.logPath("svc/api", "0badf00d")
	want := errors.New("boom")

	_ = captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintln(stdout, "line 0")
			return want
		})
		require.ErrorIs(t, err, want)
	})

	data, statErr := os.ReadFile(lp)
	require.NoError(t, statErr, "quiet-mode failure log should be retained for the output store")
	assert.Contains(t, string(data), "line 0")
}

func TestCaptureRunSilentPassNoNoticeIsSilent(t *testing.T) {
	c := silentCache(t)
	lp := c.logPath("svc/api", "feedface")

	out := captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintln(stdout, "all good, nothing to report")
			return nil
		})
		require.NoError(t, err)
	})

	assert.Empty(t, out, "a passing run with no notice lines should print nothing")
}
