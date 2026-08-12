package tty

import (
	"strconv"
	"strings"
)

// screen is a terminal emulator: it consumes the escape stream this package
// writes and reconstructs the grid a user would actually be looking at.
//
// It exists because every assertion here used to be a substring check against
// raw bytes - "the output contains \x1b[23;1H" - which proves a sequence was
// emitted and says nothing about where the text LANDED. The bugs this package
// has actually had were all of the second kind: a cursor left in the wrong
// place, a repaint that scrolled the transcript, margins that survived
// teardown. None of them are visible to a substring check, and all of them are
// obvious in a rendered grid.
//
// It implements exactly the vocabulary ansi.go emits and nothing else, so it
// is a complete model of what magus does to a terminal rather than a partial
// model of terminals in general. An unrecognised sequence is dropped rather
// than guessed at.
//
// That last rule cuts both ways, and it has already bitten once: a sequence
// this does not know is SILENTLY IGNORED, so adding one to the production code
// without adding it here makes the emulator quietly model a different terminal
// than the one users have. Anything new in ansi.go belongs here in the same
// change.
//
// The scroll-region behaviour is the part that carries its weight: a newline on
// the last row of the scroll region scrolls only rows [top, bottom], leaving
// the reserved zone below untouched. That single rule is what the whole Region
// design rests on, and emulating it is what lets a test prove the zone survives
// output rather than assuming it.
type screen struct {
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

func newScreen(width, height int) *screen {
	s := &screen{width: width, height: height, row: 1, col: 1, scrollTop: 1, scrollBot: height}
	s.cells = make([][]cell, height)
	for i := range s.cells {
		s.cells[i] = make([]cell, width)
		for j := range s.cells[i] {
			s.cells[i][j] = cell{r: ' '}
		}
	}
	return s
}

// Fd and Write make a screen usable directly as a Region's writer, so a test
// drives the real code path rather than a transcript of it.
func (*screen) Fd() uintptr { return 2 }

func (s *screen) Write(p []byte) (int, error) {
	src := string(p)
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == 0x1b:
			i += s.escape(src[i:])
		case c == '\n':
			// ONLCR: a cooked terminal turns a bare newline into carriage
			// return plus line feed, which is what every Go program printing to
			// one actually gets. Modelling the line feed alone would leave the
			// column where the text ended and misplace everything after it.
			s.col = 1
			s.lineFeed()
			i++
		case c == '\r':
			s.col = 1
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
func (s *screen) escape(src string) int {
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
		s.row, s.col = clamp(r, 1, s.height), clamp(c, 1, s.width)
	case 'A': // CUU
		s.row = clamp(s.row-oneParam(params, 1), 1, s.height)
	case 'B': // CUD
		s.row = clamp(s.row+oneParam(params, 1), 1, s.height)
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
			s.scrollTop, s.scrollBot = clamp(a, 1, s.height), clamp(b, 1, s.height)
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
func (s *screen) lineFeed() {
	if s.row != s.scrollBot {
		s.row = clamp(s.row+1, 1, s.height)
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

func (s *screen) put(r rune) {
	if s.col > s.width {
		// Wrap, which for this package is a bug rather than a feature: Clip
		// exists so a zone row never reaches here.
		s.col = 1
		s.lineFeed()
	}
	s.cells[s.row-1][s.col-1] = cell{r: r, style: s.style}
	s.col++
}

func (s *screen) eraseCols(row, from, to int) {
	if row < 1 || row > s.height {
		return
	}
	for c := from; c <= to && c <= s.width; c++ {
		s.cells[row-1][c-1] = cell{r: ' '}
	}
}

// rows renders the grid as right-trimmed text, one string per terminal row.
func (s *screen) rows() []string {
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
func (s *screen) String() string { return strings.Join(s.rows(), "\n") }

// row1 returns one 1-based row's text.
func (s *screen) row1(n int) string { return s.rows()[n-1] }

// styleAt reports the SGR parameters in force at a cell, so a test can assert
// that a warning is yellow without matching escape bytes.
func (s *screen) styleAt(row, col int) string { return s.cells[row-1][col-1].style }

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

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func decodeRune(s string) (rune, int) {
	for i, r := range s {
		if i == 0 {
			return r, len(string(r))
		}
	}
	return ' ', 1
}
