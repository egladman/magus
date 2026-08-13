package tty

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestColorizeWrapsAndResets(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "\x1b[31mboom\x1b[0m", Colorize("boom", SGRRed))
	assert.Equal(t, "\x1b[2;32mcached\x1b[0m", Colorize("cached", SGRDimGreen))
}

// TestColorizeWithoutACodeIsATransparentPassThrough is what lets a caller
// compute a colour conditionally and hand it over without branching.
func TestColorizeWithoutACodeIsATransparentPassThrough(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "plain", Colorize("plain", ""))
}

func TestClearScreenHomesThenErases(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, ClearScreen(&buf))
	assert.Equal(t, home+ed2, buf.String(), "the cursor must be homed before the erase")
}

// TestClearScreenNeverUsesTheAlternateBuffer pins the project's rule that
// terminal output stays additive: the alternate screen buffer would hide
// the user's scrollback and take over their terminal.
func TestClearScreenNeverUsesTheAlternateBuffer(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, ClearScreen(&buf))
	assert.NotContains(t, buf.String(), "?1049", "the alternate screen buffer is never used")
}

func TestEraseLinesIsANoOpForNothingDrawn(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, EraseLines(&buf, 0))
	require.NoError(t, EraseLines(&buf, -1))
	assert.Empty(t, buf.String(), "a first paint has nothing to erase")
}

func TestEraseLinesMovesUpBetweenLinesOnly(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, EraseLines(&buf, 3))
	out := buf.String()
	assert.Equal(t, 3, strings.Count(out, el2), "every line is erased")
	assert.Equal(t, 2, strings.Count(out, cuu1),
		"the cursor moves up between lines but never above the topmost one")
}

func TestEraseLinesReturnsToColumnZero(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, EraseLines(&buf, 1))
	assert.Equal(t, el2+"\r", buf.String(),
		"a single line is erased and the cursor parked at column 0 to redraw over it")
}

func TestHyperlinkWrapsAndCloses(t *testing.T) {
	t.Parallel()
	got := Hyperlink("magus query output out-42", "file:///tmp/x.log")
	assert.Equal(t, "\x1b]8;;file:///tmp/x.log\x1b\\magus query output out-42\x1b]8;;\x1b\\", got,
		"the link must be closed, or every line after it stays clickable")
}

func TestHyperlinkWithoutAURIIsPassThrough(t *testing.T) {
	t.Parallel()
	// So a caller that computed a target conditionally needs no branch.
	assert.Equal(t, "plain", Hyperlink("plain", ""))
}

func TestWantsHyperlinksRefusesWhereTheSequenceWouldShow(t *testing.T) {
	var buf ttyBuf
	t.Setenv("TERM", "xterm-256color")
	assert.True(t, WantsHyperlinks(&buf, terminal(80, 24)))

	// A pipe or CI log never gets one: the escape would be literal noise in a
	// file nobody can click.
	assert.False(t, WantsHyperlinks(&buf, notATerminal()))

	// screen mangles OSC pass-through and leaves the URI visible, which is
	// exactly the artifacting this gate exists to avoid. tmux is fine.
	for _, term := range []string{"dumb", "screen", "screen-256color"} {
		t.Setenv("TERM", term)
		assert.False(t, WantsHyperlinks(&buf, terminal(80, 24)), "TERM=%s", term)
	}
	t.Setenv("TERM", "tmux-256color")
	assert.True(t, WantsHyperlinks(&buf, terminal(80, 24)))
}

func TestWantsHyperlinksIgnoresNoColor(t *testing.T) {
	// NO_COLOR is about colour. A link is a way to REACH something, not a way
	// to decorate it, so stripping it would take away function, not tone.
	var buf ttyBuf
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	assert.True(t, WantsHyperlinks(&buf, terminal(80, 24)))
	assert.False(t, WantsColor(&buf, terminal(80, 24)), "colour still off, as asked")
}

func TestWantsHyperlinksRefusesOverSSH(t *testing.T) {
	// The links magus emits are file:// paths on the machine RUNNING it, while
	// the terminal that would open them is somewhere else. The failure mode is
	// not a dead link but a wrong one: a local file at the same path opens
	// instead.
	var buf ttyBuf
	t.Setenv("TERM", "xterm-256color")
	require.True(t, WantsHyperlinks(&buf, terminal(80, 24)))

	t.Setenv("SSH_TTY", "/dev/pts/3")
	assert.False(t, WantsHyperlinks(&buf, terminal(80, 24)))

	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "10.0.0.2 55380 10.0.0.9 22")
	assert.False(t, WantsHyperlinks(&buf, terminal(80, 24)))
}

func TestClipVisibleCountsColumnsNotBytes(t *testing.T) {
	t.Parallel()
	// The bug this exists for: a coloured cell is ONE column and about fifteen
	// bytes, so a byte budget cuts a styled row long before it is too wide -
	// inside an escape - and the terminal then eats the following text as
	// parameters.
	cell := Colorize("*", SGRGreen)
	row := strings.Repeat(cell, 8)
	require.Greater(t, len(row), 8*4, "the premise: eight visible columns, far more than eight bytes")

	got := ClipCols(row, 8)
	assert.Equal(t, row, got, "eight columns fit in eight columns, whatever they cost in bytes")

	got = ClipCols(row, 3)
	assert.Equal(t, strings.Repeat(cell, 3), got, "three columns, styles intact and closed")
}

func TestClipVisibleNeverSplitsAnEscape(t *testing.T) {
	t.Parallel()
	// Every prefix budget must leave a stream whose escapes are all complete.
	row := Colorize("abc", SGRYellow) + Colorize("def", SGRRed)
	for cols := range 8 {
		got := ClipCols(row, cols)
		for i := 0; i < len(got); {
			if n := escapeLen(got[i:]); n > 0 {
				assert.LessOrEqual(t, i+n, len(got), "escape truncated at cols=%d", cols)
				i += n
				continue
			}
			i++
		}
	}
}

func TestClipVisibleKeepsHyperlinksWhole(t *testing.T) {
	t.Parallel()
	// OSC carries a URI, which must never be counted as visible text nor cut in
	// half - a severed OSC leaves the terminal waiting for a terminator.
	link := Hyperlink("out-42", "file:///tmp/a.log")
	got := ClipCols(link+"tail", 6)
	assert.Contains(t, got, "file:///tmp/a.log", "the URI is not visible text and is not budgeted")
	assert.Contains(t, got, osc8End, "the link is closed")
	assert.NotContains(t, got, "tail")
}

func TestClipVisibleOnPlainTextMatchesColumns(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "abc", ClipCols("abcdef", 3))
	assert.Equal(t, "", ClipCols("abc", 0))
	assert.Equal(t, "abc", ClipCols("abc", 10))
}
