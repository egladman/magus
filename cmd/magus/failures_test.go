package main

import (
	"errors"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBand stands in for the run's pinned failure band. Rows 20 and 21 hold the
// two failures, matching a six-row band on a 24-row terminal with the status
// line on row 19.
type fakeBand struct {
	items    []cache.Failure
	selected int
}

func newFakeBand() *fakeBand {
	return &fakeBand{
		items: []cache.Failure{
			{Project: "api", Target: "build", Ref: "out-build"},
			{Project: "api", Target: "test", Ref: "out-test"},
		},
		selected: -1,
	}
}

func (f *fakeBand) Failures() []cache.Failure { return f.items }
func (f *fakeBand) SetSelection(n int)        { f.selected = n }
func (f *fakeBand) HitFailure(row int) (cache.Failure, bool) {
	switch row {
	case 20:
		return f.items[0], true
	case 21:
		return f.items[1], true
	}
	return cache.Failure{}, false
}

// feed turns a fixed event list into the reader dispatchFailureKeys takes. Once
// exhausted it errors, which is what a closed terminal looks like.
func feed(events ...tty.Event) func() (tty.Event, error) {
	i := 0
	return func() (tty.Event, error) {
		if i >= len(events) {
			return tty.Event{}, errors.New("no more input")
		}
		ev := events[i]
		i++
		return ev, nil
	}
}

func key(k tty.Key) tty.Event { return tty.Event{Kind: tty.EventKey, Key: k} }
func char(r rune) tty.Event   { return tty.Event{Kind: tty.EventKey, Key: tty.KeyRune, Rune: r} }
func click(row, clicks int) tty.Event {
	return tty.Event{Kind: tty.EventMouse, Button: tty.MouseLeft, Row: row, Press: true, Clicks: clicks}
}

// TestFailurePromptEscapesEveryWayAUserMightTry is the rule that matters most:
// nobody may end up unable to leave.
//
// Ctrl-C is the one people reach for when they feel stuck, and in raw mode it
// arrives as a BYTE rather than a signal - so a loop that forgot to handle it
// would leave that key doing nothing, which is exactly the experience that ends
// with someone killing the terminal.
func TestFailurePromptEscapesEveryWayAUserMightTry(t *testing.T) {
	for name, ev := range map[string]tty.Event{
		"escape": key(tty.KeyEscape),
		"ctrl-c": key(tty.KeyCtrlC),
		"ctrl-d": key(tty.KeyCtrlD),
		"q":      char('q'),
	} {
		t.Run(name, func(t *testing.T) {
			b := newFakeBand()
			sel := 0
			action, _, err := dispatchFailureKeys(feed(ev), b, &sel)
			require.NoError(t, err)
			assert.Equal(t, actionNone, action)
		})
	}
}

func TestFailurePromptClearsTheHighlightWhenInputEnds(t *testing.T) {
	// A dead terminal ends the prompt rather than failing the command: the run
	// already succeeded or failed on its own terms, and this was only an offer.
	b := newFakeBand()
	sel := 0
	action, _, err := dispatchFailureKeys(feed(), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, actionNone, action)
}

func TestFailurePromptSelectsAndActs(t *testing.T) {
	b := newFakeBand()
	sel := 0
	action, item, err := dispatchFailureKeys(feed(key(tty.KeyDown), key(tty.KeyEnter)), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "test", item.Target)
	assert.Equal(t, 1, b.selected, "the band shows which row is selected")

	b, sel = newFakeBand(), 0
	action, item, err = dispatchFailureKeys(feed(char('o')), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, actionOutput, action)
	assert.Equal(t, "out-build", item.Ref)
}

func TestFailurePromptSelectionStopsAtTheEnds(t *testing.T) {
	// A short list is read as a list, not a carousel: wrapping from the last
	// failure back to the first reads as the selection having been lost.
	b := newFakeBand()
	sel := 0
	_, item, err := dispatchFailureKeys(feed(key(tty.KeyUp), key(tty.KeyUp), key(tty.KeyEnter)), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, "build", item.Target)

	b, sel = newFakeBand(), 0
	_, item, err = dispatchFailureKeys(
		feed(key(tty.KeyDown), key(tty.KeyDown), key(tty.KeyDown), key(tty.KeyEnter)), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, "test", item.Target)
}

func TestFailurePromptClickSelectsAndDoubleClickActs(t *testing.T) {
	b := newFakeBand()
	sel := 0
	action, item, err := dispatchFailureKeys(feed(click(21, 1), key(tty.KeyEnter)), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "test", item.Target, "a single click selects; enter acts")

	b, sel = newFakeBand(), 0
	action, item, err = dispatchFailureKeys(feed(click(20, 2)), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "build", item.Target, "a double click acts on its own")
}

func TestFailurePromptIgnoresAStrayClick(t *testing.T) {
	// A stray click must never end the session, and must not silently move the
	// selection either.
	b := newFakeBand()
	sel := 0
	action, item, err := dispatchFailureKeys(
		feed(click(5, 1), click(19, 1), click(24, 1), key(tty.KeyEnter)), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "build", item.Target,
		"clicks on the transcript, the status row and an empty slot all did nothing")
}

func TestFailurePromptIgnoresReleasesAndTheWheel(t *testing.T) {
	// Only a left PRESS acts. A release would double every click, and the wheel
	// belongs to scrolling.
	b := newFakeBand()
	sel := 0
	release := tty.Event{Kind: tty.EventMouse, Button: tty.MouseLeft, Row: 21, Press: false, Clicks: 1}
	wheel := tty.Event{Kind: tty.EventMouse, Button: tty.MouseWheelDown, Row: 21, Press: true, Clicks: 1}
	action, item, err := dispatchFailureKeys(feed(release, wheel, key(tty.KeyEnter)), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "build", item.Target)
}

func motion(row int) tty.Event {
	return tty.Event{Kind: tty.EventMouse, Row: row, Motion: true}
}

// TestFailurePromptHoverMovesTheHighlight is the affordance that makes a
// clickable row look clickable. Without it a mouse-driven surface is a guessing
// game, which defeats the point of having one.
func TestFailurePromptHoverMovesTheHighlight(t *testing.T) {
	b := newFakeBand()
	sel := 0
	action, item, err := dispatchFailureKeys(feed(motion(21), key(tty.KeyEnter)), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "test", item.Target, "enter acts on whatever the pointer highlighted")
	assert.Equal(t, 1, b.selected)
}

func TestFailurePromptHoverNeverActsOnItsOwn(t *testing.T) {
	// Moving the pointer must not run anything. A surface where passing over a
	// row triggers it is unusable.
	b := newFakeBand()
	sel := 0
	action, _, err := dispatchFailureKeys(feed(motion(20), motion(21), motion(20)), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, actionNone, action)
}

func TestFailurePromptHoverOffTheBandKeepsTheHighlight(t *testing.T) {
	// Crossing the band on the way somewhere else must not clear the
	// selection - that reads as flicker, and it would strand a keyboard user
	// who had already chosen a row.
	b := newFakeBand()
	sel := 0
	action, item, err := dispatchFailureKeys(
		feed(motion(21), motion(5), motion(19), key(tty.KeyEnter)), b, &sel)
	require.NoError(t, err)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "test", item.Target, "the highlight stayed where the pointer left it")
}
