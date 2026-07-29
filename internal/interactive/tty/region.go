package tty

import (
	"fmt"
	"io"
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
// scroll, so failures written there stay on screen while ordinary
// output continues to scroll above. The zone is established with
// DECSTBM scroll margins.
//
// The contract is additive, never destructive:
//   - Scrollback above the region is preserved; selection and copy
//     still work there.
//   - Release restores the terminal. A panic or interrupted run leaves
//     the terminal in its default state, never in an alternate-screen
//     mode the user cannot escape.
//   - On any non-TTY (pipe, file, CI log, `script`), every method
//     degrades to plain line output so the caller does not branch.
//
// That last property is the difference between a useful status area and
// the full-screen takeovers some build tools use: the alternate screen
// buffer (`\e[?1049h`) is never touched here.
//
// A Region is not safe for concurrent use; callers serialise their own
// writes (the cache's pretty handler holds its mutex across a record).
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
	// cursorRow is the absolute terminal row the next line lands on. It
	// advances per line and wraps back to the top of the failure zone, so
	// the newest failure overwrites the oldest.
	cursorRow int
	// statusActive records that SetStatus has claimed the region's first
	// row. Until it does, failure lines use the whole region; afterwards
	// they start one row lower. It latches on first use rather than being
	// declared up front so a caller that never shows a status line does
	// not pay a blank row for the option.
	statusActive bool
	// buf composes one line so it reaches the terminal as a single
	// Write. Keeps cursor positioning atomic and avoids per-byte flicker.
	buf []byte
}

// NewRegion returns a Region that will pin height rows at the bottom of
// the terminal behind w, measured through p.
//
// Nothing is written to w here. The reservation happens on Reserve, or
// on the first WriteLine, so a run that never fails never touches the
// user's terminal. Enabled reports whether the reservation will be
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
	if !ok || !p.IsTerminal(fd) {
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

// Enabled reports whether this Region will drive the terminal. When
// false, WriteLine emits plain lines and Reserve and Release do nothing.
func (r *Region) Enabled() bool { return r.enabled }

// Reserve sets the scroll margins so the bottom rows stop scrolling.
// Idempotent, and WriteLine calls it for you, so callers do not have to
// pair them.
//
// The terminal is re-measured here rather than trusting the dimensions
// from NewRegion: the window may have been resized in between, and
// applying stale margins would put the cursor outside the scroll region.
// If the terminal has since become too small, the Region disables itself
// and the caller falls back to plain output.
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
	// Save the cursor BEFORE setting margins: DECSTBM homes the cursor,
	// so a save afterwards would record row 1 rather than where the
	// user's output had reached.
	r.buf = append(r.buf, cursorSave...)
	// Scrolling is confined to the rows above the region.
	r.buf = append(r.buf, fmt.Sprintf(decstbmFmt, 1, r.firstRow()-1)...)
	// Park at the top of the region and clear it so a previous run's
	// text cannot show through.
	r.buf = append(r.buf, fmt.Sprintf(cupFmt, r.firstRow(), 1)...)
	r.buf = append(r.buf, ed...)
	if _, err := r.w.Write(r.buf); err != nil {
		return err
	}
	r.open = true
	r.cursorRow = r.failureFirstRow()
	return nil
}

// WriteLine renders msg as one line inside the region, in bold red so a
// failure reads distinctly from the scrolling output above. The line is
// clipped to the terminal width, and the colour is closed so the next
// line cannot inherit it.
//
// Once the region is full the cursor wraps to the top, so the newest
// failures replace the oldest. A disabled Region writes msg plainly to
// the underlying writer, which is what makes a single call path serve
// both TTY and piped runs.
//
// The name is WriteLine, not Write, because Region deliberately does not
// implement io.Writer: the cursor accounting depends on one call being
// exactly one line.
func (r *Region) WriteLine(msg string) error {
	if !r.enabled {
		_, err := fmt.Fprintln(r.w, msg)
		return err
	}
	if err := r.Reserve(); err != nil {
		// Reserve failed mid-run. Fall back to plain output so the
		// failure still reaches the user, and report why.
		if _, werr := fmt.Fprintln(r.w, msg); werr != nil {
			return werr
		}
		return err
	}
	if !r.enabled {
		// Reserve disabled the region (the window shrank).
		_, err := fmt.Fprintln(r.w, msg)
		return err
	}
	if err := r.reflow(); err != nil {
		return err
	}
	if !r.enabled {
		// The window shrank past what the region needs.
		_, err := fmt.Fprintln(r.w, msg)
		return err
	}

	r.buf = r.buf[:0]
	// Address this line's row explicitly. Without it each write would
	// resume wherever the last one left the cursor and every line in the
	// region would run together on a single row.
	r.buf = append(r.buf, fmt.Sprintf(cupFmt, r.cursorRow, 1)...)
	r.buf = append(r.buf, sgrBoldRed...)
	// EL from column 1 clears the whole row, so a short message cannot
	// leave a longer predecessor's tail behind it.
	r.buf = append(r.buf, el...)
	r.buf = append(r.buf, Clip(msg, r.width-1)...)
	r.buf = append(r.buf, sgrReset...)
	if _, err := r.w.Write(r.buf); err != nil {
		return err
	}

	r.cursorRow++
	if r.cursorRow > r.lastRow() {
		// Wrap to the top of the failure zone so the newest failure
		// overwrites the oldest. Wrapping only past the final row means
		// the bottom row is used before reuse begins.
		r.cursorRow = r.failureFirstRow()
	}
	return nil
}

// Release restores the terminal: scroll margins cleared, cursor put back
// where it was when the region opened. Idempotent and safe to defer.
func (r *Region) Release() error {
	if !r.enabled || !r.open {
		return nil
	}
	r.buf = r.buf[:0]
	r.buf = append(r.buf, decstbmReset...)
	r.buf = append(r.buf, cursorRestore...)
	if _, err := r.w.Write(r.buf); err != nil {
		return err
	}
	r.open = false
	return nil
}

// firstRow and lastRow bound the reserved zone, in absolute terminal
// rows, using the dimensions cached at Reserve.
func (r *Region) firstRow() int { return r.termHeight - r.height + 1 }
func (r *Region) lastRow() int  { return r.termHeight }

// failureFirstRow is the top row available to WriteLine. It sits one
// below firstRow once a status line has claimed the region's first row.
func (r *Region) failureFirstRow() int {
	if r.statusActive {
		return r.firstRow() + 1
	}
	return r.firstRow()
}

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

// SetStatus pins msg to the region's first row, redrawing it in place.
//
// It is for a live counter the reader watches rather than a record they
// scroll back to: a concurrency pool's occupancy, for instance. Failure
// lines written by WriteLine sit below it and are never overwritten by
// it. The status row is dim, not red, so it reads as ambient state next
// to the failures it sits above.
//
// The first call claims the row, which shrinks the failure zone by one.
// Call it before the first WriteLine (a run reports pool state as soon
// as work starts, so this holds in practice); calling it later costs the
// oldest visible failure line.
//
// A disabled Region drops the message entirely rather than printing it:
// a status line is a repainted view, and replaying every update into a
// pipe or a CI log would be noise, not information.
func (r *Region) SetStatus(msg string) error {
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
	if !r.statusActive {
		r.statusActive = true
		// The failure zone just lost its top row; move the write cursor
		// out of the status row if it was still parked there.
		if r.cursorRow < r.failureFirstRow() {
			r.cursorRow = r.failureFirstRow()
		}
	}

	r.buf = r.buf[:0]
	r.buf = append(r.buf, fmt.Sprintf(cupFmt, r.firstRow(), 1)...)
	r.buf = append(r.buf, sgrDim...)
	r.buf = append(r.buf, el...)
	r.buf = append(r.buf, Clip(msg, r.width-1)...)
	r.buf = append(r.buf, sgrReset...)
	_, err := r.w.Write(r.buf)
	return err
}

// ReturnCursor parks the cursor at column 1 of the last row ABOVE the region,
// which is where a caller that owns its own cursor should resume writing.
//
// It exists for the interactive case. [SetStatus] leaves the cursor inside the
// region, which is harmless for a caller whose next write addresses a row anyway
// (that is every logging consumer), and fatal for one holding a prompt: the cursor
// sits outside the scroll region, so the next thing printed - and every character
// the terminal echoes as the user types - lands in the footer instead of the
// transcript.
//
// The obvious alternative is for SetStatus to save and restore the cursor itself.
// It cannot: [Reserve] already holds the terminal's single cursor-save slot
// (\e[s) for the whole life of the region so [Release] can restore it, and a
// per-repaint save would clobber that, leaving Release to restore wherever the last
// repaint happened. So the caller that owns the cursor is the one told where to put
// it back, and Region's save slot stays untouched.
//
// A disabled Region does nothing, so a piped or non-TTY caller needs no branch.
func (r *Region) ReturnCursor() error {
	if !r.enabled || !r.open {
		return nil
	}
	_, err := fmt.Fprintf(r.w, cupFmt, r.firstRow()-1, 1)
	return err
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
// No-op when w is not a terminal, so callers do not have to branch.
func ResetScrollMargins(w io.Writer, p Probe) error {
	fd, ok := Fd(w)
	if !ok || !p.IsTerminal(fd) {
		return nil
	}
	_, err := io.WriteString(w, decstbmReset)
	return err
}
