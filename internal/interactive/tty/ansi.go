package tty

import (
	"fmt"
	"io"
	"strings"
)

// ANSI control sequences. Every escape magus emits is named here rather
// than written inline at a call site, so a reader meets "erase this line"
// instead of "\x1b[2K" and a wrong sequence is a compile error in one
// place rather than a rendering bug in three.
const (
	cupFmt       = "\x1b[%d;%dH" // CUP - cursor position (row, col).
	el           = "\x1b[K"      // EL - erase from cursor to end of line.
	el2          = "\x1b[2K"     // EL - erase the whole line, cursor unmoved.
	ed           = "\x1b[J"      // ED - erase from cursor to end of screen.
	cuu1         = "\x1b[1A"     // CUU - cursor up one row.
	cuuFmt       = "\x1b[%dA"    // CUU - cursor up n rows.
	home         = "\x1b[H"      // CUP with no args - cursor to row 1, col 1.
	ed2          = "\x1b[2J"     // ED - erase the entire screen.
	decstbmFmt   = "\x1b[%d;%dr" // DECSTBM - set scroll margins (top, bottom).
	decstbmReset = "\x1b[r"      // DECSTBM reset (whole screen scrollable).
	// DECSC / DECRC - save and restore the cursor. Deliberately the ESC 7 / ESC 8
	// forms rather than the CSI s / CSI u ones this package used to emit.
	//
	// CSI s / CSI u are the ANSI.SYS (SCO) spelling, and they are not universally
	// implemented: emacs `term` ignores both, so every "cursor-transparent" repaint
	// silently was not - the cursor stayed wherever the paint left it. Worse, xterm
	// reads CSI s as DECSLRM (set left/right margin) once margin mode is on, and
	// this package is in the business of setting margins, so the one sequence meant
	// to protect the cursor could instead reconfigure the terminal.
	//
	// ESC 7 / ESC 8 are the VT100 originals and are what xterm, iTerm2, Terminal.app,
	// tmux, kitty, alacritty, vte and emacs all implement. They also save the SGR
	// state and charset alongside the position, which is a superset of what the
	// CSI pair claimed to do.
	cursorSave    = "\x1b7"
	cursorRestore = "\x1b8"
	sgrFmt        = "\x1b[%sm"
	sgrReset      = "\x1b[0m"
	sgrBoldRed    = "\x1b[1;31m"
	sgrDim        = "\x1b[2m"
)

// SGR parameter codes. These name the colour, not the meaning: a caller
// decides that "a cache hit is dim green", because what reads as low
// signal differs per surface. Shared so the codes themselves are written
// once.
const (
	SGRBold        = "1"
	SGRDim         = "2"
	SGRRed         = "31"
	SGRGreen       = "32"
	SGRYellow      = "33"
	SGRDimGreen    = "2;32"
	SGRDimGrey     = "2;37"
	SGRBrightGreen = "1;32"
)

// Colorize wraps s in an SGR sequence and closes it again, so no caller
// composes escapes by hand or forgets the reset. An empty sgr returns s
// untouched, which lets a caller pass a colour it computed conditionally
// without branching.
//
// Callers that already know output is not a terminal should skip this
// entirely rather than pass an empty code; see [WantsColor].
func Colorize(s, sgr string) string {
	if sgr == "" {
		return s
	}
	return fmt.Sprintf(sgrFmt, sgr) + s + sgrReset
}

// ClearScreen erases the screen and homes the cursor, the repaint a
// full-screen refresh loop issues before redrawing (`magus status
// --watch`).
//
// This is not the alternate screen buffer: scrollback is preserved, so a
// user who scrolls up after quitting still sees what came before. That
// restraint is deliberate and is the same rule [Region] follows.
func ClearScreen(w io.Writer) error {
	_, err := io.WriteString(w, home+ed2)
	return err
}

// EraseLines erases n lines ending at the cursor and leaves the cursor at
// column 0 of the topmost erased line, the redraw step for an in-place
// list that grows and shrinks (the picker).
//
// n <= 0 writes nothing, so a first paint needs no special case.
func EraseLines(w io.Writer, n int) error {
	if n <= 0 {
		return nil
	}
	var b strings.Builder
	b.Grow(n * (len(el2) + len(cuu1) + 1))
	for i := range n {
		b.WriteString(el2)
		b.WriteString("\r")
		if i < n-1 {
			b.WriteString(cuu1)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
