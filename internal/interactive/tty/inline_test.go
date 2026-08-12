package tty

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTerm is a writer with a synthetic descriptor, so the repainter treats it
// as a terminal without a pty.
type fakeTerm struct{ bytes.Buffer }

func (*fakeTerm) Fd() uintptr { return 1 }

// sizedProbe reports a fixed terminal size.
type sizedProbe struct{ w, h int }

func (sizedProbe) IsTerminal(uintptr) bool          { return true }
func (p sizedProbe) Size(uintptr) (int, int, error) { return p.w, p.h, nil }

// notATerm reports every descriptor as a pipe.
type notATerm struct{}

func (notATerm) IsTerminal(uintptr) bool        { return false }
func (notATerm) Size(uintptr) (int, int, error) { return 0, 0, nil }

func TestInlineViewErasesOnlyItsOwnLines(t *testing.T) {
	t.Parallel()
	var buf fakeTerm
	p := &InlineView{w: &buf, probe: sizedProbe{w: 80, h: 24}}

	require.True(t, p.Paint("alpha\nbravo\ncharlie\n"))
	assert.Equal(t, 3, len(p.painted))
	first := buf.String()
	assert.NotContains(t, first, "\x1b[2J", "a watch view must never erase the whole screen")
	assert.False(t, strings.HasSuffix(first, "\n"),
		"the cursor must end ON the last line, since that is where the next erase walks up from")

	buf.Reset()
	require.True(t, p.Paint("delta\necho\nfoxtrot\n"))
	out := buf.String()
	// Every line changed, so every line is erased and rewritten - and nothing
	// outside the block is touched.
	assert.Equal(t, 3, strings.Count(out, "\x1b[2K"))
	assert.Contains(t, out, "delta")
	assert.NotContains(t, out, "\x1b[2J")
}

func TestInlineViewTracksAShrinkingFrame(t *testing.T) {
	t.Parallel()
	// The count has to follow the frame, or a view that got shorter leaves its
	// old tail on screen and every erase after it is off by one.
	var buf fakeTerm
	p := &InlineView{w: &buf, probe: sizedProbe{w: 80, h: 24}}
	require.True(t, p.Paint("one\ntwo\nthree\nfour\n"))
	require.Equal(t, 4, len(p.painted))

	buf.Reset()
	require.True(t, p.Paint("one\ntwo\n"))
	assert.Equal(t, 4, strings.Count(buf.String(), "\x1b[2K"), "all four previous lines are erased")
	assert.Equal(t, 2, len(p.painted))
}

func TestInlineViewClipsToWidth(t *testing.T) {
	t.Parallel()
	// A line that wrapped would occupy two rows while the accounting counted
	// one, and the view would slowly eat the transcript above it.
	var buf fakeTerm
	p := &InlineView{w: &buf, probe: sizedProbe{w: 20, h: 24}}
	require.True(t, p.Paint(strings.Repeat("x", 100)+"\n"))

	body := strings.ReplaceAll(buf.String(), "\r", "")
	for _, line := range strings.Split(body, "\n") {
		assert.LessOrEqual(t, len(stripCSI(line)), 20, "no line may exceed the terminal width")
	}
	assert.Equal(t, 1, len(p.painted))
}

func TestInlineViewRefusesAFrameTallerThanTheTerminal(t *testing.T) {
	t.Parallel()
	// Erasing in place walks the cursor UPWARD, so a block as tall as the
	// terminal has nowhere to walk back to. The caller falls back rather than
	// corrupting the screen.
	var buf fakeTerm
	p := &InlineView{w: &buf, probe: sizedProbe{w: 80, h: 10}}
	assert.False(t, p.Paint(strings.Repeat("line\n", 10)))
	assert.False(t, p.Paint(strings.Repeat("line\n", 12)))
	assert.True(t, p.Paint(strings.Repeat("line\n", 9)))
}

func TestInlineViewWritesPlainlyOffATerminal(t *testing.T) {
	t.Parallel()
	// Piped or redirected: print the frame and never try to erase, which would
	// put escape sequences in a file nobody can interpret them in.
	var buf fakeTerm
	p := &InlineView{w: &buf, probe: notATerm{}}
	require.True(t, p.Paint("alpha\nbravo\n"))
	require.True(t, p.Paint("charlie\n"))
	assert.NotContains(t, buf.String(), "\x1b")
	assert.Contains(t, buf.String(), "alpha")
	assert.Contains(t, buf.String(), "charlie")
}

func TestInlineViewFinishLeavesTheFrameOnScreen(t *testing.T) {
	t.Parallel()
	// The frame is the answer the reader asked for. Erasing it on the way out
	// would be exactly the takeover this avoids; only the cursor moves off it,
	// so a shell prompt does not land on the last line.
	var buf fakeTerm
	p := &InlineView{w: &buf, probe: sizedProbe{w: 80, h: 24}}
	require.True(t, p.Paint("pool 2/8 running\n"))
	buf.Reset()

	p.Finish()
	assert.Equal(t, "\n", buf.String())
	assert.Equal(t, 0, len(p.painted))
	p.Finish()
	assert.Equal(t, "\n", buf.String(), "finish is idempotent")
}

// stripCSI removes CSI sequences so a line's printable width can be measured.
func stripCSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// TestInlineViewWritesNothingForAnUnchangedFrame is the property that makes a
// view repainted on a timer affordable: a status grid ticks several times a
// second for as long as it is open, and most ticks change nothing.
func TestInlineViewWritesNothingForAnUnchangedFrame(t *testing.T) {
	t.Parallel()
	var buf fakeTerm
	p := &InlineView{w: &buf, probe: sizedProbe{w: 80, h: 24}}
	frame := "daemon running\npool 2/8\nuptime 3h\n"
	require.True(t, p.Paint(frame))
	buf.Reset()

	require.True(t, p.Paint(frame))
	assert.Empty(t, buf.String(), "an unchanged frame must cost nothing at all")
}

// TestInlineViewRewritesOnlyWhatChanged is the diff, and the reason the cursor
// jumps to each changed line rather than walking the block.
func TestInlineViewRewritesOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	var buf fakeTerm
	p := &InlineView{w: &buf, probe: sizedProbe{w: 80, h: 24}}
	require.True(t, p.Paint("spinner |\nstatic one\nstatic two\nstatic three\n"))
	buf.Reset()

	require.True(t, p.Paint("spinner /\nstatic one\nstatic two\nstatic three\n"))
	out := buf.String()
	assert.Equal(t, 1, strings.Count(out, "\x1b[2K"), "one line changed, one line erased")
	assert.Contains(t, out, "spinner /")
	assert.NotContains(t, out, "static one", "unchanged lines are not rewritten")
}

// TestInlineViewKeepsTheGridCorrectAcrossFrames drives the real code against
// the terminal emulator, because the diff is relative cursor arithmetic and
// arithmetic that is off by one produces a plausible-looking byte stream and a
// wrong screen. Only rendering it catches that.
func TestInlineViewKeepsTheGridCorrectAcrossFrames(t *testing.T) {
	t.Parallel()
	s := newScreen(40, 24)
	fmt.Fprint(s, "a previous command\nand its output\n")
	p := &InlineView{w: s, probe: sizedProbe{w: 40, h: 24}}

	require.True(t, p.Paint("spinner |\nalpha\nbravo\n"))
	require.True(t, p.Paint("spinner /\nalpha\nbravo\n"))
	require.True(t, p.Paint("spinner -\nalpha\nCHARLIE\n"))

	assert.Equal(t, "spinner -", s.row1(3))
	assert.Equal(t, "alpha", s.row1(4))
	assert.Equal(t, "CHARLIE", s.row1(5))
	assert.Equal(t, 5, s.row, "the cursor ends on the last line of the block, every time")

	// The transcript above is untouched, which is the whole point of redrawing
	// in place rather than clearing.
	assert.Equal(t, "a previous command", s.row1(1))
	assert.Equal(t, "and its output", s.row1(2))
}

// TestInlineViewSurvivesAHeightChangeThenDiffs pins the handover between the
// two paths: a block that changed height is rewritten whole, and the next
// frame must still diff correctly against it.
func TestInlineViewSurvivesAHeightChangeThenDiffs(t *testing.T) {
	t.Parallel()
	s := newScreen(40, 24)
	p := &InlineView{w: s, probe: sizedProbe{w: 40, h: 24}}

	require.True(t, p.Paint("one\ntwo\nthree\nfour\n"))
	require.True(t, p.Paint("one\ntwo\n"))
	require.True(t, p.Paint("one\nCHANGED\n"))

	assert.Equal(t, "one", s.row1(1))
	assert.Equal(t, "CHANGED", s.row1(2))
	assert.Equal(t, "", s.row1(3), "the rows the shorter block gave up are clean")
	assert.Equal(t, "", s.row1(4))
	assert.Equal(t, 2, s.row)
}
