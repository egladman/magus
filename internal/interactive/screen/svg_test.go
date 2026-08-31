package screen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSVGRendersTheGrid(t *testing.T) {
	t.Parallel()
	s := New(20, 3)
	fmt.Fprint(s, "hello\n\x1b[32mgreen\x1b[0m\n")

	out := s.SVG(SVGOptions{})
	assert.True(t, strings.HasPrefix(out, "<svg "))
	assert.True(t, strings.HasSuffix(out, "</svg>"))
	assert.Contains(t, out, ">hello<")
	assert.Contains(t, out, DarkTheme.Green, "a coloured run carries its colour")
	assert.Contains(t, out, DarkTheme.Background)
}

func TestSVGIsDeterministic(t *testing.T) {
	t.Parallel()
	// The property that lets a rendered terminal be committed and drift-gated:
	// the same grid renders byte-for-byte the same picture, every time.
	render := func() string {
		s := New(30, 4)
		fmt.Fprint(s, "\x1b[2mpool 6/8 running\x1b[0m\n\x1b[1;31m[fail] test std\x1b[0m\n")
		return s.SVG(SVGOptions{})
	}
	first := render()
	for range 5 {
		assert.Equal(t, first, render())
	}
}

func TestSVGEscapesMarkup(t *testing.T) {
	t.Parallel()
	// Terminal output carries angle brackets and ampersands constantly - a diff,
	// a shell command, an HTML fragment in a log - and an unescaped one would
	// produce an SVG that does not parse.
	s := New(40, 2)
	fmt.Fprint(s, "if a<b && c>d\n")
	out := s.SVG(SVGOptions{})
	assert.Contains(t, out, "a&lt;b &amp;&amp; c&gt;d")
	assert.NotContains(t, out, "a<b")
}

func TestSVGDrawsReverseVideoAsASwap(t *testing.T) {
	t.Parallel()
	// Reverse marks the selected row of an interactive band. In a picture it has
	// to read as SELECTED, which means a filled rectangle, not another colour.
	s := New(20, 2)
	fmt.Fprint(s, "\x1b[7mselected\x1b[0m\n")
	out := s.SVG(SVGOptions{})
	assert.Contains(t, out, "<rect x=", "reverse video draws a filled background")
	assert.Contains(t, out, `fill="`+DarkTheme.Background+`"`)
}

func TestSVGCoalescesRunsRatherThanCells(t *testing.T) {
	t.Parallel()
	// One <text> per styled stretch, not per character: a hundred-column row is
	// one element instead of a hundred, which is the difference between a docs
	// asset and a liability.
	s := New(60, 2)
	fmt.Fprint(s, strings.Repeat("x", 40)+"\n")
	out := s.SVG(SVGOptions{})
	assert.Equal(t, 1, strings.Count(out, "<text "))
}

func TestAnimateShowsEachFrameInTurn(t *testing.T) {
	t.Parallel()
	frames := make([]*Screen, 3)
	for i := range frames {
		frames[i] = New(20, 2)
		fmt.Fprintf(frames[i], "frame %d\n", i)
	}
	out, err := Animate(frames, []float64{1, 1, 2}, SVGOptions{})
	require.NoError(t, err)

	for i := range frames {
		assert.Contains(t, out, fmt.Sprintf("frame %d", i))
	}
	assert.Equal(t, 3, strings.Count(out, "<animate "))
	assert.Contains(t, out, `dur="4s"`, "the cycle is the sum of the holds")
	assert.Contains(t, out, "repeatCount=\"indefinite\"",
		"a recording that stops on the last frame reads as a page that failed to load")
	assert.NotContains(t, out, "<script", "no player: a strict docs policy would not run one")
}

func TestAnimateRefusesMismatchedInput(t *testing.T) {
	t.Parallel()
	a, b := New(10, 2), New(12, 2)
	_, err := Animate(nil, nil, SVGOptions{})
	assert.Error(t, err)
	_, err = Animate([]*Screen{a}, []float64{1, 1}, SVGOptions{})
	assert.Error(t, err, "a hold per frame, or the timeline is a guess")
	_, err = Animate([]*Screen{a, b}, []float64{1, 1}, SVGOptions{})
	assert.Error(t, err, "frames of different sizes cannot share one viewport")
	_, err = Animate([]*Screen{a}, []float64{0}, SVGOptions{})
	assert.Error(t, err, "a zero hold is a frame nobody can see")
}

// TestAnimateSwitchesFramesRatherThanFading pins the attribute that makes the
// animation legible at all.
//
// SMIL defaults to linear interpolation, so a frame that reaches opacity 0 at
// the end of the timeline FADES there from the end of its own window - and with
// every frame doing that, all of them are superimposed at partial opacity for
// most of the loop. The first version of this shipped exactly that.
func TestAnimateSwitchesFramesRatherThanFading(t *testing.T) {
	t.Parallel()
	frames := []*Screen{New(10, 2), New(10, 2), New(10, 2)}
	out, err := Animate(frames, []float64{1, 1, 1}, SVGOptions{})
	require.NoError(t, err)

	assert.Equal(t, 3, strings.Count(out, `calcMode="discrete"`),
		"every frame switches; linear would smear them over each other")
	assert.NotContains(t, out, `values="0;1;1;0"`,
		"the trailing value must be 0, or a frame stays visible to the end of the loop")
	assert.Equal(t, 3, strings.Count(out, `values="0;1;0;0"`))
}

func TestAnimateFrameWindowsDoNotOverlap(t *testing.T) {
	t.Parallel()
	// Each frame is on for its own window and off everywhere else, so exactly
	// one is visible at any moment.
	frames := []*Screen{New(10, 2), New(10, 2), New(10, 2), New(10, 2)}
	out, err := Animate(frames, []float64{1, 2, 1, 4}, SVGOptions{})
	require.NoError(t, err)

	var starts, ends []string
	for _, seg := range strings.Split(out, `keyTimes="`)[1:] {
		parts := strings.Split(strings.Split(seg, `"`)[0], ";")
		starts = append(starts, parts[1])
		ends = append(ends, parts[2])
	}
	require.Len(t, starts, 4)
	// Total is 8, so the boundaries are 0, 1/8, 3/8, 4/8, 1.
	assert.Equal(t, []string{"0", "0.125", "0.375", "0.5"}, starts)
	assert.Equal(t, []string{"0.125", "0.375", "0.5", "1"}, ends)
	for i := range starts[:len(starts)-1] {
		assert.Equal(t, ends[i], starts[i+1], "one frame ends exactly where the next begins")
	}
}

// TestVariantPath pins the naming contract the README and docs links depend on:
// the dark variant keeps the unsuffixed name every existing reference points at.
func TestVariantPath(t *testing.T) {
	assert.Equal(t, "assets/gen/core-loop.svg", VariantPath("assets/gen/core-loop.svg", ""))
	assert.Equal(t, "assets/gen/core-loop-light.svg", VariantPath("assets/gen/core-loop.svg", "-light"))
}
