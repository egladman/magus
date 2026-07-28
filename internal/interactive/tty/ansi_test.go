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
