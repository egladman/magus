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

// region pins the bottom rows of a terminal as a zone that does not
// scroll, so what is drawn there stays on screen while ordinary output
// continues to scroll above. The zone is established with DECSTBM scroll
// margins.
//
// Callers do not build one directly: [Zone] owns the terminal's bottom rows
// for the whole process and hands out bands of them as leases. A second
// region on the same terminal would set its own margins from its own row
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
// That last property is what lets one type serve a logging caller and an
// interactive one: neither can tolerate a repaint that parks the cursor in
// the region - the log lands in the footer, or the user types over their
// own status line - and neither should need to know the geometry.
//
// The corollary is a rule about the cursor-save register: it is a single
// global slot, so it is taken and released within ONE write and never held
// across calls. Holding it for the life of the region meant any repaint
// clobbered the saved position and teardown reinstated the wrong one.
//
// The alternate screen buffer is never touched.
//
// A region is not safe for concurrent use; [Zone] is the boundary that makes
// it so, because its consumers are on different threads.
type region struct {
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
	// titleL and titleR are drawn into the top rule; see setTitle.
	titleL, titleR string
	// painted is the last frame [region.render] drew, so the next one can skip
	// rows that did not change. Nil means "nothing on screen is known", which
	// is the state after any repaint of the zone by other means (Reserve
	// clears it, a resize re-reserves), and forces a full redraw.
	painted []Line
}

// newRegion returns a region that will pin height rows at the bottom of
// the terminal behind w, measured through p.
//
// Nothing is written to w here: the reservation happens on Reserve or the
// first Render, so a run that never draws never touches the terminal.
// Enabled is false when w has no descriptor, when it is not a terminal,
// when the terminal is too small to host height rows alongside a useful
// scrolling area, or when the size query fails. A disabled region is safe
// to use and writes plain lines.
//
// Pass [SystemProbe] in production; tests pass their own Probe.
func newRegion(w io.Writer, height int, p Probe) *region {
	r := &region{w: w, probe: p, height: height, buf: make([]byte, 0, 256)}
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

// Enabled reports whether this region will drive the terminal. When false,
// Render drops its rows and Reserve and Release do nothing.
func (r *region) isEnabled() bool { return r.enabled }

// Reserve sets the scroll margins so the bottom rows stop scrolling.
// Idempotent, and render calls it for you, so callers do not have to pair
// them.
//
// The terminal is re-measured here rather than trusting the dimensions
// from NewRegion: the window may have been resized in between, and
// applying stale margins would put the cursor outside the scroll region.
// If the terminal has since become too small, the region disables itself
// and the caller falls back to plain output.
//
// It leaves the caller's cursor exactly where it found it relative to the
// caller's output - the contract this type keeps (see [region]). That is what
// the opening index sequences are for: moving down height rows and stepping
// back up guarantees the rows exist without moving the cursor relative to the
// text. At the bottom the screen scrolls; mid-screen nothing does and the step
// back is exact. Correct either way, and it never destroys a written row.
func (r *region) reserve() error {
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
func (r *region) paint(row int, body func()) error {
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
	Style SGR
	Align Align
	// Key names this span as a click target, or is empty for ordinary text.
	//
	// It exists so that a row which PRINTS an action is also a place you can
	// click to take it, without the caller doing column arithmetic that would
	// then have to be kept in step with the layout. Callers set a key, ask
	// [Zone.HitSpan] what a click landed on, and never see a column.
	//
	// Keeping the two together is the point: a hint the surface draws and a
	// hint it responds to cannot drift apart if they are the same span.
	Key string
}

// Line is one line of a whole-zone repaint.
//
// Text and Style are shorthand for the overwhelmingly common single-span row;
// Spans is the general form and wins when both are set. They are not two code
// paths - [Line.spans] normalizes the shorthand into a one-element list and the
// renderer only ever sees spans - so the convenience cannot drift from the
// general case.
type Line struct {
	Text  string
	Style SGR
	Spans []Span
}

// SpansCopy returns this line's spans as a fresh slice, normalizing the
// Text+Style shorthand on the way.
//
// Exported for a caller COMPOSING onto an existing line - the band appends a
// divider and a second column to rows it has already built. Doing that by hand
// means re-implementing the shorthand normalization, and the copy is what stops
// an append from writing into a slice the line still shares.
func (r Line) SpansCopy() []Span { return append([]Span(nil), r.spans()...) }

// spans normalizes a Line into the form the renderer works with.
func (r Line) spans() []Span {
	if len(r.Spans) > 0 {
		return r.Spans
	}
	if r.Text == "" {
		return nil
	}
	return []Span{{Text: r.Text, Style: r.Style}}
}

// equal reports whether two rows would paint identically. Row carries a slice,
// so the frame diff in [region.render] cannot use ==.
func (r Line) equal(o Line) bool {
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

// Cols is the display width of s, escape sequences excluded.
//
// Exported because internal/cache composes rows for this package's layout, so
// it has to measure them the same way - and measuring them its own way is
// exactly how this bug reached four copies.
func Cols(s string) int { return cols(s) }

// cols is the DISPLAY WIDTH of s, which is what every budget in this file is
// denominated in.
//
// len() is not that, and the difference stopped being academic the moment rows
// carried box-drawing: a tree branch is three bytes and one column, so a
// byte budget spends three columns on it and the row is clipped to a third of
// its width, leaving the box's right edge ragged. This has now been the same
// bug three times - the border, the accent bar, and the title - so the counting
// lives in one place.
func cols(s string) int {
	// Escape sequences are SKIPPED, not counted. Some callers hand us text that
	// already carries its own SGR - a status glyph colored at the point it is
	// composed - and counting those bytes as columns padded the row short,
	// putting the box's right border nine columns in from the edge on exactly
	// the rows that had a glyph.
	// Through escapeLen rather than a second scanner: this one recognized CSI
	// only and stepped two bytes past an OSC, so a hyperlink's whole URI counted
	// as visible columns. A failure ref is hyperlinked, so that inflated an
	// eight-column span to seventy, clipped text that fit, and left the box's
	// right edge short on exactly the rows carrying a link.
	n := 0
	for i := 0; i < len(s); {
		if e := escapeLen(s[i:]); e > 0 {
			i += e
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

// fit shortens text to width DISPLAY COLUMNS, marking the cut.
//
// ClipCols gets the widths right, which is what keeps the box square, but it
// cuts silently - and a row that was truncated must say so, or a reader takes a
// half value for the whole one. So the mark is put back here, inside the same
// budget, rather than by going back to the byte-counting Clip that put it there
// before.
func fit(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if cols(text) <= width {
		return text
	}
	if width <= cols(ellipsis) {
		return ellipsis[:width]
	}
	return ClipCols(text, width-cols(ellipsis)) + ellipsis
}

// spanExtent is where a keyed span ended up, in 1-based inclusive columns.
type spanExtent struct {
	key      string
	from, to int
}

// layout renders spans, discarding where they landed. Most callers only paint.
// layout composes one row's spans into the bytes for a terminal `width` wide.
//
// RIGHT-ALIGNED SPANS ARE LAID OUT FIRST AND KEPT; the left side is clipped to
// whatever remains. That order is the policy, not an implementation detail: the
// things that sit on the right are the ones that must not vanish when the
// window is narrow - the way out of a prompt, the state of a run - while the
// left is a description that degrades usefully. Doing it the other way round is
// how an 80-column terminal ends up hiding the only key that closes a prompt.
func layout(spans []Span, width int) []byte {
	b, _, _ := layoutExtents(spans, width)
	return b
}

// layoutExtents renders spans AND reports the columns each keyed one occupies.
//
// One function rather than a renderer plus a parallel column calculator: the
// clipping, the right-alignment gap and the budget are the arithmetic that
// decides both answers, and two copies of it would agree until the first time
// they did not - at which point a click would land on the wrong action with
// nothing on screen to suggest why.
func layoutExtents(spans []Span, width int) ([]byte, []spanExtent, int) {
	if width <= 0 {
		return nil, nil, 0
	}
	var extents []spanExtent
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
		rightWidth += cols(s.Text)
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
		text := fit(s.Text, budget-used)
		if text == "" {
			continue
		}
		b = appendStyled(b, text, s.Style)
		if s.Key != "" {
			extents = append(extents, spanExtent{key: s.Key, from: used + 1, to: used + cols(text)})
		}
		used += cols(text)
	}
	// A gap only exists when something is aligned right; otherwise the row ends
	// where its text ends and no trailing spaces are written.
	if rightWidth > 0 {
		for ; used < width-rightWidth; used++ {
			b = append(b, ' ')
		}
		for _, s := range right {
			text := fit(s.Text, width-used)
			if text == "" {
				continue
			}
			b = appendStyled(b, text, s.Style)
			if s.Key != "" {
				extents = append(extents, spanExtent{key: s.Key, from: used + 1, to: used + cols(text)})
			}
			used += cols(text)
		}
	}
	return b, extents, used
}

// appendStyled writes text wrapped in sgr, closing it so the next span cannot
// inherit it. An empty sgr writes the text bare rather than an empty sequence.
func appendStyled(b []byte, text string, sgr SGR) []byte {
	if sgr == "" {
		return append(b, text...)
	}
	b = append(b, fmt.Sprintf(sgrFmt, string(sgr))...)
	b = append(b, text...)
	return append(b, sgrReset...)
}

// Render repaints the ENTIRE reserved zone from rows, in a single write.
//
// The whole zone is drawn every time, which is what lets an entry VANISH: rows
// past the end of the slice are erased, so a list that shrank leaves no residue.
//
// Rows beyond the zone's height are DROPPED rather than scrolled - only the
// caller knows whether the newest or the oldest is worth keeping.
//
// A disabled region drops the call rather than printing the rows: a repainted
// view replayed into a pipe is noise. The caller decides what to print, because
// only it knows whether its content is a record or a view.
func (r *region) render(rows []Line) error {
	if !r.enabled {
		return nil
	}
	if err := r.reserve(); err != nil {
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
		// Region row 0 is the top edge and the last is the bottom, so content
		// row n is drawn one row lower. The edges compare as the zero Line,
		// which is what keeps them from being rewritten every frame: they never
		// change, so after the first paint the diff skips them.
		var row Line
		if c := i - borderRowsPerEdge; c >= 0 && c < len(rows) && i != r.height-1 {
			row = rows[c]
		}
		if i < len(r.painted) && r.painted[i].equal(row) {
			continue
		}
		changed++
		r.buf = append(r.buf, fmt.Sprintf(cupFmt, r.firstRow()+i, 1)...)
		// EL from column 1 erases the whole row, so a row that is now shorter
		// than its predecessor - or empty - cannot leave a tail behind.
		r.buf = append(r.buf, el...)
		switch i {
		case 0:
			r.buf = append(r.buf, r.edgeText(true)...)
		case r.height - 1:
			r.buf = append(r.buf, r.edgeText(false)...)
		default:
			r.buf = append(r.buf, r.frameText(row)...)
		}
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
		r.painted = make([]Line, r.height)
	}
	r.painted = r.painted[:r.height]
	for i := range r.height {
		c := i - borderRowsPerEdge
		if c < 0 || c >= len(rows) || i == r.height-1 {
			r.painted[i] = Line{}
			continue
		}
		// The Spans slice is COPIED, not aliased. Storing the caller's slice
		// would let a caller that reuses one across frames mutate what this
		// believes is on screen, so equal would compare a frame against itself
		// and the diff would skip a row that really changed.
		row := rows[c]
		if len(row.Spans) > 0 {
			row.Spans = append([]Span(nil), row.Spans...)
		}
		r.painted[i] = row
	}
	return nil
}

// Release hands the terminal back: the zone is cleared and the scroll margins
// reset. Idempotent and safe to defer.
//
// It does NOT reposition the cursor, deliberately: nothing here ever moved it,
// so it already sits after the caller's last line, which is where the shell
// prompt belongs. Restoring a position saved when the region opened put the
// prompt into the middle of the transcript.
//
// Which is why the margin reset sits INSIDE the cursor-transparent write. DECSTBM
// homes the cursor, so a reset emitted after the restore undoes it and parks the
// cursor at row 1 - the shell then draws its prompt at the top and its first
// redraw erases everything below, which looks like the output flashed up and
// vanished.
func (r *region) release() error {
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
func (r *region) firstRow() int { return r.termHeight - r.height + 1 }

// innerWidth is the columns a framed row has for its own content: the usable
// width less the two vertical edges.
//
// The usable width is width-1, not width: [region.render] holds the last column
// back so writing it cannot trigger the terminal's pending wrap.
func (r *region) innerWidth() int { return r.width - 1 - 2*borderCols }

// The border glyphs. Box-drawing rather than ASCII, and unconditionally rather
// than by locale, which are two decisions worth separating.
//
// Box-drawing because these runes JOIN: "+---+" is a row of separate marks that
// the eye reads as decoration, while a real rectangle reads as an edge, which is
// the whole job - saying which rows hold still and which scroll away.
//
// Unconditionally because one look everywhere beats the case it gives up. They
// are multi-byte, so a non-UTF-8 locale shows mojibake - but the border is only
// drawn when [CanRender] says there is an interactive terminal, and the
// environments still running non-UTF-8 locales are the ones where output is
// piped and no border is drawn. Choosing per locale would also make a committed
// picture a function of the shell that generated it.
//
// They are East Asian AMBIGUOUS width, so a CJK locale rendering them
// double-width will shear the box. `magus doctor` reports both rather than
// silently changing what magus draws.
const (
	boxH = "\u2500"
	boxV = "\u2502"
	// ROUNDED corners. Square ones (U+250C and friends) read as a table cell -
	// a form to be filled in - while the arc reads as a panel, which is what
	// this is. It is the single cheapest change that makes a terminal surface
	// look designed rather than drawn, and it costs the same one column.
	boxTL = "\u256d"
	boxTR = "\u256e"
	boxBL = "\u2570"
	boxBR = "\u256f"
)

// dim styles a border glyph, or leaves it bare when the terminal does not want
// color. The border is drawn by this type rather than composed by a caller, so
// the caller's color decision cannot reach it - it has to ask.
func (r *region) dim(s string) string {
	if !WantsColor(r.w, r.probe) {
		return s
	}
	return Colorize(s, SGRDim)
}

// SetTitle puts text into the region's top rule, so a caption costs no row.
//
// A status line IS a property of the band, so the frame is where it belongs -
// and a titled box is how every other framed UI says so. Held on the region
// rather than passed per render because it changes on its own clock (a pool
// sample, an elapsed second) independently of the rows beneath it.
func (r *region) setTitle(left, right string) {
	// UNCHANGED is the common case and must cost nothing. The caption is set on
	// every repaint, so invalidating unconditionally threw away the frame diff
	// entirely - every pool sample re-addressed and rewrote all four rows of the
	// zone, which is precisely what the diff exists to avoid.
	if r.titleL == left && r.titleR == right {
		return
	}
	r.titleL, r.titleR = left, right
	r.painted = nil // the rule is cached like any row; a new caption must redraw it
}

// edgeText renders the top or bottom rule as bytes.
//
// Bytes rather than a Line through [layout], because layout budgets in BYTES and
// a box-drawing rune is three of them to one column: routed through it, an edge
// would be clipped to a third of the width.
func (r *region) edgeText(top bool) []byte {
	inner := r.innerWidth()
	if inner < 1 {
		return nil
	}
	if !top {
		return []byte(boxRule(inner, boxBL, boxBR, "", "", r.dim))
	}
	return []byte(boxRule(inner, boxTL, boxTR, r.titleL, r.titleR, r.dim))
}

// frameText renders one content row inside the vertical edges.
//
// The content is laid out to the inner width and then padded by COLUMNS to it,
// which is why layoutExtents reports what it consumed: the padding has to put
// the right edge in the same column on every row, and the byte length of a
// styled run says nothing about where it ends on screen.
func (r *region) frameText(row Line) []byte {
	inner := r.innerWidth()
	if inner < 1 {
		return layout(row.spans(), r.width-1)
	}
	body, _, used := layoutExtents(row.spans(), inner)
	// Padded from the laid-out width rather than by re-measuring: layoutExtents
	// already knows how many COLUMNS it consumed, and body carries escape
	// sequences that a second measurement would have to skip again.
	if used < inner {
		body = append(body, strings.Repeat(" ", inner-used)...)
	}
	return []byte(boxWrap(string(body), inner, r.dim))
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
func (r *region) reflow() error {
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
		if relErr := r.release(); relErr != nil {
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
	if err := r.reserve(); err != nil {
		return err
	}
	return nil
}

// ellipsis marks a clipped line. Three ASCII dots rather than U+2026
// because this lands in user-facing terminal output.
const ellipsis = "..."

// ClipBytes returns msg shortened to fit nBytes, ending in an ellipsis when
// truncation happened.
//
// nBytes bounds the whole result, ellipsis included, and is a BYTE budget: exact
// for the ASCII this package emits and conservative for anything else. Already
// styled text needs [ClipCols] instead, or escape bytes eat the budget and the
// cut lands inside a sequence.
//
// Truncation never splits a UTF-8 sequence: a rune straddling the cut is dropped
// whole.
func ClipBytes(msg string, nBytes int) string {
	if nBytes <= 0 {
		return ""
	}
	if len(msg) <= nBytes {
		return msg
	}
	if nBytes <= len(ellipsis) {
		// Too narrow to carry both content and a truncation mark.
		return ellipsis[:nBytes]
	}
	cut := nBytes - len(ellipsis)
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
// region.Release restores a region this process knows it opened. This is
// the process-exit counterpart, for the case where a run may have
// reserved a region somewhere that the exiting code does not hold a
// handle to. Margins belong to the terminal rather than to whoever set
// them, so one reset on the way out restores the user's shell regardless
// of which component was responsible.
//
// It runs on every exit path, including commands that never opened a region, so
// it must be inert on a terminal it did not touch. DECSTBM homes the cursor, so
// a bare reset is not inert - on `magus help` it left the cursor at row 1 for the
// shell to draw its prompt over the help text. Hence the save/restore bracket,
// taken and released inside one write, per the register rule in [region].
//
// No-op when w cannot render escapes at all, so callers do not have to branch.
//
// Gated on CanRender rather than on a bare descriptor check, which is what its own doc
// asks for: this MOVES the cursor, and TERM=dumb is a real terminal that renders the
// sequence as literal garbage. Nothing is lost by skipping it there - no component that
// reserves rows will run under TERM=dumb either, so there is no margin left to reset.
func ResetScrollMargins(w io.Writer, p Probe) error {
	if !CanRender(w, p) {
		return nil
	}
	_, err := io.WriteString(w, cursorSave+decstbmReset+cursorRestore)
	return err
}
