package std

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withDefaultLogger installs a JSON logger at lvl as the process default and
// returns the buffer it writes to. It mirrors what cmd/magus/verbosity.go does
// from the -q/-v/-vv flags, which is the mechanism these tests are checking the
// module actually rides on.
//
// slog.SetDefault is global, so these tests cannot run in parallel.
func withDefaultLogger(t *testing.T, lvl slog.Level) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: lvl})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// logRecords parses the buffer's JSONL into records.
func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
		out = append(out, rec)
	}
	return out
}

func TestLogGoesToTheDefaultLogger(t *testing.T) {
	buf := withDefaultLogger(t, slog.LevelDebug)
	ctx := context.Background()

	require.NoError(t, LogInfo(ctx, "hello", map[string]any{"target": "build"}))

	recs := logRecords(t, buf)
	require.Len(t, recs, 1)
	assert.Equal(t, "hello", recs[0]["msg"])
	assert.Equal(t, "INFO", recs[0]["level"])
	// Attributes arrive as real structured fields, not interpolated into the
	// message - that is what makes them queryable in the run log.
	assert.Equal(t, "build", recs[0]["target"])
}

func TestLogHonorsTheConfiguredLevel(t *testing.T) {
	ctx := context.Background()

	// At warn (what -q installs), info and below are dropped by the handler
	// magus configured. This is the whole inheritance claim.
	buf := withDefaultLogger(t, slog.LevelWarn)
	require.NoError(t, LogTrace(ctx, "trace", nil))
	require.NoError(t, LogDebug(ctx, "debug", nil))
	require.NoError(t, LogInfo(ctx, "info", nil))
	require.NoError(t, LogWarn(ctx, "warn", nil))
	require.NoError(t, LogError(ctx, "error", nil))

	recs := logRecords(t, buf)
	require.Len(t, recs, 2)
	assert.Equal(t, "warn", recs[0]["msg"])
	assert.Equal(t, "error", recs[1]["msg"])
}

func TestLogTraceReachesTheTraceTier(t *testing.T) {
	ctx := context.Background()

	// Below debug: trace is magus's own -vvv tier, one step under slog's floor.
	buf := withDefaultLogger(t, logLevelTrace)
	require.NoError(t, LogTrace(ctx, "detail", nil))
	require.Len(t, logRecords(t, buf), 1)

	// And it is genuinely below debug, not an alias for it.
	buf = withDefaultLogger(t, slog.LevelDebug)
	require.NoError(t, LogTrace(ctx, "detail", nil))
	assert.Empty(t, logRecords(t, buf))
}

func TestLogAt(t *testing.T) {
	ctx := context.Background()
	buf := withDefaultLogger(t, logLevelTrace)

	for _, lvl := range []string{"trace", "debug", "info", "warn", "error"} {
		require.NoError(t, LogAt(ctx, lvl, lvl+" message", nil), "level %q", lvl)
	}
	assert.Len(t, logRecords(t, buf), 5)
}

func TestLogAtRejectsAnUnknownLevel(t *testing.T) {
	ctx := context.Background()
	buf := withDefaultLogger(t, logLevelTrace)

	err := LogAt(ctx, "critical", "boom", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "critical")

	// The empty string is rejected too rather than defaulting to info: reaching
	// here with "" means a caller computed a level and got nothing.
	require.Error(t, LogAt(ctx, "", "boom", nil))

	assert.Empty(t, logRecords(t, buf), "a rejected level must emit nothing")
}

func TestLogAttrsAreSorted(t *testing.T) {
	ctx := context.Background()
	buf := withDefaultLogger(t, slog.LevelInfo)

	// Go randomizes map iteration; the attrs must still come out in a stable
	// order, or the same line renders differently run to run.
	require.NoError(t, LogInfo(ctx, "m", map[string]any{"zebra": 1, "alpha": 2, "middle": 3}))

	line := buf.String()
	alpha, middle, zebra := strings.Index(line, "alpha"), strings.Index(line, "middle"), strings.Index(line, "zebra")
	assert.Less(t, alpha, middle)
	assert.Less(t, middle, zebra)
}

func TestLogSkipsWorkWhenFiltered(t *testing.T) {
	ctx := context.Background()
	buf := withDefaultLogger(t, slog.LevelError)

	// A filtered call is a no-op rather than an error, so a log.trace in a hot
	// loop is safe to leave in.
	require.NoError(t, LogDebug(ctx, "expensive", map[string]any{"a": 1}))
	assert.Empty(t, logRecords(t, buf))
}
