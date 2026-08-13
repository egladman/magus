package cache

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/screen"
	"github.com/egladman/magus/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildRecord creates a slog.Record with the given message and attrs.
func buildRecord(msg string, attrs ...slog.Attr) slog.Record {
	r := slog.NewRecord(time.Now(), slog.LevelInfo, msg, 0)
	r.AddAttrs(attrs...)
	return r
}

// plainProbe reports every descriptor as a pipe, so a handler built with
// it renders plain text and reserves no region.
type plainProbe struct{}

func (plainProbe) IsTerminal(uintptr) bool        { return false }
func (plainProbe) Size(uintptr) (int, int, error) { return 0, 0, nil }

// newTestHandler returns a PrettyHandler that writes to a bytes.Buffer in
// non-TTY (plain) mode, suitable for output assertions. It goes through
// the real constructor rather than a struct literal so the handler under
// test is wired exactly like a production one.
func newTestHandler(buf *bytes.Buffer) *PrettyHandler {
	return newPrettyHandler(buf, slog.LevelInfo, plainProbe{})
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
		// This asserted the flattened "cause: compile failed: undefined: Widget" on
		// the grounds that multi-line errors should remain scannable. That premise
		// held while a cause was always ONE error whose message happened to wrap.
		// It stopped holding once errors.Join made a cause routinely carry several
		// INDEPENDENT failures, because flattening then ran two unrelated ones into
		// a single sentence with no boundary - see
		// TestFailureCausesSplitsAndStripsPlumbing for the real string that produced.
		// Scannability is now served by a hanging indent instead of by one line.
		assert.Contains(t, out, "cause: compile failed:\n       undefined: Widget",
			"each line of a cause is its own line, aligned under the first")
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

	// A planned target renders like an executed one: the label on the glyph line,
	// the target in the repro command underneath. It carries no duration because
	// nothing ran, which is the only shape difference from cache.hit/cache.miss.
	t.Run("cache.dry", func(t *testing.T) {
		t.Parallel()
		rec := buildRecord(
			"cache.dry",
			slog.String("project", "."),
			slog.String("label", "magus"),
			slog.String("target", "ci"),
		)
		assertPlain(t, rec, "[dry] magus")
		assertPlain(t, rec, "magus run ci")
	})

	// The dry footer is the same cache.summary event a real run ends with, so
	// every output format keeps reporting a footer. Only the wording differs:
	// nothing executed, so cached/ran/failed would all read 0.
	t.Run("cache.summary dry", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.summary",
			slog.Bool("dry", true),
			slog.Int("planned", 3),
			slog.Int64("elapsed", int64(2*time.Millisecond)),
		), "summary: dry run - 3 targets would run")
	})

	// Pluralization is real rather than "target(s)".
	t.Run("cache.summary dry singular", func(t *testing.T) {
		t.Parallel()
		assertPlain(t, buildRecord(
			"cache.summary",
			slog.Bool("dry", true),
			slog.Int("planned", 1),
			slog.Int64("elapsed", int64(time.Millisecond)),
		), "1 target would run")
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
// The test applies the option directly to a zero-value Cache so it does not
// need a temp dir, the cache machinery, or any env-var gymnastics.
func TestWithLoggerOption(t *testing.T) {
	t.Parallel()
	var c Cache
	l := slog.New(slog.DiscardHandler)
	WithLogger(l)(&c)
	assert.Same(t, l, c.log, "WithLogger did not replace the cache logger")
}

// terminalProbe answers as an 80x24 terminal for every descriptor, so a
// handler under test reserves a region without needing a pty. It is a
// value passed to the handler, not global state, which is what lets these
// tests run in parallel.
type terminalProbe struct{}

func (terminalProbe) IsTerminal(uintptr) bool        { return true }
func (terminalProbe) Size(uintptr) (int, int, error) { return 80, 24, nil }

// ttyBuf is a bytes.Buffer carrying a synthetic descriptor, so tty.Fd
// reports one and the region treats it as a real terminal.
type ttyBuf struct{ bytes.Buffer }

func (*ttyBuf) Fd() uintptr { return 2 }

// newTerminalHandler returns a handler wired to a fake 80x24 terminal.
func newTerminalHandler(buf *ttyBuf) *PrettyHandler {
	return newPrettyHandler(buf, slog.LevelInfo, terminalProbe{})
}

// TestPrettyHandlerErrorWritesHeadingToStickyRegion verifies that on a TTY
// writer, the [fail] heading reaches the sticky region (DECSTBM + bold-red
// SGR + heading text) while the trailing cause/output/inspect lines stay
// in the scrolling region above.
func TestPrettyHandlerErrorWritesHeadingToStickyRegion(t *testing.T) {
	t.Parallel()

	var buf ttyBuf
	h := newTerminalHandler(&buf)
	require.True(t, h.lease.Enabled(), "a TTY writer must be granted a band in the zone")

	require.NoError(t, h.Handle(context.Background(), buildRecord(
		"cache.error",
		slog.String("project", "api"),
		slog.String("target", "build"),
		slog.Int64("duration", int64(250*time.Millisecond)),
		slog.String("error", "exit status 2"),
		slog.String("ref", "out-42"),
	)), "Handle cache.error")

	out := buf.String()
	assert.Contains(t, out, "\x1b[1;31m", "heading should use bold-red SGR")
	// The duration is now its own right-aligned span, so the heading is the
	// target alone rather than one pre-formatted string.
	assert.Contains(t, out, "[fail]", "the glyph is present")
	assert.Contains(t, out, "build", "the failing target is present, under its project")
	assert.Contains(t, out, "cause: exit status 2", "cause line stays in scroll region")
	assert.Contains(t, out, "output: out-42", "output line stays in scroll region")
	assert.Contains(t, out, "inspect: magus query output out-42", "inspect line stays in scroll region")
	// 24 rows less the band's six and the zone's box.
	// One failure row, inside the box, with the status in the top rule.
	assert.Contains(t, out, "\x1b[1;21r", "scroll margins reserve the bottom rows for the sticky region")
}

// TestPrettyHandlerSummaryReleasesStickyRegion verifies that a cache.summary
// record ends the sticky region so the user's shell prompt returns to a
// clean full-screen terminal.
func TestPrettyHandlerSummaryReleasesStickyRegion(t *testing.T) {
	t.Parallel()

	var buf ttyBuf
	h := newTerminalHandler(&buf)

	require.NoError(t, h.Handle(context.Background(), buildRecord(
		"cache.error",
		slog.String("project", "api"),
		slog.String("target", "build"),
		slog.Int64("duration", int64(100*time.Millisecond)),
		slog.String("error", "boom"),
	)), "Handle cache.error")
	// Everything the summary itself writes, isolated from what came before it.
	// The band GROWS as failures arrive, and a grow rebuilds the region - which
	// resets the margins on its way to setting new ones. Asserting over the
	// whole transcript would see that earlier reset and read it as a teardown
	// the summary did not perform.
	before := buf.Len()
	require.NoError(t, h.Handle(context.Background(), buildRecord(
		"cache.summary",
		slog.Int("hits", 3),
		slog.Int("misses", 1),
		slog.Int("errors", 1),
		slog.Int64("elapsed", int64(2*time.Second)),
	)), "Handle cache.summary")

	out := buf.String()[before:]
	// The writer reports a descriptor and the probe calls it a terminal,
	// so the summary takes the "Summary:" form. The sticky-region
	// lifecycle is what this test cares about.
	assert.Contains(t, out, "3 cached, 1 ran, 1 failed", "summary line present")
	assert.Regexp(t, `\x1b\[\d+;\d+r`, buf.String(), "DECSTBM was set by the sticky region")

	// The band is HELD here rather than released, because a failure is pinned
	// in it: releasing would erase the list at the exact moment it became
	// actionable. Whoever offers the prompt gives the rows back, and the
	// process exit path releases them regardless if nobody does.
	assert.NotContains(t, out, "\x1b[r", "a band with pinned failures survives the summary")

	require.NoError(t, h.ReleaseBand())
	assert.Contains(t, buf.String(), "\x1b[r", "and the rows come back when the prompt is done")
}

// TestPrettyHandlerSummaryReleasesACleanBand is the other half: with nothing
// pinned there is nothing to act on, so the rows go back immediately and the
// shell prompt returns to a full-screen terminal.
func TestPrettyHandlerSummaryReleasesACleanBand(t *testing.T) {
	t.Parallel()

	var buf ttyBuf
	h := newTerminalHandler(&buf)
	require.NoError(t, h.Handle(context.Background(), buildRecord(
		"cache.pool",
		slog.Int("capacity", 8),
		slog.Int("running", 2),
		slog.Int("queued", 0),
	)), "Handle cache.pool")
	require.NoError(t, h.Handle(context.Background(), buildRecord(
		"cache.summary",
		slog.Int("hits", 4),
		slog.Int("misses", 0),
		slog.Int("errors", 0),
		slog.Int64("elapsed", int64(time.Second)),
	)), "Handle cache.summary")

	assert.Contains(t, buf.String(), "\x1b[r", "no failures means nothing to hold the rows for")
}

// TestPrettyHandlerCloseIsIdempotent verifies Close() can be called multiple
// times and on a non-TTY handler without panicking, so defer h.Close() is
// always safe at call sites.
func TestPrettyHandlerCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, slog.LevelInfo)
	require.NotPanics(t, func() {
		h.Close()
		h.Close()
	}, "Close() must be safe to call twice on a non-TTY writer")
}

// TestPrettyHandlerNonTTYSkipsStickyRegion verifies the fallback path: a
// bytes.Buffer writer (no fd, no TTY probe) gets plain output and no
// DECSTBM escape.
func TestPrettyHandlerNonTTYSkipsStickyRegion(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, slog.LevelInfo)
	assert.False(t, h.lease.Enabled(), "a non-TTY writer must be granted no rows")

	require.NoError(t, h.Handle(context.Background(), buildRecord(
		"cache.error",
		slog.String("project", "api"),
		slog.String("target", "build"),
		slog.Int64("duration", int64(100*time.Millisecond)),
		slog.String("error", "boom"),
	)), "Handle cache.error")

	out := buf.String()
	assert.NotContains(t, out, "\x1b[r", "non-TTY must not emit DECSTBM")
	assert.NotContains(t, out, "\x1b[1;31m", "non-TTY must not emit colour SGR")
	assert.Contains(t, out, "[fail] api build (ran,", "plain prefix still present")
}

func TestStatusLineRender(t *testing.T) {
	t.Parallel()
	base := time.Now()
	for _, tc := range []struct {
		name string
		line statusLine
		want string
	}{
		{"pool only, nothing done yet", statusLine{capacity: 8, running: 3}, "■ ■ ■ □ □ □ □ □ (3/8)"},
		{"queued work is shown", statusLine{capacity: 8, running: 8, queued: 2}, "■ ■ ■ ■ ■ ■ ■ ■ (8/8), 2 queued"},
		{"a quiet queue is omitted", statusLine{capacity: 4}, "□ □ □ □ (0/4)"},
		// A blocked run leads, because the pool counters alone would read as a stall
		// with no cause. This is the clause that describes doing nothing.
		// The blocked state deliberately does NOT appear here: it is announced
		// once, as the pinned notification, which is bold and carries the
		// remedy. Saying it in both places said the same facts twice for one
		// event. The fields are still set - blockedMessage reads them.
		{"a blocked run does not repeat itself here", statusLine{capacity: 8, blocked: "web/api"}, "□ □ □ □ □ □ □ □ (0/8)"},
		{"nor when the holder is known", statusLine{capacity: 8, blocked: ".", blockedBy: "pid 71557 (magus run serve)"}, "□ □ □ □ □ □ □ □ (0/8)"},
		{
			"tally appears once work completes",
			statusLine{capacity: 8, running: 2, passed: 3, cached: 2},
			"■ ■ □ □ □ □ □ □ (2/8)   5 ok (2 cached)",
		},
		{
			"failures are called out",
			statusLine{capacity: 8, running: 1, passed: 4, failed: 1},
			"■ □ □ □ □ □ □ □ (1/8)   4 ok  1 failed",
		},
		{
			"a clean run shows no cached or failed clause",
			statusLine{capacity: 8, running: 1, passed: 4},
			"■ □ □ □ □ □ □ □ (1/8)   4 ok",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			left, _ := tc.line.render(base)
			assert.Equal(t, tc.want, left)
		})
	}
}

// TestStatusLineRenderShowsElapsed keeps the progress readout honest: the
// row is what a reader watches to know a long run is still moving.
func TestStatusLineRenderShowsElapsed(t *testing.T) {
	t.Parallel()
	start := time.Now()
	line := statusLine{capacity: 4, running: 1, start: start}
	left, elapsed := line.render(start.Add(90 * time.Second))
	assert.Equal(t, "1m30s", elapsed, "elapsed is returned separately so the band can pin it right")
	assert.NotContains(t, left, "1m30s", "and does not shove the counters sideways once a second")
}

// TestPrettyHandlerPoolEventPaintsTheStatusRow verifies a cache.pool
// record lands in the region's status row rather than the scrolling
// output, so it repaints in place instead of accumulating.
func TestPrettyHandlerPoolEventPaintsTheStatusRow(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	h := newTerminalHandler(&buf)

	require.NoError(t, h.Handle(context.Background(), buildRecord(
		"cache.pool",
		slog.Int("capacity", 8),
		slog.Int("running", 3),
		slog.Int("queued", 1),
	)), "Handle cache.pool")

	out := buf.String()
	assert.Contains(t, out, "■ ■ ■ □ □ □ □ □ (3/8), 1 queued", "the pool sample is rendered")
	assert.Contains(t, out, "\x1b[23;1H", "it is pinned under the region's top rule")
}

// TestPrettyHandlerPoolEventIsSilentOffATerminal is what keeps two events
// per step out of CI logs and out of stdout JSON.
func TestPrettyHandlerPoolEventIsSilentOffATerminal(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := newTestHandler(&buf)

	require.NoError(t, h.Handle(context.Background(), buildRecord(
		"cache.pool",
		slog.Int("capacity", 8),
		slog.Int("running", 3),
		slog.Int("queued", 1),
	)), "Handle cache.pool")

	assert.Empty(t, buf.String(), "a non-TTY run must not see pool samples at all")
}

// TestPrettyHandlerRendersStatusGatesEmission mirrors the check the cache
// makes before sampling the limiter, so the cheap path stays cheap.
func TestPrettyHandlerRendersStatusGatesEmission(t *testing.T) {
	t.Parallel()

	var plain bytes.Buffer
	assert.False(t, newTestHandler(&plain).RendersBand(),
		"a buffer-backed handler has no region to paint into")

	var term ttyBuf
	assert.True(t, newTerminalHandler(&term).RendersBand(),
		"a terminal-backed handler does")
}

// TestPrettyHandlerPrintsInspectHintEverywhere pins the reversal portable refs
// earned. The hint used to be suppressed on CI because a ref addressed a blob in
// the local cache of the machine that minted it, so pasting the command off an
// ephemeral runner could only fail. A ref is now a truncation of the cache key,
// so it means the same thing on every machine, and a reader who cannot resolve
// it gets the target that would produce it or a --publish route to the bytes.
// CI is where a ref is most often read, so CI is where the hint matters most.
func TestPrettyHandlerPrintsInspectHintEverywhere(t *testing.T) {
	for name, ci := range map[string]string{"on CI": "true", "off CI": ""} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CI", ci)

			var buf bytes.Buffer
			h := newPrettyHandler(&buf, slog.LevelInfo, plainProbe{})
			require.NoError(t, h.Handle(context.Background(), buildRecord(
				"cache.error",
				slog.String("project", "api"),
				slog.String("target", "build"),
				slog.Int64("duration", int64(5*time.Millisecond)),
				slog.String("error", "boom"),
				slog.String("ref", "outdeadbeef"),
			)))

			out := buf.String()
			assert.Contains(t, out, "output: outdeadbeef", "the ref correlates with the journal")
			assert.Contains(t, out, "inspect: magus query output outdeadbeef")
			assert.Contains(t, out, "reproduce: magus run build api")
		})
	}
}

// remoteSuffix decides whether a step's line gains a "remote" qualifier. The whole value
// is in staying silent: a suffix on every line is one nobody reads, so these pin the
// cases that must NOT render as hard as the case that must.
func TestRemoteSuffix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		remote, total time.Duration
		want          string
	}{
		{"most of the wait was the network", 4200 * time.Millisecond, 6100 * time.Millisecond, ", 4.2s remote"},
		{"exactly at the share threshold", 1 * time.Second, 4 * time.Second, ", 1.0s remote"},
		{"a trivial probe on a fast step", 30 * time.Millisecond, 40 * time.Millisecond, ""},
		{"material but a small share of a long build", 600 * time.Millisecond, 10 * time.Second, ""},
		{"no remote work at all", 0, 5 * time.Second, ""},
		{"no duration recorded", 2 * time.Second, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, remoteSuffix(tc.remote, tc.total))
		})
	}
}

// TestPrettyHandlerRedactsSecrets pins the THIRD path a credential reached the user
// through, found only by writing a real provider spell and watching a `-vv` run:
//
//	$ sh -c echo using token=<the actual token>
//
// That line is a slog record rendered by this handler, not a journal event, so neither
// the output tap nor journal.Emit's redaction covered it. Redacting in printf covers
// every record kind this handler will ever render rather than just run.exec.
func TestPrettyHandlerRedactsSecrets(t *testing.T) {
	ctx := secret.ContextWithResolver(t.Context(), secret.New())
	t.Setenv("MAGUS_TEST_LOG_TOKEN", "ghp_never_echo_me")
	_, err := secret.ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_LOG_TOKEN")
	require.NoError(t, err)

	var sink bytes.Buffer
	h := NewPrettyHandler(&sink, slog.LevelDebug)
	slog.New(h).DebugContext(ctx, "run.exec",
		"cmd", "sh", "args", []string{"-c", "echo tok=ghp_never_echo_me"}, "dir", ".")

	assert.NotContains(t, sink.String(), "ghp_never_echo_me")
	assert.Contains(t, sink.String(), "***")
}

// TestPrettyHandlerIsUnchangedWithoutSecrets pins that the funnel is inert on the
// common path: a run that reads no secret renders byte-for-byte as before.
func TestPrettyHandlerIsUnchangedWithoutSecrets(t *testing.T) {
	var sink bytes.Buffer
	h := NewPrettyHandler(&sink, slog.LevelDebug)
	slog.New(h).DebugContext(context.Background(), "run.exec",
		"cmd", "go", "args", []string{"build", "./..."}, "dir", ".")

	assert.Contains(t, sink.String(), "$ go build ./...")
	assert.NotContains(t, sink.String(), "***")
}

// TestPrettyHandlerPrintsAfterCancellation pins that a cancelled context does NOT silence
// a record. PrettyHandler is the DEFAULT handler, and it used to early-return on
// ctx.Err(). That was inert for as long as it existed - every call site logged through
// slog.Logger.Info, which passes context.Background() - so nothing noticed. When the run
// path began passing its real context (so records could reach the secret resolver), the
// check woke up and started eating output: in a concurrent run the first failure cancels
// the errgroup, so every [pass]/[fail] line that finished afterwards, the [summary]
// footer, and the Ctrl-C service-release warning all disappeared from the default format
// while `-o json` still showed them.
//
// A log handler must not use record delivery as a cancellation channel.
func TestPrettyHandlerPrintsAfterCancellation(t *testing.T) {
	var sink bytes.Buffer
	lg := slog.New(NewPrettyHandler(&sink, slog.LevelInfo))

	ctx, cancel := context.WithCancel(t.Context())
	lg.InfoContext(ctx, "cache.scope", slog.String("label", "before"), slog.String("source", "x"))
	cancel()
	lg.InfoContext(ctx, "cache.scope", slog.String("label", "after"), slog.String("source", "x"))

	out := sink.String()
	assert.Contains(t, out, "before")
	assert.Contains(t, out, "after", "a cancelled context must not silence a record")
}

// TestFailureCausesSplitsAndStripsPlumbing pins the readability of the `cause:`
// line, using the exact string a real `magus run ci .` produced. Two independent
// failures were joined by errors.Join with a newline and then flattened by
// strings.Fields, so they ran together into one sentence with no boundary -
// "dprint exited 20 test: ctx.needs: advice-test: ..." reads as a single clause
// and names neither failure clearly.
func TestFailureCausesSplitsAndStripsPlumbing(t *testing.T) {
	const joined = "ctx.needs: build: ctx.needs: go-build: ctx.needs: ctx.needs: format: dprint exited 20\n" +
		"test: ctx.needs: advice-test: magus.buzz: buzz -t blast-radius.buzz exited with code 1\n" +
		"compress-cgo-test: go exited 1"

	got := failureCauses(joined)

	require.Len(t, got, 3, "each independent failure gets its own line")
	assert.Equal(t, "build -> go-build -> format: dprint exited 20", got[0],
		"the markers are consumed and the hops they named read as a path")
	assert.Equal(t, "test -> advice-test: magus.buzz: buzz -t blast-radius.buzz exited with code 1", got[1],
		"magus.buzz carries no marker, so it stays part of the message")
	assert.Equal(t, "compress-cgo-test: go exited 1", got[2])
	for _, c := range got {
		assert.NotContains(t, c, "ctx.needs:", "no plumbing prefix survives")
	}
}

// TestFailureCausesKeepsASingleCauseWhole guards the common case: one failure
// must not be reshaped, and a cause with no newline still returns exactly one line.
func TestFailureCausesKeepsASingleCauseWhole(t *testing.T) {
	got := failureCauses("lint: markdownlint exited 1")
	require.Len(t, got, 1)
	assert.Equal(t, "lint: markdownlint exited 1", got[0])
}

// TestArrowChainLeavesAmbiguousCasesAlone guards the shape heuristic. A single
// hop needs no arrow (one colon is unambiguous), and nothing after the first
// space-bearing segment may be rewritten, or a message's own colons would be
// mangled into fake hops.
func TestHopChainUsesTheMarkersNotGuesswork(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"single hop", "lint: markdownlint exited 1", "lint: markdownlint exited 1"},
		{"no colon at all", "go exited 1", "go exited 1"},
		// Each of these has a second segment that LOOKS like a target and is not
		// one. No marker precedes it, so none of them becomes a hop.
		{"exec.Error is not a hop", `format: exec: "dprint": executable file not found in $PATH`,
			`format: exec: "dprint": executable file not found in $PATH`},
		{"a file:line:col is not a hop", "build: ./main.go:5:2: undefined: Widget",
			"build: ./main.go:5:2: undefined: Widget"},
		{"a URL is not a hop", "fetch: https://example.com/x: 404", "fetch: https://example.com/x: 404"},
		{"a filename is not a hop", "advice-test: config.json: missing key: name",
			"advice-test: config.json: missing key: name"},
		// With markers, the same shapes DO become hops, because the chain says so.
		{"markers name the hops", "test: ctx.needs: advice-test: magus.buzz: buzz -t x.buzz exited 1",
			"test -> advice-test: magus.buzz: buzz -t x.buzz exited 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, hopChain(tc.in))
		})
	}
}

// TestHitFailureResolvesAClickToTheTargetThatFailed is the property that makes
// the band actionable rather than merely visible.
func TestHitFailureResolvesAClickToTheTargetThatFailed(t *testing.T) {
	var buf ttyBuf
	h := newTerminalHandler(&buf)
	require.True(t, h.lease.Enabled())

	for _, target := range []string{"build", "test"} {
		require.NoError(t, h.Handle(context.Background(), buildRecord(
			"cache.error",
			slog.String("project", "api"),
			slog.String("target", target),
			slog.Int64("duration", int64(250*time.Millisecond)),
			slog.String("error", "exit status 2"),
			slog.String("ref", "out-"+target),
		)), "Handle cache.error")
	}

	// The band holds only what it has, and the status now lives in the top
	// rule rather than a row: on a 24-row terminal that is the captioned rule
	// on 21, the two failures on 22 and 23, and the bottom rule on 24.
	_, ok := h.HitFailure(21)
	assert.False(t, ok, "the status row is not a failure")

	got, ok := h.HitFailure(22)
	require.True(t, ok)
	assert.Equal(t, "build", got.Target)
	assert.Equal(t, "api", got.Project)
	assert.Equal(t, "out-build", got.OutputRef, "the ref travels with the row, so the output is one click away")

	got, ok = h.HitFailure(23)
	require.True(t, ok)
	assert.Equal(t, "test", got.Target)

	_, ok = h.HitFailure(24)
	assert.False(t, ok, "the bottom rule is not a failure")

	// Rows above the band belong to the scrolling transcript and the terminal's
	// own selection.
	for _, row := range []int{1, 15, 18} {
		_, ok := h.HitFailure(row)
		assert.False(t, ok, "row %d", row)
	}

	failures := h.Failures()
	require.Len(t, failures, 2, "the keyboard path sees the same list")
	assert.Equal(t, "build", failures[0].Target)
	assert.Equal(t, "test", failures[1].Target)
}

// TestPrettyHandlerResetsPerRunStateAcrossRuns is the long-lived-process
// property: this handler is per-PROCESS, but everything it shows is per-RUN.
//
// Anything that outlives a single run - a TUI left open, the daemon - drives
// more than one through the same handler, and without the split the second run
// reports the first one's failures and a clock that started before it did.
func TestPrettyHandlerResetsPerRunStateAcrossRuns(t *testing.T) {
	t.Parallel()

	var buf ttyBuf
	h := newTerminalHandler(&buf)
	ctx := context.Background()

	require.NoError(t, h.Handle(ctx, buildRecord("cache.scope",
		slog.String("label", "api"), slog.String("source", "vcs"))))
	require.NoError(t, h.Handle(ctx, buildRecord("cache.error",
		slog.String("project", "api"), slog.String("target", "build"),
		slog.Int64("duration", int64(time.Second)), slog.String("error", "boom"))))
	require.NoError(t, h.Handle(ctx, buildRecord("cache.summary",
		slog.Int("hits", 0), slog.Int("misses", 1), slog.Int("errors", 1),
		slog.Int64("elapsed", int64(time.Second)))))
	require.Len(t, h.Failures(), 1)
	firstStart := h.status.start
	require.False(t, firstStart.IsZero())

	// A second run through the SAME handler.
	require.NoError(t, h.Handle(ctx, buildRecord("cache.scope",
		slog.String("label", "api"), slog.String("source", "vcs"))))

	assert.Empty(t, h.Failures(), "the previous run's failures must not carry over")
	assert.Zero(t, h.status.failed, "nor its counters")
	assert.True(t, h.status.start.IsZero(), "nor its clock, or the second run reports the first one's elapsed time")

	require.NoError(t, h.Handle(ctx, buildRecord("cache.hit",
		slog.String("project", "api"), slog.String("target", "build"),
		slog.Int64("duration", int64(time.Millisecond)))))
	assert.Equal(t, 1, h.status.cached)
	assert.Equal(t, 0, h.status.failed)
}

// TestNoEscapeSequencesEverReachAPipe is the CI persona's one demand, as a
// gate rather than a promise.
//
// Everything this package gained - a pinned band, notifications, a selection
// highlight, hyperlinked refs - emits escape sequences, and every one of them
// is supposed to be gated on the writer being a terminal. Gates are easy to add
// and easy to forget, and the failure mode is not subtle: a CI log full of
// \x1b[2m garbage, in the output people read when something is already broken.
//
// So this asserts the whole class at once rather than one gate at a time: drive
// a realistic failing run through a writer with no descriptor and require that
// not one escape byte comes out.
func TestNoEscapeSequencesEverReachAPipe(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer // no Fd(), so never a terminal
	h := NewPrettyHandler(&buf, slog.LevelInfo)
	ctx := context.Background()

	for _, rec := range []slog.Record{
		buildRecord("cache.scope", slog.String("label", "api"), slog.String("source", "vcs")),
		buildRecord("cache.pool", slog.Int("capacity", 8), slog.Int("running", 3), slog.Int("queued", 1)),
		buildRecord("lock.waiting", slog.String("project", "api"),
			slog.String("holder_pid", "4211"), slog.String("holder_command", "magus run build")),
		buildRecord("lock.acquired"),
		buildRecord("cache.hit", slog.String("project", "api"), slog.String("target", "build"),
			slog.Int64("duration", int64(time.Millisecond))),
		buildRecord("cache.miss", slog.String("project", "api"), slog.String("target", "test"),
			slog.Int64("duration", int64(time.Second))),
		buildRecord("cache.error", slog.String("project", "api"), slog.String("target", "test"),
			slog.Int64("duration", int64(2*time.Second)), slog.String("error", "exit status 1"),
			slog.String("ref", "out-7c21"), slog.String("log", "/tmp/magus/logs/api/abc.log")),
		buildRecord("cache.warn", slog.String("msg", "something worth saying")),
		buildRecord("cache.summary", slog.Int("hits", 1), slog.Int("misses", 1),
			slog.Int("errors", 1), slog.Int64("elapsed", int64(3*time.Second))),
	} {
		require.NoError(t, h.Handle(ctx, rec), rec.Message)
	}

	out := buf.String()
	require.NotEmpty(t, out, "the run must still be reported, just plainly")
	assert.NotContains(t, out, "\x1b", "no escape byte may reach a writer that is not a terminal")

	// And the content survives the plainness: a CI log is the copy people read
	// when something is already broken, so it has to carry the actionable bits.
	assert.Contains(t, out, "exit status 1", "the cause")
	assert.Contains(t, out, "out-7c21", "the output ref")
	assert.Contains(t, out, "magus query output out-7c21", "the command that retrieves it")
	assert.Contains(t, out, "1 cached, 1 ran, 1 failed", "the summary")
}

// TestBandHonoursNoColor closes a gap the non-TTY audit exposed: term.notify
// already stripped styling under NO_COLOR while the handler's own band did not,
// so the same run answered the same question two ways.
func TestBandHonoursNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	var buf ttyBuf
	h := newTerminalHandler(&buf)
	ctx := context.Background()
	require.NoError(t, h.Handle(ctx, buildRecord("cache.error",
		slog.String("project", "api"), slog.String("target", "build"),
		slog.Int64("duration", int64(time.Second)), slog.String("error", "boom"))))

	out := buf.String()
	require.Contains(t, out, "api", "the project is still pinned, just not coloured")
	require.Contains(t, out, "build", "and so is the target under it")
	assert.NotContains(t, out, "\x1b[1;31m", "NO_COLOR has no exception for the parts we like")
	assert.NotContains(t, out, "\x1b[2m")

	// The selection survives, because it is not decoration: it says which row a
	// keypress acts on, and reverse video is not a colour.
	buf.Reset()
	h.SetSelection(0)
	assert.Contains(t, buf.String(), "\x1b[7m", "the selection must stay legible under NO_COLOR")
}

// TestClickCoordinatesMatchWhereTheBandActuallyDrew closes a loop that spans
// two packages and had never been checked end to end.
//
// A click resolves through tty.Zone's row arithmetic into this handler's band
// layout. If those two ever disagree by one row, clicking a failure reruns a
// DIFFERENT target than the one under the pointer - silently, and destructively,
// since rerunning is an action. Both sides were tested against their own idea of
// where the rows are; neither was tested against where the text actually landed.
//
// So this asks the terminal. It renders through the emulator, FINDS the row the
// failure was really drawn on, and requires hit-testing to agree.
func TestClickCoordinatesMatchWhereTheBandActuallyDrew(t *testing.T) {
	t.Parallel()
	s := screen.New(80, 24)
	fmt.Fprint(s, "$ magus affected ci\n")
	h := newPrettyHandler(s, slog.LevelInfo, terminalProbe{})
	ctx := context.Background()

	targets := []string{"build", "test", "lint"}
	for _, target := range targets {
		require.NoError(t, h.Handle(ctx, buildRecord("cache.error",
			slog.String("project", "api"),
			slog.String("target", target),
			slog.Int64("duration", int64(time.Second)),
			slog.String("error", "exit status 1"),
			slog.String("ref", "out-"+target))))
	}

	for _, target := range targets {
		// Where the terminal actually put it, not where anyone computed it
		// should go.
		// The band is a tree, so the target sits on its own row under the
		// project header rather than on a row naming both.
		// Matched on the branch too: the transcript above the band also mentions
		// these targets, and FindRow returns the FIRST match.
		drawn := s.FindRow(treeEnd + target)
		if drawn == 0 {
			drawn = s.FindRow(treeTee + target)
		}
		require.NotZero(t, drawn, "the failure for %q must be visible somewhere", target)

		got, ok := h.HitFailure(drawn)
		require.True(t, ok, "row %d shows %q but hit-testing claims nothing is there", drawn, target)
		assert.Equal(t, target, got.Target,
			"clicking the row that displays %q must rerun %q, not %q", target, target, got.Target)
		assert.Equal(t, "out-"+target, got.OutputRef)
	}

	// And the rows above the band, where the transcript lives, belong to the
	// terminal's own selection rather than to magus.
	transcript := s.FindRow("$ magus affected ci")
	require.NotZero(t, transcript)
	_, ok := h.HitFailure(transcript)
	assert.False(t, ok, "a click on the transcript must not resolve to a failure")

	// A project HEADER names no target, so clicking it must resolve to nothing
	// rather than to whichever failure happens to sit beneath it. This is the
	// row the flat-list arithmetic had no concept of.
	header := s.FindRow(glyph(false, "fail", colRed) + " api")
	require.NotZero(t, header)
	_, ok = h.HitFailure(header)
	assert.False(t, ok, "a project header is not a failure")
}

// TestRecordBoolSurvivesAWrongType guards the logging path against a panic.
// slog.Value.Bool panics on a kind mismatch, and this runs inside the handler,
// so a record carrying the wrong type for "dry" would take the process down
// from the one place that is supposed to be reporting problems.
func TestRecordBoolSurvivesAWrongType(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, slog.LevelInfo)
	assert.NotPanics(t, func() {
		_ = h.Handle(context.Background(), buildRecord("cache.summary",
			slog.String("dry", "not a bool"),
			slog.Int("hits", 1), slog.Int("misses", 0), slog.Int("errors", 0),
			slog.Int64("elapsed", int64(time.Second))))
	})
	assert.Contains(t, buf.String(), "1 cached", "and still reports the run")
}

// TestRecordDurAcceptsBothSpellings guards the silent zero. A caller reaching
// for slog.Duration - the obvious constructor - used to get 0 back, because
// only the Int64 spelling was accepted.
func TestRecordDurAcceptsBothSpellings(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 250*time.Millisecond,
		recordDur(buildRecord("x", slog.Int64("duration", int64(250*time.Millisecond))), "duration"))
	assert.Equal(t, 250*time.Millisecond,
		recordDur(buildRecord("x", slog.Duration("duration", 250*time.Millisecond)), "duration"))
	assert.Zero(t, recordDur(buildRecord("x", slog.String("duration", "nope")), "duration"),
		"a wrong type is still zero, not a panic")
	assert.Zero(t, recordDur(buildRecord("x"), "duration"))
}

// TestPrettyHandlerRefLegend covers the one line that makes a bare output ref
// actionable to a reader who has never met one - most often an agent, in a fresh
// worktree, under a tool that installed no magus skills.
func TestPrettyHandlerRefLegend(t *testing.T) {
	t.Parallel()

	summary := func() slog.Record {
		return buildRecord("cache.summary",
			slog.Int("hits", 1), slog.Int("misses", 1), slog.Int("errors", 0),
			slog.Int64("elapsed", int64(time.Second)))
	}

	t.Run("printed once when a ref was minted", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		h := newTestHandler(&buf)
		require.NoError(t, h.Handle(context.Background(), buildRecord(
			"cache.miss",
			slog.String("project", "api"),
			slog.Int64("duration", int64(80*time.Millisecond)),
			slog.String("ref", "out1a2b3c4d5e6f"),
		)), "miss")
		require.NoError(t, h.Handle(context.Background(), summary()), "summary")

		out := buf.String()
		assert.Contains(t, out, "outputs: magus query output <ref>")
		assert.Equal(t, 1, strings.Count(out, "outputs: magus query output"),
			"the legend is per run, not per target")
	})

	// A run that minted none would otherwise explain a notation nothing on
	// screen used.
	t.Run("absent when no ref was minted", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		h := newTestHandler(&buf)
		require.NoError(t, h.Handle(context.Background(), summary()), "summary")
		assert.NotContains(t, buf.String(), "outputs:")
	})

	// The legend names a magus command and nothing downstream of it. A vendor in
	// a build tool's output ages badly and is the docs' job, not the CLI's.
	t.Run("names no agent vendor", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		h := newTestHandler(&buf)
		require.NoError(t, h.Handle(context.Background(), buildRecord(
			"cache.miss",
			slog.String("project", "api"),
			slog.Int64("duration", int64(80*time.Millisecond)),
			slog.String("ref", "out1a2b3c4d5e6f"),
		)), "miss")
		require.NoError(t, h.Handle(context.Background(), summary()), "summary")
		for _, vendor := range []string{"claude", "codex", "cursor", "copilot"} {
			assert.NotContains(t, strings.ToLower(buf.String()), vendor)
		}
	})
}

// TestPoolGaugeIsSlotForSlot pins the meaning, not the look: one mark is one
// slot, which is what makes it readable as the console's cubes are.
func TestPoolGaugeIsSlotForSlot(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "■ ■ ■ □ □ □ □ □ (3/8)", PoolGauge(3, 8))
	assert.Equal(t, "□ □ □ □ (0/4)", PoolGauge(0, 4))
	assert.Equal(t, "■ ■ ■ ■ (4/4)", PoolGauge(4, 4), "a saturated pool is solid")
	assert.Equal(t, 5, strings.Count(PoolGauge(2, 5), "■")+strings.Count(PoolGauge(2, 5), "□"), "one mark per slot, always")
}

// TestPoolGaugeDropsRatherThanScales is the honesty guard. A scaled bar looks
// exactly like a literal one while meaning something else, so past the cap the
// gauge is omitted and the numbers carry it alone.
func TestPoolGaugeDropsRatherThanScales(t *testing.T) {
	t.Parallel()
	assert.Empty(t, PoolGauge(30, 64), "too many slots to draw one-for-one")
	assert.Empty(t, PoolGauge(0, 0), "no pool, no gauge")
	assert.Equal(t, slotCap, strings.Count(PoolGauge(1, slotCap), "■")+strings.Count(PoolGauge(1, slotCap), "□"), "the cap itself still draws")
}

// TestPoolGaugeClampsImpossibleCounts: a sample can report more running than
// capacity across a resize, and a gauge must not panic or overdraw.
func TestPoolGaugeClampsImpossibleCounts(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "■ ■ ■ ■ (4/4)", PoolGauge(9, 4))
	assert.Equal(t, "□ □ □ □ (0/4)", PoolGauge(-1, 4))
}

// TestPreviewMakesASecondColumn is the "two views, one run" surface: the
// failure tree on the left, the selected failure's captured output on the
// right, in rows this handler already owns.
func TestPreviewMakesASecondColumn(t *testing.T) {
	t.Parallel()
	s := screen.New(120, 24)
	h := newPrettyHandler(s, slog.LevelInfo, terminalProbe{})
	require.NoError(t, h.Handle(context.Background(), buildRecord("cache.error",
		slog.String("project", "api"), slog.String("target", "test"),
		slog.Int64("duration", int64(time.Second)), slog.String("error", "boom"))))

	h.SetPreview([]string{"--- FAIL: TestThing", "    thing_test.go:42: boom"})

	// Both columns are on the SAME rows, so neither can scroll away from the
	// other and the transcript above is untouched.
	row := s.FindRow("--- FAIL: TestThing")
	require.NotZero(t, row, "the output is shown")
	assert.Contains(t, s.Row(row), previewDivider, "divided from the left column")

	// And the band grew to fit the taller column rather than truncating it.
	assert.NotZero(t, s.FindRow("thing_test.go:42: boom"),
		"the preview is not clipped to the number of failures")
}

// TestPreviewIsNotClickable: only the left column names targets. A click on the
// output must not resolve to whatever failure shares its row.
func TestPreviewIsNotClickable(t *testing.T) {
	t.Parallel()
	s := screen.New(120, 24)
	h := newPrettyHandler(s, slog.LevelInfo, terminalProbe{})
	require.NoError(t, h.Handle(context.Background(), buildRecord("cache.error",
		slog.String("project", "api"), slog.String("target", "test"),
		slog.Int64("duration", int64(time.Second)), slog.String("error", "boom"))))
	h.SetPreview([]string{"one", "two", "three", "four"})

	// A row that exists only because the preview is taller than the tree.
	row := s.FindRow("four")
	require.NotZero(t, row)
	_, ok := h.HitFailure(row)
	assert.False(t, ok, "a row carrying only output is not a failure")
}

// TestPreviewClearsBackToOneColumn: dropping the preview must not leave the
// divider behind.
func TestPreviewClearsBackToOneColumn(t *testing.T) {
	t.Parallel()
	s := screen.New(120, 24)
	h := newPrettyHandler(s, slog.LevelInfo, terminalProbe{})
	require.NoError(t, h.Handle(context.Background(), buildRecord("cache.error",
		slog.String("project", "api"), slog.String("target", "test"),
		slog.Int64("duration", int64(time.Second)), slog.String("error", "boom"))))
	h.SetPreview([]string{"PREVIEWLINE"})
	require.NotZero(t, s.FindRow("PREVIEWLINE"))

	h.SetPreview(nil)
	assert.Zero(t, s.FindRow("PREVIEWLINE"), "the second column is gone")
}

// TestSplitFollowsTheGoldenRatio pins the proportion, not a column number.
//
// A fixed split is only ever right at one width: 34 columns is a third of a
// 100-column window and nearly half an 80-column one, so the same layout read
// as balanced on one machine and cramped on another. The ratio holds at every
// width, and the two shares sum to one, so focus TRADES the space rather than
// reflowing the layout around a third number.
func TestSplitFollowsTheGoldenRatio(t *testing.T) {
	t.Parallel()
	for _, inner := range []int{80, 100, 132, 200} {
		usable := inner - len(previewDivider)
		tree := splitAt(inner, FocusTree)
		preview := splitAt(inner, FocusPreview)

		assert.InDelta(t, phiMajor, float64(tree)/float64(usable), 0.01,
			"the focused tree takes the major share at width %d", inner)
		assert.InDelta(t, phiMinor, float64(preview)/float64(usable), 0.01,
			"and yields it when the preview is focused at width %d", inner)
		assert.Equal(t, usable, tree+(usable-tree), "the shares account for every column")
	}
}

// TestSplitStaysLegibleWhenNarrow: a share of a small number rounds to nothing
// useful, so both views keep a floor.
func TestSplitStaysLegibleWhenNarrow(t *testing.T) {
	t.Parallel()
	for _, inner := range []int{10, 20, 26, 40} {
		at := splitAt(inner, FocusPreview)
		assert.GreaterOrEqual(t, at, 0, "never negative at width %d", inner)
		assert.LessOrEqual(t, at, inner, "never wider than the band at width %d", inner)
	}
}

// TestFitsInBandDrawsTheLastFailureWhenItFits is the off-by-one regression: the
// overflow row used to be reserved before it was known to be needed, so a set
// that fitted exactly lost its final failure to a "+1 more" line counting one.
func TestFitsInBandDrawsTheLastFailureWhenItFits(t *testing.T) {
	t.Parallel()

	// One project, five failures: a header row plus five rows is six.
	drawn := make([]Failure, 5)
	for i := range drawn {
		drawn[i] = Failure{Project: "web", Target: fmt.Sprintf("t%d", i)}
	}
	assert.Equal(t, 5, fitsInBand(drawn, 6), "an exact fit draws every failure")
	assert.Equal(t, 6, bandRows(drawn), "header plus one row each")

	// One row short: the overflow line is now real and takes a row of its own.
	assert.Less(t, fitsInBand(drawn, 5), 5, "over budget, a row goes to the overflow line")
}

// TestFitsInBandCountsAHeaderPerProject keeps the budget honest across the
// grouped tree, where each project change costs a row nothing else pays for.
func TestFitsInBandCountsAHeaderPerProject(t *testing.T) {
	t.Parallel()

	drawn := []Failure{
		{Project: "web", Target: "test"},
		{Project: "api", Target: "test"},
	}
	assert.Equal(t, 4, bandRows(drawn), "two projects, two headers, two rows")
	assert.Equal(t, 2, fitsInBand(drawn, 4))
	assert.Less(t, fitsInBand(drawn, 3), 2, "three rows cannot hold both trees")
}
