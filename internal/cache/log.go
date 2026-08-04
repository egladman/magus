package cache

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/ci/annotate"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/secret"
)

// levelTrace mirrors config.LevelTrace (slog.LevelDebug-4); duplicated here
// because config imports cache, so cache cannot import config back.
const levelTrace slog.Level = slog.LevelDebug - 4

// newLogger returns a *slog.Logger for the given format ("text", "json", or "pretty") and level.
//
// Human formats (pretty, plain) render to stderr so stdout stays clean for machine
// output; json/text keep their slog handlers. Pretty uses the shared PrettyHandler,
// which is also installed as the process-wide default logger (see cmd/magus) so that
// general diagnostics render in the same compact style as cache events instead of raw
// "time=... level=..." lines interleaving with the pretty output.
func newLogger(format string, level slog.Level) *slog.Logger {
	switch strings.ToLower(format) {
	case "text":
		return slog.New(secret.NewRedactingHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	case "json":
		return slog.New(secret.NewRedactingHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
	default:
		return slog.New(NewPrettyHandler(os.Stderr, level))
	}
}

// PrettyHandler renders both cache events (known cache.* messages) and general
// diagnostics in a compact, scannable style: coloured ASCII status glyphs on a TTY,
// bracketed prefixes on plain streams. It carries no timestamps or level=/key=
// boilerplate; that noise is what makes raw slog output hard to read interactively.
//
// On a TTY, errors are written into a sticky region at the bottom of the
// terminal so they never scroll off, under a live pool status line (scroll
// margins are set on the first cache.pool or cache.error, and reset on
// cache.summary or Close). Non-TTY writers fall through to plain output;
// the region is harmless when disabled.
type PrettyHandler struct {
	mu    sync.Mutex
	w     io.Writer // write destination (os.Stderr in production; bytes.Buffer in tests)
	probe tty.Probe
	level slog.Level
	// err latches the first write failure seen during a single Handle
	// call so the many print helpers can stay linear instead of each
	// growing an error branch. Reset at the top of Handle, returned at
	// the bottom; guarded by mu like every other field.
	err error
	// recordCtx is the context of the record currently being handled, held so printf
	// can reach the run's secret resolver without every print helper taking a ctx.
	// Set and cleared in Handle; guarded by mu like every other field.
	recordCtx context.Context
	region    *tty.Region // sticky error region; disabled when the writer is not a TTY
	status    statusLine  // live counters painted into the region's first row
	// onCI suppresses hints whose command cannot work where it is printed.
	// Resolved once at construction: the environment does not change
	// mid-run, and this is consulted per failure.
	onCI bool
}

// statusLine accumulates what the sticky region's top row shows: pool
// occupancy plus the running tally, so a reader watching the bottom of
// the screen knows how far along the run is without scrolling.
//
// Counters live here rather than being read back from the Cache because
// the handler already sees every event that changes them, and a handler
// that reached into the cache for state it was just told about would
// invert the direction the rest of this file flows.
type statusLine struct {
	capacity, running, queued int
	passed, failed, cached    int
	// start is when the first event arrived, which is close enough to the
	// run's start for a progress readout and needs no plumbing.
	start time.Time
	// blocked names the project this run is waiting on a lock for, and who holds
	// it. It is the one clause that describes a run doing NOTHING, which is exactly
	// why it belongs in a region that does not scroll: the log line announcing the
	// wait scrolls away, and what is left on screen is silence.
	blocked, blockedBy string
}

// render composes the status row. Clauses that carry no information are
// omitted: a steady "0 queued" or "0 failed" is noise on a line whose
// whole job is to show change.
func (s statusLine) render(now time.Time) string {
	var b strings.Builder
	// Leads, because a blocked run is not making progress and the pool counters
	// below it would otherwise read as a stall with no explanation.
	if s.blocked != "" {
		fmt.Fprintf(&b, "WAITING on lock: %s", s.blocked)
		if s.blockedBy != "" {
			fmt.Fprintf(&b, " (held by %s)", s.blockedBy)
		}
		b.WriteString("   ")
	}
	fmt.Fprintf(&b, "pool %d/%d running", s.running, s.capacity)
	if s.queued > 0 {
		fmt.Fprintf(&b, ", %d queued", s.queued)
	}
	if done := s.passed + s.cached + s.failed; done > 0 {
		fmt.Fprintf(&b, "   %d ok", s.passed+s.cached)
		if s.cached > 0 {
			fmt.Fprintf(&b, " (%d cached)", s.cached)
		}
		if s.failed > 0 {
			fmt.Fprintf(&b, "  %d failed", s.failed)
		}
	}
	if !s.start.IsZero() {
		fmt.Fprintf(&b, "   %s", fmtDur(now.Sub(s.start)))
	}
	return b.String()
}

// paintStatus repaints the region's status row. It is called after every
// event that moves a counter, so the row tracks the run rather than only
// the pool samples that first populated it. A disabled region drops it.
func (h *PrettyHandler) paintStatus() {
	if !h.region.Enabled() {
		return
	}
	if h.status.start.IsZero() {
		h.status.start = time.Now()
	}
	h.fail(h.region.SetStatus(h.status.render(time.Now())))
}

// NewPrettyHandler builds the unified pretty handler. Colour is driven by the
// terminal-ness of w's file descriptor (any writer exposing Fd(), not just an
// *os.File); a writer without one -- a bytes.Buffer in tests, a pipe -- renders
// plain. TTY detection runs per-Handle so late redirects are noticed.
//
// On a TTY writer, a sticky region is reserved at the bottom of the terminal:
// a live pool status line on top, then [fail] lines that do not scroll off.
// The reservation is lazy (the region opens on the first cache.pool or
// cache.error) and reset on cache.summary or Close; a pipe / bytes.Buffer /
// non-TTY writer disables it.
func NewPrettyHandler(w io.Writer, level slog.Level) *PrettyHandler {
	return newPrettyHandler(w, level, tty.SystemProbe)
}

// newPrettyHandler is the probe-injecting form. Tests use it to render
// terminal output into a buffer without opening a pty.
func newPrettyHandler(w io.Writer, level slog.Level, p tty.Probe) *PrettyHandler {
	return &PrettyHandler{
		w:      w,
		probe:  p,
		level:  level,
		region: tty.NewRegion(w, stickyRegionRows, p),
		onCI:   annotate.OnCI(),
	}
}

// stickyRegionRows is how many terminal rows the sticky region claims:
// one for the live pool status line, five for failures. Small on
// purpose, so the scrolling output above stays the main view.
const stickyRegionRows = 6

// rendersStatus reports whether this handler has a live region to paint
// a status line into. The cache asks before emitting pool samples, so a
// piped, JSON, or CI run pays nothing for a feature it cannot show.
func (h *PrettyHandler) rendersStatus() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.region.Enabled()
}

// Close releases the sticky error region if one was reserved. Idempotent
// and safe to defer; required so a panic or interrupted run does not leave
// the terminal with the scroll margins still set. The next non-magus
// command the user runs inherits a clean terminal state.
func (h *PrettyHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.region.Release()
}

// wantsColor reports whether output to this writer should carry ANSI
// colour: the writer must be a terminal, and NO_COLOR must be unset.
// It is consulted per record so a late redirect is noticed.
//
// The descriptor comes from tty.Fd, so any writer exposing Fd() is
// treated uniformly and the region sees the same writer this does.
func (h *PrettyHandler) wantsColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fd, ok := tty.Fd(h.w)
	if !ok {
		return false
	}
	return h.probe.IsTerminal(fd)
}

// fail latches the first write error of the current record. Reading and
// writing h.err is safe because Handle holds h.mu for the whole record.
func (h *PrettyHandler) fail(err error) {
	if err != nil && h.err == nil {
		h.err = err
	}
}

// printf writes one formatted line to the handler's writer, latching
// the first error so callers stay linear. Once a write has failed the
// rest of the record is skipped: a broken stderr will not recover
// mid-line, and continuing would only pile up identical errors.
func (h *PrettyHandler) printf(format string, args ...any) {
	if h.err != nil {
		return
	}
	// Redacted at the funnel, not per record type. This handler renders a dozen kinds
	// (run.exec, cache.stage, charms, ...) and any of them can carry a value a magusfile
	// read through magus\secret.read - run.exec does, because it echoes the argv of a
	// command a target may have passed a token to. Redacting each kind separately is the
	// rule that gets forgotten when the thirteenth is added; this is every line the
	// handler will ever print, in one place.
	//
	// The resolver comes from the record context captured in Handle rather than a
	// parameter, so nothing that calls printf has to know secrets exist.
	_, err := fmt.Fprintf(h.w, "%s", secret.RedactString(h.recordCtx, fmt.Sprintf(format, sanitizeArgs(args)...)))
	h.fail(err)
}

// sanitizeArgs strips terminal control characters from the values interpolated into
// a log line, for the same reason redaction happens at this funnel rather than per
// record kind: it is every line the handler will ever print, in one place.
//
// Project names, paths, branch names and captured error excerpts are all
// workspace-controlled, and this handler writes straight to a TTY. A project
// directory named with an ESC repaints the user's screen, and a CR plus a crafted
// string forges a magus status line - a hostile repo can print its own
// "[pass] security-check (cached, 3ms)" that is byte-indistinguishable from a real
// one. Only the ARGUMENTS are sanitized: the format strings are magus's own and
// legitimately carry SGR colour codes.
func sanitizeArgs(args []any) []any {
	for i, a := range args {
		if s, ok := a.(string); ok {
			args[i] = stripControl(s)
		}
	}
	return args
}

// stripControl removes C0, DEL and C1 control characters. It returns s unchanged
// when there is nothing to strip, which is the overwhelmingly common case.
func stripControl(s string) string {
	if !strings.ContainsFunc(s, isControlRune) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isControlRune(r) {
			return -1
		}
		return r
	}, s)
}

func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

func (h *PrettyHandler) Enabled(_ context.Context, lvl slog.Level) bool { return lvl >= h.level }
func (h *PrettyHandler) WithAttrs(_ []slog.Attr) slog.Handler           { return h }
func (h *PrettyHandler) WithGroup(_ string) slog.Handler                { return h }

// Handle renders one record. It deliberately does NOT skip on ctx.Err(): a handler must
// not treat cancellation as permission to drop output. The check that used to live here
// was inert for as long as it existed, because every call site reached slog through
// Logger.Info/Warn/..., which passes context.Background() - Err() was never non-nil. Once
// the run path started passing its REAL context (so records could reach the secret
// resolver), it woke up and began eating exactly the lines that matter most: in a
// concurrent run, the first failure cancels the errgroup, and every [pass]/[fail] that
// finished afterwards, plus the [summary] footer and the Ctrl-C service-release warning,
// vanished from the default output while -o json still showed them.
func (h *PrettyHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Held for the duration of this record so printf can reach the run's secret
	// resolver. Guarded by the same mutex as every other field here.
	h.recordCtx = ctx
	defer func() { h.recordCtx = nil }()

	// One record, one error: clear the latch on entry so a failure on an
	// earlier record does not suppress this one's output.
	h.err = nil

	colorize := h.wantsColor()
	project := recordStr(r, "project") // real path; used for the runnable repro command
	label := displayProjectLabel(recordStr(r, "label"), project)
	dur := recordDur(r, "duration")
	ref := recordStr(r, "ref") // target-output reference id (see internal/cache/output_store.go)
	// Time this step spent waiting on magus's own remote I/O. Rendered INSIDE the parens
	// beside the duration, because it qualifies that number rather than adding a fact:
	// "6.1s" and "6.1s, 4.2s remote" describe different problems with opposite fixes.
	remote := remoteSuffix(recordDur(r, "remote_ns"), dur)

	switch r.Message {
	case "lock.waiting":
		// State, not a failure: the run is correctly queued behind a peer. It is
		// pinned rather than logged-and-forgotten because the wait is unbounded.
		h.status.blocked = recordStr(r, "project")
		// Composed here, from fields: the emitting package sends who the holder IS,
		// this package decides how a terminal shows it.
		if pid := recordStr(r, "holder_pid"); pid != "" && pid != "0" {
			h.status.blockedBy = "pid " + pid
			if cmd := recordStr(r, "holder_command"); cmd != "" {
				h.status.blockedBy += " (" + cmd + ")"
			}
		} else {
			h.status.blockedBy = ""
		}
		h.paintStatus()
		return h.err
	case "lock.acquired":
		h.status.blocked, h.status.blockedBy = "", ""
		h.paintStatus()
		return h.err
	case "cache.hit":
		// Cached: passed without running. Dimmed green so a cache hit reads as
		// low-signal next to work that actually ran. Cache state lives in the parens,
		// mirroring the cross-tool convention (e.g. Bazel's "(cached) PASSED").
		h.printf("%s %s (cached, %s%s)\n", h.glyph(colorize, "pass", colDimGreen), label, fmtDur(dur), remote)
		h.printRepro(colorize, project, recordStr(r, "target"))
		h.printRef(colorize, ref)
		h.status.cached++
		h.paintStatus()
	case "cache.miss":
		h.printf("%s %s (ran, %s%s)\n", h.glyph(colorize, "pass", colGreen), label, fmtDur(dur), remote)
		h.printRepro(colorize, project, recordStr(r, "target"))
		h.printRef(colorize, ref)
		h.status.passed++
		h.paintStatus()
	case "cache.error":
		h.printFailure(colorize, label, project, recordStr(r, "target"), dur, recordStr(r, "error"), ref)
		h.status.failed++
		h.paintStatus()
	case "cache.warn":
		h.printf("%s %s\n", h.glyph(colorize, "warn", colYellow), recordStr(r, "msg"))
	case "cache.pool":
		// A live occupancy sample, folded into the row pinned at the top of
		// the sticky region. Deliberately not printed on a non-TTY: this
		// fires twice per step, and replaying every sample into a pipe or a
		// CI log would bury the actual results. paintStatus drops it when
		// the region is disabled, so no branch is needed here.
		h.status.capacity = recordInt(r, "capacity")
		h.status.running = recordInt(r, "running")
		h.status.queued = recordInt(r, "queued")
		h.paintStatus()
	case "cache.summary":
		elapsed := recordDur(r, "elapsed")
		// A dry run ends with the same footer a real one does - that is the whole
		// point of routing it through this event - but it cannot borrow the real
		// wording: nothing executed, so "cached / ran / failed" would all read 0
		// for a plan that intends to run plenty. Dry-ness is stated here and
		// nowhere else in the run, so the two outputs differ in one line.
		lead := "summary: "
		if colorize {
			lead = "\nSummary: "
		}
		if recordBool(r, "dry") {
			h.printf("%sdry run - %s would run (%s)\n",
				lead, plural(recordInt(r, "planned"), "target"), fmtDur(elapsed))
		} else {
			h.printf("%s%d cached, %d ran, %d failed (%s)\n",
				lead, recordInt(r, "hits"), recordInt(r, "misses"), recordInt(r, "errors"), fmtDur(elapsed))
		}
		// End of run: release the sticky error region so the user's
		// shell prompt returns to a clean full-screen terminal. Safe
		// to call when the region was never opened (idempotent).
		h.fail(h.region.Release())
	case "cache.dry.banner":
		if colorize {
			h.printf("\x1b[2mdry run - commands shown, not executed\x1b[0m\n")
		} else {
			h.printf("dry run - commands shown, not executed\n")
		}
	case "cache.dry":
		// Neutral glyph: a dry run has no pass/fail outcome (nothing executes), and
		// no duration for the same reason. Everything else matches the executed
		// line - including the repro command underneath - so a plan and a run read
		// the same way and only the glyph and the footer say which one you got.
		h.printf("%s %s\n", h.glyph(colorize, "dry", colDim), label)
		h.printRepro(colorize, recordStr(r, "project"), recordStr(r, "target"))
	case "cache.scope":
		label := recordStr(r, "label")
		source := recordStr(r, "source")
		if source != "" {
			h.printf("projects: %s (%s)\n", label, source)
		} else {
			h.printf("projects: %s\n", label)
		}
	case "cache.charms":
		if charms := recordStr(r, "charms"); charms != "" {
			h.printf("charms: %s\n", charms)
		} else {
			h.printf("charms: (none)\n")
		}
	case "run.exec":
		// Every subprocess magus spawns (os.exec, fork spells) logs through this event
		// in run.Exec. Rendered as a shell-style echo, indented under the owning
		// project/stage. At debug level it surfaces with -v during a real run; in a dry
		// run run.Exec logs it at info so the planned commands always show.
		cmd := recordStr(r, "cmd")
		if args := recordStrs(r, "args"); len(args) > 0 {
			cmd += " " + strings.Join(args, " ")
		}
		if colorize {
			h.printf("  \x1b[2m$ %s\x1b[0m\n", cmd)
		} else {
			h.printf("  $ %s\n", cmd)
		}
	case "cache.stage":
		// One indented line per magus.needs sub-target as it completes, so a collapsed
		// project still shows what ran. Project-qualified because stages from concurrently
		// running projects interleave. A stage always ran, so it is pass/fail only.
		target := recordStr(r, "target")
		name, color := "pass", colGreen
		if recordStr(r, "error") != "" {
			name, color = "fail", colRed
		}
		h.printf("  %s %s %s (%s)\n", h.glyph(colorize, name, color), label, target, fmtDur(dur))
	default:
		h.handleGeneric(colorize, r)
	}
	return h.err
}

// displayProjectLabel keeps the real project path for commands while making the
// root project readable even when a caller did not provide its workspace label.
func displayProjectLabel(label, project string) string {
	label = strings.TrimSpace(label)
	if label != "" && label != "." {
		return label
	}
	project = strings.TrimSpace(project)
	if project == "" || project == "." {
		return "workspace"
	}
	return project
}

// ANSI colour codes used by the status glyphs. Cache state is conveyed by colour as
// well as by the parenthetical: a cached pass is dim, a fresh run is bright green.
const (
	colDimGreen = "2;32" // cached (passed without running) — low signal
	colGreen    = "32"   // ran and passed
	colRed      = "31"   // failed
	colYellow   = "33"   // warning
	colDim      = "2"    // info/debug
)

// glyph renders a bracketed status glyph like "[pass]" or "[fail]", ASCII only (no
// Unicode symbols or emoji), coloured only on a TTY. pass/fail are the per-target
// outcome words; cache state (cached vs ran) is shown separately in the line's
// parenthetical, the orthogonal split every major build tool uses (e.g. Bazel's
// "(cached) PASSED"). Named to match the doctor command's statusGlyph.
func (h *PrettyHandler) glyph(colorize bool, label, color string) string {
	s := "[" + label + "]"
	if colorize {
		return "\x1b[" + color + "m" + s + "\x1b[0m"
	}
	return s
}

// handleGeneric renders any non-cache slog record (the 76-odd general diagnostics
// across the codebase) in the same compact style: a level glyph, the message, and any
// attrs trailing dimmed. No timestamp or level= boilerplate. The "dir" attr that the
// process-wide handler stamps on every context-aware record is suppressed above debug
// level, since it is a correlation aid, not something a reader needs on each line.
func (h *PrettyHandler) handleGeneric(colorize bool, r slog.Record) {
	label, color := "debug", colDim
	switch {
	case r.Level >= slog.LevelError:
		label, color = "error", colRed
	case r.Level >= slog.LevelWarn:
		label, color = "warn", colYellow
	case r.Level >= slog.LevelInfo:
		label, color = "info", colDim
	}
	attrs := formatAttrs(r)
	if colorize && attrs != "" {
		attrs = "\x1b[2m" + attrs + "\x1b[0m"
	}
	h.printf("%s %s%s\n", h.glyph(colorize, label, color), r.Message, attrs)
}

// formatAttrs renders a record's attrs as " key=value" pairs, skipping the noisy
// "dir" correlation attr unless the record is at debug level or below.
func formatAttrs(r slog.Record) string {
	var b strings.Builder
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "dir" && r.Level > slog.LevelDebug {
			return true
		}
		_, _ = fmt.Fprintf(&b, " %s=%s", a.Key, a.Value.String())
		return true
	})
	return b.String()
}

// printRepro prints the standalone `magus run <target> <project>` for a result.
func (h *PrettyHandler) printRepro(colorize bool, project, target string) {
	if project == "" || target == "" {
		return
	}
	repro := clihint.Run.With(target, project)
	if colorize {
		h.printf("  \x1b[2m%s\x1b[0m\n", repro)
	} else {
		h.printf("  %s\n", repro)
	}
}

// printFailure keeps the failure and every useful next step together so a reader
// does not need to infer which concurrent project, target, or captured log failed.
//
// The heading goes to the sticky error region (so it never scrolls off)
// when one is reserved; the trailing cause / output / inspect / reproduce
// lines stay in the scrolling region so the user can copy them with
// normal terminal selection. Non-TTY writers and disabled regions fall
// through to plain output unchanged.
func (h *PrettyHandler) printFailure(colorize bool, label, project, target string, dur time.Duration, cause, ref string) {
	heading := label
	if target != "" {
		heading += " " + target
	}
	if h.region.Enabled() {
		// Write only the heading to the sticky region; the trailing
		// lines (cause, output, inspect, reproduce) stay in the
		// scrolling region above where the user can select them
		// without fighting the live update.
		//
		// The glyph is rendered uncoloured here even though this is a
		// TTY: the region wraps the whole line in bold red, and a
		// coloured glyph would close that with its own reset, leaving
		// the project and target after it in the default colour.
		line := fmt.Sprintf("%s %s (ran, %s)", h.glyph(false, "fail", colRed), heading, fmtDur(dur))
		if err := h.region.WriteLine(line); err != nil {
			// Deliberately not latched. Latching here would short-circuit
			// printf and swallow the cause, output ref, and reproduce
			// command below -- the detail the user most needs on the one
			// path where the terminal is already misbehaving. Repeat the
			// heading plainly instead so the failure is never invisible.
			h.printf("%s %s (ran, %s)\n", h.glyph(colorize, "fail", colRed), heading, fmtDur(dur))
		}
	} else {
		h.printf("%s %s (ran, %s)\n", h.glyph(colorize, "fail", colRed), heading, fmtDur(dur))
	}
	if cause != "" {
		h.printf("  cause: %s\n", failureCauseExcerpt(cause))
	}
	if ref != "" {
		h.printf("  output: %s\n", ref)
		// The inspect hint is suppressed on CI. An output ref addresses a
		// blob in the local cache of the machine that produced it, so on an
		// ephemeral runner the command is guaranteed not to work for the
		// person reading the log - and the runner has already dumped the
		// failing output inline above it, which is what they wanted anyway.
		// Printing an un-runnable command next to the answer is worse than
		// printing nothing. The ref itself stays: it correlates this failure
		// with the run's journal and with the console.
		if !h.onCI {
			full := clihint.QueryOutput.With(ref)
			if colorize {
				h.printf("  \x1b[2minspect: %s\x1b[0m\n", full)
			} else {
				h.printf("  inspect: %s\n", full)
			}
		}
	} else {
		h.printf("  output: unavailable (no output was captured)\n")
	}
	if project == "" || target == "" {
		return
	}
	repro := clihint.Run.With(target, project)
	if colorize {
		h.printf("  \x1b[2mreproduce: %s\x1b[0m\n", repro)
	} else {
		h.printf("  reproduce: %s\n", repro)
	}
}

func failureCauseExcerpt(cause string) string {
	const maxRunes = 240
	cause = strings.Join(strings.Fields(cause), " ")
	if len([]rune(cause)) <= maxRunes {
		return cause
	}
	return string([]rune(cause)[:maxRunes-1]) + "…"
}

// printRef prints a successful target's output reference id on its own line.
func (h *PrettyHandler) printRef(colorize bool, ref string) {
	if ref == "" {
		return
	}
	// Labeled like the failure path's "output:" line: a bare hex token at the
	// end of a passing run reads as debris, and nothing in-band connects it to
	// the command that expands it. The inspect hint is suppressed on CI for the
	// same reason as in printFailure: the ref addresses this machine's cache.
	line := "output: " + ref
	if !h.onCI {
		line += " - inspect: " + clihint.QueryOutput.With(ref)
	}
	if colorize {
		h.printf("\x1b[2m%s\x1b[0m\n", line)
	} else {
		h.printf("%s\n", line)
	}
}

func recordStr(r slog.Record, key string) string {
	var v string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value.String()
			return false
		}
		return true
	})
	return v
}

// recordStrs extracts a []string attr (e.g. a command's args) from a record.
func recordStrs(r slog.Record, key string) []string {
	var out []string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			if s, ok := a.Value.Any().([]string); ok {
				out = s
			}
			return false
		}
		return true
	})
	return out
}

func recordDur(r slog.Record, key string) time.Duration {
	var d time.Duration
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			if a.Value.Kind() == slog.KindInt64 {
				d = time.Duration(a.Value.Int64())
			}
			return false
		}
		return true
	})
	return d
}

func fmtDur(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fµs", float64(d.Nanoseconds())/1000)
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Second*10:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

// plural renders a count with its noun, pluralized. Written out rather than
// emitted as "target(s)": the parenthesized form is a writer refusing to pick,
// and it lands in output a reader is already scanning under pressure.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func recordBool(r slog.Record, key string) bool {
	var v bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value.Bool()
			return false
		}
		return true
	})
	return v
}

func recordInt(r slog.Record, key string) int {
	var i int
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			if a.Value.Kind() == slog.KindInt64 {
				i = int(a.Value.Int64())
			}
			return false
		}
		return true
	})
	return i
}

// remoteSuffix renders the remote-I/O share of a step's duration, or "" when there was
// none or it was too small to explain anything.
//
// The threshold is deliberate. A number that appears on every line stops being read, and
// a 30ms cache probe is not why your build felt slow - so this stays silent until remote
// waiting is both material in absolute terms and a real share of the step. What survives
// is the case worth interrupting for: most of your wait was the network, not your build.
func remoteSuffix(remote, total time.Duration) string {
	const minRemote = 500 * time.Millisecond
	if remote < minRemote || total <= 0 || remote*4 < total {
		return ""
	}
	return ", " + fmtDur(remote) + " remote"
}
