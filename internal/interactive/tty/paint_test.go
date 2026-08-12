package tty

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are the tests that assert what a USER SEES, by driving the real code
// against the terminal emulator in screen_test.go. Everything else in this
// package checks that the right bytes were emitted; these check that the right
// picture came out.

// TestPaintZoneSurvivesScrollingOutput is the property the whole design rests
// on: ordinary output keeps scrolling, in real scrollback, while the leased
// rows below it hold still.
func TestPaintZoneSurvivesScrollingOutput(t *testing.T) {
	t.Parallel()
	s := newScreen(80, 24)
	z := NewZone(s, terminal(80, 24))
	failures := z.Acquire(6)
	toasts := z.Acquire(3)
	// 9 leased rows on a 24-row terminal: the zone is rows 16-24, and rows 1-15
	// keep scrolling.
	require.True(t, failures.Enabled())
	require.True(t, toasts.Enabled())

	_, err := failures.Set([]Row{{Text: "pool 4/8 running", Style: SGRDim}})
	require.NoError(t, err)
	_, err = toasts.Set([]Row{{}, {}, {Text: "api built in 1.2s", Style: SGRGreen}})
	require.NoError(t, err)

	before := s.scrolled
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(s, "compiling package %d\n", i)
	}

	// The transcript scrolled...
	assert.Greater(t, s.scrolled, before, "output must scroll normally")
	// Line 40's own trailing newline scrolls it up one, so the last line of
	// output sits on row 14 with the cursor waiting on row 15.
	assert.Contains(t, s.row1(14), "compiling package 40")
	assert.Equal(t, 15, s.row, "the cursor stays inside the scrolling area, never in the zone")
	// ...and the zone did not move with it.
	assert.Equal(t, "pool 4/8 running", s.row1(16), "the status row held its place under 40 lines of output")
	assert.Equal(t, "api built in 1.2s", s.row1(24), "the toast held its place too")
	assert.Equal(t, SGRGreen, s.styleAt(24, 1), "and kept its colour")
}

// TestPaintLeavesTheCursorWhereTheCallerLeftIt is the second half of that
// contract: a repaint must be invisible to whatever is writing above it.
func TestPaintLeavesTheCursorWhereTheCallerLeftIt(t *testing.T) {
	t.Parallel()
	s := newScreen(80, 24)
	z := NewZone(s, terminal(80, 24))
	l := z.Acquire(4)

	fmt.Fprint(s, "a partial line, mid-write")
	row, col := s.row, s.col

	_, err := l.Set([]Row{{Text: "repaint one"}, {Text: "repaint two"}})
	require.NoError(t, err)
	assert.Equal(t, row, s.row, "a repaint must not move the caller's cursor")
	assert.Equal(t, col, s.col)

	// And the caller's own line is intact and can be continued.
	fmt.Fprint(s, " ...continued")
	assert.Contains(t, s.row1(row), "a partial line, mid-write ...continued")
}

// TestPaintNotificationsExpireWithoutResidue is the toast property: an entry
// vanishes with nothing to replace it, leaving a clean row rather than a stale
// one.
func TestPaintNotificationsExpireWithoutResidue(t *testing.T) {
	t.Parallel()
	s := newScreen(80, 24)
	z := NewZone(s, terminal(80, 24))
	n, clock := newTestNotifier(z, 3)

	require.NoError(t, n.Notify("lint failed in std/", SGRRed, 5*time.Second))
	require.NoError(t, n.Notify("api built", SGRGreen, 30*time.Second))
	assert.Equal(t, "lint failed in std/", s.row1(23))
	assert.Equal(t, "api built", s.row1(24))
	assert.Equal(t, SGRRed, s.styleAt(23, 1))

	clock.advance(6 * time.Second)
	n.sweep()
	// The expired toast is gone and the survivor slid down to sit closest to
	// the reader. No fragment of the old text remains on either row.
	assert.Equal(t, "", s.row1(23), "an expired notification leaves a clean row")
	assert.Equal(t, "api built", s.row1(24))
}

// TestPaintReleaseHandsBackAWholeScreen pins teardown: the rows come back, the
// margins are cleared, and the transcript above is untouched.
func TestPaintReleaseHandsBackAWholeScreen(t *testing.T) {
	t.Parallel()
	s := newScreen(80, 24)
	z := NewZone(s, terminal(80, 24))
	l := z.Acquire(6)
	fmt.Fprint(s, "transcript line\n")
	_, err := l.Set([]Row{{Text: "pool 1/4 running"}})
	require.NoError(t, err)
	require.Equal(t, "pool 1/4 running", s.row1(19))

	require.NoError(t, z.Close())
	assert.Equal(t, 1, s.scrollTop, "margins reset, or the user's shell stays pinned")
	assert.Equal(t, 24, s.scrollBot)
	assert.Equal(t, "", s.row1(19), "the leased rows are cleared, not left as litter")
	assert.Contains(t, s.String(), "transcript line", "and the transcript survives teardown")
}

// TestPaintTwoConsumersShareOneTerminal is the bug the ownership refactor
// fixed, expressed as a picture: before it, a second consumer set its own
// scroll margins from its own row arithmetic and the two bands overwrote each
// other.
func TestPaintTwoConsumersShareOneTerminal(t *testing.T) {
	t.Parallel()
	s := newScreen(80, 24)
	z := NewZone(s, terminal(80, 24))
	failures := z.Acquire(6)
	n, _ := newTestNotifier(z, 3)

	_, err := failures.Set([]Row{
		{Text: "pool 6/8 running   2 ok  1 failed", Style: SGRDim},
		{Text: "[fail] test std (ran, 4.1s)", Style: SGRBoldRed},
	})
	require.NoError(t, err)
	require.NoError(t, n.Notify("cache stampede on go-build", SGRYellow, time.Minute))

	// One margin set, covering both bands. Seven rows, not nine: the failure
	// band's six plus the ONE row the single notification actually needs.
	assert.Equal(t, 1, s.scrollTop)
	assert.Equal(t, 17, s.scrollBot)
	// Both consumers are visible at once, in acquisition order.
	assert.Equal(t, "pool 6/8 running   2 ok  1 failed", s.row1(18))
	assert.Equal(t, "[fail] test std (ran, 4.1s)", s.row1(19))
	assert.Equal(t, "cache stampede on go-build", s.row1(24))
}
