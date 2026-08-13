package cache

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
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
	// mintedRef records that this run printed at least one bare output ref, so
	// the summary can say once what those ids are for.
	//
	// Per RUN rather than a constant line, because a run that minted none would
	// otherwise explain a notation nothing on screen uses.
	mintedRef bool
	// rowFailure maps each band row to the index in the drawn failure list it
	// shows, or -1 for a row that is not a failure (a project header). Written
	// by band, read by HitFailure.
	rowFailure []int
	// focus is the view the keys drive, and so the one the golden ratio gives
	// the major share of the width to. See splitAt.
	focus PaneFocus
	// preview is the selected failure's captured output, as the right-hand
	// column of the band shows it. Empty means one column.
	//
	// PUSHED rather than read here: resolving it means opening a file, and the
	// band repaints from a timer under this mutex. The prompt loads it when the
	// selection moves, which is the only time it changes.
	preview []string
	// now reads the clock, so a caller rendering to a picture can hold it still.
	//
	// The status row carries elapsed time, which makes an otherwise pure
	// function of the records a function of WHEN it ran - and the documentation
	// renderer commits its output, where a live clock is drift on every run.
	// Never nil: the constructors default it.
	now func() time.Time
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
	// The blocked state is NOT rendered here, though this type holds it: the
	// pinned notification is the better home, since it carries the remedy and
	// this line's job is a steady readout of pool and counts. Rendering both
	// announced one lock wait twice.
	//
	// The fields stay because blockedMessage composes the notification from
	// them; see [PrettyHandler.blockedMessage].
	if g := PoolGauge(s.running, s.capacity); g != "" {
		b.WriteString(g)
	} else {
		fmt.Fprintf(&b, "pool %d/%d", s.running, s.capacity)
	}
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

// Pool slots, drawn as the console draws them: ONE mark per slot, filled when
// that slot is working. A ratio is a shape before it is a number, and "6/8" made
// the reader parse two integers to see something the eye can take in whole.
const (
	slotBusy = "\u25a0" // filled square
	slotIdle = "\u25a1" // hollow square
	// slotCap is where slot-for-slot stops being readable. Past it the gauge is
	// dropped rather than SCALED: a scaled bar looks identical to a literal one
	// while meaning something else entirely, and a reader who has learned that
	// one mark is one slot would be quietly misled. The numbers still say it.
	slotCap = 16

	// The tree's branches. A target under its project, and the last one closing
	// the group so the eye can see where a project's failures end.
	treeTee = "\u251c\u2500 "
	treeEnd = "\u2570\u2500 "
)

// PoolGauge renders the pool as filled and empty slots, or "" when there are too
// many slots to draw honestly.
// PoolGauge is exported for the documentation renderer, which draws the same
// gauge a run draws rather than a copy of what it looks like.
func PoolGauge(running, capacity int) string {
	if capacity <= 0 || capacity > slotCap {
		return ""
	}
	running = min(max(running, 0), capacity)
	marks := make([]string, 0, capacity)
	for i := range capacity {
		if i < running {
			marks = append(marks, slotBusy)
			continue
		}
		marks = append(marks, slotIdle)
	}
	// SPACED, not butted together. Contiguous blocks read as one bar being
	// filled - a proportion - and this is not a proportion: it is eight
	// discrete slots, and a reader should be able to count them. The space is
	// what makes them countable.
	//
	// The numbers ride along in parentheses because the gauge answers "how
	// busy" at a glance and the digits answer "exactly how many" on a second
	// look. Neither replaces the other.
	return strings.Join(marks, " ") + fmt.Sprintf(" (%d/%d)", running, capacity)
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
		h.status.start = h.now()
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
	shown := 0
	// The elapsed time goes to the right edge. It is the one clause that grows
	// a character at a time, so left-aligned it shoved everything before it
	// sideways once a second; pinned right, the counters hold still and the
	// eye stops chasing them.
	// The status goes in the box's TOP RULE, not in a row of its own. It
	// describes the band, so the frame is where it belongs - and it stops
	// costing a row, which on a run with nothing wrong was the difference
	// between a two-row band and a three-row one.
	left, elapsed := h.status.render(h.now())
	_, _ = h.zone.SetTitle(left, elapsed)
	// Grouped by project, drawn as a tree: one project in trouble reads
	// differently from five at a glance, without counting. The band is the tree
	// PRUNED to what went wrong, so a run with nothing wrong draws no tree.
	//
	// rowFailure is built here because only this loop knows which rows are
	// targets and which are headers - hit-testing by position resolved a click
	// one row off.
	drawn := h.drawnLocked()
	// BUDGETED before drawing, because a tree costs a header row as well as a
	// row per failure and the band's ceiling did not move when it stopped being
	// a flat list. Rows past the ceiling are dropped by Lease.Set, silently, and
	// the "+N more" count was computed from rows DRAWN - so a band that had
	// discarded two failures reported none hidden while the title said four
	// failed. Deciding what fits here is what makes that count honest.
	drawn = drawn[:fitsInBand(drawn, h.ceilingLocked())]
	h.rowFailure = h.rowFailure[:0]
	prev := ""
	for i, f := range drawn {
		if f.Project != prev {
			prev = f.Project
			// The glyph rides the HEADER, once per project, not once per
			// target. It is the only thing saying these rows are failures on a
			// terminal with no colour, and repeating it down every branch of a
			// tree is noise the tree already carries structurally.
			rows = append(rows, tty.Line{Spans: []tty.Span{
				{Text: glyph(color, "fail", colRed) + " ", Style: dim},
				{Text: f.Project, Style: dim},
			}})
			h.rowFailure = append(h.rowFailure, -1)
		}
		shown++
		branch := treeTee
		if i == len(drawn)-1 || drawn[i+1].Project != f.Project {
			branch = treeEnd
		}
		marker := " "
		selected := i == h.selected
		if selected {
			marker = tty.SelectMark
		}
		weight := dim
		if color {
			weight = tty.SGRBoldRed
		}
		row := tty.Line{Spans: []tty.Span{
			{Text: marker + branch, Style: dim},
			{Text: f.Target, Style: weight},
		}}
		if f.Dur > 0 {
			row.Spans = append(row.Spans, tty.Span{Text: fmtDur(f.Dur), Style: dim, Align: tty.AlignRight})
		}
		if selected {
			for j := range row.Spans {
				row.Spans[j].Style = tty.SGRReverse
				if color {
					row.Spans[j].Style += ";" + tty.SGRBoldRed
				}
			}
		}
		rows = append(rows, row)
		h.rowFailure = append(h.rowFailure, i)
	}
	// Say what is NOT on screen. The ring holds five, and a run can fail fifty:
	// without this the band shows the newest five under a status row reading
	// "50 failed" and never states the relationship, so forty-five targets are
	// missing with nothing to suggest they exist. The count comes from the
	// status counters, which see every failure rather than only the ones that
	// fit.
	if hidden := h.status.failed - len(drawn); hidden > 0 {
		rows = append(rows, tty.Line{
			Text:  fmt.Sprintf("  +%d more above, in the transcript", hidden),
			Style: dim,
		})
		// Kept in step. rowFailure is a parallel array, and its whole
		// justification is that the code DRAWING a row records what it is - so
		// a row appended without an entry silently shifts every index below it.
		// Harmless today only because nothing clickable follows.
		h.rowFailure = append(h.rowFailure, -1)
	}
	return h.withPreview(rows, dim)
}

// withPreview folds the selected failure's output into a second column.
//
// The divider and the right column are appended to each row as SPANS, so the
// existing layout does the alignment and clipping - there is no second region,
// no pty, and nothing here that could hold content the handler did not produce.
// A vertical split drawn by the terminal would need DECSLRM, which is not
// portable; composing the columns into rows magus already repaints is.
//
// The band grows to whichever column is taller, so the output is not truncated
// to the number of failures that happen to be showing.
func (h *PrettyHandler) withPreview(rows []tty.Line, dim tty.SGR) []tty.Line {
	if len(h.preview) == 0 {
		return rows
	}
	// The band is capped (see repaint), so the preview is windowed to what will
	// actually be drawn - and the window is taken from the END.
	//
	// Assigning preview[i] to row i showed the FIRST lines of a tail that was
	// collected precisely because the last ones matter: a reader saw
	// "go: downloading ..." and never the assertion or the FAIL line. The tail
	// of the tail is the part worth the rows.
	// Recomputed per paint: the terminal can be resized under a running prompt,
	// and a split cached from the old width puts the divider through the text.
	split := splitAt(h.lease.Width(), h.focus)
	preview := h.preview
	if room := h.ceilingLocked() - len(rows); room > 0 && len(preview) > room {
		preview = preview[len(preview)-room:]
	}
	for len(rows) < len(preview) {
		rows = append(rows, tty.Line{})
		h.rowFailure = append(h.rowFailure, -1)
	}
	for i := range rows {
		// Anything the row aligned RIGHT was aligned against the whole terminal,
		// which is no longer where its column ends: a duration would sail past
		// the divider and land beyond the output. So the left column is
		// flattened here - its right-aligned spans are padded into place inside
		// the split column and then laid out left, which is the only way a
		// row-global alignment can serve a column.
		var left, tail []tty.Span
		for _, sp := range rows[i].SpansCopy() {
			if sp.Align == tty.AlignRight {
				sp.Align = tty.AlignLeft
				tail = append(tail, sp)
				continue
			}
			left = append(left, sp)
		}
		pad := split - spansCols(left) - spansCols(tail)
		if pad < 1 {
			pad = 1
		}
		var right string
		if i < len(preview) {
			right = preview[i]
		}
		out := append(left, tty.Span{Text: strings.Repeat(" ", pad), Style: dim})
		out = append(out, tail...)
		out = append(out, tty.Span{Text: previewDivider, Style: dim}, tty.Span{Text: right, Style: dim})
		rows[i].Spans = out
		rows[i].Text, rows[i].Style = "", ""
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
	rows := h.band()
	// The band is sized to what it HAS, not to what it might one day hold.
	// It used to lease six rows from the first paint, so a run with nothing
	// wrong reserved five blank rows for failures that never came - and once
	// the zone drew a box around them, the emptiness became the most visible
	// thing on screen. Growing on demand costs one Grow per new failure and
	// gives the scrolling transcript back the rows nobody was using.
	//
	// It only ever grows. Shrinking mid-run would make the transcript jump
	// upward as failures aged out, which is motion the reader did not cause.
	if len(rows) > h.lease.Rows() {
		_ = h.lease.Grow(min(len(rows), h.ceilingLocked()))
	}
	rendered, _ := h.lease.Set(rows)
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
	// display is process-scoped because the terminal is. Two handlers - the
	// CLI's process-wide default and the Cache's own - became two views of one
	// run holding half its state each, so failures pinned by one were
	// unreachable from the other.
	//
	// Keyed on w being standard error, the same identity check tty.ZoneFor
	// makes, so a handler pointed at a log file or test buffer is its own.
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
		w:     w,
		probe: p,
		level: level,
		zone:  z,
		// ONE row to start: the live status line. The band grows from repaint as
		// failures actually arrive (see repaint), so a run that never fails
		// never reserves a row for one. stickyRegionRows is the ceiling now,
		// not the opening claim.
		lease:    z.Acquire(1),
		notifier: n,
		selected: -1,
		now:      time.Now,
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
		h.printRepro(project, recordStr(r, "target"))
		h.printRef(ref)
		h.status.cached++
		h.paintStatus()
	case "cache.miss":
		h.printf("%s %s (ran, %s%s)\n", glyph(colorize, "pass", colGreen), label, fmtDur(dur), remote)
		h.printRepro(project, recordStr(r, "target"))
		h.printRef(ref)
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
		h.printRefLegend(colorize)
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
		h.printRepro(recordStr(r, "project"), recordStr(r, "target"))
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
func (h *PrettyHandler) printRepro(project, target string) {
	if project == "" || target == "" {
		return
	}
	h.printf("  %s\n", clihint.Run.With(target, project))
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
			Dur:       dur,
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
// The hops are not guessed: every ctx.needs hop wraps the message again, so each
// marker names the segment that follows it, and the markers are read before being
// removed. Guessing from shape cannot work - `exec`, `config.json` and
// `./main.go:5:2` all look like target names.
//
// The leading segment joins the path when any marker follows it. Everything else
// is the message, colons and all.
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
//
// Bare on purpose: a passing target prints one of these EACH, so spelling the
// retrieval command out here would add a line per target to every run. The
// summary carries that once instead - see the legend in cache.summary.
func (h *PrettyHandler) printRef(ref string) {
	if ref == "" {
		return
	}
	h.mintedRef = true
	h.printf("%s\n", ref)
}

// printRefLegend says once, at the end of a run, what the bare ids above are.
//
// Without it a reader who has not met output refs has fourteen characters and no
// verb - often an AGENT in a fresh worktree with no magus skills installed, for
// which the transcript is the only surface guaranteed to reach it.
//
// One line per run, and only when a ref was minted, so it never explains a
// notation nothing on screen used. Deliberately vendor-neutral.
func (h *PrettyHandler) printRefLegend(colorize bool) {
	if !h.mintedRef {
		return
	}
	line := "  outputs: " + clihint.QueryOutput.With("<ref>")
	if colorize {
		line = tty.Colorize(line, colDim)
	}
	h.printf("%s\n", line)
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
	// The PROJECT is named here, and that is not decoration. It used to be
	// stated on the status row; moving the status into the box's title dropped
	// the clause, and with it the only thing on screen saying WHICH project was
	// blocked - a reader saw a pid and had to guess what it was holding.
	what := "the workspace lock"
	if h.status.blocked != "" {
		what = "the lock on " + h.status.blocked
	}
	if h.status.blockedBy == "" {
		return "waiting on " + what + " - another magus run holds it"
	}
	return fmt.Sprintf("waiting on %s held by %s - wait, or stop it", what, h.status.blockedBy)
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
	// Dur is how long the target ran before it failed, kept as a FIELD rather
	// than only baked into Heading.
	//
	// A single pre-formatted string cannot be laid out. Durations are the one
	// column a reader scans down - "which of these was slow" - and that only
	// works if they share a right edge, which needs them separable from the
	// text they follow. Heading stays for the plain-output path, where there
	// is no column to align to.
	Dur time.Duration
}

// SetFocus moves focus between the two views, which resizes them: the focused
// one takes the golden ratio's major share.
//
// Resizing on focus rather than offering a drag handle is deliberate - there is
// no pointer contract to invent, and the pane you are working in is the one
// that should be big.
func (h *PrettyHandler) SetFocus(f PaneFocus) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.focus == f {
		return
	}
	h.focus = f
	h.repaint()
}

// ToggleFocus swaps which view has the major share, and reports the new focus.
func (h *PrettyHandler) ToggleFocus() PaneFocus {
	h.mu.Lock()
	f := FocusPreview
	if h.focus == FocusPreview {
		f = FocusTree
	}
	h.mu.Unlock()
	h.SetFocus(f)
	return f
}

// SetPreview gives the band a right-hand column: the captured output of
// whatever is selected. Nil or empty returns it to a single column.
//
// This is the "two views, one run" surface. It is deliberately not two PANES -
// nothing here manages a terminal, and a caller cannot put arbitrary content in
// it. Both columns are things this handler already owns, which is the line
// between showing a reader their run and becoming a multiplexer.
func (h *PrettyHandler) SetPreview(lines []string) {
	// The lock is held ACROSS the repaint, like every other caller: repaint
	// composes the band, which reads the failure ring and writes rowFailure.
	// Unlocking first raced the paint timer for both.
	h.mu.Lock()
	defer h.mu.Unlock()
	h.preview = lines
	h.repaint()
}

// The GOLDEN RATIO governs the split: the focused view gets the major share,
// the other the minor.
//
// phiMajor is 1/phi. The two shares sum to 1, so the divider lands at the same
// place whichever view has focus - the panes trade sizes rather than the layout
// reflowing around a third number.
//
// A ratio rather than a fixed column because a fixed one is only ever right at
// one terminal width: 34 columns is a third of a 100-column window and nearly
// half an 80-column one, so the same layout read as balanced on one machine and
// cramped on another.
const (
	phiMajor = 0.6180339887
	phiMinor = 1 - phiMajor
	// previewMinCols keeps both views legible on a narrow terminal, where a
	// share of a small number rounds to nothing useful.
	previewMinCols = 12
)

// splitAt is the column the divider sits at, given the width available to both
// views and which one has focus.
func splitAt(inner int, focus PaneFocus) int {
	usable := inner - len(previewDivider)
	if usable < 2*previewMinCols {
		return usable / 2
	}
	share := phiMinor
	if focus == FocusTree {
		share = phiMajor
	}
	at := int(float64(usable)*share + 0.5)
	return min(max(at, previewMinCols), usable-previewMinCols)
}

// PaneFocus names the view the keys are driving, which is the one the golden

// PaneFocus names the view the keys are driving, which is the one the golden

type PaneFocus int

const (
	FocusTree PaneFocus = iota
	FocusPreview
)

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
	if !ok || lease != h.lease {
		return Failure{}, false
	}
	// Indexed against the DRAWN list, not the ring, and through rowFailure,
	// which band builds as it draws.
	//
	// Every attempt to re-derive this has resolved a click one row off - worse
	// than not resolving, since it reruns a target the reader did not point at.
	// The band skips empty slots and its rows are not all failures (a project
	// header names no target), so the mapping is recorded by the code that draws
	// it rather than recomputed by the code that reads it. Sharing drawnLocked
	// with Failures keeps the keyboard and the mouse on one sequence by
	// construction.
	if index < 0 || index >= len(h.rowFailure) {
		return Failure{}, false
	}
	at := h.rowFailure[index]
	if at < 0 {
		return Failure{}, false // a project header is not clickable
	}
	drawn := h.drawnLocked()
	if at >= len(drawn) {
		return Failure{}, false
	}
	return drawn[at], true
}

// previewBandRows is the band's ceiling while a second column is showing.
//
// Taller than the one-column ceiling on purpose: the tree and the output share
// the rows, so at six the tree took five and left the log ONE line - of a tail
// collected precisely because the last lines matter. This is a deliberate
// interaction a reader opened, not a background band, so it may take the room;
// it goes back to stickyRegionRows the moment the preview is cleared.
const previewBandRows = 14

// ceilingLocked is the band's row limit for the current mode. Callers hold h.mu.
func (h *PrettyHandler) ceilingLocked() int {
	if len(h.preview) > 0 {
		return previewBandRows
	}
	return stickyRegionRows
}

// fitsInBand returns how many of drawn can be shown without the band silently
// discarding rows.
//
// A grouped tree spends a row on each project as well as each failure, and one
// more on the "+N more" line the moment anything is left over. The ceiling is
// the lease's, so the arithmetic has to happen before the rows exist.
func fitsInBand(drawn []Failure, ceiling int) int {
	// The overflow row is budgeted only once it is known to be needed. Reserving
	// it unconditionally cost the last failure its row: five failures in a
	// six-row band is one header plus five rows, which fits exactly, yet the
	// loop stopped at four and spent the sixth row on "+1 more" - so the fifth
	// pinned failure was never drawable at all. The guard that was meant to
	// catch this compared n and rows AFTER the loop had already truncated, and
	// both of its arms returned the same n.
	if bandRows(drawn) <= ceiling {
		return len(drawn)
	}
	rows, prev, n := 0, "", 0
	for _, f := range drawn {
		cost := 1
		if f.Project != prev {
			cost = 2 // its header too
		}
		// One row is now genuinely spoken for by the "+N more" line.
		if rows+cost > ceiling-1 {
			break
		}
		rows += cost
		prev = f.Project
		n++
	}
	return n
}

// bandRows is what drawn occupies untruncated: a row each, plus a header row
// wherever the project changes.
func bandRows(drawn []Failure) int {
	rows, prev := 0, ""
	for _, f := range drawn {
		if f.Project != prev {
			rows++
			prev = f.Project
		}
		rows++
	}
	return rows
}

// drawnLocked is the failures the band draws, in the order it draws them.
// Callers hold h.mu.
func (h *PrettyHandler) drawnLocked() []Failure {
	out := make([]Failure, 0, failureRows)
	for _, f := range h.failures {
		if f.Target != "" {
			out = append(out, f)
		}
	}
	// SORTED by project, because the band draws a tree and a tree needs its
	// children adjacent. Ring order is arrival order, and under a pool of eight
	// failures interleave across projects by default - so "start a header when
	// the project changes" was run-length encoding, not grouping: api, std, api
	// drew TWO api headers and a five-failure band spent half its rows on
	// repeated titles. Stable, so within a project the arrival order the ring
	// recorded is preserved.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out
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
	// TRUNCATED the same way band() truncates. Returning the full list let the
	// prompt's arrow keys walk past the last drawn row: the highlight vanished
	// and Enter reran a target that was not on screen. One list, one length.
	drawn := h.drawnLocked()
	return drawn[:fitsInBand(drawn, h.ceilingLocked())]
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
// Terminal ownership - the zone, the lease, the notification band - is
// process-scoped and must survive across runs; counters, the elapsed clock, the
// pinned failures and the selection describe a single run and must not.
//
// Deliberately does NOT touch the lease: dropping it here would make a new run
// re-reserve rows it already had, a visible reflow at the start of every run.
func (h *PrettyHandler) resetRun() {
	h.status = statusLine{}
	h.failures = [failureRows]Failure{}
	h.failureAt = 0
	h.selected = -1
	h.mintedRef = false
	// The preview belongs to a failure from the run that just ended. Left set,
	// a rerun drew rows of the PREVIOUS run's log beside an empty tree - stale
	// content pinned on a surface whose whole promise is that it holds still.
	h.preview = nil
	h.rowFailure = h.rowFailure[:0]
}

// The failure prompt's instruction row: the text, and the click keys that name
// each action.
//
// It lives beside the band rather than in the command that dispatches it: the
// failure list and the row saying what you can do with one are a single
// presentation, drawn into adjacent leases of the same zone. The command owns the
// VERBS; this owns what a reader is told and what a click resolves to.
//
// Keeping them together also keeps the docs honest - a renderer that cannot
// import an unexported const hand-copies the strings, and the drift gate then
// compares that copy against itself while the terminal says something else.
const (
	HintKeyRerun  = "rerun"
	HintKeyFocus  = "focus"
	HintKeyCopy   = "copy"
	HintKeyOutput = "output"
	HintKeyDone   = "done"

	hintLead   = "[up/down] select   "
	hintRerun  = "[enter] rerun"
	hintFocus  = "[tab] focus"
	hintCopy   = "[y] copy"
	hintOutput = "[o] output"
	hintDone   = "[esc] done"
)

// FailureHint composes the instruction row.
//
// The way out is aligned RIGHT so it is the last thing clipped rather than the
// first: as one string this row was 86 columns, and an 80-column terminal cut
// it to exactly "[esc] do".
func FailureHint() []tty.Line {
	return []tty.Line{{Spans: []tty.Span{
		{Text: hintLead, Style: colDim},
		{Text: hintRerun, Style: colDim, Key: HintKeyRerun},
		{Text: "   ", Style: colDim},
		{Text: hintFocus, Style: colDim, Key: HintKeyFocus},
		{Text: "   ", Style: colDim},
		{Text: hintCopy, Style: colDim, Key: HintKeyCopy},
		{Text: "   ", Style: colDim},
		{Text: hintOutput, Style: colDim, Key: HintKeyOutput},
		{Text: hintDone, Style: colDim, Align: tty.AlignRight, Key: HintKeyDone},
	}}}
}

// FailureHintPlain is the same instruction as one line, for a terminal with no
// room to pin it. Printed rather than dropped: every other thing the prompt
// draws is a view, but this is the only statement of how to leave.
func FailureHintPlain() string {
	return hintLead + hintRerun + "   " + hintFocus + "   " + hintCopy + "   " + hintOutput + "   " + hintDone
}

// NewPrettyHandlerFor returns a handler that draws into w, measured through p.
//
// Exported for a caller that is deliberately NOT the process terminal: the
// documentation renderer, which drives a real handler against an in-memory
// screen so the pictures it publishes are drawn by the code a reader will
// actually meet. [NewPrettyHandler] is the production entry point and keys on
// standard error to keep one handler per terminal; this one makes no such
// claim, so two callers get two independent handlers and neither takes the
// process band.
// A nil now defaults to time.Now; pass a fixed one to render a picture whose
// bytes do not depend on when it was rendered.
func NewPrettyHandlerFor(w io.Writer, level slog.Level, p tty.Probe, now func() time.Time) *PrettyHandler {
	h := newPrettyHandler(w, level, p)
	if now != nil {
		h.now = now
	}
	return h
}

// Zone returns the terminal owner this handler paints its band into.
//
// Exposed so a second consumer can lease rows from the SAME owner: the failure
// prompt puts its instruction row directly beneath the band, and two zones over
// one terminal each compute their margins from their own row arithmetic and
// overwrite each other - the exact failure tty.Zone exists to prevent. In
// production both sides reach the same singleton through standard error and the
// sharing is invisible; for any other writer it has to be asked for.
func (h *PrettyHandler) Zone() *tty.Zone { return h.zone }

// previewDivider separates the band's two columns.
// previewDivider separates the two views, with a column of air on BOTH sides.
//
// Without the leading space the left column ran straight into it - a duration
// touching the rule reads as a rendering fault rather than as a boundary, and
// the line stops looking like a line.
const previewDivider = " │ "

// spansCols is the display width a run of spans occupies, so the divider lands
// in the same column on every row.
//
// Delegated to tty rather than counted here: this package composes rows for
// tty's layout, and measuring them differently is precisely how the same
// byte-for-column bug reached four copies.
func spansCols(spans []tty.Span) int {
	n := 0
	for _, sp := range spans {
		n += tty.Cols(sp.Text)
	}
	return n
}
