package tty

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilter_AND(t *testing.T) {
	items := []string{
		"apps/web/dashboard",
		"apps/mobile/dashboard",
		"services/api",
		"tools/scripts",
	}

	t.Run("empty matches all", func(t *testing.T) {
		assert.Equal(t, []int{0, 1, 2, 3}, Filter(items, ""))
	})
	t.Run("single token substring", func(t *testing.T) {
		assert.Equal(t, []int{0, 1}, Filter(items, "dash"))
	})
	t.Run("AND narrows", func(t *testing.T) {
		assert.Equal(t, []int{1}, Filter(items, "dash mobile"))
	})
	t.Run("AND no match", func(t *testing.T) {
		assert.Empty(t, Filter(items, "dash api"))
	})
	t.Run("case insensitive", func(t *testing.T) {
		assert.Equal(t, []int{0}, Filter(items, "DASH WEB"))
	})
	t.Run("order independent", func(t *testing.T) {
		assert.Equal(t, []int{1}, Filter(items, "mobile dash"))
	})
	t.Run("surrounding whitespace is split, not matched", func(t *testing.T) {
		assert.Equal(t, []int{1}, Filter(items, "   mobile   dash   "))
	})
}

// newTestSession builds a picker session rendering into buf and reading
// keys from input, so the filter, cursor, and redraw logic can be driven
// without raw mode or a pty. Pick itself needs a real terminal to call
// term.MakeRaw; every decision it delegates is covered here.
func newTestSession(buf *bytes.Buffer, input string, items []string, opts Options) *session {
	if opts.MaxRows <= 0 {
		opts.MaxRows = 10
	}
	s := &session{
		items:  items,
		opts:   opts,
		filter: opts.InitialFilter,
		out:    buf,
		reader: bufio.NewReader(strings.NewReader(input)),
	}
	s.refilter()
	s.cursor = s.findInitial()
	return s
}

func TestSessionFindInitialLocatesTheRequestedItem(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newTestSession(&buf, "", []string{"alpha", "beta", "gamma"}, Options{Initial: 2})
	assert.Equal(t, 2, s.cursor, "with no filter the initial index is its own match index")
}

// TestSessionFindInitialFallsBackToFirst covers the case where the
// requested item is filtered out: the cursor must land somewhere valid
// rather than pointing past the end of the match list.
func TestSessionFindInitialFallsBackToFirst(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newTestSession(&buf, "", []string{"alpha", "beta"}, Options{Initial: 1, InitialFilter: "alpha"})
	assert.Equal(t, 0, s.cursor, "an initial item that no longer matches falls back to the first")
}

func TestSessionDrawMarksTheCursorRow(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newTestSession(&buf, "", []string{"alpha", "beta", "gamma"}, Options{Prompt: "project"})
	s.cursor = 1
	s.draw()

	out := buf.String()
	assert.Contains(t, out, "> beta", "the cursor row is marked")
	assert.Contains(t, out, "  alpha", "non-cursor rows are indented, not marked")
	assert.Contains(t, out, "project: _", "the prompt and filter caret are drawn")
	assert.Equal(t, 4, s.linesOut, "three matches plus the prompt line")
}

func TestSessionDrawUsesADefaultPrompt(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newTestSession(&buf, "", []string{"alpha"}, Options{})
	s.draw()
	assert.Contains(t, buf.String(), "filter: _", "an unset prompt falls back to a generic label")
}

func TestSessionDrawReportsNoMatches(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newTestSession(&buf, "", []string{"alpha"}, Options{InitialFilter: "zzz"})
	s.draw()

	assert.Contains(t, buf.String(), "(no matches)")
	assert.Equal(t, 2, s.linesOut, "the empty-state line plus the prompt line")
}

// TestSessionDrawErasesThePreviousRender is what keeps a shrinking list
// from leaving orphaned rows on screen: the second paint must erase as
// many lines as the first one drew.
func TestSessionDrawErasesThePreviousRender(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newTestSession(&buf, "", []string{"alpha", "beta", "gamma"}, Options{})
	s.draw()
	drawn := s.linesOut
	require.Equal(t, 4, drawn)

	buf.Reset()
	s.filter = "alpha"
	s.refilter()
	s.cursor = 0
	s.draw()

	out := buf.String()
	assert.Equal(t, drawn, strings.Count(out, el2),
		"every previously drawn line must be erased before the redraw")
	assert.Equal(t, 2, s.linesOut, "one match plus the prompt line")
}

func TestSessionDrawWindowsAroundTheCursor(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newTestSession(&buf, "", []string{"a0", "a1", "a2", "a3", "a4", "a5"}, Options{MaxRows: 3})
	s.cursor = 5 // past the window; the view must scroll to include it
	s.draw()

	out := buf.String()
	assert.Contains(t, out, "> a5", "the cursor item must be visible")
	assert.Contains(t, out, "a3", "the window ends at the cursor")
	assert.NotContains(t, out, "a0", "items above the window are not drawn")
	assert.Equal(t, 4, s.linesOut, "MaxRows rows plus the prompt line")
}

func TestSessionCleanupErasesEverythingItDrew(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newTestSession(&buf, "", []string{"alpha", "beta"}, Options{})
	s.draw()
	drawn := s.linesOut

	buf.Reset()
	s.cleanup()

	assert.Equal(t, drawn, strings.Count(buf.String(), el2),
		"cleanup must erase exactly what it drew, so the picker leaves no trace")
	assert.Zero(t, s.linesOut)
}

func TestSessionReadKeyDecodesControlAndArrowKeys(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		input string
		want  rune
	}{
		{"enter", "\r", keyEnter},
		{"ctrl-c", "\x03", keyCtrlC},
		{"ctrl-d", "\x04", keyCtrlD},
		{"ctrl-u", "\x15", keyCtrlU},
		{"backspace", "\x7f", keyBackspace},
		{"printable rune", "x", 'x'},
		{"arrow up", "\x1b[A", keyUp},
		{"arrow down", "\x1b[B", keyDown},
		{"arrow right", "\x1b[C", keyRight},
		{"arrow left", "\x1b[D", keyLeft},
		{"bare escape", "\x1b", keyEscape},
		{"escape with a non-CSI follower", "\x1bZ", keyEscape},
		{"unrecognised CSI final byte", "\x1b[Z", keyEscape},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			s := newTestSession(&buf, tc.input, []string{"item"}, Options{})
			got, err := s.readKey()
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSessionReadKeyReportsEOF(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newTestSession(&buf, "", []string{"item"}, Options{})
	_, err := s.readKey()
	assert.Error(t, err, "an exhausted reader must surface as an error so Pick aborts")
}

func TestPickRejectsAnEmptyItemList(t *testing.T) {
	t.Parallel()
	idx, err := Pick(nil, Options{})
	assert.Equal(t, -1, idx)
	assert.Error(t, err, "there is nothing to pick from")
}
