package cache

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	// zone owns the terminal's bottom rows for the whole process; lease is this
	// handler's band within it. The handler no longer sets scroll margins
	// itself: it used to, and so did the CLI's own handler, and two components
	// driving one global DECSTBM setting is what tty.Zone exists to end.
	zone  *tty.Zone
	lease *tty.Lease
	// notifier is nil for the handler on standard error, which reaches the
	// process band through tty.StderrNotifier instead. Caching that one here
	// was a bug: applyDisplay runs several times per invocation and each run
	// tears the process band down, so a handler holding a pointer from before
	// the teardown kept a CLOSED notifier forever and every magus-raised
	// notification silently vanished.
	notifier *tty.Notifier
	status   statusLine // live counters painted into the band's first row
	// failures is the pinned failure ring, one entry per row beneath the status
	// line. A fixed array written at failureAt and rendered in place, so the
	// newest entry replaces the oldest and the rest do not shuffle.
	//
	// Entries carry IDENTITY, not just the rendered heading. A band whose rows
	// are strings can be looked at; one whose rows know which target failed can
	// be acted on, which is what lets a click on a row rerun it.
	failures  [failureRows]Failure
	failureAt int
	// selected is the ring index highlighted by an interactive prompt, or -1
	// for none. The handler owns it because the handler owns the band: a
	// prompt that painted its own highlight would be drawing over rows this
	// repaints from a timer.
	selected int
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
// render composes the status row: everything that reads left to right, and
// separately the elapsed time, which the band pins to the right edge.
func (s statusLine) render(now time.Time) (left, elapsed string) {
	if !s.start.IsZero() {
		elapsed = fmtDur(now.Sub(s.start))
	}
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
	return b.String(), elapsed
}

// paintStatus repaints the region's status row. It is called after every
// event that moves a counter, so the row tracks the run rather than only
// the pool samples that first populated it. A disabled region drops it.
func (h *PrettyHandler) paintStatus() {
	h.ensureLease()
	if !h.lease.Enabled() {
		return
	}
	if h.status.start.IsZero() {
		h.status.start = time.Now()
	}
	h.repaint()
}

// band composes this handler's rows: the live status line on top, then the
// failure ring beneath it.
//
// The status row is reserved unconditionally rather than latched on first use.
// The old Region latched it so a handler that never showed a status line did
// not pay a blank row for the option, but a run reports pool occupancy as soon
// as work starts, so the row was claimed in every case that mattered - and
// latching cost the oldest visible failure whenever it happened late.
func (h *PrettyHandler) band() []tty.Line {
	// NO_COLOR applies here too. It is easy to miss, because the band only
	// exists on a terminal and colour is what makes it readable - but the
	// variable says any non-empty value disables colour, without an exception
	// for the parts an author is fond of. Position and wording already carry
	// the meaning: the status line is on top, failures below it.
	//
	// The selection is the one thing that CANNOT be dropped, since it is not
	// decoration - it says which row a keypress will act on, and without it the
	// prompt is unusable. Reverse video is not a colour, so it stays.
	color := h.wantsColor()
	var dim tty.SGR
	if color {
		dim = tty.SGRDim
	}
	rows := make([]tty.Line, 0, stickyRegionRows)
	// The elapsed time goes to the right edge. It is the one clause that grows
	// a character at a time, so left-aligned it shoved everything before it
	// sideways once a second; pinned right, the counters hold still and the
	// eye stops chasing them.
	left, elapsed := h.status.render(time.Now())
	status := tty.Line{Spans: []tty.Span{{Text: left, Style: dim}}}
	if elapsed != "" {
		status.Spans = append(status.Spans, tty.Span{Text: elapsed, Style: dim, Align: tty.AlignRight})
	}
	rows = append(rows, status)
	for i, f := range h.failures {
		// A blank entry still occupies its row: the ring's positions are fixed,
		// so a failure does not move once painted.
		row := tty.Line{Text: f.Heading}
		if color {
			row.Style = tty.SGRBoldRed
		}
		if i == h.selected && f.Target != "" {
			row.Style = tty.SGRReverse
			if color {
				row.Style += ";" + tty.SGRBoldRed
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// repaint pushes the current band to the lease, reporting whether it landed.
// Callers hold h.mu.
//
// A write error is deliberately NOT latched into h.err. The bool is the signal
// callers act on, and latching would short-circuit the printf helpers on
// exactly the path where the terminal is already misbehaving - swallowing the
// cause and reproduce lines a reader needs most.
func (h *PrettyHandler) repaint() bool {
	rendered, _ := h.lease.Set(h.band())
	return rendered
}

// ensureLease acquires the handler's band, or re-acquires one released at the
// end of a previous run. A refused grant yields the disabled zero Lease, and
// every caller falls back to plain output.
func (h *PrettyHandler) ensureLease() {
	if h.lease.Enabled() {
		return
	}
	h.lease = h.zone.Acquire(stickyRegionRows)
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
	// One handler per terminal, for the same reason there is one tty.Zone: the
	// display is a process-scoped resource because the terminal is.
	//
	// Two used to exist, and the split was not arbitrary - the CLI installs one
	// as the process-wide slog default for general diagnostics, while the Cache
	// builds its own for cache.* events because it is a LIBRARY and cannot
	// assume a caller configured a global logger. Both are right in isolation
	// and wrong together: on one terminal they became two views of one run,
	// each holding half its state, so the failures were pinned by one handler
	// and unreachable from the other.
	//
	// Keyed on w being standard error, the same identity check tty.ZoneFor
	// makes. A handler pointed at a log file or a test buffer is its own, and
	// an SDK consumer that never touches stderr is unaffected.
	if w != os.Stderr {
		return newPrettyHandler(w, level, tty.SystemProbe)
	}
	stderrPrettyMu.Lock()
	defer stderrPrettyMu.Unlock()
	if stderrPretty == nil {
		stderrPretty = newPrettyHandlerZone(w, level, tty.SystemProbe, tty.ZoneFor(w), tty.NotifierFor(w))
		return stderrPretty
	}
	// The level comes from the most recent caller. In the CLI both callers read
	// it from the same config, so this only decides a tie that cannot happen;
	// it matters when an SDK consumer opens a cache after the CLI configured
	// display, where the later, more specific request should win.
	stderrPretty.setLevel(level)
	return stderrPretty
}

// The process's display handler for standard error. See NewPrettyHandler.
var (
	stderrPrettyMu sync.Mutex
	stderrPretty   *PrettyHandler
)

// StderrHandler returns the process's display handler if one was built, or nil.
// It is how a caller that did not construct the handler - the CLI's exit path,
// offering an interactive prompt over the run's pinned failures - reaches the
// one holding them.
func StderrHandler() *PrettyHandler {
	stderrPrettyMu.Lock()
	defer stderrPrettyMu.Unlock()
	return stderrPretty
}

// setLevel updates the minimum level this handler renders.
func (h *PrettyHandler) setLevel(level slog.Level) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.level = level
}

// newPrettyHandler is the probe-injecting form. Tests use it to render
// terminal output into a buffer without opening a pty.
func newPrettyHandler(w io.Writer, level slog.Level, p tty.Probe) *PrettyHandler {
	z := tty.NewZone(w, p)
	return newPrettyHandlerZone(w, level, p, z, tty.NewNotifier(z, 3))
}

// newPrettyHandlerZone is the form that takes an explicit zone, so a test can
// give two handlers the same one and watch them share the terminal.
func newPrettyHandlerZone(w io.Writer, level slog.Level, p tty.Probe, z *tty.Zone, n *tty.Notifier) *PrettyHandler {
	return &PrettyHandler{
		w:        w,
		probe:    p,
		level:    level,
		zone:     z,
		lease:    z.Acquire(stickyRegionRows),
		notifier: n,
		selected: -1,
	}
}

// stickyRegionRows is how many terminal rows this handler leases: one for the
// live pool status line, five for failures. Small on purpose, so the scrolling
// output above stays the main view - and so a second consumer of the zone (a
// notification band) can still be granted rows on an ordinary 24-row terminal.
const stickyRegionRows = 6

// failureRows is the ring beneath the status line.
const failureRows = stickyRegionRows - 1

// RendersBand reports whether this handler has a live band to paint into. The
// cache asks before emitting pool samples, so a piped, JSON, or CI run pays
// nothing for a feature it cannot show, and the CLI asks before offering an
// interactive prompt over failures it may not have drawn.
//
// Not named Enabled: that one belongs to slog.Handler and answers an entirely
// different question (whether a level is worth handling).
func (h *PrettyHandler) RendersBand() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lease.Enabled()
}

// Close releases the sticky error region if one was reserved. Idempotent
// and safe to defer; required so a panic or interrupted run does not leave
// the terminal with the scroll margins still set. The next non-magus
// command the user runs inherits a clean terminal state.
func (h *PrettyHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.releaseBand()
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
	_, err := fmt.Fprintf(h.w, "%s", secret.RedactString(h.recordCtx, fmt.Sprintf(format, args...)))
	h.fail(err)
}

// Enabled reports whether a level is worth handling. slog calls it from every
// logging goroutine, and setLevel writes h.level under the mutex, so this reads
// it under the mutex too.
func (h *PrettyHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return lvl >= h.level
}
func (h *PrettyHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *PrettyHandler) WithGroup(_ string) slog.Handler      { return h }

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
		// The ONE run event that earns a notification. It is not information
		// about the run - it is the run having stopped, for an unbounded time,
		// on something only the user can shorten: go see what that process is,
		// or wait for it. Everything else magus knows during a run is either
		// passive (a cache hit, a pool sample, a summary) or already pinned
		// where it will not scroll away (a failure), and a toast for any of
		// those is noise that teaches the reader to ignore the band.
		//
		// Pinned rather than expiring, because it reports a CONDITION: the wait
		// does not end when a timer says so.
		h.fail(h.notify().Pin(lockNotifyKey, h.blockedMessage(), tty.SGRYellow))
		return h.err
	case "lock.acquired":
		h.status.blocked, h.status.blockedBy = "", ""
		h.paintStatus()
		h.fail(h.notify().Clear(lockNotifyKey))
		return h.err
	case "cache.hit":
		// Cached: passed without running. Dimmed green so a cache hit reads as
		// low-signal next to work that actually ran. Cache state lives in the parens,
		// mirroring the cross-tool convention (e.g. Bazel's "(cached) PASSED").
		h.printf("%s %s (cached, %s%s)\n", glyph(colorize, "pass", colDimGreen), label, fmtDur(dur), remote)
		h.printRepro(colorize, project, recordStr(r, "target"))
		h.printRef(colorize, ref)
		h.status.cached++
		h.paintStatus()
	case "cache.miss":
		h.printf("%s %s (ran, %s%s)\n", glyph(colorize, "pass", colGreen), label, fmtDur(dur), remote)
		h.printRepro(colorize, project, recordStr(r, "target"))
		h.printRef(colorize, ref)
		h.status.passed++
		h.paintStatus()
	case "cache.error":
		h.printFailure(colorize, failureReport{
			label:   label,
			project: project,
			target:  recordStr(r, "target"),
			dur:     dur,
			cause:   recordStr(r, "error"),
			ref:     ref,
			logPath: recordStr(r, "log"),
		})
		h.status.failed++
		h.paintStatus()
	case "cache.warn":
		h.printf("%s %s\n", glyph(colorize, "warn", colYellow), recordStr(r, "msg"))
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
		// End of run: give the leased rows back so the user's shell prompt
		// returns to a clean full-screen terminal. Safe to call when nothing
		// was ever painted (idempotent), and ensureLease takes a fresh band if
		// another run follows in the same process.
		//
		// UNLESS there are failures still pinned on a live band. Releasing then
		// would erase, at the exact moment they became actionable, the list a
		// reader is about to be offered - so the rows are held and whoever runs
		// the prompt gives them back. Nothing leaks if no prompt follows: the
		// process exit path releases every lease regardless.
		if !h.hasPinnedFailures() || !h.lease.Enabled() {
			h.fail(h.lease.Release())
		}
	case "cache.dry.banner":
		if colorize {
			h.printf("%s\n", tty.Colorize("dry run - commands shown, not executed", colDim))
		} else {
			h.printf("dry run - commands shown, not executed\n")
		}
	case "cache.dry":
		// Neutral glyph: a dry run has no pass/fail outcome (nothing executes), and
		// no duration for the same reason. Everything else matches the executed
		// line - including the repro command underneath - so a plan and a run read
		// the same way and only the glyph and the footer say which one you got.
		h.printf("%s %s\n", glyph(colorize, "dry", colDim), label)
		h.printRepro(colorize, recordStr(r, "project"), recordStr(r, "target"))
	case "cache.scope":
		// Run start. Everything the band shows is per-RUN, and this handler is
		// per-PROCESS, so the two have to be separated explicitly or a process
		// that outlives one run reports the sum of every run it has ever seen.
		// That is invisible in a one-shot CLI and wrong the moment anything
		// long-lived - a TUI left open, the daemon - drives more than one.
		h.resetRun()
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
	case "cache.backend":
		h.printf("cache: %s (%s)\n", recordStr(r, "tier"), recordStr(r, "mode"))
	case "cache.base":
		if vcs := recordStr(r, "vcs"); vcs != "" {
			h.printf("base: %s (%s)\n", recordStr(r, "base"), vcs)
		} else {
			h.printf("base: %s\n", recordStr(r, "base"))
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
			h.printf("  %s\n", tty.Colorize("$ "+cmd, colDim))
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
		h.printf("  %s %s %s (%s)\n", glyph(colorize, name, color), label, target, fmtDur(dur))
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
	// These name the palette tty already defines rather than restating the
	// codes, so a wrong sequence stays a compile error in one place. The file
	// used both spellings before, which is how they were free to drift.
	colDimGreen = tty.SGRDimGreen // cached (passed without running) - low signal
	colGreen    = tty.SGRGreen    // ran and passed
	colRed      = tty.SGRRed      // failed
	colYellow   = tty.SGRYellow   // warning
	colDim      = tty.SGRDim      // info/debug
)

// glyph renders a bracketed status glyph like "[pass]" or "[fail]", ASCII only (no
// Unicode symbols or emoji), coloured only on a TTY. pass/fail are the per-target
// outcome words; cache state (cached vs ran) is shown separately in the line's
// parenthetical, the orthogonal split every major build tool uses (e.g. Bazel's
// "(cached) PASSED"). Named to match the doctor command's statusGlyph.
func glyph(colorize bool, label string, color tty.SGR) string {
	s := "[" + label + "]"
	if !colorize {
		return s
	}
	return tty.Colorize(s, color)
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
		attrs = tty.Colorize(attrs, colDim)
	}
	h.printf("%s %s%s\n", glyph(colorize, label, color), r.Message, attrs)
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
		h.printf("  %s\n", repro)
	} else {
		h.printf("  %s\n", repro)
	}
}

// failureReport is one failure as printFailure needs it.
//
// A struct rather than a parameter list because the list had five adjacent
// strings - label, project, target, cause, ref, logPath - and any two of them
// transposed compiles cleanly and prints a plausible, wrong failure. Named
// fields make the same mistake unwriteable.
type failureReport struct {
	label   string
	project string
	target  string
	dur     time.Duration
	cause   string
	ref     string
	logPath string
}

// printFailure keeps the failure and every useful next step together so a reader
// does not need to infer which concurrent project, target, or captured log failed.
//
// The heading goes to the sticky error region (so it never scrolls off)
// when one is reserved; the trailing cause / output / inspect / reproduce
// lines stay in the scrolling region so the user can copy them with
// normal terminal selection. Non-TTY writers and disabled regions fall
// through to plain output unchanged.
func (h *PrettyHandler) printFailure(colorize bool, f failureReport) {
	label, project, target := f.label, f.project, f.target
	dur, cause, ref, logPath := f.dur, f.cause, f.ref, f.logPath
	heading := label
	if target != "" {
		heading += " " + target
	}
	h.ensureLease()
	pinned := false
	if h.lease.Enabled() {
		// Pin only the heading; the trailing lines (cause, output, inspect,
		// reproduce) stay in the scrolling region above where the user can
		// select them without fighting the live update.
		//
		// The glyph is rendered uncoloured here even though this is a TTY: the
		// band draws the whole row in bold red, and a coloured glyph would
		// close that with its own reset, leaving the project and target after
		// it in the default colour.
		h.failures[h.failureAt] = Failure{
			Project:   project,
			Target:    target,
			OutputRef: ref,
			LogPath:   logPath,
			Heading:   fmt.Sprintf("%s %s (ran, %s)", glyph(false, "fail", colRed), heading, fmtDur(dur)),
		}
		h.failureAt = (h.failureAt + 1) % failureRows
		pinned = h.repaint()
	}
	if !pinned {
		// A failure is a RECORD, not a view: when it cannot be pinned - no
		// terminal, no room, a window that shrank mid-run, a write that failed
		// - it still has to reach the user, so it goes to the scrolling output
		// instead. This is the branch tty.Lease.Set's bool exists for.
		//
		// A write error here is deliberately not latched: latching would
		// short-circuit printf and swallow the cause, output ref, and reproduce
		// command below, which is the detail the user most needs on the one
		// path where the terminal is already misbehaving.
		h.printf("%s %s (ran, %s)\n", glyph(colorize, "fail", colRed), heading, fmtDur(dur))
	}
	if cause != "" {
		causes := failureCauses(cause)
		h.printf("  cause: %s\n", causes[0])
		// One line per independent failure; flattened, two read as one sentence.
		for _, c := range causes[1:] {
			h.printf("       %s\n", c)
		}
	}
	if ref != "" {
		h.printf("  output: %s\n", ref)
		// The inspect hint prints EVERYWHERE, CI included. It used to be
		// suppressed on CI on the grounds that a ref addressed a blob in the
		// local cache of the machine that minted it, so pasting the command
		// from an ephemeral runner's log could only fail. Portable refs
		// retired that premise: the ref is a truncation of the cache key, so
		// the same inputs mint the same ref anywhere, and a reader who cannot
		// resolve it locally now gets the target that would produce it
		// (Magus.IdentifyRef behind MGS8001) or a `--publish` route to the
		// exact bytes. The command is actionable off the runner, which was
		// the only bar it ever had to clear.
		full := clihint.QueryOutput.With(ref)
		if colorize {
			// The command is a hyperlink to the captured log on disk, so the ref
			// can be REACHED from the transcript as well as retyped. This is the
			// only way to make something up there actionable: those rows scroll,
			// so magus cannot hit-test a click on them - they belong to the
			// terminal's own selection. Terminals without OSC 8 get the plain
			// command, which is what a reader copies anyway.
			h.printf("  %s\n", tty.Colorize("inspect: "+h.linkify(full, logPath), colDim))
		} else {
			h.printf("  inspect: %s\n", full)
		}
	} else {
		h.printf("  output: unavailable (no output was captured)\n")
	}
	if project == "" || target == "" {
		return
	}
	repro := clihint.Run.With(target, project)
	if colorize {
		h.printf("  %s\n", tty.Colorize("reproduce: "+repro, colDim))
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
	return string([]rune(cause)[:maxRunes-3]) + "..."
}

// hopChain rewrites a cause's dependency hops as a path and drops the plumbing
// that marked them:
//
//	ctx.needs: build: ctx.needs: go-build: ctx.needs: ctx.needs: format: dprint exited 20
//	build -> go-build -> format: dprint exited 20
//
// The hops are not guessed. Every ctx.needs hop wraps the message again, so each
// marker names exactly the segment that follows it - which is why this reads the
// markers before removing them. Guessing from shape instead cannot work: `exec`,
// `config.json` and `./main.go:5:2` all look like target names, and reading
// `exec: "dprint": executable file not found` as a hop invents a target.
//
// The leading segment joins the path when any marker follows it, since it is the
// target the chain hangs from. Everything else is the message, colons and all.
func hopChain(cause string) string {
	const marker = "ctx.needs"
	segs := strings.Split(cause, ": ")
	var hops, rest []string
	pending := false // the previous segment was a marker, so this one is a hop
	for i, seg := range segs {
		switch {
		case seg == marker:
			// rest is empty at the top of every iteration - the loop breaks the
			// moment it is not - so there is nothing to guard against here.
			pending = true
		case pending || (i == 0 && len(segs) > 1 && segs[1] == marker):
			hops = append(hops, seg)
			pending = false
		default:
			rest = segs[i:]
		}
		if len(rest) > 0 {
			break
		}
	}
	if len(hops) < 2 || len(rest) == 0 {
		return strings.ReplaceAll(cause, marker+": ", "")
	}
	return strings.Join(hops, " -> ") + ": " + strings.Join(rest, ": ")
}

// failureCauses splits a joined failure into one line per independent cause and
// renders its dependency hops as a path. Splitting first also gives each cause its own
// excerpt budget, so a long first failure cannot truncate the rest away.
func failureCauses(cause string) []string {
	var out []string
	for _, part := range strings.Split(cause, "\n") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, failureCauseExcerpt(hopChain(part)))
	}
	if len(out) == 0 {
		return []string{failureCauseExcerpt(cause)}
	}
	return out
}

// printRef prints a successful target's output reference id on its own line.
func (h *PrettyHandler) printRef(colorize bool, ref string) {
	if ref == "" {
		return
	}
	if colorize {
		h.printf("%s\n", ref)
	} else {
		h.printf("%s\n", ref)
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

// recordDur reads a duration attr, accepting both spellings callers use.
//
// slog.Int64("duration", int64(d)) arrives as KindInt64 and slog.Duration(...)
// as KindDuration, and this used to accept only the first - so a caller using
// the obvious constructor got a silent zero. internal/handler/mcp logs exactly
// that shape. Harmless today only because the one reader of those records
// ignores the field, which is not a property worth relying on.
func recordDur(r slog.Record, key string) time.Duration {
	var d time.Duration
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != key {
			return true
		}
		switch a.Value.Kind() {
		case slog.KindInt64:
			d = time.Duration(a.Value.Int64())
		case slog.KindDuration:
			d = a.Value.Duration()
		}
		return false
	})
	return d
}

func fmtDur(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fus", float64(d.Nanoseconds())/1000)
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

// recordBool reads a bool attr, defaulting to false when it is absent or not a
// bool. The kind check is not defensive padding: slog.Value.Bool PANICS on a
// mismatch, and this runs inside a log handler, so a record carrying the wrong
// type for a key would take down the process from the logging path. Its
// siblings recordInt and recordDur already guard the same way.
func recordBool(r slog.Record, key string) bool {
	var v bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			if a.Value.Kind() == slog.KindBool {
				v = a.Value.Bool()
			}
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

// notify resolves the band this handler raises notifications into.
//
// Resolved per call rather than held, because the process band is torn down and
// rebuilt between runs (applyDisplay -> restoreTerminal -> CloseStderr) and a
// cached pointer survives that teardown pointing at a closed notifier. A
// handler that is not on standard error keeps its own, which nothing else
// closes.
func (h *PrettyHandler) notify() *tty.Notifier {
	if h.notifier != nil {
		return h.notifier
	}
	return tty.StderrNotifier()
}

// lockNotifyKey names the pinned notification raised while a run is queued
// behind another process's workspace lock.
const lockNotifyKey = "lock.waiting"

// blockedMessage renders the pinned lock notification. It names the ACTION
// available rather than only the state: "waiting" alone leaves a reader
// watching a stalled run with nothing to do about it, and the whole reason
// this one event is worth a notification is that there is something to do.
func (h *PrettyHandler) blockedMessage() string {
	if h.status.blockedBy == "" {
		return "waiting on the workspace lock - another magus run holds it"
	}
	return fmt.Sprintf("waiting on the workspace lock held by %s - wait, or stop it", h.status.blockedBy)
}

// Failure is one failed target, as the pinned band holds it.
//
// It carries what an ACTION needs - which project, which target, which captured
// output - alongside the line the reader sees. The band used to hold only the
// rendered heading, which meant the failures were on screen and unreachable:
// everything needed to rerun one was known at the moment it was formatted and
// thrown away immediately afterwards.
type Failure struct {
	Project string
	Target  string
	// OutputRef is the reference for the captured log, the same one the
	// scrolling `inspect:` line names. Deterministic, so it stays valid for as
	// long as the entry is in the cache.
	OutputRef string
	// LogPath is where the captured output was written. It is what makes the
	// ref linkable: a file:// URL resolves with nothing running.
	LogPath string
	// Heading is the line drawn in the band.
	Heading string
}

// HitFailure maps an absolute terminal row to the failure drawn on it.
//
// The handler answers this rather than exposing its band layout, because the
// layout is its own business: the status line owns the first row and the ring
// owns the rest, and a caller that had to know that would be a second copy of
// the arrangement waiting to drift. Callers pass the Row from a mouse event
// straight through.
//
// Reports false for the status row, for an empty ring slot, and for any row
// outside this handler's band - including a click on another consumer's rows.
func (h *PrettyHandler) HitFailure(row int) (Failure, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	lease, index, ok := h.zone.HitTest(row)
	if !ok || lease != h.lease || index == 0 {
		return Failure{}, false
	}
	f := h.failures[index-1]
	if f.Target == "" {
		return Failure{}, false
	}
	return f, true
}

// Failures returns the failures currently pinned in the band, in the order they
// are DRAWN, skipping empty slots. It is the keyboard counterpart to
// HitFailure: a caller offering "select with the arrow keys" needs the list,
// not a row.
//
// Drawn order, not arrival order. The ring keeps a failure in the row it was
// painted into, so once it has wrapped the oldest entry sits in the middle -
// and the list has to match the screen, because a reader picks by position.
func (h *PrettyHandler) Failures() []Failure {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Failure, 0, failureRows)
	for _, f := range h.failures {
		if f.Target != "" {
			out = append(out, f)
		}
	}
	return out
}

// SetSelection highlights the nth pinned failure, counting only the occupied
// slots that [PrettyHandler.Failures] returns. A negative n clears it.
//
// Selection lives here rather than in the prompt for the reason the band does:
// this repaints on a timer, so a highlight painted from outside would be
// erased by the next status tick.
func (h *PrettyHandler) SetSelection(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.selected = -1
	if n >= 0 {
		seen := 0
		for i, f := range h.failures {
			if f.Target == "" {
				continue
			}
			if seen == n {
				h.selected = i
				break
			}
			seen++
		}
	}
	if h.lease.Enabled() {
		h.repaint()
	}
}

// hasPinnedFailures reports whether any ring slot is occupied. Callers hold h.mu.
func (h *PrettyHandler) hasPinnedFailures() bool {
	for _, f := range h.failures {
		if f.Target != "" {
			return true
		}
	}
	return false
}

// ReleaseBand hands this handler's rows back, for a caller that held them past
// the end of a run to offer an interactive prompt over the pinned failures.
func (h *PrettyHandler) ReleaseBand() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.releaseBand()
}

// releaseBand hands the rows back and forgets any selection. Callers hold h.mu.
//
// One body under two exported names, which is deliberate rather than sloppy:
// Close is the io.Closer a caller defers, ReleaseBand is what an interactive
// prompt calls when it is done with rows it asked to keep past the summary.
// They mean different things to their callers and the same thing to the
// terminal.
func (h *PrettyHandler) releaseBand() error {
	h.selected = -1
	return h.lease.Release()
}

// linkify makes text a hyperlink to the captured log at path, or returns it
// unchanged when there is no path or the terminal cannot render one.
//
// file:// rather than the daemon's output viewer, which renders the same log
// far better: the viewer is only reachable while the daemon is running, and a
// link that is dead half the time is worse than no link. The log file is
// written before the failure is ever printed, so locally this one cannot be.
//
// "Locally" is load-bearing - over ssh the path names a file on the remote
// machine and the terminal would resolve it locally - which is why
// tty.WantsHyperlinks refuses there rather than this deciding it.
func (h *PrettyHandler) linkify(text, path string) string {
	if path == "" || !tty.WantsHyperlinks(h.w, h.probe) {
		return text
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return text
	}
	return tty.Hyperlink(text, "file://"+abs)
}

// resetRun clears everything that describes ONE run, leaving what describes the
// terminal. Callers hold h.mu.
//
// The split is the whole point. Terminal ownership - the zone, the lease, the
// notification band - is process-scoped because the terminal is, and must
// survive across runs. Counters, the elapsed clock, the pinned failures and the
// selection describe a single run and must not. Sharing one handler across runs
// was what made the distinction necessary; before that each run built its own
// and the question never arose.
//
// Deliberately does NOT touch the lease. A band held past the last summary is
// handed back by whoever held it, and dropping it here would make a new run
// re-reserve rows it already had - a visible reflow at the start of every run.
func (h *PrettyHandler) resetRun() {
	h.status = statusLine{}
	h.failures = [failureRows]Failure{}
	h.failureAt = 0
	h.selected = -1
}
