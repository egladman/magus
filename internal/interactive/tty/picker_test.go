package tty

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interactive/screen"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilter_AND(t *testing.T) {
	items := []string{
		"apps/web/dashboard",
		"apps/mobile/dashboard",
		"services/api",
		"tools/scripts",
	}

	t.Run("empty matches all", func(t *testing.T) {
		assert.Equal(t, []int{0, 1, 2, 3}, filterIndices(items, ""))
	})
	t.Run("single token substring", func(t *testing.T) {
		assert.Equal(t, []int{0, 1}, filterIndices(items, "dash"))
	})
	t.Run("AND narrows", func(t *testing.T) {
		assert.Equal(t, []int{1}, filterIndices(items, "dash mobile"))
	})
	t.Run("AND no match", func(t *testing.T) {
		assert.Empty(t, filterIndices(items, "dash api"))
	})
	t.Run("case insensitive", func(t *testing.T) {
		assert.Equal(t, []int{0}, filterIndices(items, "DASH WEB"))
	})
	t.Run("order independent", func(t *testing.T) {
		assert.Equal(t, []int{1}, filterIndices(items, "mobile dash"))
	})
	t.Run("surrounding whitespace is split, not matched", func(t *testing.T) {
		assert.Equal(t, []int{1}, filterIndices(items, "   mobile   dash   "))
	})
}

// newTestSession builds a picker session rendering into buf, so the filter,
// cursor, windowing and redraw logic can be driven without raw mode or a pty.
// Pick itself needs a real terminal; every decision it delegates is covered
// here, and key decoding now belongs to Input, which has its own tests.
func newTestSession(buf *ttyBuf, items []string, opts PickOptions) *session {
	if opts.MaxRows <= 0 {
		opts.MaxRows = 10
	}
	p := terminal(80, 24)
	s := &session{
		items:  items,
		opts:   opts,
		filter: opts.InitialFilter,
		out:    buf,
		probe:  p,
		view:   NewInlineView(buf, p),
	}
	// Background: this helper builds a session for assertions, not a live loop.
	_ = s.refilter(context.Background())
	s.cursor = s.findInitial()
	return s
}

func TestSessionFindInitialLocatesTheRequestedItem(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	s := newTestSession(&buf, []string{"alpha", "beta", "gamma"}, PickOptions{Initial: 2})
	assert.Equal(t, 2, s.cursor, "with no filter the initial index is its own match index")
}

// TestSessionFindInitialFallsBackToFirst covers the case where the
// requested item is filtered out: the cursor must land somewhere valid
// rather than pointing past the end of the match list.
func TestSessionFindInitialFallsBackToFirst(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	s := newTestSession(&buf, []string{"alpha", "beta"}, PickOptions{Initial: 1, InitialFilter: "alpha"})
	assert.Equal(t, 0, s.cursor, "an initial item that no longer matches falls back to the first")
}

func TestSessionDrawMarksTheCursorRow(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	s := newTestSession(&buf, []string{"alpha", "beta", "gamma"}, PickOptions{Prompt: "project"})
	s.cursor = 1
	s.draw()

	out := unbox(stripANSI(buf.String()))
	assert.Contains(t, out, SelectMark+" beta", "the cursor row is marked")
	assert.Contains(t, out, "  alpha", "non-cursor rows are indented, not marked")
	assert.Contains(t, out, "project: _", "the prompt and filter caret are drawn")
	assert.Equal(t, 3+pickerChrome, s.view.Lines(), "three matches inside the box")
}

func TestSessionDrawUsesADefaultPrompt(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	s := newTestSession(&buf, []string{"alpha"}, PickOptions{})
	s.draw()
	assert.Contains(t, unbox(stripANSI(buf.String())), "filter: _", "an unset prompt falls back to a generic label")
}

func TestSessionDrawReportsNoMatches(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	s := newTestSession(&buf, []string{"alpha"}, PickOptions{InitialFilter: "zzz"})
	s.draw()

	assert.Contains(t, unbox(stripANSI(buf.String())), "(no matches)")
	assert.Equal(t, 1+pickerChrome, s.view.Lines(), "the empty-state line inside the box")
}

// TestSessionDrawErasesThePreviousRender is what keeps a shrinking list
// from leaving orphaned rows on screen: the second paint must erase as
// many lines as the first one drew.
func TestSessionDrawErasesThePreviousRender(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	s := newTestSession(&buf, []string{"alpha", "beta", "gamma"}, PickOptions{})
	s.draw()
	drawn := s.view.Lines()
	require.Equal(t, 3+pickerChrome, drawn)

	buf.Reset()
	s.filter = "alpha"
	_ = s.refilter(t.Context())
	s.cursor = 0
	s.draw()

	// RAW, not unboxed: this counts escape sequences, which stripANSI removes.
	assert.Equal(t, drawn, strings.Count(buf.String(), el2),
		"every previously drawn line must be erased before the redraw")
	assert.Equal(t, 1+pickerChrome, s.view.Lines(), "one match inside the box")
}

func TestSessionDrawWindowsAroundTheCursor(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	s := newTestSession(&buf, []string{"a0", "a1", "a2", "a3", "a4", "a5"}, PickOptions{MaxRows: 3})
	s.cursor = 5 // past the window; the view must scroll to include it
	s.draw()

	out := unbox(stripANSI(buf.String()))
	assert.Contains(t, out, SelectMark+" a5", "the cursor item must be visible")
	assert.Contains(t, out, "a3", "the window ends at the cursor")
	assert.NotContains(t, out, "a0", "items above the window are not drawn")
	assert.Equal(t, 3+pickerChrome, s.view.Lines(), "MaxRows rows inside the box")
}

func TestSessionCleanupErasesEverythingItDrew(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	s := newTestSession(&buf, []string{"alpha", "beta"}, PickOptions{})
	s.draw()
	drawn := s.view.Lines()

	buf.Reset()
	s.cleanup()

	assert.Equal(t, drawn, strings.Count(buf.String(), el2),
		"cleanup must erase exactly what it drew, so the picker leaves no trace")
	assert.Zero(t, s.view.Lines())
}

func TestPickRejectsAnEmptyItemList(t *testing.T) {
	t.Parallel()
	idx, err := Pick(t.Context(), os.Stdin, os.Stderr, notATerminal(), nil, PickOptions{})
	assert.Equal(t, -1, idx)
	assert.Error(t, err, "there is nothing to pick from")
}

func TestSessionMatchAtResolvesAClickToAnItem(t *testing.T) {
	t.Parallel()
	// Items sit directly above the prompt, so the topmost is `visible` rows up
	// from it. Without this the picker knows its own shape but not where it is,
	// and a click coordinate cannot be resolved against it at all.
	var buf ttyBuf
	s := newTestSession(&buf, []string{"alpha", "beta", "gamma"}, PickOptions{})
	s.draw()
	s.mouseOK, s.promptRow = true, 20 // three items on rows 17-19, prompt on 20

	for row, want := range map[int]int{17: 0, 18: 1, 19: 2} {
		got, ok := s.matchAt(row)
		require.True(t, ok, "row %d", row)
		assert.Equal(t, want, got, "row %d", row)
	}

	// The prompt line and anything off the block belong to nobody. A stray
	// click must never pick something the pointer was not even over.
	for _, row := range []int{20, 16, 1, 40} {
		_, ok := s.matchAt(row)
		assert.False(t, ok, "row %d", row)
	}
}

func TestSessionMatchAtFollowsTheScrolledWindow(t *testing.T) {
	t.Parallel()
	// With more matches than rows the list scrolls, so a screen row maps to a
	// different match than it did before. Mapping by screen position alone
	// would select the wrong item as soon as the user typed.
	var buf ttyBuf
	items := []string{"a1", "a2", "a3", "a4", "a5", "a6"}
	s := newTestSession(&buf, items, PickOptions{MaxRows: 3})
	s.cursor = 5
	s.draw()
	s.mouseOK, s.promptRow = true, 20
	require.Equal(t, 3, s.start, "the window scrolled to keep the cursor visible")

	got, ok := s.matchAt(17)
	require.True(t, ok)
	assert.Equal(t, 3, got, "the top visible row is the fourth match, not the first")
}

func TestSessionMatchAtIsInertWithoutAKnownPosition(t *testing.T) {
	t.Parallel()
	// A terminal that will not report the cursor leaves the picker keyboard
	// only, which is exactly what it was before. It must not guess.
	var buf ttyBuf
	s := newTestSession(&buf, []string{"alpha", "beta"}, PickOptions{})
	s.draw()
	s.mouseOK = false
	s.promptRow = 20
	_, ok := s.matchAt(19)
	assert.False(t, ok)
}

// TestSessionAsksTheTerminalOnlyOnce is a LATENCY test, not a correctness one.
//
// Every position query is a full round trip to the terminal - microseconds
// locally, an RTT over ssh - and the block changes height on any keystroke that
// filters the list past the visible rows. Querying per redraw put an RTT of lag
// on most of the typing, on exactly the connection where lag already hurts.
func TestSessionAsksTheTerminalOnlyOnce(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	items := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	s := &session{items: items, opts: PickOptions{MaxRows: 10}, out: &buf,
		probe: terminal(80, 24), view: NewInlineView(&buf, terminal(80, 24)), mouseOK: true}
	queries := 0
	s.queryRow = func() (int, bool) { queries++; return 20, true }

	_ = s.refilter(t.Context())
	s.draw()
	require.Equal(t, 1, queries, "the first draw has to anchor against the terminal")
	require.Equal(t, 20, s.promptRow)

	// Filter down and back up: the block changes height four times, and not one
	// of them may cost another round trip.
	for _, f := range []string{"a", "al", "alp", ""} {
		s.filter = f
		_ = s.refilter(t.Context())
		s.draw()
	}
	assert.Equal(t, 1, queries, "every later position is arithmetic")
}

func TestSessionTracksThePromptRowThroughHeightChanges(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	items := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	s := &session{items: items, opts: PickOptions{MaxRows: 10}, out: &buf,
		probe: terminal(80, 24), view: NewInlineView(&buf, terminal(80, 24)), mouseOK: true}
	s.queryRow = func() (int, bool) { return 10, true }
	_ = s.refilter(t.Context())
	s.draw() // 5 items + prompt = 6 lines, prompt anchored at row 10

	// Narrowing to one match drops the block to 2 lines, so the prompt rises
	// by four.
	s.filter = "alpha"
	_ = s.refilter(t.Context())
	s.draw()
	assert.Equal(t, 6, s.promptRow)

	// Widening again pushes it back down.
	s.filter = ""
	_ = s.refilter(t.Context())
	s.draw()
	assert.Equal(t, 10, s.promptRow)
}

func TestSessionClampsThePromptRowToTheLastLine(t *testing.T) {
	t.Parallel()
	// Growing a block that is already at the bottom scrolls the screen, so the
	// prompt stays on the last row rather than running off it.
	var buf ttyBuf
	items := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	s := &session{items: items, opts: PickOptions{MaxRows: 10}, out: &buf,
		probe: terminal(80, 24), view: NewInlineView(&buf, terminal(80, 24)), mouseOK: true}
	s.queryRow = func() (int, bool) { return 24, true }
	s.filter = "a"
	_ = s.refilter(t.Context())
	s.draw() // 1 match + prompt = 2 lines at the bottom

	s.filter = ""
	_ = s.refilter(t.Context())
	s.draw() // 8 matches + prompt = 9 lines: the screen scrolled
	assert.Equal(t, 24, s.promptRow, "the prompt cannot be pushed past the last row")
}

func TestSessionReanchorsAfterAResize(t *testing.T) {
	t.Parallel()
	// A resize invalidates the arithmetic, so the one case that must pay for
	// another round trip does.
	var buf ttyBuf
	p := &resizingProbe{width: 80, height: 24}
	s := &session{items: []string{"a", "b"}, opts: PickOptions{MaxRows: 10}, out: &buf,
		probe: p, view: NewInlineView(&buf, p), mouseOK: true}
	queries := 0
	s.queryRow = func() (int, bool) { queries++; return 20, true }
	_ = s.refilter(t.Context())
	s.draw()
	require.Equal(t, 1, queries)

	p.height = 40
	s.draw()
	assert.Equal(t, 2, queries, "a resized window has to be re-anchored")
}

// TestSessionKeepsTheScreenCorrectAcrossRedraws drives the picker against the
// terminal emulator. Its tests have always checked the bytes it emitted; this
// checks the picture those bytes produce, which is the only way to catch a
// redraw that lands in the wrong place.
func TestSessionKeepsTheScreenCorrectAcrossRedraws(t *testing.T) {
	t.Parallel()
	s := screen.New(40, 24)
	fmt.Fprint(s, "$ magus x\n")
	p := terminal(40, 24)
	sess := &session{
		items: []string{"console", "docs", "libs/gopherbuzz"},
		opts:  PickOptions{MaxRows: 10, Prompt: "project"},
		out:   s, probe: p, view: NewInlineView(s, p),
	}
	_ = sess.refilter(t.Context())
	sess.draw()

	// Row 2 is the box's top rule, which carries the prompt; the list starts
	// under it and the way out closes it.
	assert.Contains(t, s.Row(2), "project: _", "the prompt rides the top rule")
	assert.Equal(t, SelectMark+" console", unboxRow(s, 3))
	assert.Equal(t, "  docs", unboxRow(s, 4))
	assert.Equal(t, "  libs/gopherbuzz", unboxRow(s, 5))
	assert.Contains(t, s.Row(6), "[esc] cancel", "and the way out closes the box")

	// Moving the highlight rewrites two rows and must leave the rest alone.
	sess.cursor = 1
	sess.draw()
	assert.Equal(t, "  console", unboxRow(s, 3))
	assert.Equal(t, SelectMark+" docs", unboxRow(s, 4))
	assert.Equal(t, "  libs/gopherbuzz", unboxRow(s, 5))

	// Filtering shrinks the block; the rows it gives up must come back clean.
	sess.filter = "doc"
	_ = sess.refilter(t.Context())
	sess.cursor = 0
	sess.draw()
	assert.Contains(t, s.Row(2), "project: doc_", "the filter is on the rule")
	assert.Equal(t, SelectMark+" docs", unboxRow(s, 3))
	assert.Equal(t, "", s.Row(5), "the vacated rows are erased, not left as litter")
	assert.Equal(t, "", s.Row(6))

	// And the transcript above is untouched throughout.
	assert.Equal(t, "$ magus x", s.Row(1))

	sess.cleanup()
	assert.Equal(t, "", s.Row(2), "cleanup leaves the terminal as though the picker never ran")
	assert.Equal(t, "$ magus x", s.Row(1))
}

// TestSessionAlwaysDrawsSomethingOnAShortTerminal is the frozen-terminal bug.
//
// The picker asks for ten rows plus a prompt. On a terminal shorter than that,
// the inline view refuses the block - erasing it would walk off the top of the
// screen - and before the window was bounded the picker drew nothing at all,
// while still reading keys in raw mode. A blank terminal with no echo and no
// prompt is indistinguishable from a hung process, and an eleven-row window is
// an ordinary split pane.
func TestSessionAlwaysDrawsSomethingOnAShortTerminal(t *testing.T) {
	t.Parallel()
	for _, height := range []int{24, 12, 11, 8, 4, 3} {
		t.Run(fmt.Sprintf("height=%d", height), func(t *testing.T) {
			t.Parallel()
			sc := screen.New(40, height)
			p := terminal(40, height)
			// Enough items that the window genuinely wants all ten rows, so
			// the block is eleven lines and short terminals actually bite.
			sess := &session{
				items: benchItems(30),
				opts:  PickOptions{MaxRows: 10, Prompt: "project"},
				out:   sc, probe: p, view: NewInlineView(sc, p),
			}
			_ = sess.refilter(t.Context())
			sess.draw()

			require.NotZero(t, sc.FindRow("project:"),
				"the prompt must be on screen at %d rows, or the picker looks hung", height)
			assert.LessOrEqual(t, sess.view.Lines(), height-1,
				"the block has to stay shorter than the screen to be redrawable")
		})
	}
}

func TestSessionShowsAsManyItemsAsFit(t *testing.T) {
	t.Parallel()
	// The bound is on what the terminal can show, not a blanket reduction: a
	// roomy terminal still gets the configured window.
	sc := screen.New(40, 24)
	p := terminal(40, 24)
	items := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	sess := &session{items: items, opts: PickOptions{MaxRows: 10}, out: sc, probe: p,
		view: NewInlineView(sc, p)}
	_ = sess.refilter(t.Context())
	sess.draw()
	assert.Equal(t, 10, sess.visible, "a tall terminal shows the configured window")

	short := screen.New(40, 8)
	sp := terminal(40, 8)
	sess = &session{items: items, opts: PickOptions{MaxRows: 10}, out: short, probe: sp,
		view: NewInlineView(short, sp)}
	_ = sess.refilter(t.Context())
	sess.draw()
	assert.Equal(t, 8-pickerRules-1, sess.visible, "a short one shows what it can, and still shows the prompt")
	assert.NotZero(t, short.FindRow("filter:"))
}

// TestPickerLiveQueryReplacesTheList covers the graph-aware mode: the candidates
// come from a lookup per keystroke rather than from filtering what the picker
// was opened with, so it can find things the caller could not enumerate.
func TestPickerLiveQueryReplacesTheList(t *testing.T) {
	t.Parallel()
	var asked []string
	s := &session{
		items: []string{"seed"},
		opts: PickOptions{Query: func(_ context.Context, f string) ([]string, error) {
			asked = append(asked, f)
			if f == "" {
				return []string{"seed"}, nil
			}
			return []string{"from-graph-" + f, "second"}, nil
		}},
	}

	s.filter = "hash"
	_ = s.refilter(t.Context())

	assert.Equal(t, []string{"from-graph-hash", "second"}, s.items,
		"the query's results become the list, not a subset of the old one")
	assert.Equal(t, []int{0, 1}, s.matches,
		"and every result is a match, since the query already did the filtering")
	assert.Equal(t, []string{"hash"}, asked, "asked once per filter change")
}

// TestPickerWithoutQueryStillFilters: a complete list must keep the substring
// behaviour, which is right when the caller already knows every candidate.
func TestPickerWithoutQueryStillFilters(t *testing.T) {
	t.Parallel()
	s := &session{items: []string{"apps/admin", "libs/authkit", "std"}}
	s.filter = "a"
	_ = s.refilter(t.Context())
	assert.Equal(t, []int{0, 1}, s.matches, "substring filter over the fixed list")
}

// TestSelectMarkIsOneGlyphEverywhere guards the thing that made the two
// surfaces feel like two products: the picker and the run's failure band are
// lists a reader drives the same way, and they were marking the current row
// with different characters.
func TestSelectMarkIsOneGlyphEverywhere(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1, len([]rune(SelectMark)),
		"one column, so a surface can pad it to its own row shape")
	assert.NotContains(t, SelectMark, ">",
		"a keyboard character standing in for a pointer is what this replaced")
}

// pickerChrome is what the box costs the picker in rows: a rule above and a
// rule below. The prompt moved onto the top rule and the way out onto the
// bottom, so the list itself keeps every line it had - the net cost is one row,
// and the surface now looks like the run's band because it IS the same box.
const pickerChrome = pickerRules

// TestPickerDrawsTheSameBoxAsTheBand is the integration this closes: two lists a
// reader drives identically must not look like two products. Position still
// differs on purpose - the picker draws where the cursor is - but the frame,
// the corners and the captions come from one place.
func TestPickerDrawsTheSameBoxAsTheBand(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	s := &session{
		items: []string{"console", "docs"}, out: &buf, probe: terminal(60, 24),
		view: NewInlineView(&buf, terminal(60, 24)),
		opts: PickOptions{Prompt: "project", MaxRows: 10},
	}
	_ = s.refilter(t.Context())
	s.draw()

	out := stripANSI(buf.String())
	assert.Contains(t, out, boxTL, "the same rounded corner the band draws")
	assert.Contains(t, out, boxBR)
	assert.Contains(t, out, "project", "the prompt rides the top rule")
	assert.Contains(t, out, "[esc] cancel", "and the way out rides the bottom")
}

// unboxRow is one screen row with the picker's vertical edges taken off, so an
// assertion about an item does not restate the frame around it.
func unboxRow(s *screen.Screen, row int) string {
	return strings.TrimRight(strings.Trim(s.Row(row), boxV), " ")
}
