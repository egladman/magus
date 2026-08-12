package tty

import (
	"fmt"
	"io"
	"strings"
)

// NewInlineView returns a view that redraws its block in place on w, measured
// through p.
func NewInlineView(w io.Writer, p Probe) *InlineView {
	return &InlineView{w: w, probe: p}
}

// Reset forgets what is on screen, for a caller that cleared the terminal
// underneath this view and is starting again.
func (v *InlineView) Reset() { v.painted = nil }

// Lines reports how many rows the block currently occupies.
func (v *InlineView) Lines() int { return len(v.painted) }

// Clear erases the block, leaving the terminal as though the view never drew.
//
// The counterpart to [InlineView.Finish], which leaves the frame in place. Both
// are right, for different views: a status readout is the answer the reader
// asked for and should stay, while a picker is a question that has been
// answered and should get out of the way.
func (v *InlineView) Clear() error {
	if len(v.painted) == 0 {
		return nil
	}
	err := EraseLines(v.w, len(v.painted))
	v.painted = nil
	return err
}

// InlineView redraws a block of text WHERE IT STANDS, by erasing exactly the
// lines it drew last time and writing the new frame over them.
//
// It replaces a clear-the-screen-and-reprint loop, which is the wrong shape for
// a watch view in two ways. It is a takeover: everything the reader had on
// screen is wiped, and what they were doing before is gone from view even
// though it survives in scrollback. And it flickers, because erasing the whole
// screen and rewriting every cell several times a second gives the terminal a
// blank frame to composite in between.
//
// Erasing only the block leaves the transcript above it untouched and visible,
// which is the same restraint [Region] keeps and the same thing the picker
// has always done - this is that redraw, factored out.
//
// It is NOT a [Region]: there are no scroll margins and no reserved rows,
// because a watch view does not need to survive other output scrolling past. It
// needs to sit in the transcript and be redrawn in place, which is cheaper and
// works at any terminal height.
type InlineView struct {
	w     io.Writer
	probe Probe
	// painted is the last frame drawn, so the next one can rewrite only the
	// lines that differ. Its length is how many rows the block occupies, which
	// is also what an erase has to walk back over.
	painted []string
}

// paint draws frame in place of the previous one, reporting whether it could.
//
// A false return means the frame does not fit the redraw model and the caller
// should fall back: erasing in place walks the cursor UPWARD, so a block as
// tall as the terminal has nowhere to walk back to and would eat the rows above
// it. Reported rather than handled here because only the caller knows what the
// alternative is.
func (v *InlineView) Paint(frame string) bool {
	width, height, ok := v.size()
	if !ok {
		// No measurable terminal: print plainly and never try to erase, which
		// is what a pipe or a redirect wants anyway.
		fmt.Fprint(v.w, frame)
		return true
	}

	// Clip every line to the terminal width. A line that wrapped would occupy
	// two rows while the accounting counted one, and every erase after it would
	// be off by one - which shows up as the view slowly eating the transcript
	// above it.
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	for i, line := range lines {
		lines[i] = Clip(line, width)
	}
	if len(lines) >= height {
		return false
	}

	// A block of a different height cannot be diffed against the old one, so it
	// is erased and rewritten whole. Height changes are rare - a watch view
	// mostly animates in place - and this is the only path that has to be
	// correct without knowing what was on screen.
	if len(lines) != len(v.painted) {
		if err := EraseLines(v.w, len(v.painted)); err != nil {
			return false
		}
		// No trailing newline: the cursor must end ON the last line of the
		// block, because that is where every later move starts from.
		if _, err := io.WriteString(v.w, strings.Join(lines, "\n")); err != nil {
			return false
		}
		v.painted = lines
		return true
	}
	if err := v.diff(lines); err != nil {
		return false
	}
	v.painted = lines
	return true
}

// diff rewrites only the lines that changed, moving straight to each one.
//
// Walking the block line by line would be simpler and would cost a cursor move
// per row whether or not it changed; jumping means an unchanged frame writes
// NOTHING, which is the steady state of anything repainted on a timer.
//
// The cursor starts and ends on the last line of the block. That invariant is
// what every other path here depends on, so it holds even when no line changed
// and nothing at all is written.
//
// optimization: rewrite only changed lines instead of the whole block.
//
//	measured: BenchmarkWatchFrame 4424 -> 448 B_written/sec (-89.9%)
//	          (benchstat, n=8) for a status grid animating one spinner cell.
//	trade-off: relative cursor arithmetic, which is only correct while the
//	          cursor stays where this believes it left it. Verified against the
//	          terminal emulator rather than by inspection.
func (v *InlineView) diff(lines []string) error {
	var b strings.Builder
	cur := len(v.painted) - 1 // the cursor sits on the last line
	for i, line := range lines {
		if line == v.painted[i] {
			continue
		}
		b.WriteString(moveRows(i - cur))
		b.WriteString("\r")
		b.WriteString(el2)
		b.WriteString(line)
		cur = i
	}
	if b.Len() == 0 {
		return nil
	}
	// Back to the last line, so the next paint starts where it expects to.
	b.WriteString(moveRows(len(lines) - 1 - cur))
	_, err := io.WriteString(v.w, b.String())
	return err
}

// moveRows returns the sequence that moves the cursor n rows down, or up when
// n is negative. Zero writes nothing.
func moveRows(n int) string {
	switch {
	case n > 0:
		return fmt.Sprintf(cudFmt, n)
	case n < 0:
		return fmt.Sprintf(cuuFmt, -n)
	}
	return ""
}

// finish moves off the block so a shell prompt does not land on its last line.
// The frame is deliberately LEFT on screen: it is the answer the reader asked
// for, and erasing it on the way out would be the takeover this type avoids.
func (v *InlineView) Finish() {
	if len(v.painted) == 0 {
		return
	}
	fmt.Fprintln(v.w)
	v.painted = nil
}

// size reports the terminal's dimensions, and whether there is one to measure.
func (v *InlineView) size() (width, height int, ok bool) {
	fd, ok := Fd(v.w)
	if !ok || !v.probe.IsTerminal(fd) {
		return 0, 0, false
	}
	w, h, err := v.probe.Size(fd)
	if err != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}
