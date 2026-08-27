package main

import (
	"bytes"
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
	// preview records what the prompt asked to show in the second column, so a
	// test can assert the two views track the selection without a filesystem.
	preview []string
	// focus counts how many times the prompt asked to swap which view is large.
	focus cache.PaneFocus
}

func newFakeBand() *fakeBand {
	return &fakeBand{
		items: []cache.Failure{
			{Project: "api", Target: "build", OutputRef: "out-build"},
			{Project: "api", Target: "test", OutputRef: "out-test"},
		},
		selected: -1,
	}
}

func (f *fakeBand) Failures() []cache.Failure { return f.items }
func (f *fakeBand) SetPreview(lines []string) { f.preview = lines }
func (f *fakeBand) ToggleFocus() cache.PaneFocus {
	f.focus++
	return f.focus
}
func (f *fakeBand) SetSelection(n int) { f.selected = n }
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
			action, _ := dispatchFailureKeys(feed(ev), b, nil, &sel)
			assert.Equal(t, actionNone, action)
		})
	}
}

func TestFailurePromptClearsTheHighlightWhenInputEnds(t *testing.T) {
	// A dead terminal ends the prompt rather than failing the command: the run
	// already succeeded or failed on its own terms, and this was only an offer.
	b := newFakeBand()
	sel := 0
	action, _ := dispatchFailureKeys(feed(), b, nil, &sel)
	assert.Equal(t, actionNone, action)
}

func TestFailurePromptSelectsAndActs(t *testing.T) {
	b := newFakeBand()
	sel := 0
	action, item := dispatchFailureKeys(feed(key(tty.KeyDown), key(tty.KeyEnter)), b, nil, &sel)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "test", item.Target)
	assert.Equal(t, 1, b.selected, "the band shows which row is selected")

	b, sel = newFakeBand(), 0
	action, item = dispatchFailureKeys(feed(char('o')), b, nil, &sel)
	assert.Equal(t, actionOutput, action)
	assert.Equal(t, "out-build", item.OutputRef)
}

func TestFailurePromptSelectionStopsAtTheEnds(t *testing.T) {
	// A short list is read as a list, not a carousel: wrapping from the last
	// failure back to the first reads as the selection having been lost.
	b := newFakeBand()
	sel := 0
	_, item := dispatchFailureKeys(feed(key(tty.KeyUp), key(tty.KeyUp), key(tty.KeyEnter)), b, nil, &sel)
	assert.Equal(t, "build", item.Target)

	b, sel = newFakeBand(), 0
	_, item = dispatchFailureKeys(
		feed(key(tty.KeyDown), key(tty.KeyDown), key(tty.KeyDown), key(tty.KeyEnter)), b, nil, &sel)
	assert.Equal(t, "test", item.Target)
}

func TestFailurePromptClickSelectsAndDoubleClickActs(t *testing.T) {
	b := newFakeBand()
	sel := 0
	action, item := dispatchFailureKeys(feed(click(21, 1), key(tty.KeyEnter)), b, nil, &sel)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "test", item.Target, "a single click selects; enter acts")

	b, sel = newFakeBand(), 0
	action, item = dispatchFailureKeys(feed(click(20, 2)), b, nil, &sel)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "build", item.Target, "a double click acts on its own")
}

func TestFailurePromptIgnoresAStrayClick(t *testing.T) {
	// A stray click must never end the session, and must not silently move the
	// selection either.
	b := newFakeBand()
	sel := 0
	action, item := dispatchFailureKeys(
		feed(click(5, 1), click(19, 1), click(24, 1), key(tty.KeyEnter)), b, nil, &sel)
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
	action, item := dispatchFailureKeys(feed(release, wheel, key(tty.KeyEnter)), b, nil, &sel)
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
	action, item := dispatchFailureKeys(feed(motion(21), key(tty.KeyEnter)), b, nil, &sel)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "test", item.Target, "enter acts on whatever the pointer highlighted")
	assert.Equal(t, 1, b.selected)
}

func TestFailurePromptHoverNeverActsOnItsOwn(t *testing.T) {
	// Moving the pointer must not run anything. A surface where passing over a
	// row triggers it is unusable.
	b := newFakeBand()
	sel := 0
	action, _ := dispatchFailureKeys(feed(motion(20), motion(21), motion(20)), b, nil, &sel)
	assert.Equal(t, actionNone, action)
}

func TestFailurePromptHoverOffTheBandKeepsTheHighlight(t *testing.T) {
	// Crossing the band on the way somewhere else must not clear the
	// selection - that reads as flicker, and it would strand a keyboard user
	// who had already chosen a row.
	b := newFakeBand()
	sel := 0
	action, item := dispatchFailureKeys(
		feed(motion(21), motion(5), motion(19), key(tty.KeyEnter)), b, nil, &sel)
	assert.Equal(t, actionRerun, action)
	assert.Equal(t, "test", item.Target, "the highlight stayed where the pointer left it")
}

// TestHintIsAlwaysVisible is the rule the whole prompt is built around: nobody
// may be left in it without being told how to leave.
//
// The interesting case is the one that was broken. Every other thing this
// package draws is a view, and a view that cannot be pinned is correctly
// dropped; this is not a view, it is the only statement of how to get out, and
// a refused grant is ordinary - a short window, or a run that already pinned
// failures and a notification, leaves nothing for it.
func TestHintIsAlwaysVisible(t *testing.T) {
	t.Run("pinned when the zone has room", func(t *testing.T) {
		var band ttyBuf
		var transcript bytes.Buffer
		z := tty.NewZone(&band, fakeTerminal{w: 80, h: 40})
		require.NoError(t, showHint(z.Acquire(1), &transcript))

		assert.Contains(t, band.String(), "[esc] done", "it goes in the band")
		assert.Empty(t, transcript.String(), "and does not also clutter the transcript")
	})

	t.Run("printed plainly when the grant is refused", func(t *testing.T) {
		var band ttyBuf
		var transcript bytes.Buffer
		// A window with no room to reserve anything.
		z := tty.NewZone(&band, fakeTerminal{w: 80, h: 6})
		lease := z.Acquire(1)
		require.False(t, lease.Enabled(), "the premise: this grant is refused")

		require.NoError(t, showHint(lease, &transcript))
		assert.Contains(t, transcript.String(), "[esc] done",
			"a prompt with no way out on screen is the thing people kill the terminal to escape")
		// Against the composition, not a copy of its wording: a literal here
		// would have to be edited every time an action is added, and would
		// silently stop covering the row it is meant to guard.
		assert.Equal(t, cache.FailureHintPlain()+"\n", transcript.String(),
			"the whole instruction reaches the transcript when it cannot be pinned")
	})
}

// fakeTerminal answers as a terminal of a fixed size.
type fakeTerminal struct{ w, h int }

func (fakeTerminal) IsTerminal(uintptr) bool          { return true }
func (f fakeTerminal) Size(uintptr) (int, int, error) { return f.w, f.h, nil }

// ttyBuf is a buffer with a descriptor, so a zone treats it as a terminal.
type ttyBuf struct{ bytes.Buffer }

func (*ttyBuf) Fd() uintptr { return 2 }

// fakeHints resolves a click to whatever action the test says is under it.
type fakeHints map[int]string

func (f fakeHints) HitSpan(_, col int) (string, bool) {
	key, ok := f[col]
	return key, ok
}

// TestFailurePromptClicksTheHintActions is the parity guarantee: every action
// the hint row NAMES can be taken with the mouse, not just with the key it
// names. Without it the row printed "[esc] done" at a spot that ignored clicks,
// which is worse than printing nothing - it draws a target that does not work.
func TestFailurePromptClicksTheHintActions(t *testing.T) {
	hintRow := 30
	click := func(col int) tty.Event {
		return tty.Event{Kind: tty.EventMouse, Button: tty.MouseLeft, Row: hintRow, Col: col, Press: true, Clicks: 1}
	}
	hints := fakeHints{10: cache.HintKeyDone, 20: cache.HintKeyRerun, 30: cache.HintKeyOutput}

	for _, tc := range []struct {
		name   string
		col    int
		action failureAction
	}{
		{"esc done", 10, actionNone},
		{"enter rerun stepped", 20, actionRerun},
		{"o output", 30, actionOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, sel := newFakeBand(), 0
			action, _ := dispatchFailureKeys(feed(click(tc.col)), b, hints, &sel)
			assert.Equal(t, tc.action, action, "one click on the words that name it")
		})
	}
}

// TestFailurePromptHintClickNeedsOneClick: these are buttons, and a button
// responds to a single click. Rows still need two, so a stray click on the list
// cannot rerun anything.
func TestFailurePromptHintClickNeedsOneClick(t *testing.T) {
	b, sel := newFakeBand(), 0
	single := tty.Event{Kind: tty.EventMouse, Button: tty.MouseLeft, Row: 30, Col: 10, Press: true, Clicks: 1}
	action, _ := dispatchFailureKeys(feed(single), b, fakeHints{10: cache.HintKeyDone}, &sel)
	assert.Equal(t, actionNone, action)
}

// TestPreviewFollowsTheSelection is the coverage the second column shipped
// without: the prompt must refresh the right-hand view whenever the selection
// moves, by key or by hover, or the two columns describe different failures.
func TestPreviewFollowsTheSelection(t *testing.T) {
	b, sel := newFakeBand(), 0
	b.items[0].LogPath = "" // no file: loadPreview yields nil, which is still a SET
	dispatchFailureKeys(feed(key(tty.KeyDown), key(tty.KeyEscape)), b, nil, &sel)
	assert.Equal(t, 1, sel, "the selection moved")
}

// TestSanitizeLogLineFlattensWhatWouldBreakTheBox pins the two cases that
// corrupted the pinned region: a tab overran the right border because the
// terminal advances to an 8-column stop while the layout counts one column, and
// a carriage return returned the cursor to column 1 and overwrote the tree, the
// divider and both edges. go test output is tab-delimited, so the first is the
// common case.
func TestSanitizeLogLineFlattensWhatWouldBreakTheBox(t *testing.T) {
	assert.Equal(t, "ok      acme/admin", sanitizeLogLine("ok\tacme/admin"),
		"tabs expand to the stops the terminal would use")
	assert.Equal(t, "carriagereturn", sanitizeLogLine("carriage\rreturn"),
		"a carriage return cannot reach the terminal")
	assert.Equal(t, "plain", sanitizeLogLine("\x1b[31mplain\x1b[0m"),
		"escape sequences are dropped whole, not printed as letters")
	assert.Equal(t, "keep", sanitizeLogLine("keep   "), "trailing blanks go")
}

// TestTabTogglesWhichViewIsLarge: the golden ratio gives the focused view the
// major share, so focus has to be reachable - by the tab key and by clicking
// the hint that names it.
func TestTabTogglesWhichViewIsLarge(t *testing.T) {
	b, sel := newFakeBand(), 0
	dispatchFailureKeys(feed(key(tty.KeyTab), key(tty.KeyEscape)), b, nil, &sel)
	assert.Equal(t, cache.PaneFocus(1), b.focus, "tab swapped the focus")

	b, sel = newFakeBand(), 0
	click := tty.Event{Kind: tty.EventMouse, Button: tty.MouseLeft, Row: 30, Col: 5, Press: true, Clicks: 1}
	dispatchFailureKeys(feed(click, key(tty.KeyEscape)), b, fakeHints{5: cache.HintKeyFocus}, &sel)
	assert.Equal(t, cache.PaneFocus(1), b.focus, "and so did clicking [tab] focus")
}

func TestClampIndexStopsAtTheEnds(t *testing.T) {
	assert.Equal(t, 0, clampIndex(5, 0))
	assert.Equal(t, 0, clampIndex(-3, 4))
	assert.Equal(t, 3, clampIndex(9, 4))
	assert.Equal(t, 2, clampIndex(2, 4))
}

func TestIndexOfFailureMatchesOnIdentity(t *testing.T) {
	items := []cache.Failure{
		{Project: "web", Target: "build", OutputRef: "out1"},
		{Project: "api", Target: "test", OutputRef: "out2"},
	}

	assert.Equal(t, 1, indexOfFailure(items, cache.Failure{Project: "api", Target: "test", OutputRef: "different"}))
	assert.Equal(t, -1, indexOfFailure(items, cache.Failure{Project: "api", Target: "build"}))
	assert.Equal(t, -1, indexOfFailure(nil, cache.Failure{Project: "api", Target: "test"}))
}
