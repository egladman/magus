// Package screen is a terminal emulator: it consumes an escape stream and
// reconstructs the grid a reader would be looking at.
//
// It exists so a test can assert on the PICTURE rather than the bytes. The
// difference is not cosmetic: a substring check proves a sequence was emitted
// and says nothing about where the text landed, and every bug this codebase has
// actually had in terminal rendering was of the second kind - a cursor left in
// the wrong place, a repaint that ate the transcript, a downward move the
// receiving end ignored.
//
// It lives in its own package rather than beside the code it checks because two
// other packages need it: internal/cache, whose pinned band has to line up with
// the coordinates a mouse click resolves against, and eventually a recorder
// that replays a session for documentation.
package screen

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// tabWidth is the column interval between tab stops. Eight is the value every
// terminal ships with and the one the tools being recorded assume; magus never
// changes it, so it is a constant rather than a configurable stop table.
const tabWidth = 8

// Screen is a terminal: a grid of cells, a cursor, and a scroll region.
//
// It implements exactly the vocabulary magus emits and nothing else, so it is a
// complete model of what magus does to a terminal rather than a partial model
// of terminals in general. An unrecognised sequence is dropped rather than
// guessed at.
//
// That rule cuts both ways, and it has already bitten once: a sequence this
// does not know is SILENTLY IGNORED, so adding one to the production code
// without adding it here makes this quietly model a different terminal than the
// one users have. Anything new in the emitted vocabulary belongs here in the
// same change.
//
// The scroll-region behaviour is the part that carries its weight: a newline on
// the last row of the scroll region scrolls only rows [top, bottom], leaving
// the reserved zone below untouched. That single rule is what the whole Region
// design rests on, and emulating it is what lets a test prove the zone survives
// output rather than assuming it.
type Screen struct {
	width, height int
	cells         [][]cell
	// row and col are 1-based, matching the CUP coordinates the terminal takes.
	row, col             int
	savedRow, savedCol   int
	savedStyle           string
	scrollTop, scrollBot int
	style                string
	// scrolled counts lines pushed off the top of the scroll region, so a test
	// can assert that a repaint did NOT disturb the transcript.
	scrolled int
}

type cell struct {
	r     rune
	style string
}

// New returns a Screen of the given size, cursor at the top left and the whole
// grid scrollable.
func New(width, height int) *Screen {
	s := &Screen{width: width, height: height, row: 1, col: 1, scrollTop: 1, scrollBot: height}
	s.cells = make([][]cell, height)
	for i := range s.cells {
		s.cells[i] = make([]cell, width)
		for j := range s.cells[i] {
			s.cells[i][j] = cell{r: ' '}
		}
	}
	return s
}

// Fd and Write make a Screen usable directly as a writer wherever production
// code expects a terminal, so a test drives the real code path rather than a
// transcript of it.
func (*Screen) Fd() uintptr { return 2 }

func (s *Screen) Write(p []byte) (int, error) {
	src := string(p)
	for i := 0; i < len(src); {
		c := src[i]
		switch c {
		case 0x1b:
			i += s.escape(src[i:])
		case '\n':
			// ONLCR: a cooked terminal turns a bare newline into carriage
			// return plus line feed, which is what every Go program printing to
			// one actually gets. Modelling the line feed alone would leave the
			// column where the text ended and misplace everything after it.
			s.col = 1
			s.lineFeed()
			i++
		case '\r':
			s.col = 1
			i++
		case '\t':
			// HT advances to the next 8-column stop; it does not erase, so the
			// cells it skips keep whatever is already in them.
			//
			// Modelling it as an ordinary character put ONE cell was wrong in a
			// way only a picture shows: `go test` prints "ok  \tacme/admin\t0.531s",
			// so the columns after each tab landed up to seven cells left of
			// where the terminal actually put them - and the rendered SVG
			// carried a literal tab into its <text>, which no renderer expands.
			// The drift gate could not catch it, because it compares the
			// renderer against itself rather than against a terminal.
			s.col = min(((s.col-1)/tabWidth+1)*tabWidth+1, s.width)
			i++
		default:
			r, size := decodeRune(src[i:])
			s.put(r)
			i += size
		}
	}
	return len(p), nil
}

// escape consumes one escape sequence and returns its length in bytes.
func (s *Screen) escape(src string) int {
	if len(src) < 2 {
		return len(src)
	}
	// ESC 7 / ESC 8 - save and restore the cursor (and the SGR state with it).
	switch src[1] {
	case '7':
		s.savedRow, s.savedCol, s.savedStyle = s.row, s.col, s.style
		return 2
	case '8':
		s.row, s.col, s.style = s.savedRow, s.savedCol, s.savedStyle
		return 2
	case 'D': // IND - line feed, column untouched.
		s.lineFeed()
		return 2
	case ']':
		// OSC: ESC ] ... terminated by BEL or ST. Hyperlinks carry a URI that
		// must not land in the grid as text. Consumed and dropped - the model
		// has no notion of a link, only of what occupies a cell.
		for i := 2; i < len(src); i++ {
			if src[i] == 0x07 {
				return i + 1
			}
			if src[i] == 0x1b && i+1 < len(src) && src[i+1] == '\\' {
				return i + 2
			}
		}
		return len(src)
	case '[':
	default:
		return 2
	}

	// CSI: parameters, then a final byte.
	end := 2
	for end < len(src) && (src[end] == ';' || src[end] == '?' || (src[end] >= '0' && src[end] <= '9')) {
		end++
	}
	if end >= len(src) {
		return len(src)
	}
	params, final := src[2:end], src[end]
	n := end + 1

	switch final {
	case 'H': // CUP
		r, c := 1, 1
		if a, b, ok := twoParams(params); ok {
			r, c = a, b
		}
		s.row, s.col = clamp(r, s.height), clamp(c, s.width)
	case 'A': // CUU
		s.row = clamp(s.row-oneParam(params, 1), s.height)
	case 'B': // CUD
		s.row = clamp(s.row+oneParam(params, 1), s.height)
	case 'K': // EL
		switch params {
		case "2":
			s.eraseCols(s.row, 1, s.width)
		default:
			s.eraseCols(s.row, s.col, s.width)
		}
	case 'J': // ED
		switch params {
		case "2":
			for r := 1; r <= s.height; r++ {
				s.eraseCols(r, 1, s.width)
			}
		default:
			s.eraseCols(s.row, s.col, s.width)
			for r := s.row + 1; r <= s.height; r++ {
				s.eraseCols(r, 1, s.width)
			}
		}
	case 'r': // DECSTBM
		if params == "" {
			s.scrollTop, s.scrollBot = 1, s.height
			break
		}
		if a, b, ok := twoParams(params); ok {
			top, bot := clamp(a, s.height), clamp(b, s.height)
			// An INVERTED region is ignored, as a real terminal ignores it.
			// Clamping the two independently accepted ESC[10;3r, and lineFeed
			// then sliced cells[9:2] and panicked - a malformed sequence from
			// captured output taking the process down. Crop guards the same
			// shape and says so; this is where it is also reachable.
			if top < bot {
				s.scrollTop, s.scrollBot = top, bot
			}
		}
	case 'm': // SGR
		if params == "" || params == "0" {
			s.style = ""
			break
		}
		s.style = params
	}
	return n
}

// lineFeed advances a row, scrolling the SCROLL REGION - not the screen - when
// the cursor is already on its last row. This is the rule the reserved zone
// depends on.
func (s *Screen) lineFeed() {
	if s.row != s.scrollBot {
		s.row = clamp(s.row+1, s.height)
		return
	}
	copy(s.cells[s.scrollTop-1:s.scrollBot-1], s.cells[s.scrollTop:s.scrollBot])
	blank := make([]cell, s.width)
	for i := range blank {
		blank[i] = cell{r: ' '}
	}
	s.cells[s.scrollBot-1] = blank
	s.scrolled++
}

func (s *Screen) put(r rune) {
	if s.col > s.width {
		// Wrap, which for this package is a bug rather than a feature: Clip
		// exists so a zone row never reaches here.
		s.col = 1
		s.lineFeed()
	}
	s.cells[s.row-1][s.col-1] = cell{r: r, style: s.style}
	s.col++
}

func (s *Screen) eraseCols(row, from, to int) {
	if row < 1 || row > s.height {
		return
	}
	for c := from; c <= to && c <= s.width; c++ {
		s.cells[row-1][c-1] = cell{r: ' '}
	}
}

// Rows renders the grid as right-trimmed text, one string per terminal row.
func (s *Screen) Rows() []string {
	out := make([]string, s.height)
	for i, row := range s.cells {
		var b strings.Builder
		for _, c := range row {
			b.WriteRune(c.r)
		}
		out[i] = strings.TrimRight(b.String(), " ")
	}
	return out
}

// String renders the whole screen, for a failure message that shows what the
// user would have been looking at.
func (s *Screen) String() string { return strings.Join(s.Rows(), "\n") }

// Row returns one 1-based row's text.
func (s *Screen) Row(n int) string { return s.Rows()[n-1] }

// StyleAt reports the SGR parameters in force at a cell, so a test can assert
// that a warning is yellow without matching escape bytes.
func (s *Screen) StyleAt(row, col int) string { return s.cells[row-1][col-1].style }

func oneParam(p string, def int) int {
	if p == "" {
		return def
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return def
	}
	return n
}

func twoParams(p string) (int, int, bool) {
	a, b, ok := strings.Cut(p, ";")
	if !ok {
		return 0, 0, false
	}
	x, err1 := strconv.Atoi(a)
	y, err2 := strconv.Atoi(b)
	return x, y, err1 == nil && err2 == nil
}

// clamp bounds v to [1, hi]. Every caller is a 1-based terminal coordinate, so
// the lower bound is not a parameter.
func clamp(v, hi int) int { return min(max(v, 1), hi) }

func decodeRune(s string) (rune, int) {
	// Not a hand-rolled decode: the range-loop version reported the width of
	// RuneError as 3, so an invalid byte made the model skip two valid ones.
	return utf8.DecodeRuneInString(s)
}

// Snapshot returns an independent copy of the screen as it is right now.
//
// This is how a frame is captured for a recording: the terminal keeps being
// written to, and a sequence of pointers to one live Screen would all show its
// final state. The copy carries the CELLS, styles included - which the obvious
// alternative of re-rendering String() into a fresh screen does not, because
// String is plain text and drops every colour.
//
// Cursor position and scroll region are copied too, so a snapshot is a complete
// terminal rather than a picture of one.
func (s *Screen) Snapshot() *Screen {
	c := &Screen{
		width: s.width, height: s.height,
		row: s.row, col: s.col,
		savedRow: s.savedRow, savedCol: s.savedCol, savedStyle: s.savedStyle,
		scrollTop: s.scrollTop, scrollBot: s.scrollBot,
		style: s.style, scrolled: s.scrolled,
	}
	c.cells = make([][]cell, len(s.cells))
	for i, row := range s.cells {
		c.cells[i] = make([]cell, len(row))
		copy(c.cells[i], row)
	}
	return c
}

// LastUsedRow returns the 1-based row of the last row with any text on it, or 0
// when the screen is blank.
//
// What it is for: a recording is taken at a terminal size the SESSION needs, and
// the rows a reserved band occupied are blank again once the band is released.
// Rendering those to a picture spends a sixth of the image on nothing.
func (s *Screen) LastUsedRow() int {
	for r := len(s.cells); r > 0; r-- {
		for _, c := range s.cells[r-1] {
			if c.r != 0 && c.r != ' ' {
				return r
			}
		}
	}
	return 0
}

// Crop returns a copy holding only the first rows rows.
//
// Cropping rather than recording at the smaller size on purpose: the size drives
// how the tools being recorded lay their output out and how many rows magus
// reserves, so shrinking the terminal changes the session. This changes only the
// picture of it. A rows outside 1..height is clamped.
func (s *Screen) Crop(rows int) *Screen {
	c := s.Snapshot()
	if rows < 1 {
		rows = 1
	}
	if rows >= c.height {
		return c
	}
	c.cells = c.cells[:rows]
	c.height = rows
	// EVERY row coordinate, not just the obvious two. The result is a *Screen,
	// which is an io.Writer like any other, so a caller may write to it - and a
	// saved cursor past the new height indexes out of range on the ESC 8 that
	// restores it, while scrollTop past scrollBot panics in lineFeed's copy on
	// a low > high slice.
	c.row = clamp(c.row, rows)
	c.savedRow = clamp(c.savedRow, rows)
	c.scrollBot = clamp(c.scrollBot, rows)
	c.scrollTop = clamp(c.scrollTop, c.scrollBot)
	return c
}

// Cursor reports where the cursor is, in 1-based terminal coordinates.
func (s *Screen) Cursor() (row, col int) { return s.row, s.col }

// ScrollRegion reports the rows the terminal will scroll, 1-based inclusive.
// Rows outside it are pinned.
func (s *Screen) ScrollRegion() (top, bottom int) { return s.scrollTop, s.scrollBot }

// Scrolled reports how many lines have been pushed off the top of the scroll
// region, so a test can assert a repaint did not disturb the transcript.
func (s *Screen) Scrolled() int { return s.scrolled }

// FindRow returns the 1-based row whose text contains substr, or 0.
//
// It is how a test asks "where did this actually end up" without hard-coding
// the arithmetic it is trying to check.
func (s *Screen) FindRow(substr string) int {
	for i, row := range s.Rows() {
		if strings.Contains(row, substr) {
			return i + 1
		}
	}
	return 0
}
