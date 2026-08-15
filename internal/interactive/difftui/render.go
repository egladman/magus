package difftui

import (
	"strings"

	"github.com/egladman/magus/internal/interactive/tty"
)

// The keys, stated on screen. A viewer whose bindings are only in the documentation is a
// viewer nobody drives with anything but the arrow keys.
const (
	readHelp     = "]/[ hunk   }/{ file   v read   . generated   esc overview   q quit"
	overviewHelp = "up/down file   enter jump   esc back"
)

// The palette, named for what each colour MEANS in a changeset rather than for the colour, the
// way the cache log names its own. Every one is a code tty already defines, so this package
// invents no escape sequence of its own.
//
// Emphasis is the line's colour made BOLD, which the palette already carries as a pair. It reads
// as more of the same thing rather than as a different kind of thing, and it deliberately is not
// SGRReverse: reverse means "the selected row" everywhere else in magus, and a diff where every
// changed word looked selected would be saying something it does not mean.
const (
	sgrAdd      = tty.SGRGreen
	sgrAddEmph  = tty.SGRBrightGreen // bold green
	sgrDel      = tty.SGRRed
	sgrDelEmph  = tty.SGRBoldRed
	sgrFileHead = tty.SGRBold
	sgrHunkHead = tty.SGRDim
)

// unrankedBanner is the caveat that changes how the whole list should be read, so it is
// PINNED above the viewport rather than scrolled with the rows: as a scrolling line it goes
// off screen after one page and the reader spends the rest of the review believing the top
// file is the most dangerous one. Same wording `magus diff` prints, same reason.
var unrankedBanner = []string{
	"UNRANKED: no symbol index, so there is no consequence to rank by.",
	"This is path order, not a ranking. `magus graph build` gives it one.",
}

// Chrome is how many rows a frame spends on things that are not changeset rows, so a caller
// can size the viewport against the terminal before painting.
func Chrome(m *Model) int {
	if m.Overview() {
		return 2 // the heading and the help line
	}
	if m.Unranked() {
		return len(unrankedBanner) + 1
	}
	return 1
}

// Frame composes what the viewer looks like right now.
//
// It returns a string rather than writing one so [tty.InlineView] can rewrite only the rows
// that differ - moving the cursor changes two lines and costs two lines of terminal traffic.
//
// colour is decided by the caller, from [tty.WantsColor], and is a parameter rather than
// something read here so the model stays as testable as it is: false must produce the same bytes
// this drew before there was a palette at all.
func Frame(m *Model, color bool) string {
	if m.Overview() {
		return overviewFrame(m)
	}
	var b strings.Builder
	if m.Unranked() {
		for _, line := range unrankedBanner {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	rows := m.Rows()
	end := min(m.Top()+m.Height(), len(rows))
	for i := m.Top(); i < end; i++ {
		b.WriteString(gutter(i == m.CursorRow()))
		b.WriteString(rowText(rows[i], color))
		b.WriteString("\n")
	}
	// Pad to a fixed height so the block does not change shape as the reader moves through a
	// short file, which would make InlineView erase and rewrite the whole thing every time.
	for i := end - m.Top(); i < m.Height(); i++ {
		b.WriteString("\n")
	}
	b.WriteString(readHelp)
	return b.String()
}

// overviewFrame is the changeset at a glance: what is in it and how much is read.
//
// Deliberately unstyled, which is not an oversight: bold marks a FILE HEADING apart from the
// diff body it introduces, and here every row is a file heading. Bolding all of them would
// distinguish nothing and only make the screen heavier.
func overviewFrame(m *Model) string {
	var b strings.Builder
	b.WriteString("changeset overview\n")
	rows := m.OverviewRows()
	start := 0
	if m.OverviewCursor() >= m.Height() {
		start = m.OverviewCursor() - m.Height() + 1
	}
	end := min(start+m.Height(), len(rows))
	for i := start; i < end; i++ {
		b.WriteString(gutter(i == m.OverviewCursor()))
		b.WriteString(rows[i])
		b.WriteString("\n")
	}
	for i := end - start; i < m.Height(); i++ {
		b.WriteString("\n")
	}
	b.WriteString(overviewHelp)
	return b.String()
}

// rowText styles one row, or hands back exactly what the model composed when the terminal gets
// no styling. That plain branch is the one that has to stay byte-identical: it is what a reader
// sees under NO_COLOR, on a dumb terminal, and in every test that compares a frame.
func rowText(r Row, color bool) string {
	if !color {
		return r.Text
	}
	switch r.Kind {
	case RowFile:
		return tty.Colorize(r.Text, sgrFileHead)
	case RowHunk:
		return tty.Colorize(r.Text, sgrHunkHead)
	case RowLine:
		return lineText(r)
	}
	return r.Text
}

// lineText colours one line of a hunk and draws the part that changed harder than the rest.
//
// It cuts the PLAIN text and styles each piece separately, which is what makes it impossible to
// slice an escape sequence in half - the mistake that turns a viewer into a corrupted screen
// rather than a slightly wrong one. Each piece closes its own style, so nothing bleeds into the
// next.
func lineText(r Row) string {
	var base, emph tty.SGR
	switch {
	case isAdd(r.Text):
		base, emph = sgrAdd, sgrAddEmph
	case isDel(r.Text):
		base, emph = sgrDel, sgrDelEmph
	default:
		// Context, and the marker line saying a file has no trailing newline: nothing changed
		// here, so nothing is coloured.
		return r.Text
	}
	if r.Emph.Empty() {
		return tty.Colorize(r.Text, base)
	}
	return paint(r.Text[:r.Emph.Start], base) +
		paint(r.Text[r.Emph.Start:r.Emph.End], emph) +
		paint(r.Text[r.Emph.End:], base)
}

// paint styles a piece of a line, and leaves an empty piece empty rather than emitting a pair of
// escapes that style nothing.
func paint(s string, sgr tty.SGR) string {
	if s == "" {
		return ""
	}
	return tty.Colorize(s, sgr)
}

// gutter is the two columns every row starts with: the mark on the row a keypress acts on,
// blank everywhere else. Same glyph the picker and the run's failure band use, so one
// gesture looks like one affordance across every interactive surface.
func gutter(cursor bool) string {
	if cursor {
		return tty.SelectMark + " "
	}
	return "  "
}
