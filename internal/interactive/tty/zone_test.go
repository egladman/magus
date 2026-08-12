package tty

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZoneLeaseIsDisabledWithoutATerminal(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, notATerminal())
	l := z.Acquire(3)
	assert.False(t, l.Enabled())

	rendered, err := l.Set([]Row{{Text: "hello"}})
	require.NoError(t, err)
	assert.False(t, rendered, "a caller must be told its rows did not land, so it can fall back")
	assert.Empty(t, buf.String(), "nothing may reach a writer that is not a terminal")
}

func TestZoneLeaseIsDisabledWithoutADescriptor(t *testing.T) {
	t.Parallel()
	// A bytes.Buffer has no Fd(), so even an all-terminals probe must not grant.
	var buf bytes.Buffer
	z := NewZone(&buf, terminal(80, 24))
	assert.False(t, z.Acquire(3).Enabled())
}

func TestZoneWritesNothingUntilSomethingDraws(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	l := z.Acquire(3)
	require.True(t, l.Enabled())
	assert.Empty(t, buf.String(), "acquiring rows must not touch the terminal; the paint does")
}

func TestZoneCompositesLeasesTopToBottom(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	top := z.Acquire(2)
	bottom := z.Acquire(1)

	rendered, err := top.Set([]Row{{Text: "status"}, {Text: "failure"}})
	require.NoError(t, err)
	assert.True(t, rendered)

	rendered, err = bottom.Set([]Row{{Text: "toast"}})
	require.NoError(t, err)
	assert.True(t, rendered)

	out := buf.String()
	// Bands sit in acquisition order, top to bottom, so the newest lease is
	// the one closest to the reader's cursor.
	assert.Less(t, strings.Index(out, "status"), strings.Index(out, "failure"))
	assert.Less(t, strings.Index(out, "failure"), strings.Index(out, "toast"))

	// And the second paint touched ONLY the row that changed: one lease
	// updating must not cost a rewrite of its neighbour's rows.
	i := strings.Index(out, "toast")
	assert.NotContains(t, out[i-len("toast"):], "status")
}

func TestZoneBlanksRowsALeaseNoLongerFills(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	l := z.Acquire(3)

	_, err := l.Set([]Row{{Text: "first"}, {Text: "second"}, {Text: "third"}})
	require.NoError(t, err)
	buf.Reset()

	// The band shrank: the vanished entries must leave no residue, which is the
	// property an expiring notification depends on.
	_, err = l.Set([]Row{{Text: "first"}})
	require.NoError(t, err)
	out := buf.String()
	assert.NotContains(t, out, "second")
	assert.NotContains(t, out, "third")
	assert.Equal(t, 2, strings.Count(out, el), "only the two vacated rows are erased")
	assert.NotContains(t, out, "first", "an unchanged row is not rewritten")
}

func TestZoneWritesNothingWhenTheFrameIsUnchanged(t *testing.T) {
	t.Parallel()
	// The property a repaint-on-a-timer depends on: a status line that ticks
	// without changing, or a sweep that expires nothing, must cost zero bytes.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	l := z.Acquire(2)
	_, err := l.Set([]Row{{Text: "pool 3/8 running"}})
	require.NoError(t, err)
	buf.Reset()

	rendered, err := l.Set([]Row{{Text: "pool 3/8 running"}})
	require.NoError(t, err)
	assert.True(t, rendered, "an unchanged frame still counts as rendered")
	assert.Empty(t, buf.String())
}

func TestZoneRedrawsInFullAfterAResize(t *testing.T) {
	t.Parallel()
	// A resize re-reserves, which erases the zone, so nothing the diff believes
	// is on screen still is.
	var buf ttyBuf
	p := &shrinkingProbe{fakeProbe: fakeProbe{isTTY: true, width: 80, height: 40}}
	z := NewZone(&buf, p)
	l := z.Acquire(2)
	_, err := l.Set([]Row{{Text: "keep me"}})
	require.NoError(t, err)
	buf.Reset()

	p.height = 39
	_, err = l.Set([]Row{{Text: "keep me"}})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "keep me",
		"an unchanged row must be redrawn when the zone underneath it was erased")
}

func TestZoneRefusesAGrantThatWouldStarveTheIncumbent(t *testing.T) {
	t.Parallel()
	// minUsefulHeight is 8, so a 16-row terminal has room for 8 leased rows and
	// no more.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 16))
	incumbent := z.Acquire(6)
	require.True(t, incumbent.Enabled())

	newcomer := z.Acquire(3)
	assert.False(t, newcomer.Enabled(), "a grant that does not fit is refused, not squeezed in")

	// The incumbent must be untouched by the refusal: that asymmetry is the
	// arbitration rule.
	rendered, err := incumbent.Set([]Row{{Text: "still working"}})
	require.NoError(t, err)
	assert.True(t, rendered)
	assert.Contains(t, buf.String(), "still working")
}

func TestZoneResizesTheRegionWhenALeaseArrives(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 40))
	first := z.Acquire(2)
	_, err := first.Set([]Row{{Text: "a"}})
	require.NoError(t, err)

	z.mu.Lock()
	assert.Equal(t, 2, z.region.height)
	z.mu.Unlock()

	second := z.Acquire(3)
	_, err = second.Set([]Row{{Text: "b"}})
	require.NoError(t, err)

	z.mu.Lock()
	assert.Equal(t, 5, z.region.height, "the zone grows to the leased total rather than reserving up front")
	z.mu.Unlock()
}

func TestZoneReleasingTheLastLeaseHandsTheTerminalBack(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	l := z.Acquire(3)
	_, err := l.Set([]Row{{Text: "toast"}})
	require.NoError(t, err)
	buf.Reset()

	require.NoError(t, l.Release())
	assert.Contains(t, buf.String(), decstbmReset, "the margins must go back, or the user's shell stays pinned")

	z.mu.Lock()
	assert.Nil(t, z.region)
	z.mu.Unlock()

	// Released leases are inert rather than a panic: teardown ordering is not
	// something every caller can control.
	rendered, err := l.Set([]Row{{Text: "late"}})
	require.NoError(t, err)
	assert.False(t, rendered)
	assert.NoError(t, l.Release())
}

func TestZoneCloseReleasesEveryLease(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	a := z.Acquire(2)
	b := z.Acquire(2)
	_, err := a.Set([]Row{{Text: "a"}})
	require.NoError(t, err)
	buf.Reset()

	require.NoError(t, z.Close())
	assert.Contains(t, buf.String(), decstbmReset)
	assert.False(t, a.Enabled())
	assert.False(t, b.Enabled())
}

func TestZoneReportsNotRenderedWhenTheWindowShrinks(t *testing.T) {
	t.Parallel()
	// The window is measurable at grant time but too small by the time the
	// Region is built, which is what a resize mid-run looks like.
	var buf ttyBuf
	p := &shrinkingProbe{fakeProbe: fakeProbe{isTTY: true, width: 80, height: 40}}
	z := NewZone(&buf, p)
	l := z.Acquire(6)
	require.True(t, l.Enabled())

	p.height = 10 // 6 leased rows leaves 4 to scroll; minUsefulHeight is 8.
	rendered, err := l.Set([]Row{{Text: "failure"}})
	require.NoError(t, err)
	assert.False(t, rendered, "a caller whose rows cannot be pinned must be told, so a failure still reaches the user")
}

// shrinkingProbe reports a size that the test mutates between calls.
type shrinkingProbe struct{ fakeProbe }

func (p *shrinkingProbe) Size(uintptr) (int, int, error) {
	return p.width, p.height, p.sizeErr
}
