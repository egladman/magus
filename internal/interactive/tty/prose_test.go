package tty

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFoldProseKeepsEveryLineInsideTheBudget(t *testing.T) {
	t.Parallel()
	body := "the quick brown fox jumps over the lazy dog and keeps on running well past the margin"
	for _, width := range []int{20, 40, 80} {
		for _, line := range foldProse("", body, width) {
			assert.LessOrEqual(t, Cols(line), width, "width %d, line %q", width, line)
		}
	}
}

func TestFoldProseLeavesAWordWiderThanTheBudgetWhole(t *testing.T) {
	t.Parallel()
	// Cutting one would break a path or a URL the reader has to copy.
	long := "https://example.invalid/a/very/long/path/that/exceeds/the/budget"
	lines := foldProse("", "see "+long+" for more", 20)
	assert.Contains(t, lines, long)
}

func TestFoldProseJoinsWhatTheCallerSplit(t *testing.T) {
	t.Parallel()
	// A budget wide enough for the whole paragraph yields one line, which is
	// what makes a hand-wrapped block greppable again.
	lines := foldProse("", "One sentence. Two sentences. Three.", 100)
	assert.Equal(t, []string{"One sentence. Two sentences. Three."}, lines)
}

func TestFoldProseHangsContinuationLinesUnderTheLabel(t *testing.T) {
	t.Parallel()
	lines := foldProse("  --impact   ", "append the blast radius of landing this change", 40)
	assert.Greater(t, len(lines), 1, "the body must not fit on one line")
	assert.True(t, strings.HasPrefix(lines[0], "  --impact   append"), lines[0])
	for _, line := range lines[1:] {
		assert.True(t, strings.HasPrefix(line, strings.Repeat(" ", 13)), "hangs under the label: %q", line)
		assert.NotEqual(t, ' ', rune(line[13]), "no double indent: %q", line)
	}
}

func TestFoldProseKeepsALabelWithNoBody(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"  --force"}, foldProse("  --force   ", "", 80))
	assert.Nil(t, foldProse("", "", 80))
}

func TestProseFallsBackToEightyOffATerminal(t *testing.T) {
	t.Parallel()
	// A CI log and a redirected file have no width to ask for.
	var plain bytes.Buffer
	assert.Equal(t, proseFallback, proseWidth(&plain, SystemProbe), "a writer with no descriptor")

	var buf ttyBuf
	assert.Equal(t, proseFallback, proseWidth(&buf, notATerminal()), "a descriptor that is a pipe")
	assert.Equal(t, proseFallback, proseWidth(&buf, fakeProbe{isTTY: true, sizeErr: errors.New("no size")}))
	assert.Equal(t, proseFallback, proseWidth(&buf, terminal(0, 24)), "a terminal reporting no width")
}

func TestProseTakesTheTerminalWidthUpToTheReadabilityCap(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	assert.Equal(t, 48, proseWidth(&buf, terminal(48, 24)), "a narrow terminal wraps narrow")
	assert.Equal(t, proseMax, proseWidth(&buf, terminal(200, 24)), "a wide one stops at the cap")
}

func TestProseWritesOneParagraphPerCall(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	Prose(&buf, terminal(30, 24), "One sentence here.", "", "And a second one after it.")
	assert.Equal(t, "One sentence here. And a\nsecond one after it.\n", buf.String())
}

func TestProseItemHangsUnderTheLabel(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	ProseItem(&buf, terminal(30, 24), "  --tar    ", "stream a tar archive to stdout.")
	assert.Equal(t, "  --tar    stream a tar\n           archive to stdout.\n", buf.String())
}
