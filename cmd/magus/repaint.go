package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus/internal/interactive/tty"
)

// inlineRepaint redraws a block of text WHERE IT STANDS, by erasing exactly the
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
// which is the same restraint [tty.Region] keeps and the same thing the picker
// has always done - this is that redraw, factored out.
//
// It is NOT a [tty.Region]: there are no scroll margins and no reserved rows,
// because a watch view does not need to survive other output scrolling past. It
// needs to sit in the transcript and be redrawn in place, which is cheaper and
// works at any terminal height.
type inlineRepaint struct {
	w     io.Writer
	probe tty.Probe
	// lines is how many terminal rows the last frame occupied, which is what
	// the next erase has to walk back over.
	lines int
}

// paint draws frame in place of the previous one, reporting whether it could.
//
// A false return means the frame does not fit the redraw model and the caller
// should fall back: erasing in place walks the cursor UPWARD, so a block as
// tall as the terminal has nowhere to walk back to and would eat the rows above
// it. Reported rather than handled here because only the caller knows what the
// alternative is.
func (p *inlineRepaint) paint(frame string) bool {
	width, height, ok := p.size()
	if !ok {
		// No measurable terminal: print plainly and never try to erase, which
		// is what a pipe or a redirect wants anyway.
		fmt.Fprint(p.w, frame)
		return true
	}

	// Clip every line to the terminal width. A line that wrapped would occupy
	// two rows while the accounting counted one, and every erase after it would
	// be off by one - which shows up as the view slowly eating the transcript
	// above it.
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	for i, line := range lines {
		lines[i] = tty.Clip(line, width)
	}
	if len(lines) >= height {
		return false
	}

	if err := tty.EraseLines(p.w, p.lines); err != nil {
		return false
	}
	// No trailing newline: the cursor must end ON the last line of the block,
	// because that is where EraseLines starts walking up from next time.
	if _, err := io.WriteString(p.w, strings.Join(lines, "\n")); err != nil {
		return false
	}
	p.lines = len(lines)
	return true
}

// finish moves off the block so a shell prompt does not land on its last line.
// The frame is deliberately LEFT on screen: it is the answer the reader asked
// for, and erasing it on the way out would be the takeover this type avoids.
func (p *inlineRepaint) finish() {
	if p.lines == 0 {
		return
	}
	fmt.Fprintln(p.w)
	p.lines = 0
}

// size reports the terminal's dimensions, and whether there is one to measure.
func (p *inlineRepaint) size() (width, height int, ok bool) {
	fd, ok := tty.Fd(p.w)
	if !ok || !p.probe.IsTerminal(fd) {
		return 0, 0, false
	}
	w, h, err := p.probe.Size(fd)
	if err != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}
