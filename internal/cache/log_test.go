package cache

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/interactive"
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
	require.NotNil(t, h.region, "region should be reserved on a TTY writer")
	require.True(t, h.region.Enabled(), "region should be enabled for 24x80 writer")

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
	assert.Contains(t, out, "[fail] api build (ran,", "heading should be present")
	assert.Contains(t, out, "cause: exit status 2", "cause line stays in scroll region")
	assert.Contains(t, out, "output: out-42", "output line stays in scroll region")
	assert.Contains(t, out, "inspect: magus query output out-42", "inspect line stays in scroll region")
	assert.Contains(t, out, "\x1b[1;18r", "scroll margins reserve the bottom rows for the sticky region")
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
	require.NoError(t, h.Handle(context.Background(), buildRecord(
		"cache.summary",
		slog.Int("hits", 3),
		slog.Int("misses", 1),
		slog.Int("errors", 1),
		slog.Int64("elapsed", int64(2*time.Second)),
	)), "Handle cache.summary")

	out := buf.String()
	// The writer reports a descriptor and the probe calls it a terminal,
	// so the summary takes the "Summary:" form. The sticky-region
	// lifecycle is what this test cares about.
	assert.Contains(t, out, "3 cached, 1 ran, 1 failed", "summary line present")
	assert.Regexp(t, `\x1b\[\d+;\d+r`, out, "DECSTBM was set by the sticky region")
	assert.Contains(t, out, "\x1b[r", "DECSTBM is reset on cache.summary")
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
	assert.False(t, h.region.Enabled(), "region should be disabled on a non-TTY writer")

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
		{"pool only, nothing done yet", statusLine{capacity: 8, running: 3}, "pool 3/8 running"},
		{"queued work is shown", statusLine{capacity: 8, running: 8, queued: 2}, "pool 8/8 running, 2 queued"},
		{"a quiet queue is omitted", statusLine{capacity: 4}, "pool 0/4 running"},
		// A blocked run leads, because the pool counters alone would read as a stall
		// with no cause. This is the clause that describes doing nothing.
		{"a blocked run leads with the wait", statusLine{capacity: 8, blocked: "web/api"}, "WAITING on lock: web/api   pool 0/8 running"},
		{"the holder is named when known", statusLine{capacity: 8, blocked: ".", blockedBy: "pid 71557 (magus run serve)"}, "WAITING on lock: . (held by pid 71557 (magus run serve))   pool 0/8 running"},
		{
			"tally appears once work completes",
			statusLine{capacity: 8, running: 2, passed: 3, cached: 2},
			"pool 2/8 running   5 ok (2 cached)",
		},
		{
			"failures are called out",
			statusLine{capacity: 8, running: 1, passed: 4, failed: 1},
			"pool 1/8 running   4 ok  1 failed",
		},
		{
			"a clean run shows no cached or failed clause",
			statusLine{capacity: 8, running: 1, passed: 4},
			"pool 1/8 running   4 ok",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.line.render(base))
		})
	}
}

// TestStatusLineRenderShowsElapsed keeps the progress readout honest: the
// row is what a reader watches to know a long run is still moving.
func TestStatusLineRenderShowsElapsed(t *testing.T) {
	t.Parallel()
	start := time.Now()
	line := statusLine{capacity: 4, running: 1, start: start}
	assert.Contains(t, line.render(start.Add(90*time.Second)), "1m30s")
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
	assert.Contains(t, out, "pool 3/8 running, 1 queued", "the pool sample is rendered")
	assert.Contains(t, out, "\x1b[19;1H", "it is pinned to the region's first row")
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
	assert.False(t, newTestHandler(&plain).rendersStatus(),
		"a buffer-backed handler has no region to paint into")

	var term ttyBuf
	assert.True(t, newTerminalHandler(&term).rendersStatus(),
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
		"the repeated ctx.needs plumbing is dropped, and the target hops read as a path")
	assert.Equal(t, "test -> advice-test -> magus.buzz: buzz -t blast-radius.buzz exited with code 1", got[1],
		"the message keeps its own colons; only the target hops become arrows")
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
func TestArrowChainLeavesAmbiguousCasesAlone(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"single hop", "lint: markdownlint exited 1", "lint: markdownlint exited 1"},
		{"two hops", "build -> go-build: exited 2", "build -> go-build: exited 2"},
		{"colons inside the message survive", "test -> advice: buzz -t x.buzz: exited 1",
			"test -> advice: buzz -t x.buzz: exited 1"},
		{"no colon at all", "go exited 1", "go exited 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, arrowChain(strings.ReplaceAll(tc.in, " -> ", ": ")))
		})
	}
}
