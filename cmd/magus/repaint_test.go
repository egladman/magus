package main

import (
	"bytes"
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

func TestInlineRepaintErasesOnlyItsOwnLines(t *testing.T) {
	t.Parallel()
	var buf fakeTerm
	p := &inlineRepaint{w: &buf, probe: sizedProbe{w: 80, h: 24}}

	require.True(t, p.paint("alpha\nbravo\ncharlie\n"))
	assert.Equal(t, 3, p.lines)
	first := buf.String()
	assert.NotContains(t, first, "\x1b[2J", "a watch view must never erase the whole screen")
	assert.False(t, strings.HasSuffix(first, "\n"),
		"the cursor must end ON the last line, since that is where the next erase walks up from")

	buf.Reset()
	require.True(t, p.paint("delta\necho\nfoxtrot\n"))
	out := buf.String()
	// Three lines erased, three redrawn: exactly the block, nothing above it.
	assert.Equal(t, 3, strings.Count(out, "\x1b[2K"))
	assert.Equal(t, 2, strings.Count(out, "\x1b[1A"), "one cursor-up between each pair of lines")
	assert.Contains(t, out, "delta")
	assert.NotContains(t, out, "\x1b[2J")
}

func TestInlineRepaintTracksAShrinkingFrame(t *testing.T) {
	t.Parallel()
	// The count has to follow the frame, or a view that got shorter leaves its
	// old tail on screen and every erase after it is off by one.
	var buf fakeTerm
	p := &inlineRepaint{w: &buf, probe: sizedProbe{w: 80, h: 24}}
	require.True(t, p.paint("one\ntwo\nthree\nfour\n"))
	require.Equal(t, 4, p.lines)

	buf.Reset()
	require.True(t, p.paint("one\ntwo\n"))
	assert.Equal(t, 4, strings.Count(buf.String(), "\x1b[2K"), "all four previous lines are erased")
	assert.Equal(t, 2, p.lines)
}

func TestInlineRepaintClipsToWidth(t *testing.T) {
	t.Parallel()
	// A line that wrapped would occupy two rows while the accounting counted
	// one, and the view would slowly eat the transcript above it.
	var buf fakeTerm
	p := &inlineRepaint{w: &buf, probe: sizedProbe{w: 20, h: 24}}
	require.True(t, p.paint(strings.Repeat("x", 100)+"\n"))

	body := strings.ReplaceAll(buf.String(), "\r", "")
	for _, line := range strings.Split(body, "\n") {
		assert.LessOrEqual(t, len(stripCSI(line)), 20, "no line may exceed the terminal width")
	}
	assert.Equal(t, 1, p.lines)
}

func TestInlineRepaintRefusesAFrameTallerThanTheTerminal(t *testing.T) {
	t.Parallel()
	// Erasing in place walks the cursor UPWARD, so a block as tall as the
	// terminal has nowhere to walk back to. The caller falls back rather than
	// corrupting the screen.
	var buf fakeTerm
	p := &inlineRepaint{w: &buf, probe: sizedProbe{w: 80, h: 10}}
	assert.False(t, p.paint(strings.Repeat("line\n", 10)))
	assert.False(t, p.paint(strings.Repeat("line\n", 12)))
	assert.True(t, p.paint(strings.Repeat("line\n", 9)))
}

func TestInlineRepaintWritesPlainlyOffATerminal(t *testing.T) {
	t.Parallel()
	// Piped or redirected: print the frame and never try to erase, which would
	// put escape sequences in a file nobody can interpret them in.
	var buf fakeTerm
	p := &inlineRepaint{w: &buf, probe: notATerm{}}
	require.True(t, p.paint("alpha\nbravo\n"))
	require.True(t, p.paint("charlie\n"))
	assert.NotContains(t, buf.String(), "\x1b")
	assert.Contains(t, buf.String(), "alpha")
	assert.Contains(t, buf.String(), "charlie")
}

func TestInlineRepaintFinishLeavesTheFrameOnScreen(t *testing.T) {
	t.Parallel()
	// The frame is the answer the reader asked for. Erasing it on the way out
	// would be exactly the takeover this avoids; only the cursor moves off it,
	// so a shell prompt does not land on the last line.
	var buf fakeTerm
	p := &inlineRepaint{w: &buf, probe: sizedProbe{w: 80, h: 24}}
	require.True(t, p.paint("pool 2/8 running\n"))
	buf.Reset()

	p.finish()
	assert.Equal(t, "\n", buf.String())
	assert.Equal(t, 0, p.lines)
	p.finish()
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
