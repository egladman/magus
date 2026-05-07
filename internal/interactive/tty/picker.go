// Package tty is a minimal interactive list picker for the magus CLI.
//
// Items are filtered by an AND substring search over whitespace-split
// tokens of the filter input. Render goes to stderr so stdout stays
// clean for downstream pipes; the caller is expected to have already
// verified that stdin and stderr are TTYs.
package tty

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrAborted is returned when the user presses ESC, Ctrl-C, or Ctrl-D.
var ErrAborted = errors.New("picker: aborted")

// Options configures a single Pick call.
type Options struct {
	// Prompt is the label drawn before the filter input (e.g. "project").
	Prompt string
	// InitialFilter pre-populates the filter string. The picker filters
	// items against it on first paint.
	InitialFilter string
	// Initial is the index in items that should be highlighted on first
	// paint when the post-filter list is non-empty and contains it.
	Initial int
	// MaxRows caps the visible window of matches. Defaults to 10.
	MaxRows int
}

// Pick blocks until the user selects an item or aborts. On success it
// returns the index into the original items slice. On abort it returns
// -1 and ErrAborted.
func Pick(items []string, opts Options) (int, error) {
	if len(items) == 0 {
		return -1, errors.New("picker: no items")
	}
	if opts.MaxRows <= 0 {
		opts.MaxRows = 10
	}

	stdinFD := int(os.Stdin.Fd())
	if !StdinIsTerminal() {
		return -1, errors.New("picker: stdin is not a TTY")
	}

	old, err := term.MakeRaw(stdinFD)
	if err != nil {
		return -1, fmt.Errorf("picker: enter raw mode: %w", err)
	}
	defer func() { _ = term.Restore(stdinFD, old) }()

	s := &session{
		items:    items,
		opts:     opts,
		filter:   opts.InitialFilter,
		out:      os.Stderr,
		reader:   bufio.NewReader(os.Stdin),
		linesOut: 0,
	}
	s.refilter()
	s.cursor = s.findInitial()
	s.draw()
	defer s.cleanup()

	for {
		r, err := s.readKey()
		if err != nil {
			return -1, ErrAborted
		}
		switch r {
		case keyEnter:
			if len(s.matches) == 0 {
				continue
			}
			return s.matches[s.cursor], nil
		case keyEscape, keyCtrlC, keyCtrlD:
			return -1, ErrAborted
		case keyUp:
			if len(s.matches) > 0 {
				s.cursor = (s.cursor - 1 + len(s.matches)) % len(s.matches)
			}
		case keyDown:
			if len(s.matches) > 0 {
				s.cursor = (s.cursor + 1) % len(s.matches)
			}
		case keyBackspace:
			if len(s.filter) > 0 {
				// Strip one rune.
				rs := []rune(s.filter)
				s.filter = string(rs[:len(rs)-1])
				s.refilter()
				s.cursor = 0
			}
		case keyCtrlU:
			s.filter = ""
			s.refilter()
			s.cursor = 0
		default:
			if r >= 0x20 && r != 0x7f {
				s.filter += string(r)
				s.refilter()
				s.cursor = 0
			}
		}
		s.draw()
	}
}

const (
	keyEnter     rune = '\r'
	keyCtrlC     rune = 0x03
	keyCtrlD     rune = 0x04
	keyCtrlU     rune = 0x15
	keyBackspace rune = 0x7f
	keyEscape    rune = 0x1b
	keyUp        rune = 0xE001
	keyDown      rune = 0xE002
	keyLeft      rune = 0xE003
	keyRight     rune = 0xE004
)

type session struct {
	items   []string
	opts    Options
	filter  string
	matches []int // indices into items, post-filter
	cursor  int   // index into matches
	out     *os.File
	reader  *bufio.Reader

	linesOut int // tracks how many lines we last drew so redraws can clear
}

// Filter is exposed for tests: it returns the indices of items that
// match the given filter string under the AND-substring rule.
func Filter(items []string, filter string) []int {
	tokens := strings.Fields(strings.ToLower(filter))
	out := make([]int, 0, len(items))
	for i, it := range items {
		lc := strings.ToLower(it)
		ok := true
		for _, t := range tokens {
			if !strings.Contains(lc, t) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, i)
		}
	}
	return out
}

func (s *session) refilter() {
	s.matches = Filter(s.items, s.filter)
}

func (s *session) findInitial() int {
	for j, idx := range s.matches {
		if idx == s.opts.Initial {
			return j
		}
	}
	return 0
}

func (s *session) readKey() (rune, error) {
	b, err := s.reader.ReadByte()
	if err != nil {
		return 0, err
	}
	if b != 0x1b {
		return rune(b), nil
	}
	// Possible escape sequence: peek with a non-blocking read. On a TTY
	// in raw mode an arrow key arrives as ESC '[' 'A' atomically, so a
	// second ReadByte succeeds immediately if more bytes are buffered.
	if s.reader.Buffered() == 0 {
		return keyEscape, nil
	}
	b2, err := s.reader.ReadByte()
	if err != nil || b2 != '[' {
		return keyEscape, nil //nolint:nilerr // short/!CSI sequence: treat as a bare Escape key
	}
	b3, err := s.reader.ReadByte()
	if err != nil {
		return keyEscape, nil //nolint:nilerr // truncated escape sequence: treat as a bare Escape key
	}
	switch b3 {
	case 'A':
		return keyUp, nil
	case 'B':
		return keyDown, nil
	case 'C':
		return keyRight, nil
	case 'D':
		return keyLeft, nil
	}
	return keyEscape, nil
}

func (s *session) draw() {
	// Erase previous render.
	for i := 0; i < s.linesOut; i++ {
		fmt.Fprint(s.out, "\x1b[2K\r") // clear line, return to col 0
		if i < s.linesOut-1 {
			fmt.Fprint(s.out, "\x1b[1A") // move up
		}
	}
	if s.linesOut > 0 {
		fmt.Fprint(s.out, "\r")
	}

	var b strings.Builder
	visible := s.opts.MaxRows
	if visible > len(s.matches) {
		visible = len(s.matches)
	}
	// Window the visible slice around the cursor.
	start := 0
	if s.cursor >= s.opts.MaxRows {
		start = s.cursor - s.opts.MaxRows + 1
	}
	end := start + visible
	if end > len(s.matches) {
		end = len(s.matches)
		start = end - visible
		if start < 0 {
			start = 0
		}
	}

	for i := start; i < end; i++ {
		idx := s.matches[i]
		marker := "  "
		if i == s.cursor {
			marker = "> "
		}
		b.WriteString(marker)
		b.WriteString(s.items[idx])
		b.WriteString("\r\n")
	}
	if len(s.matches) == 0 {
		b.WriteString("  (no matches)\r\n")
		visible = 1
	}
	// Footer / prompt line.
	prompt := s.opts.Prompt
	if prompt == "" {
		prompt = "filter"
	}
	fmt.Fprintf(&b, "%s: %s_", prompt, s.filter)

	fmt.Fprint(s.out, b.String())
	s.linesOut = visible + 1
}

func (s *session) cleanup() {
	// Erase our drawing so the parent terminal looks like the picker
	// never ran. Caller prints whatever permanent output it wants.
	for i := 0; i < s.linesOut; i++ {
		fmt.Fprint(s.out, "\x1b[2K\r")
		if i < s.linesOut-1 {
			fmt.Fprint(s.out, "\x1b[1A")
		}
	}
	s.linesOut = 0
}
