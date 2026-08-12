package tty

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedClock drives expiry without waiting on a real one.
type fixedClock struct{ t time.Time }

func (c *fixedClock) now() time.Time          { return c.t }
func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newTestNotifier builds a notifier on a controllable clock with no sweeper
// goroutine, so a test drives expiry by calling sweep directly. The band is
// claimed and grown exactly as in production.
func newTestNotifier(z *Zone, max int) (*Notifier, *fixedClock) {
	c := &fixedClock{t: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)}
	// started:true keeps ensureLease from spawning the real sweeper, so the
	// test owns the clock AND the only goroutine touching the screen. Without
	// it the sweeper paints while the test reads the emulator.
	n := &Notifier{zone: z, max: max, now: c.now, started: true,
		stop: make(chan struct{}), wake: make(chan struct{}, 1)}
	return n, c
}

func TestNotifierIsInertWithoutATerminal(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	n := NewNotifier(NewZone(&buf, notATerminal()), 3)
	require.NoError(t, n.Notify("built", SGRGreen, time.Second))
	assert.Empty(t, buf.String(), "a toast is a view: it is dropped, not replayed, when it cannot be shown")
	require.NoError(t, n.Close())
}

func TestNotifierStacksNewestClosestToTheReader(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	n, _ := newTestNotifier(z, 3)

	require.NoError(t, n.Notify("first", "", time.Second))
	require.NoError(t, n.Notify("second", "", time.Second))
	out := buf.String()
	assert.Less(t, strings.Index(out, "first"), strings.Index(out, "second"))

	// The band fills from the bottom, so two toasts in a three-row band occupy
	// the last two rows.
	assert.Contains(t, out, "\x1b[23;1H")
	assert.Contains(t, out, "\x1b[24;1H")
}

func TestNotifierExpiresOnItsOwn(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	n, clock := newTestNotifier(z, 3)

	require.NoError(t, n.Notify("lint failed", SGRRed, 5*time.Second))
	buf.Reset()

	// Not yet: a sweep before the deadline must not touch the terminal.
	clock.advance(4 * time.Second)
	n.sweep()
	assert.Empty(t, buf.String())

	clock.advance(2 * time.Second)
	n.sweep()
	assert.Contains(t, buf.String(), el, "the vacated row is erased")

	n.mu.Lock()
	assert.Empty(t, n.toasts)
	n.mu.Unlock()
}

func TestNotifierKeepsAStickyToast(t *testing.T) {
	t.Parallel()
	// A zero ttl is the deliberate "stays until something displaces it" case,
	// for a message that should not vanish while the reader is still working.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	n, clock := newTestNotifier(z, 3)

	require.NoError(t, n.Notify("daemon unreachable", SGRRed, 0))
	clock.advance(time.Hour)
	n.sweep()

	n.mu.Lock()
	assert.Len(t, n.toasts, 1)
	n.mu.Unlock()
}

func TestNotifierDropsTheOldestOnOverflow(t *testing.T) {
	t.Parallel()
	// Discarding the NEWEST would make a burst look like nothing happened.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	n, _ := newTestNotifier(z, 2)

	require.NoError(t, n.Notify("one", "", time.Minute))
	require.NoError(t, n.Notify("two", "", time.Minute))
	buf.Reset()
	require.NoError(t, n.Notify("three", "", time.Minute))

	n.mu.Lock()
	got := []string{n.toasts[0].text, n.toasts[1].text}
	n.mu.Unlock()
	assert.Equal(t, []string{"two", "three"}, got)
	assert.NotContains(t, buf.String(), "one")
}

func TestNotifierCloseGivesTheBandBack(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	n := NewNotifier(z, 3)
	require.NoError(t, n.Notify("done", SGRGreen, time.Minute))
	buf.Reset()

	require.NoError(t, n.Close())
	assert.Contains(t, buf.String(), decstbmReset)
	// Idempotent, and inert afterwards: teardown ordering is not something
	// every caller controls.
	require.NoError(t, n.Close())
	require.NoError(t, n.Notify("late", "", time.Minute))
}

func TestNotifierSharesTheZoneWithAnotherLease(t *testing.T) {
	t.Parallel()
	// The whole point of the ownership refactor: a run's failure band and a
	// notification band coexist instead of fighting over the scroll margins.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 40))
	failures := z.Acquire(6)
	n, _ := newTestNotifier(z, 3)

	rendered, err := failures.Set([]Line{{Text: "pool 2/8 running", Style: SGRDim}})
	require.NoError(t, err)
	require.True(t, rendered)
	require.NoError(t, n.Notify("api built", SGRGreen, time.Minute))

	out := buf.String()
	assert.Less(t, strings.Index(out, "pool 2/8 running"), strings.Index(out, "api built"))
	assert.Equal(t, 1, strings.Count(out, "\x1b[1;33r"),
		"one owner, one DECSTBM: 6 failure rows plus one notification leaves 33 scrolling")
}

func TestNotifierClaimsNoRowsUntilItNotifies(t *testing.T) {
	t.Parallel()
	// The cost discipline: a run that never raises a notification must not pay
	// three rows of scrolling area for the possibility. Under the rule that
	// only actionable things are worth notifying about, that is most runs.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	other := z.Acquire(6)
	n := NewNotifier(z, 3)

	_, err := other.Set([]Line{{Text: "pool 1/8 running"}})
	require.NoError(t, err)
	// 6 leased rows on 24 means margins 1;18, not the 1;15 nine rows would give.
	assert.Contains(t, buf.String(), "\x1b[1;18r", "the unused band must cost nothing")

	z.mu.Lock()
	assert.Equal(t, 6, z.region.height)
	z.mu.Unlock()

	require.NoError(t, n.Notify("waiting on the lock", SGRYellow, 0))
	z.mu.Lock()
	assert.Equal(t, 7, z.region.height,
		"one notification claims ONE row, not the band's worst case")
	z.mu.Unlock()

	require.NoError(t, n.Notify("second", "", time.Minute))
	z.mu.Lock()
	assert.Equal(t, 8, z.region.height, "and it grows only as more are actually showing")
	z.mu.Unlock()
}

func TestNotifierHoldsItsRowsOnceClaimed(t *testing.T) {
	t.Parallel()
	// The other half of the same discipline: releasing on an empty stack would
	// grow and shrink the zone as notifications came and went, and every one of
	// those is a visible reflow of the whole screen. One reflow per run, at
	// most.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	n, clock := newTestNotifier(z, 3)
	require.NoError(t, n.Notify("transient", "", time.Second))
	buf.Reset()

	clock.advance(2 * time.Second)
	n.sweep()
	assert.NotContains(t, buf.String(), decstbmReset, "an emptied band must not hand its rows back mid-run")
}

func TestNotifierPinSurvivesABurstOfTransients(t *testing.T) {
	t.Parallel()
	// A pinned condition was raised once, when the condition began. If ordinary
	// notifications could evict it, it would be gone for good while the problem
	// it reports is still true.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	n, _ := newTestNotifier(z, 2)

	require.NoError(t, n.Pin("lock", "waiting on the workspace lock", SGRYellow))
	for _, m := range []string{"one", "two", "three"} {
		require.NoError(t, n.Notify(m, "", time.Minute))
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	require.Len(t, n.toasts, 2)
	assert.Equal(t, "lock", n.toasts[0].key, "the pin holds its row")
	assert.Equal(t, "three", n.toasts[1].text, "and the newest transient takes the other")
}

func TestNotifierPinUpdatesInPlaceAndClears(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	n, _ := newTestNotifier(z, 3)

	require.NoError(t, n.Pin("lock", "waiting on the lock", SGRYellow))
	require.NoError(t, n.Pin("lock", "waiting on the lock (pid 4211)", SGRYellow))
	n.mu.Lock()
	require.Len(t, n.toasts, 1, "the same condition must not stack")
	assert.Equal(t, "waiting on the lock (pid 4211)", n.toasts[0].text)
	n.mu.Unlock()

	require.NoError(t, n.Clear("lock"))
	n.mu.Lock()
	assert.Empty(t, n.toasts)
	n.mu.Unlock()

	// Retracting a condition that was never shown is a no-op, so a caller
	// reporting the end does not have to know whether the start was displayed.
	assert.NoError(t, n.Clear("lock"))
}

func TestNotifierSleepsWhenNothingCanExpire(t *testing.T) {
	t.Parallel()
	// The long-running property: a band holding only pinned conditions - which
	// have no deadline by design - gives the sweeper nothing to wait for, so it
	// arms no timer and wakes zero times. A TUI left open all afternoon must be
	// as close to free as a process doing nothing.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	n, _ := newTestNotifier(z, 3)

	_, ok := n.untilNextDeadline()
	assert.False(t, ok, "an empty band has nothing to wait for")

	require.NoError(t, n.Pin("lock", "waiting on the workspace lock", SGRYellow))
	_, ok = n.untilNextDeadline()
	assert.False(t, ok, "a pinned condition never expires on a clock")

	require.NoError(t, n.Notify("transient", "", 5*time.Second))
	d, ok := n.untilNextDeadline()
	require.True(t, ok)
	assert.Equal(t, 5*time.Second, d, "and the timer is armed to the soonest deadline, not a fixed interval")
}

func TestNotifierArmsToTheSoonestOfSeveral(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	n, clock := newTestNotifier(z, 3)

	require.NoError(t, n.Notify("slow", "", 30*time.Second))
	require.NoError(t, n.Notify("quick", "", 2*time.Second))
	d, ok := n.untilNextDeadline()
	require.True(t, ok)
	assert.Equal(t, 2*time.Second, d)

	// Once the soonest is gone the next one takes over, rather than the loop
	// spinning on an already-passed deadline.
	clock.advance(3 * time.Second)
	n.sweep()
	d, ok = n.untilNextDeadline()
	require.True(t, ok)
	assert.Equal(t, 27*time.Second, d)
}

func TestNotifierCloseStopsTheSweeper(t *testing.T) {
	t.Parallel()
	// A long-running process may open and close many of these; each must take
	// its goroutine with it.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	n := NewNotifier(z, 3)
	require.NoError(t, n.Notify("x", "", time.Minute))
	require.NoError(t, n.Close())

	select {
	case <-n.stop:
	default:
		t.Fatal("Close must stop the sweeper")
	}
}

// TestNotifierPaintNeverDropsAPinFromTheModel is the second half of the
// eviction rule, on the path that used to have its own contradictory copy.
//
// When a grow is refused the band holds fewer rows than the stack, and paint
// used to truncate the STACK from the front to fit. A pin dropped that way is
// gone from the model, so Clear can never retract it and the condition it
// reports stays true with nothing on screen saying so.
func TestNotifierPaintNeverDropsAPinFromTheModel(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	// Room for the band to be claimed, but not to grow past one row.
	z := NewZone(&buf, terminal(80, 10))
	n, _ := newTestNotifier(z, 3)

	require.NoError(t, n.Pin("lock", "waiting on the workspace lock", SGRYellow))
	require.NoError(t, n.Notify("one", "", time.Minute))
	require.NoError(t, n.Notify("two", "", time.Minute))

	n.mu.Lock()
	keys := make([]string, 0, len(n.toasts))
	for _, tst := range n.toasts {
		keys = append(keys, tst.key)
	}
	n.mu.Unlock()
	assert.Contains(t, keys, "lock", "the pin survives in the model however few rows are painted")

	// And it is still retractable, which is the point of keeping it.
	require.NoError(t, n.Clear("lock"))
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, tst := range n.toasts {
		assert.NotEqual(t, "lock", tst.key)
	}
}
