package tty

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// A region needs a scrolling area above it that is still worth reading,
// and enough columns that a clipped line carries information. Below
// either bound the region is refused and the caller falls back to plain
// output.
const (
	minUsefulHeight = 8
	minUsefulWidth  = 20
)

// Region pins the bottom rows of a terminal as a zone that does not
// scroll, so what is drawn there stays on screen while ordinary output
// continues to scroll above. The zone is established with DECSTBM scroll
// margins.
//
// Callers do not build one directly: [Zone] owns the terminal's bottom rows
// for the whole process and hands out bands of them as leases. A second
// Region on the same terminal would set its own margins from its own row
// arithmetic, and the two would overwrite each other.
//
// The contract is additive, never destructive:
//   - Scrollback above the region is preserved; selection and copy
//     still work there.
//   - Release restores the terminal. A panic or interrupted run leaves
//     the terminal in its default state, never in an alternate-screen
//     mode the user cannot escape.
//   - On any non-TTY (pipe, file, CI log, `script`), every method is inert,
//     so the caller does not branch. A repainted view replayed line by line
//     into a pipe would be noise; the caller decides what to print instead.
//   - THE CALLER'S CURSOR NEVER MOVES. Reserve makes room without
//     shifting it relative to the caller's own text, every paint saves
//     and restores it inside one write, and Release does not reposition
//     it at all.
//
// That last property is what lets one type serve two very different
// consumers. A logging caller interleaves its own scrolling output with
// region repaints; an interactive caller holds a prompt and has the
// terminal echoing keystrokes at the cursor. Neither can tolerate a
// repaint that leaves the cursor parked in the region - the log lands in
// the footer, or the user types over their own status line - and neither
// should have to know the region's geometry to put it back. So the region
// owns the rows it reserved and nothing else, and painting is invisible.
//
// The corollary is a rule about the terminal's cursor-save register: it is
// a single global slot, so it is taken and released within one write and
// never held across calls. An earlier version held it for the whole life
// of the region so Release could restore it, which meant any repaint
// clobbered the saved position and teardown reinstated the wrong one.
//
// The alternate screen buffer (`\e[?1049h`) is never touched, which is the
// difference between a useful status area and the full-screen takeovers
// some build tools use.
//
// A Region is not safe for concurrent use. [Zone] is the boundary that makes
// it so: every access to the Region it owns goes through the zone's mutex,
// because the consumers leasing from it (a run's pool, a notification
// sweeper, a daemon job) are on different threads.
type Region struct {
	w      io.Writer
	probe  Probe
	fd     uintptr
	height int

	// enabled records that w is a terminal big enough to host the
	// region. False means every method falls back to plain output.
	enabled bool
	// open records that the scroll margins are currently set, so
	// Reserve is idempotent and Release knows there is work to do.
	open bool
	// width and termHeight are the terminal's dimensions as of the last
	// Reserve. Cached rather than re-queried per line: an ioctl per log
	// line is waste, and a failed mid-run query would otherwise drive
	// the row arithmetic negative.
	width, termHeight int
	// buf composes one line so it reaches the terminal as a single
	// Write. Keeps cursor positioning atomic and avoids per-byte flicker.
	buf []byte
	// painted is the last frame [Region.Render] drew, so the next one can skip
	// rows that did not change. Nil means "nothing on screen is known", which
	// is the state after any repaint of the zone by other means (Reserve
	// clears it, a resize re-reserves), and forces a full redraw.
	painted []Row
}

// NewRegion returns a Region that will pin height rows at the bottom of
// the terminal behind w, measured through p.
//
// Nothing is written to w here. The reservation happens on Reserve, or on
// the first Render, so a run that never draws never touches the user's
// terminal. Enabled reports whether the reservation will be
// attempted at all: it is false when w has no descriptor, when p says
// the descriptor is not a terminal, when the terminal is too small to
// host height rows alongside a useful scrolling area, or when the size
// query fails. A disabled Region is safe to use and writes plain lines.
//
// Pass [SystemProbe] in production; tests pass their own Probe.
func NewRegion(w io.Writer, height int, p Probe) *Region {
	r := &Region{w: w, probe: p, height: height, buf: make([]byte, 0, 256)}
	if height <= 0 {
		return r
	}
	fd, ok := Fd(w)
	if !ok || !CanRender(w, p) {
		return r
	}
	width, termHeight, err := p.Size(fd)
	if err != nil || !fits(width, termHeight, height) {
		return r
	}
	r.fd = fd
	r.width, r.termHeight = width, termHeight
	r.enabled = true
	return r
}

// fits reports whether a terminal of the given dimensions can host a
// region of height rows and still leave a readable scrolling area.
func fits(width, termHeight, height int) bool {
	return width >= minUsefulWidth && termHeight >= minUsefulHeight+height
}

// Enabled reports whether this Region will drive the terminal. When false,
// Render drops its rows and Reserve and Release do nothing.
func (r *Region) Enabled() bool { return r.enabled }

// Reserve sets the scroll margins so the bottom rows stop scrolling.
// Idempotent, and Render calls it for you, so callers do not have to pair
// them.
//
// The terminal is re-measured here rather than trusting the dimensions
// from NewRegion: the window may have been resized in between, and
// applying stale margins would put the cursor outside the scroll region.
// If the terminal has since become too small, the Region disables itself
// and the caller falls back to plain output.
//
// It leaves the caller's cursor exactly where it found it, relative to the
// caller's own output. That is the whole contract this type keeps (see
// [Region]), and it is what the opening index sequences are for: moving down
// height rows and stepping back up over them guarantees height rows exist
// below the cursor without moving the cursor relative to the text. When the
// cursor was already at the bottom, the screen scrolls and the transcript
// slides up out of the way; when it was mid-screen, nothing scrolls and the
// step back is exact. One sequence, correct either way, and it never
// destroys a row the caller had written.
func (r *Region) Reserve() error {
	if !r.enabled || r.open {
		return nil
	}
	width, termHeight, err := r.probe.Size(r.fd)
	if err != nil {
		return err
	}
	if !fits(width, termHeight, r.height) {
		// The window shrank below what this region needs. Applying the
		// margins anyway would emit an inverted or negative range.
		r.enabled = false
		return nil
	}
	r.width, r.termHeight = width, termHeight

	r.buf = r.buf[:0]
	// Make room first, before any margin is set, so the scroll is an ordinary
	// one the terminal performs over the whole screen. IND rather than a
	// newline, so the caller's column survives; see [ind].
	r.buf = append(r.buf, strings.Repeat(ind, r.height)...)
	r.buf = append(r.buf, fmt.Sprintf(cuuFmt, r.height)...)
	// Save AFTER making room and BEFORE the margins: DECSTBM homes the cursor
	// in some terminals, so a save afterwards would record row 1.
	r.buf = append(r.buf, cursorSave...)
	// Scrolling is confined to the rows above the region.
	r.buf = append(r.buf, fmt.Sprintf(decstbmFmt, 1, r.firstRow()-1)...)
	// Clear the zone so a previous run's text cannot show through, then put
	// the caller's cursor back.
	r.buf = append(r.buf, fmt.Sprintf(cupFmt, r.firstRow(), 1)...)
	r.buf = append(r.buf, ed...)
	r.buf = append(r.buf, cursorRestore...)
	if _, err := r.w.Write(r.buf); err != nil {
		return err
	}
	r.open = true
	// The zone was just erased, so nothing Render believes is on screen still
	// is. Dropping the cache here is what keeps the diff honest across a
	// resize, which re-reserves.
	r.painted = nil
	return nil
}

// paint wraps one region write so the caller's cursor is saved, the region
// row is addressed, and the cursor is put back - all in a single write to
// the terminal.
//
// Every write into the zone goes through this, which is what makes painting
// invisible: a caller mid-line, or holding a prompt, sees nothing move. The
// save register is taken and released inside this one sequence and is never
// held across calls, because it is a single global slot - the terminal has
// exactly one - and treating it as ownable for the life of the region is
// what previously made a repaint and a teardown fight over it.
func (r *Region) paint(row int, body func()) error {
	r.buf = r.buf[:0]
	r.buf = append(r.buf, cursorSave...)
	r.buf = append(r.buf, fmt.Sprintf(cupFmt, row, 1)...)
	body()
	r.buf = append(r.buf, cursorRestore...)
	_, err := r.w.Write(r.buf)
	return err
}

// Align says which edge a [Span] is laid out from.
type Align int

const (
	// AlignLeft packs a span after the previous left-aligned one, from column 1.
	AlignLeft Align = iota
	// AlignRight packs a span against the right edge, after any other
	// right-aligned spans on the row.
	AlignRight
)

// Span is one styled, aligned segment of a row.
//
// Spans exist because a row often carries two unrelated things - what is
// happening on the left, and how to get out of it on the right - and a single
// string cannot express that. The alignment is resolved at PAINT time rather
// than by the caller, because only the region knows the terminal's width, and a
// caller padding to a width it guessed is a caller that is wrong after the
// first resize.
type Span struct {
	Text  string
	Style string
	Align Align
}

// Row is one line of a whole-zone repaint.
//
// Text and Style are shorthand for the overwhelmingly common single-span row;
// Spans is the general form and wins when both are set. They are not two code
// paths - [Row.spans] normalises the shorthand into a one-element list and the
// renderer only ever sees spans - so the convenience cannot drift from the
// general case.
type Row struct {
	Text  string
	Style string
	Spans []Span
}

// spans normalises a Row into the form the renderer works with.
func (r Row) spans() []Span {
	if len(r.Spans) > 0 {
		return r.Spans
	}
	if r.Text == "" {
		return nil
	}
	return []Span{{Text: r.Text, Style: r.Style}}
}

// equal reports whether two rows would paint identically. Row carries a slice,
// so the frame diff in [Region.Render] cannot use ==.
func (r Row) equal(o Row) bool {
	a, b := r.spans(), o.spans()
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// layout composes one row's spans into the bytes for a terminal `width` wide.
//
// RIGHT-ALIGNED SPANS ARE LAID OUT FIRST AND KEPT; the left side is clipped to
// whatever remains. That order is the policy, not an implementation detail: the
// things that sit on the right are the ones that must not vanish when the
// window is narrow - the way out of a prompt, the state of a run - while the
// left is a description that degrades usefully. Doing it the other way round is
// how an 80-column terminal ends up hiding the only key that closes a prompt.
func layout(spans []Span, width int) []byte {
	if width <= 0 {
		return nil
	}
	var left, right []Span
	for _, s := range spans {
		if s.Align == AlignRight {
			right = append(right, s)
			continue
		}
		left = append(left, s)
	}

	rightWidth := 0
	for _, s := range right {
		rightWidth += len(s.Text)
	}
	if rightWidth > width {
		rightWidth = width
	}

	var b []byte
	used := 0
	budget := width - rightWidth
	for _, s := range left {
		if used >= budget {
			break
		}
		text := Clip(s.Text, budget-used)
		if text == "" {
			continue
		}
		b = appendStyled(b, text, s.Style)
		used += len(text)
	}
	// A gap only exists when something is aligned right; otherwise the row ends
	// where its text ends and no trailing spaces are written.
	if rightWidth > 0 {
		for ; used < width-rightWidth; used++ {
			b = append(b, ' ')
		}
		for _, s := range right {
			text := Clip(s.Text, width-used)
			if text == "" {
				continue
			}
			b = appendStyled(b, text, s.Style)
			used += len(text)
		}
	}
	return b
}

// appendStyled writes text wrapped in sgr, closing it so the next span cannot
// inherit it. An empty sgr writes the text bare rather than an empty sequence.
func appendStyled(b []byte, text, sgr string) []byte {
	if sgr == "" {
		return append(b, text...)
	}
	b = append(b, fmt.Sprintf(sgrFmt, sgr)...)
	b = append(b, text...)
	return append(b, sgrReset...)
}

// Render repaints the ENTIRE reserved zone from rows, in a single write.
//
// It is the counterpart to [Region.WriteLine], for content that is a VIEW
// rather than a record. WriteLine appends into a ring, so a line, once
// written, stays until something newer displaces it; that is right for
// failures and wrong for anything that can disappear on its own. Render draws
// the whole zone every time, which is what lets an entry vanish: rows past the
// end of the slice are erased, so a list that shrank leaves no residue behind
// it.
//
// Rows beyond the zone's height are DROPPED rather than scrolled. The caller
// owns the choice of which entries fit, because only it knows whether the
// newest or the oldest is the one worth keeping.
//
// A disabled Region drops the call entirely rather than printing the rows, for
// the reason [Region.SetStatus] gives: a repainted view replayed line by line
// into a pipe or a CI log is noise, not information.
//
// A Region is driven EITHER by WriteLine/SetStatus or by Render, not both:
// they keep separate ideas of which rows are spoken for, and interleaving them
// makes each overwrite the other's.
func (r *Region) Render(rows []Row) error {
	if !r.enabled {
		return nil
	}
	if err := r.Reserve(); err != nil {
		return err
	}
	if !r.enabled {
		return nil
	}
	if err := r.reflow(); err != nil {
		return err
	}
	if !r.enabled {
		return nil
	}

	// One write for the whole frame, bracketed by a single save/restore: a
	// per-row write would let a concurrent caller's output land between two
	// rows, and would leave the cursor parked mid-zone in between.
	//
	// Only CHANGED rows are addressed and redrawn. A zone repainted on a timer
	// - a status line ticking, a notification expiring - is mostly unchanged
	// from frame to frame, and rewriting every row costs bytes on the wire and
	// shows as flicker where the terminal repaints faster than it composites.
	// That is affordable for a six-row failure region and is not for a tall
	// inline viewport, which is the direction this is built for.
	r.buf = r.buf[:0]
	r.buf = append(r.buf, cursorSave...)
	changed := 0
	for i := range r.height {
		var row Row
		if i < len(rows) {
			row = rows[i]
		}
		if i < len(r.painted) && r.painted[i].equal(row) {
			continue
		}
		changed++
		r.buf = append(r.buf, fmt.Sprintf(cupFmt, r.firstRow()+i, 1)...)
		// EL from column 1 erases the whole row, so a row that is now shorter
		// than its predecessor - or empty - cannot leave a tail behind.
		r.buf = append(r.buf, el...)
		r.buf = append(r.buf, layout(row.spans(), r.width-1)...)
	}
	r.buf = append(r.buf, cursorRestore...)

	// Record the frame BEFORE the write is attempted only if it succeeds: a
	// partial write leaves the screen in a state neither frame describes, so
	// the cache is dropped and the next Render redraws in full.
	if changed > 0 {
		if _, err := r.w.Write(r.buf); err != nil {
			r.painted = nil
			return err
		}
	}
	// Padded to the zone's full height, so a frame with fewer rows than the
	// zone still records the blanks it drew and the next frame does not
	// needlessly rewrite them.
	if cap(r.painted) < r.height {
		r.painted = make([]Row, r.height)
	}
	r.painted = r.painted[:r.height]
	for i := range r.height {
		if i < len(rows) {
			r.painted[i] = rows[i]
			continue
		}
		r.painted[i] = Row{}
	}
	return nil
}

// Release hands the terminal back: the zone is cleared and the scroll margins
// reset. Idempotent and safe to defer.
//
// It does NOT reposition the cursor, and that is the point rather than an
// omission. Because nothing here ever moved the caller's cursor, it is already
// sitting exactly after the caller's last line of output - which is where the
// shell prompt belongs. The previous version restored a position saved when the
// region opened, thousands of scrolled lines earlier in a long run, which put
// the prompt back into the middle of the transcript and let it overwrite the
// output the run had just produced.
//
// Which is why the margin reset sits INSIDE the cursor-transparent write rather
// than after it. DECSTBM homes the cursor - the same behaviour Reserve already
// works around when it saves before setting margins - so a reset emitted after
// the restore silently undoes it and parks the cursor at row 1. The shell then
// draws its prompt at the top of the screen and its first redraw erases
// everything below, wiping the transcript the run had just produced. It looks
// like the output flashed up and vanished.
func (r *Region) Release() error {
	if !r.enabled || !r.open {
		return nil
	}
	// Clear the zone and give the rows back in one cursor-transparent write.
	if err := r.paint(r.firstRow(), func() {
		r.buf = append(r.buf, ed...)
		r.buf = append(r.buf, decstbmReset...)
	}); err != nil {
		return err
	}
	r.open = false
	return nil
}

// firstRow is the top of the reserved zone, in absolute terminal rows, using
// the dimensions cached at Reserve.
func (r *Region) firstRow() int { return r.termHeight - r.height + 1 }

// reflow re-measures the terminal and re-applies the scroll margins when
// the window has been resized since the region was reserved.
//
// Without this, a resize leaves the margins describing the old geometry
// while the row arithmetic describes the new one, so lines land outside
// the reserved zone and the scrolling area above is silently clipped.
// The measurement is one ioctl and happens only on a region write, which
// is a failure, not a log line.
//
// A window that has shrunk below what the region needs disables it, and
// the caller falls back to plain output for the rest of the run.
func (r *Region) reflow() error {
	width, termHeight, err := r.probe.Size(r.fd)
	if err != nil {
		return err
	}
	if width == r.width && termHeight == r.termHeight {
		return nil
	}
	if !fits(width, termHeight, r.height) {
		// Give the rows back before standing down, or the shell keeps the
		// old margins after the run ends.
		if relErr := r.Release(); relErr != nil {
			return relErr
		}
		r.enabled = false
		return nil
	}
	r.width, r.termHeight = width, termHeight
	// Re-issue the margins for the new geometry and restart the failure
	// zone; the previous contents scrolled or clipped with the resize and
	// cannot be located reliably.
	r.open = false
	if err := r.Reserve(); err != nil {
		return err
	}
	return nil
}

// ellipsis marks a clipped line. Three ASCII dots rather than U+2026
// because this lands in user-facing terminal output.
const ellipsis = "..."

// Clip returns msg shortened to fit n bytes, ending in an ellipsis when
// truncation happened.
//
// n bounds the whole result, ellipsis included. Callers size n from the
// terminal width, so a result that overshot would wrap onto a second
// screen row and desynchronise the one-row-per-line cursor accounting.
// Truncation never splits a UTF-8 sequence: a multi-byte rune straddling
// the cut is dropped whole.
//
// Counting bytes rather than display cells is exact for the ASCII this
// package emits, and conservative (never over-wide) for anything else.
func Clip(msg string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(msg) <= n {
		return msg
	}
	if n <= len(ellipsis) {
		// Too narrow to carry both content and a truncation mark.
		return ellipsis[:n]
	}
	cut := n - len(ellipsis)
	// Walk back until the cut lands on a rune boundary. Testing the byte
	// AT the cut (rather than before it) is what drops a straddling rune
	// whole: stopping on its lead byte would keep half a sequence and the
	// terminal would render a replacement character.
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut] + ellipsis
}

// ResetScrollMargins clears any DECSTBM margins on w unconditionally.
//
// Region.Release restores a region this process knows it opened. This is
// the process-exit counterpart, for the case where a run may have
// reserved a region somewhere that the exiting code does not hold a
// handle to. Margins belong to the terminal rather than to whoever set
// them, so one reset on the way out restores the user's shell regardless
// of which component was responsible.
//
// It runs on every exit path, including those of commands that never opened a
// region at all, so it has to be inert on a terminal it did not touch. DECSTBM
// homes the cursor, which makes a bare reset anything but inert: on `magus
// help` it was the only escape the whole command emitted, and it left the
// cursor at row 1 for the shell to draw its prompt over the help text. Hence
// the save/restore bracket - taken and released inside this one write, per the
// register rule in [Region].
//
// No-op when w is not a terminal, so callers do not have to branch.
func ResetScrollMargins(w io.Writer, p Probe) error {
	fd, ok := Fd(w)
	if !ok || !p.IsTerminal(fd) {
		return nil
	}
	_, err := io.WriteString(w, cursorSave+decstbmReset+cursorRestore)
	return err
}
