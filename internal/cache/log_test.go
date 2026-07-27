package cache

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
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
		), "[pass] api (cached,")
	})

	t.Run("cache.miss", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.miss",
			slog.String("project", "web/studio"),
			slog.Int64("duration", int64(80*time.Millisecond)),
		), "[pass] web/studio (ran,")
	})

	t.Run("cache.error", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.error",
			slog.String("project", "api"),
			slog.Int64("duration", int64(5*time.Millisecond)),
			slog.String("error", "build failed"),
		), "[fail] api (ran,")
	})

	t.Run("cache.miss ref line", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		h := newTestHandler(&buf)
		require.NoError(t, h.Handle(context.Background(), buildRecord(
			"cache.miss",
			slog.String("project", "api"),
			slog.Int64("duration", int64(80*time.Millisecond)),
			slog.String("ref", "out1a2b3c4d"),
		)))
		out := buf.String()
		assert.Contains(t, out, "\nout1a2b3c4d\n", "the ref must sit alone on its own bare line for clean copy")
		assert.NotContains(t, out, "full output:", "a passing run gets no failure hint")
	})

	t.Run("cache.error action card", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		h := newTestHandler(&buf)
		require.NoError(t, h.Handle(context.Background(), buildRecord(
			"cache.error",
			slog.String("project", "api"),
			slog.String("label", "api"),
			slog.String("target", "build"),
			slog.Int64("duration", int64(5*time.Millisecond)),
			slog.String("error", "compile failed:\nundefined: Widget"),
			slog.String("ref", "outdeadbeef"),
		)))
		out := buf.String()
		assert.Contains(t, out, "[fail] api build (ran,", "failure heading identifies the project and target")
		assert.Contains(t, out, "cause: compile failed: undefined: Widget", "multi-line errors should remain scannable")
		assert.Contains(t, out, "output: outdeadbeef")
		assert.Contains(t, out, "inspect: magus query output outdeadbeef")
		assert.Contains(t, out, "reproduce: magus run build api")
	})

	t.Run("cache.summary", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.summary",
			slog.Int("hits", 3),
			slog.Int("misses", 1),
			slog.Int("errors", 0),
			slog.Int64("elapsed", int64(2*time.Second)),
		), "3 cached, 1 ran, 0 failed")
	})

	t.Run("cache.scope", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.scope",
			slog.String("label", "api"),
			slog.String("source", "cwd"),
		), "projects: api (cwd)")
	})

	t.Run("cache.charms", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.charms",
			slog.String("charms", "rw"),
		), "charms: rw")
	})

	t.Run("cache.stage ok", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.stage",
			slog.String("label", "magus"), // normalized: root reads as the workspace name, never "."
			slog.String("target", "lint"),
			slog.Int64("duration", int64(3100*time.Millisecond)),
		), "  [pass] magus lint (")
	})

	t.Run("cache.stage fail", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.stage",
			slog.String("label", "magus"),
			slog.String("target", "test"),
			slog.Int64("duration", int64(5*time.Second)),
			slog.String("error", "go test: exit 1"),
		), "  [fail] magus test (")
	})

	t.Run("cache.warn", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.warn",
			slog.String("msg", "gc: corrupt manifest foo.json: unexpected EOF"),
		), "gc: corrupt manifest foo.json")
	})

	t.Run("cache.dry.banner", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord("cache.dry.banner"), "dry run - commands shown, not executed")
	})

	t.Run("cache.dry", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.dry",
			slog.String("label", "magus"),
			slog.String("target", "ci"),
		), "[dry] magus ci")
	})

	t.Run("run.exec", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"run.exec",
			slog.String("cmd", "go"),
			slog.Any("args", []string{"test", "./..."}),
		), "  $ go test ./...")
	})
}

// TestPrettyHandlerUsesLabelForDisplay verifies a root project (path ".") renders by
// its normalized label on the status line, while the repro command keeps the real
// path so it stays runnable.
func TestPrettyHandlerUsesLabelForDisplay(t *testing.T) {
	rec := buildRecord(
		"cache.miss",
		slog.String("project", "."),   // real path -> repro
		slog.String("label", "magus"), // display name -> status line
		slog.String("target", "ci"),
		slog.Int64("duration", int64(12*time.Second)),
	)
	var buf bytes.Buffer
	h := newTestHandler(&buf)
	require.NoError(t, h.Handle(context.Background(), rec), "Handle")
	out := buf.String()
	assert.Contains(t, out, "[pass] magus (ran,", "status line should use the normalized label")
	assert.NotContains(t, out, "[pass] . (ran", "status line must not show the bare '.'")
	assert.Contains(t, out, "magus run ci .", "repro must keep the real runnable path")
}

func TestPrettyHandlerNormalizesMissingRootLabel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := newTestHandler(&buf)
	rec := buildRecord(
		"cache.error",
		slog.String("project", "."),
		slog.String("target", "ci"),
		slog.Int64("duration", int64(time.Second)),
		slog.String("error", "failed"),
	)
	require.NoError(t, h.Handle(context.Background(), rec), "Handle")
	out := buf.String()
	assert.Contains(t, out, "[fail] workspace ci (ran,", "root display must never fall back to a bare dot")
	assert.NotContains(t, out, "[fail] . ", "root display must never use a bare dot")
}

// TestPrettyHandlerGenericMessage verifies that a non-cache message renders in the
// compact generic style: a level tag, the message, and trailing key=value attrs,
// with no timestamp or level= boilerplate.
func TestPrettyHandlerGenericMessage(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := newTestHandler(&buf)
	r := buildRecord("magus: something happened", slog.String("key", "val"))
	require.NoError(t, h.Handle(context.Background(), r), "Handle")
	out := buf.String()
	assert.Contains(t, out, "[info] magus: something happened")
	assert.Contains(t, out, "key=val")
	assert.NotContains(t, out, "time=", "generic pretty output must not carry a timestamp")
	assert.NotContains(t, out, "level=", "generic pretty output must not carry a level= field")
}

// TestPrettyHandlerGenericLevels verifies the level-to-tag mapping for generic records.
func TestPrettyHandlerGenericLevels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		level slog.Level
		tag   string
	}{
		{slog.LevelError, "[error]"},
		{slog.LevelWarn, "[warn]"},
		{slog.LevelInfo, "[info]"},
		{slog.LevelDebug, "[debug]"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		h := newTestHandler(&buf)
		r := slog.NewRecord(time.Now(), tc.level, "msg", 0)
		require.NoError(t, h.Handle(context.Background(), r), "Handle")
		assert.Truef(t, strings.HasPrefix(buf.String(), tc.tag+" "), "level %s: want prefix %q, got %q", tc.level, tc.tag, buf.String())
	}
}

// TestPrettyHandlerSkipsDirAttr verifies the noisy "dir" correlation attr is hidden
// above debug level but shown at debug level.
func TestPrettyHandlerSkipsDirAttr(t *testing.T) {
	t.Parallel()
	var info bytes.Buffer
	hi := newTestHandler(&info)
	require.NoError(t, hi.Handle(context.Background(), buildRecord("msg", slog.String("dir", "/repo"))), "Handle")
	assert.NotContains(t, info.String(), "dir=/repo", "dir attr should be hidden at info level")

	var dbg bytes.Buffer
	hd := newTestHandler(&dbg)
	rec := slog.NewRecord(time.Now(), slog.LevelDebug, "msg", 0)
	rec.AddAttrs(slog.String("dir", "/repo"))
	require.NoError(t, hd.Handle(context.Background(), rec), "Handle")
	assert.Contains(t, dbg.String(), "dir=/repo", "dir attr should be shown at debug level")
}

// TestPrettyHandlerReproLine verifies that a per-project result always prints its
// copy-pasteable command, including on a non-interactive stream.
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
}

func TestPrettyHandlerFailureCardIgnoresHintsToggle(t *testing.T) {
	interactive.SetHintsEnabled(false)
	t.Cleanup(func() { interactive.SetHintsEnabled(true) })
	var buf bytes.Buffer
	h := newTestHandler(&buf) // a bytes.Buffer is explicitly non-TTY.
	// The renderer must not make an actionable failure depend on interactive mode.
	require.NoError(t, h.Handle(context.Background(), buildRecord(
		"cache.error",
		slog.String("project", "web"),
		slog.String("target", "test"),
		slog.String("error", "exit status 1"),
		slog.String("ref", "outbadcafe"),
	)), "Handle")
	out := buf.String()
	assert.Contains(t, out, "inspect: magus query output outbadcafe")
	assert.Contains(t, out, "reproduce: magus run test web")
}

func TestFailureExcerptBoundsMultilineError(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "one two", failureCauseExcerpt(" one\n\t two "))
	assert.Len(t, []rune(failureCauseExcerpt(strings.Repeat("x", 300))), 240)
}

// TestReproTarget verifies the target token the repro line uses: the bare name, or
// name:charm1,charm2 when charms are active (the `magus run` charm-suffix syntax).
func TestReproTarget(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "test", reproTarget(Step{Target: "test"}))
	assert.Equal(t, "test:debug,race", reproTarget(Step{Target: "test", Charms: []string{"debug", "race"}}))
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
