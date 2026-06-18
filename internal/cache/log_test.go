package cache

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/egladman/magus/internal/interactive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildRecord creates a slog.Record with the given message and attrs.
func buildRecord(msg string, attrs ...slog.Attr) slog.Record {
	r := slog.NewRecord(time.Now(), slog.LevelInfo, msg, 0)
	r.AddAttrs(attrs...)
	return r
}

// newTestHandler returns a prettyHandler that writes to a bytes.Buffer
// in non-TTY (plain) mode — suitable for output assertions in tests.
func newTestHandler(buf *bytes.Buffer) *prettyHandler {
	return &prettyHandler{w: buf} // fd nil → non-TTY
}

// TestPrettyHandlerPlainOutput verifies the plain ([cache] prefix) output
// for every recognised message key.
func TestPrettyHandlerPlainOutput(t *testing.T) {
	t.Parallel()

	// assertPlain runs one record through a fresh handler and asserts the
	// output contains the expected fragment.
	assertPlain := func(t *testing.T, rec slog.Record, mustContain string) {
		var buf bytes.Buffer
		h := newTestHandler(&buf)
		require.NoError(t, h.Handle(context.Background(), rec), "Handle")
		assert.Contains(t, buf.String(), mustContain)
	}

	t.Run("cache.hit", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.hit",
			slog.String("project", "api"),
			slog.Int64("duration", int64(42*time.Millisecond)),
			slog.String("hash", "abc123"),
		), "[cache] hit  api")
	})

	t.Run("cache.miss", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.miss",
			slog.String("project", "web/studio"),
			slog.Int64("duration", int64(80*time.Millisecond)),
		), "[cache] miss web/studio")
	})

	t.Run("cache.error", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.error",
			slog.String("project", "api"),
			slog.Int64("duration", int64(5*time.Millisecond)),
			slog.String("error", "build failed"),
		), "[cache] error api")
	})

	t.Run("cache.summary", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.summary",
			slog.Int("hits", 3),
			slog.Int("misses", 1),
			slog.Int("errors", 0),
			slog.Int64("elapsed", int64(2*time.Second)),
		), "3 hit, 1 miss, 0 error")
	})

	t.Run("cache.scope", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.scope",
			slog.String("label", "api"),
			slog.String("source", "cwd"),
		), "[scope] api (cwd)")
	})

	t.Run("cache.warn", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.warn",
			slog.String("msg", "gc: corrupt manifest foo.json: unexpected EOF"),
		), "gc: corrupt manifest foo.json")
	})
}

// TestPrettyHandlerUnknownMsgSilent verifies that an unrecognised message
// produces no output and no error.
func TestPrettyHandlerUnknownMsgSilent(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := newTestHandler(&buf)
	r := buildRecord("some.other.event", slog.String("key", "val"))
	require.NoError(t, h.Handle(context.Background(), r), "Handle")
	assert.Zero(t, buf.Len(), "expected no output for unknown message, got %q", buf.String())
}

// TestPrettyHandlerReproLine verifies that a per-project result prints the
// copy-pasteable `magus run <target> <project>` line, and that the hints toggle
// silences it. Not parallel: it flips the process-wide hints switch.
func TestPrettyHandlerReproLine(t *testing.T) {
	rec := buildRecord(
		"cache.miss",
		slog.String("project", "web/studio"),
		slog.String("target", "test:debug"),
		slog.Int64("duration", int64(80*time.Millisecond)),
	)

	var buf bytes.Buffer
	h := newTestHandler(&buf)
	require.NoError(t, h.Handle(context.Background(), rec), "Handle")
	assert.Contains(t, buf.String(), "magus run test:debug web/studio", "output does not contain repro command")

	// The hints toggle silences it.
	interactive.SetEnabled(false)
	defer interactive.SetEnabled(true)
	buf.Reset()
	require.NoError(t, h.Handle(context.Background(), rec), "Handle")
	assert.NotContains(t, buf.String(), "magus run", "repro command should be suppressed when hints are off")
}

// TestReproTarget verifies the target token the repro line uses: the bare name, or
// name:charm1,charm2 when charms are active (the `magus run` charm-suffix syntax).
func TestReproTarget(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "test", reproTarget(Spec{Target: "test"}))
	assert.Equal(t, "test:debug,race", reproTarget(Spec{Target: "test", Charms: []string{"debug", "race"}}))
}

// TestRecordAttrExtraction verifies the attr extraction helpers.
func TestRecordAttrExtraction(t *testing.T) {
	t.Parallel()
	r := buildRecord(
		"test",
		slog.String("project", "web/studio"),
		slog.Int64("duration", int64(123*time.Millisecond)),
		slog.Int("hits", 7),
	)

	assert.Equal(t, "web/studio", recordStr(r, "project"))
	assert.Empty(t, recordStr(r, "missing"), "recordStr(missing) should be empty")
	assert.Equal(t, 123*time.Millisecond, recordDur(r, "duration"))
	assert.Equal(t, 7, recordInt(r, "hits"))
}

// TestWithLoggerOption verifies that WithLogger replaces the cache's logger.
func TestWithLoggerOption(t *testing.T) {
	var buf bytes.Buffer
	customHandler := &prettyHandler{w: &buf} // fd nil → non-TTY
	l := slog.New(customHandler)

	dir := t.TempDir()
	t.Setenv("MAGUS_CACHE_MODE", "off")
	c, err := Open(dir, WithLogger(l))
	require.NoError(t, err)
	assert.Same(t, l, c.log, "WithLogger did not replace the cache logger")
}
