package tty

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
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
	cudFmt       = "\x1b[%dB"    // CUD - cursor down n rows.
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
	// IND - index: move down one row, scrolling at the bottom margin, and leave
	// the COLUMN alone.
	//
	// This is what reserves rows, rather than the "\n" it used to be. A cooked
	// terminal has ONLCR set, so a newline is carriage-return-plus-line-feed
	// and silently returns the caller's cursor to column 1. region promises the
	// caller's cursor never moves, and for a caller mid-line - a REPL holding a
	// prompt, anything writing a partial line - that promise was broken by the
	// one sequence meant to be invisible. IND is the VT100 original with
	// exactly the semantics wanted here, and needs no mode to behave.
	ind      = "\x1bD"
	sgrFmt   = "\x1b[%sm"
	sgrReset = "\x1b[0m"
)

// SGR is a set of Select Graphic Rendition parameters - the part of
// "\x1b[1;31m" between the bracket and the m.
//
// A named type rather than a bare string because the calls that carry one also
// carry message text: Notify(text, style, ttl) and Pin(key, text, style) put
// two same-typed strings in different positions, so a transposition used to
// compile and paint a message that read as a style code. It cannot now.
type SGR string

// SGR parameter codes. These name the colour, not the meaning: a caller
// decides that "a cache hit is dim green", because what reads as low
// signal differs per surface. Shared so the codes themselves are written
// once.
const (
	SGRBold    SGR = "1"
	SGRDim     SGR = "2"
	SGRRed     SGR = "31"
	SGRGreen   SGR = "32"
	SGRYellow  SGR = "33"
	SGRBoldRed SGR = "1;31"
	// SGRReverse swaps foreground and background. It marks the selected row of
	// an interactive band, and is used instead of a colour because it composes:
	// "7;1;31" is the same red the row already had, highlighted.
	SGRReverse     SGR = "7"
	SGRDimGreen    SGR = "2;32"
	SGRDimGrey     SGR = "2;37"
	SGRBrightGreen SGR = "1;32"
)

// Colorize wraps s in an SGR sequence and closes it again, so no caller
// composes escapes by hand or forgets the reset. An empty sgr returns s
// untouched, which lets a caller pass a colour it computed conditionally
// without branching.
//
// Callers that already know output is not a terminal should skip this
// entirely rather than pass an empty code; see [WantsColor].
func Colorize(s string, sgr SGR) string {
	if sgr == "" {
		return s
	}
	return fmt.Sprintf(sgrFmt, string(sgr)) + s + sgrReset
}

// OSC 8 hyperlink framing: ESC ] 8 ; params ; URI ST ... ESC ] 8 ; ; ST.
const (
	osc8Prefix = "\x1b]8;;"
	osc8Fmt    = "\x1b]8;;%s\x1b\\"
	osc8End    = "\x1b]8;;\x1b\\"
)

// Hyperlink makes text a real clickable link to uri, for terminals that
// support OSC 8 - iTerm2, kitty, VTE, WezTerm, Windows Terminal - and returns
// text unchanged for everything else.
//
// It is worth having where a mouse cannot reach: the reserved zone can be
// hit-tested because magus knows where it drew, but the SCROLLING transcript
// belongs to the terminal, and a click there is the user selecting text. A
// hyperlink is the only way to make something up there actionable, and it
// needs no mouse capture at all, survives into scrollback, and stays
// copy-pasteable as plain text.
//
// Callers gate on [WantsHyperlinks] first; this composes the sequence and does
// not decide whether to.
func Hyperlink(text, uri string) string {
	if uri == "" {
		return text
	}
	return fmt.Sprintf(osc8Fmt, uri) + text + osc8End
}

// ClipVisible shortens s to cols DISPLAY COLUMNS, counting only what the
// reader can see and never cutting inside an escape sequence.
//
// [Clip] counts bytes, which is exact for the plain text it was written for and
// catastrophic for text that is already styled. A single coloured cell is one
// column and about fifteen bytes, so a byte budget cuts a coloured row long
// before it is actually too wide - in the middle of an escape - after which the
// terminal reads the following text as parameters and swallows it. That is a
// corrupted screen, repainted several times a second.
//
// Escape sequences are copied through whole and cost nothing against the
// budget, so the styling of the part that survives is preserved along with any
// reset that closes it.
func ClipVisible(s string, cols int) string {
	if cols <= 0 {
		return ""
	}
	var b strings.Builder
	used, i := 0, 0
	for i < len(s) && used < cols {
		if n := escapeLen(s[i:]); n > 0 {
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		used++
		i += size
	}
	// Sequences sitting immediately after the last visible column are the ones
	// that CLOSE it, so they come along: stopping at the budget would otherwise
	// cut before the reset and bleed the last style into whatever is written
	// next. Only closers - copying whatever escape happens to be next would
	// pick up the OPENING of the cell that did not fit.
	for i < len(s) {
		n := escapeLen(s[i:])
		if n == 0 || !closesStyle(s[i:i+n]) {
			break
		}
		b.WriteString(s[i : i+n])
		i += n
	}
	out := b.String()
	if i >= len(s) {
		return out
	}
	// Truncated. Anything still open has to be closed here, because the text
	// that would have closed it is exactly what was dropped. A redundant
	// terminator is harmless; a missing one leaves the terminal styled, or
	// waiting for the end of a hyperlink.
	if strings.Count(out, osc8Prefix)-strings.Count(out, osc8End) > 0 {
		out += osc8End
	}
	if strings.Contains(out, "\x1b[") && !strings.HasSuffix(out, sgrReset) {
		out += sgrReset
	}
	return out
}

// closesStyle reports whether seq ends a style or a hyperlink rather than
// starting one.
func closesStyle(seq string) bool {
	return seq == sgrReset || seq == "\x1b[m" || seq == osc8End
}

// escapeLen reports the length of the escape sequence starting at s, or 0 when
// s does not start with one.
//
// It recognises the two shapes magus emits: CSI (ESC [ ... final byte) and OSC
// (ESC ] ... ST or BEL), the latter because hyperlinks carry a URI that must
// never be counted as visible text or cut in half.
func escapeLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b {
		return 0
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	// A two-byte escape (ESC 7, ESC 8, ESC D).
	return 2
}

// ClearScreen erases the screen and homes the cursor, the repaint a
// full-screen refresh loop issues before redrawing (`magus status
// --watch`).
//
// This is not the alternate screen buffer: scrollback is preserved, so a
// user who scrolls up after quitting still sees what came before. That
// restraint is deliberate and is the same rule [region] follows.
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
