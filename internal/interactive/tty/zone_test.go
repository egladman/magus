package tty

import (
	"bytes"
	"fmt"
	"github.com/egladman/magus/internal/interactive/screen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestZoneLeaseIsDisabledWithoutATerminal(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, notATerminal())
	l := z.Acquire(3)
	assert.False(t, l.Enabled())

	rendered, err := l.Set([]Line{{Text: "hello"}})
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

	rendered, err := top.Set([]Line{{Text: "status"}, {Text: "failure"}})
	require.NoError(t, err)
	assert.True(t, rendered)

	rendered, err = bottom.Set([]Line{{Text: "toast"}})
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

	_, err := l.Set([]Line{{Text: "first"}, {Text: "second"}, {Text: "third"}})
	require.NoError(t, err)
	buf.Reset()

	// The band shrank: the vanished entries must leave no residue, which is the
	// property an expiring notification depends on.
	_, err = l.Set([]Line{{Text: "first"}})
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
	_, err := l.Set([]Line{{Text: "pool 3/8 running"}})
	require.NoError(t, err)
	buf.Reset()

	rendered, err := l.Set([]Line{{Text: "pool 3/8 running"}})
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
	_, err := l.Set([]Line{{Text: "keep me"}})
	require.NoError(t, err)
	buf.Reset()

	p.height = 39
	_, err = l.Set([]Line{{Text: "keep me"}})
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
	rendered, err := incumbent.Set([]Line{{Text: "still working"}})
	require.NoError(t, err)
	assert.True(t, rendered)
	assert.Contains(t, buf.String(), "still working")
}

func TestZoneResizesTheRegionWhenALeaseArrives(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 40))
	first := z.Acquire(2)
	_, err := first.Set([]Line{{Text: "a"}})
	require.NoError(t, err)

	z.mu.Lock()
	assert.Equal(t, 2+borderRows, z.region.height)
	z.mu.Unlock()

	second := z.Acquire(3)
	_, err = second.Set([]Line{{Text: "b"}})
	require.NoError(t, err)

	z.mu.Lock()
	assert.Equal(t, 5+borderRows, z.region.height, "the zone grows to the leased total rather than reserving up front")
	z.mu.Unlock()
}

func TestZoneReleasingTheLastLeaseHandsTheTerminalBack(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	l := z.Acquire(3)
	_, err := l.Set([]Line{{Text: "toast"}})
	require.NoError(t, err)
	buf.Reset()

	require.NoError(t, l.Release())
	assert.Contains(t, buf.String(), decstbmReset, "the margins must go back, or the user's shell stays pinned")

	z.mu.Lock()
	assert.Nil(t, z.region)
	z.mu.Unlock()

	// Released leases are inert rather than a panic: teardown ordering is not
	// something every caller can control.
	rendered, err := l.Set([]Line{{Text: "late"}})
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
	_, err := a.Set([]Line{{Text: "a"}})
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
	// region is built, which is what a resize mid-run looks like.
	var buf ttyBuf
	p := &shrinkingProbe{fakeProbe: fakeProbe{isTTY: true, width: 80, height: 40}}
	z := NewZone(&buf, p)
	l := z.Acquire(6)
	require.True(t, l.Enabled())

	p.height = 10 // 6 leased rows leaves 4 to scroll; minUsefulHeight is 8.
	rendered, err := l.Set([]Line{{Text: "failure"}})
	require.NoError(t, err)
	assert.False(t, rendered, "a caller whose rows cannot be pinned must be told, so a failure still reaches the user")
}

// shrinkingProbe reports a size that the test mutates between calls.
type shrinkingProbe struct{ fakeProbe }

func (p *shrinkingProbe) Size(uintptr) (int, int, error) {
	return p.width, p.height, p.sizeErr
}

// TestZoneCloseRacesTheRun reproduces the shape the exit path actually has:
// Zone.Close runs from the signal handler while the run's own goroutines are
// still painting and asking their leases whether they are alive.
//
// Every Lease field is written under the zone's lock, so every read has to take
// it too. Reading `released` or `rows` unlocked passed every sequential test in
// this package and is a live race the moment somebody presses Ctrl-C.
func TestZoneCloseRacesTheRun(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(120, 60))
	leases := []*Lease{z.Acquire(4), z.Acquire(3), z.Acquire(2)}

	var wg sync.WaitGroup
	for i, l := range leases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range 200 {
				if !l.Enabled() {
					continue
				}
				_, _ = l.Set([]Line{{Text: fmt.Sprintf("lease %d frame %d", i, n)}})
				_ = l.Rows()
				_ = l.Grow(l.Rows() + 1)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		// The exit path, arriving mid-run.
		_ = z.Close()
	}()
	wg.Wait()

	// Whatever the interleaving, teardown wins: nothing is left leased.
	for _, l := range leases {
		assert.False(t, l.Enabled())
		assert.Zero(t, l.Rows(), "a released lease holds no rows")
	}
}

// No same-named source file, deliberately: these are cross-cutting, and pairing
// them with any one file would misname what they cover. They exercise region,
// Zone and Notifier TOGETHER through the terminal emulator.
//
// These are the tests that assert what a USER SEES, by driving the real code
// against the terminal emulator in screen_test.go. Everything else in this
// package checks that the right bytes were emitted; these check that the right
// picture came out.

// TestPaintZoneSurvivesScrollingOutput is the property the whole design rests
// on: ordinary output keeps scrolling, in real scrollback, while the leased
// rows below it hold still.
// toastRow is bandRow with the notification's accent bar taken off, so an
// assertion about the MESSAGE does not restate its decoration.
func toastRow(s *screen.Screen, row int) string {
	return strings.TrimPrefix(bandRow(s, row), accentBar+" ")
}

// bandRow is the text of an absolute terminal row with the zone's box taken
// off, so an assertion about what a lease drew does not have to know the region
// frames it.
func bandRow(s *screen.Screen, row int) string {
	return strings.TrimRight(strings.Trim(s.Row(row), boxV), " ")
}

func TestPaintZoneSurvivesScrollingOutput(t *testing.T) {
	t.Parallel()
	s := screen.New(80, 24)
	z := NewZone(s, terminal(80, 24))
	failures := z.Acquire(6)
	toasts := z.Acquire(3)
	// 9 leased rows plus the zone's border on a 24-row terminal: the zone is
	// rows 15-24, and rows 1-14 keep scrolling.
	require.True(t, failures.Enabled())
	require.True(t, toasts.Enabled())

	_, err := failures.Set([]Line{{Text: "pool 4/8 running", Style: SGRDim}})
	require.NoError(t, err)
	_, err = toasts.Set([]Line{{}, {}, {Text: "api built in 1.2s", Style: SGRGreen}})
	require.NoError(t, err)

	before := s.Scrolled()
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(s, "compiling package %d\n", i)
	}

	// The transcript scrolled...
	assert.Greater(t, s.Scrolled(), before, "output must scroll normally")
	// Line 40's own trailing newline scrolls it up one, so the last line of
	// output sits one row above the cursor, which waits on the last scrolling row.
	assert.Contains(t, s.Row(14-borderRows), "compiling package 40")
	assert.Equal(t, 15-borderRows, cursorRowOf(s), "the cursor stays inside the scrolling area, never in the zone")
	// ...and the zone did not move with it.
	assert.Equal(t, "pool 4/8 running", bandRow(s, 16-borderRowsPerEdge), "the status row held its place under 40 lines of output")
	assert.Equal(t, "api built in 1.2s", toastRow(s, 24-borderRowsPerEdge), "the toast held its place too")
	assert.Equal(t, string(SGRGreen), s.StyleAt(24-borderRowsPerEdge, 1+borderCols), "and kept its colour")
}

// TestPaintLeavesTheCursorWhereTheCallerLeftIt is the second half of that
// contract: a repaint must be invisible to whatever is writing above it.
func TestPaintLeavesTheCursorWhereTheCallerLeftIt(t *testing.T) {
	t.Parallel()
	s := screen.New(80, 24)
	z := NewZone(s, terminal(80, 24))
	l := z.Acquire(4)

	fmt.Fprint(s, "a partial line, mid-write")
	row, col := cursorRowOf(s), cursorColOf(s)

	_, err := l.Set([]Line{{Text: "repaint one"}, {Text: "repaint two"}})
	require.NoError(t, err)
	assert.Equal(t, row, cursorRowOf(s), "a repaint must not move the caller's cursor")
	assert.Equal(t, col, cursorColOf(s))

	// And the caller's own line is intact and can be continued.
	fmt.Fprint(s, " ...continued")
	assert.Contains(t, s.Row(row), "a partial line, mid-write ...continued")
}

// TestPaintNotificationsExpireWithoutResidue is the toast property: an entry
// vanishes with nothing to replace it, leaving a clean row rather than a stale
// one.
func TestPaintNotificationsExpireWithoutResidue(t *testing.T) {
	t.Parallel()
	s := screen.New(80, 24)
	z := NewZone(s, terminal(80, 24))
	n, clock := newTestNotifier(z, 3)

	require.NoError(t, n.Notify("lint failed in std/", SGRRed, 5*time.Second))
	require.NoError(t, n.Notify("api built", SGRGreen, 30*time.Second))
	assert.Equal(t, "lint failed in std/", toastRow(s, 23-borderRowsPerEdge))
	assert.Equal(t, "api built", toastRow(s, 24-borderRowsPerEdge))
	// Column 1 is the box's left edge now, so the text starts at column 2.
	assert.Equal(t, string(SGRRed), s.StyleAt(23-borderRowsPerEdge, 1+borderCols))

	clock.advance(6 * time.Second)
	n.sweep()
	// The expired toast is gone and the survivor slid down to sit closest to
	// the reader. No fragment of the old text remains on either row.
	assert.Equal(t, "", bandRow(s, 23-borderRowsPerEdge), "an expired notification leaves a clean row")
	assert.Equal(t, "api built", toastRow(s, 24-borderRowsPerEdge))
}

// TestPaintReleaseHandsBackAWholeScreen pins teardown: the rows come back, the
// margins are cleared, and the transcript above is untouched.
func TestPaintReleaseHandsBackAWholeScreen(t *testing.T) {
	t.Parallel()
	s := screen.New(80, 24)
	z := NewZone(s, terminal(80, 24))
	l := z.Acquire(6)
	fmt.Fprint(s, "transcript line\n")
	_, err := l.Set([]Line{{Text: "pool 1/4 running"}})
	require.NoError(t, err)
	require.Equal(t, "pool 1/4 running", bandRow(s, 19-borderRowsPerEdge))

	require.NoError(t, z.Close())
	assert.Equal(t, 1, scrollTopOf(s), "margins reset, or the user's shell stays pinned")
	assert.Equal(t, 24, scrollBotOf(s))
	assert.Equal(t, "", bandRow(s, 19-borderRowsPerEdge), "the leased rows are cleared, not left as litter")
	assert.Contains(t, s.String(), "transcript line", "and the transcript survives teardown")
}

// TestPaintTwoConsumersShareOneTerminal is the bug the ownership refactor
// fixed, expressed as a picture: before it, a second consumer set its own
// scroll margins from its own row arithmetic and the two bands overwrote each
// other.
func TestPaintTwoConsumersShareOneTerminal(t *testing.T) {
	t.Parallel()
	s := screen.New(80, 24)
	z := NewZone(s, terminal(80, 24))
	failures := z.Acquire(6)
	n, _ := newTestNotifier(z, 3)

	_, err := failures.Set([]Line{
		{Text: "pool 6/8 running   2 ok  1 failed", Style: SGRDim},
		{Text: "[fail] test std (ran, 4.1s)", Style: SGRBoldRed},
	})
	require.NoError(t, err)
	require.NoError(t, n.Notify("cache stampede on go-build", SGRYellow, time.Minute))

	// One margin set, covering both bands. Seven leased rows, not nine: the
	// failure band's six plus the ONE row the single notification actually
	// needs - and the zone's border above them.
	assert.Equal(t, 1, scrollTopOf(s))
	assert.Equal(t, 17-borderRows, scrollBotOf(s))
	// The border marks where the scrolling stops.
	assert.Equal(t, boxTL+strings.Repeat(boxH, 77)+boxTR, s.Row(18-borderRows), "the top rule spans the region, corners included")
	// Both consumers are visible at once, in acquisition order.
	assert.Equal(t, "pool 6/8 running   2 ok  1 failed", bandRow(s, 18-borderRowsPerEdge))
	assert.Equal(t, "[fail] test std (ran, 4.1s)", bandRow(s, 19-borderRowsPerEdge))
	// A single notification rides the bottom rule rather than taking a row.
	assert.Equal(t, "cache stampede on go-build", toastRow(s, 24-borderRowsPerEdge))
}
