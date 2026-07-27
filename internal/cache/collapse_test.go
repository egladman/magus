package cache

import (
	"context"
	"errors"
	"fmt"
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

// TestCaptureRunCollapseShowsFailureExcerpt verifies that on failure collapse
// displays a bounded diagnostic excerpt on stderr, while retaining the full log.
func TestCaptureRunCollapseShowsFailureExcerpt(t *testing.T) {
	c := collapseCache(t)
	lp := c.logPath("svc/api", "cafef00d")
	want := errors.New("boom")

	stderrOut := captureStderr(t, func() {
		_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(ctx context.Context) error {
			stdout, _ := runPkg.OutputWriters(ctx)
			fmt.Fprintln(stdout, "lint: undefined symbol foo")
			return want
		})
		require.ErrorIs(t, err, want)
	})

	// Header and concise diagnostic are both on the human-facing stderr stream.
	assert.Contains(t, stderrOut, "-- svc/api (failed) --")
	assert.Contains(t, stderrOut, "lint: undefined symbol foo")
	// The failure log is retained (Run persists it to the output store under a ref
	// so `magus query ref...` can replay a failing target's exact output).
	data, statErr := os.ReadFile(lp)
	require.NoError(t, statErr, "collapse failure log should be retained after replay")
	assert.Contains(t, string(data), "lint: undefined symbol foo")
}
