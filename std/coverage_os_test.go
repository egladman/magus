package std

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covRetryCallback fails its first failures attempts and then answers with ret.
type covRetryCallback struct {
	calls    int
	failures int
	ret      []any
	err      error
}

func (c *covRetryCallback) Call(context.Context, ...any) ([]any, error) {
	c.calls++
	if c.calls <= c.failures {
		if c.err != nil {
			return nil, c.err
		}
		return nil, errors.New("attempt failed")
	}
	return c.ret, nil
}

// covFastRetry keeps the backoff below a millisecond so exercising the retry loop
// costs no wall clock.
var covFastRetry = map[string]any{"backoff_ms": 0.1, "max_backoff_ms": 1.0}

func TestOsRetryReturnsTheFirstSuccess(t *testing.T) {
	cb := &covRetryCallback{ret: []any{"answer"}}
	got, err := OsRetry(context.Background(), 3, cb, nil)
	require.NoError(t, err)
	assert.Equal(t, "answer", got)
	assert.Equal(t, 1, cb.calls, "a success must not be retried")
}

func TestOsRetryRetriesUntilItSucceeds(t *testing.T) {
	cb := &covRetryCallback{failures: 2, ret: []any{42}}
	got, err := OsRetry(context.Background(), 5, cb, covFastRetry)
	require.NoError(t, err)
	assert.Equal(t, 42, got)
	assert.Equal(t, 3, cb.calls)
}

// TestOsRetryWithNoReturnValue: a callback that returns nothing succeeded, so the
// result is the no-value result rather than an error.
func TestOsRetryWithNoReturnValue(t *testing.T) {
	got, err := OsRetry(context.Background(), 2, &covRetryCallback{}, nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestOsRetryExhaustsAndReportsTheLastError(t *testing.T) {
	boom := errors.New("still broken")
	cb := &covRetryCallback{failures: 99, err: boom}

	_, err := OsRetry(context.Background(), 3, cb, covFastRetry)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom, "the caller needs the reason, not just the count")
	assert.Contains(t, err.Error(), "os.retry: 3 attempt(s)")
	assert.Equal(t, 3, cb.calls)
}

func TestOsRetryRejectsANilCallback(t *testing.T) {
	_, err := OsRetry(context.Background(), 3, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fn must not be nil")
}

// TestOsRetryReadsIntegerBackoffOptions: the VM hands numbers over as float64,
// but a Go caller (and a hand-built map) may pass int or int64, so all three are
// accepted and anything else falls back to the default.
func TestOsRetryReadsIntegerBackoffOptions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		opts     map[string]any
		failures int
	}{
		{"int", map[string]any{"backoff_ms": 1, "max_backoff_ms": 2}, 1},
		{"int64", map[string]any{"backoff_ms": int64(1), "max_backoff_ms": int64(2)}, 1},
		// A value of the wrong type falls back to the 500ms default, so this case
		// must not retry: the point is that the option is READ without erroring.
		{"unusable values fall back to the defaults", map[string]any{"backoff_ms": "not a number", "max_backoff_ms": nil}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cb := &covRetryCallback{failures: tc.failures, ret: []any{"ok"}}
			got, err := OsRetry(context.Background(), 2, cb, tc.opts)
			require.NoError(t, err)
			assert.Equal(t, "ok", got)
		})
	}
}

// TestOsRetryHonorsCancellationDuringBackoff: an interrupted run must wake out of
// the backoff sleep rather than serving the full delay.
func TestOsRetryHonorsCancellationDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cb := &covRetryCallback{failures: 99}
	_, err := OsRetry(ctx, 3, cb, map[string]any{"backoff_ms": 60000.0})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, cb.calls, "the cancellation must land in the first backoff")
}

// TestOsWithEnv: the overrides ride the context so subprocesses started inside the
// callback inherit them. The process's own environment is never touched, which is
// what keeps a daemon serving other workspaces unaffected.
func TestOsWithEnv(t *testing.T) {
	var inner []string
	var nested []string

	err := OsWithEnv(context.Background(), map[string]string{"MAGUS_COV_WITH_ENV": "outer"},
		cbFunc(func(ctx context.Context) error {
			inner, _ = ctx.Value(withEnvKey{}).([]string)
			return OsWithEnv(ctx, map[string]string{"MAGUS_COV_WITH_ENV_INNER": "inner"},
				cbFunc(func(ctx context.Context) error {
					nested, _ = ctx.Value(withEnvKey{}).([]string)
					return nil
				}))
		}))
	require.NoError(t, err)

	assert.Equal(t, []string{"MAGUS_COV_WITH_ENV=outer"}, inner)
	assert.Equal(t, []string{"MAGUS_COV_WITH_ENV=outer", "MAGUS_COV_WITH_ENV_INNER=inner"}, nested,
		"a nested with_env merges onto the outer overrides rather than replacing them")

	_, ok := os.LookupEnv("MAGUS_COV_WITH_ENV")
	assert.False(t, ok, "with_env must never reach the process environment")
}

func TestOsWithEnvPropagatesTheCallbackError(t *testing.T) {
	boom := errors.New("callback failed")
	err := OsWithEnv(context.Background(), map[string]string{"A": "1"},
		cbFunc(func(context.Context) error { return boom }))
	assert.ErrorIs(t, err, boom)
}

func TestOsSleep(t *testing.T) {
	ctx := context.Background()

	start := time.Now()
	require.NoError(t, OsSleep(ctx, 0))
	require.NoError(t, OsSleep(ctx, -5))
	assert.Less(t, time.Since(start), 50*time.Millisecond, "a non-positive duration must not sleep")

	require.NoError(t, OsSleep(ctx, 1))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	assert.ErrorIs(t, OsSleep(cancelled, 60000), context.Canceled)
	assert.Less(t, time.Since(start), time.Second,
		"an interrupted run wakes immediately rather than serving the full duration")
}

// TestOsExit does not exit: it returns an ExitError the engine maps to a process
// status, and records the code on ctx so it survives a VM that stringifies the
// error type away.
func TestOsExit(t *testing.T) {
	ctx, readExit := types.WithExitCapture(context.Background())

	err := OsExit(ctx, 3)
	var exitErr types.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 3, exitErr.Code)

	code, set := readExit()
	assert.True(t, set, "the code must survive out-of-band")
	assert.Equal(t, 3, code)

	// Outside a captured run it is a plain error and nothing records the code.
	err = OsExit(context.Background(), 1)
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.Code)
}

// TestOsExitClampsToAProcessStatus pins the clamp at the source, so the CLI, the daemon
// reply and the out-of-band capture cannot disagree. Unclamped, os.exit(256) truncated
// to 0 in os.Exit and a failing run reported success.
func TestOsExitClampsToAProcessStatus(t *testing.T) {
	ctx, readExit := types.WithExitCapture(context.Background())

	err := OsExit(ctx, 256)
	var exitErr types.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.Code, "a failure that would truncate to 0 fails as 1")

	code, set := readExit()
	require.True(t, set)
	assert.Equal(t, 1, code, "the capture must agree with the error")
}

func TestOsPlatform(t *testing.T) {
	osName, arch, variant, err := OsPlatform(context.Background())
	require.NoError(t, err)

	wantOS, wantArch, wantVariant := HostPlatform()
	assert.Equal(t, wantOS, osName)
	assert.Equal(t, wantArch, arch)
	assert.Equal(t, wantVariant, variant)
	assert.NotEmpty(t, osName)
	assert.NotEmpty(t, arch)
}

func TestOsStdinIsTerminal(t *testing.T) {
	got, err := OsStdinIsTerminal(context.Background())
	require.NoError(t, err)
	assert.False(t, got, "the test binary's stdin is never a terminal")
}
