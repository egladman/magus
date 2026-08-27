package difftui

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
)

// testFiles is the shape that exercises every boundary at once: a multi-hunk file, a
// single-hunk file, and a generated file that is folded away by default. Index is spelled out
// because it is the coordinate talk is anchored by, and the caller supplies it from the patch.
func testFiles() []File {
	return []File{
		{Path: "a.go", Facts: []string{"PUBLIC SURFACE", "in root"}, Hunks: []Hunk{
			{Index: 0, Header: "@@ -1 +1 @@", NewStart: 1, Lines: []string{"-one", "+two"}, Digest: "da0"},
			{Index: 1, Header: "@@ -9 +9 @@", Lines: []string{"-three", "+four"}, Digest: "da1"},
		}},
		{Path: "b.go", Hunks: []Hunk{
			{Index: 0, Header: "@@ -2 +2 @@", Lines: []string{"+five"}, Digest: "db0"},
		}},
		{Path: "gen/out.json", Generated: true, Hunks: []Hunk{
			{Index: 0, Header: "@@ -3 +3 @@", Lines: []string{"+six"}, Digest: "dg0"},
		}},
	}
}

// move applies one navigation key and reports whether the cursor moved.
func move(t *testing.T, m *Model, key rune) bool {
	t.Helper()
	switch key {
	case ']':
		return m.NextHunk()
	case '[':
		return m.PrevHunk()
	case '}':
		return m.NextFile()
	case '{':
		return m.PrevFile()
	}
	t.Fatalf("unknown move %q", key)
	return false
}

func at(path string, hunk int) types.DiffCursor { return types.DiffCursor{Path: path, Hunk: hunk} }

// TestCursorPublishesThePatchIndexNotTheRowPosition pins the coordinate the shared session is
// keyed by. Hunk.Index and the position in Hunks agree while the viewer holds every hunk of
// every file, so only a fixture where they differ can tell the two apart - and Index is the one
// the console and the MCP surface resolve talk by, the same one talkRows joins on.
func TestCursorPublishesThePatchIndexNotTheRowPosition(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{
		{Index: 4, Header: "@@ -1 +1 @@", NewStart: 1, Lines: []string{"+one"}, Digest: "d0"},
		{Index: 7, Header: "@@ -9 +9 @@", Lines: []string{"+two"}, Digest: "d1"},
	}}}})
	assert.Equal(t, at("a.go", -1), m.Cursor(), "a heading names no hunk")
	require.True(t, m.NextHunk())
	assert.Equal(t, at("a.go", 4), m.Cursor(), "the first row is patch hunk 4")
	require.True(t, m.NextHunk())
	assert.Equal(t, at("a.go", 7), m.Cursor(), "the second row is patch hunk 7, not hunk 1")
}

func TestCursorMotionCrossesFileAndHunkBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// keys is applied in order; wantMoved is the verdict of the LAST one, which is how a
		// refusal at either end is asserted rather than inferred from the cursor not changing.
		keys      string
		want      types.DiffCursor
		wantMoved bool
	}{
		{"opens on the first file's heading", "", at("a.go", -1), false},
		{"the first hunk is one step from the heading", "]", at("a.go", 0), true},
		{"walks within a file", "]]", at("a.go", 1), true},
		{"crosses into the next file", "]]]", at("b.go", 0), true},
		{"a folded generated file has no hunks to reach", "]]]]", at("b.go", 0), false},
		{"steps back across the boundary to the previous file's LAST hunk", "]]][", at("a.go", 1), true},
		{"refuses to step back past the first hunk", "][", at("a.go", 0), false},
		{"file steps land on the heading", "}", at("b.go", -1), true},
		{"file steps reach the generated file even folded", "}}", at("gen/out.json", -1), true},
		{"refuses to step past the last file", "}}}", at("gen/out.json", -1), false},
		{"refuses to step back past the first file", "{", at("a.go", -1), false},
		{"a hunk step from a heading enters that file", "}]", at("b.go", 0), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := New(Input{Files: testFiles()})
			moved := false
			for _, k := range tc.keys {
				moved = move(t, m, k)
			}
			assert.Equal(t, tc.want, m.Cursor())
			if tc.keys != "" {
				assert.Equal(t, tc.wantMoved, moved)
			}
		})
	}
}

func TestFoldRecomputesTheVisibleRows(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: testFiles()})

	assert.Equal(t, 1, countKind(m, RowFold), "a generated file folds to one line")
	assert.Zero(t, hunkRowsFor(m, 2), "a folded file shows no hunks")

	m.ToggleGenerated()
	assert.Zero(t, countKind(m, RowFold))
	assert.Equal(t, 1, hunkRowsFor(m, 2), "unfolding shows the generated file's hunks")

	// The hunks of the OTHER files are untouched by the fold, which is what says the toggle
	// is scoped to declared outputs rather than to everything below the cursor.
	assert.Equal(t, 2, hunkRowsFor(m, 0))
}

func TestFoldingRetreatsACursorItWouldStrand(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: testFiles(), Unfolded: true})
	require.True(t, m.NextFile())
	require.True(t, m.NextFile())
	require.True(t, m.NextHunk())
	require.Equal(t, at("gen/out.json", 0), m.Cursor())

	m.ToggleGenerated()
	assert.Equal(t, at("gen/out.json", -1), m.Cursor(), "the cursor retreats to the heading it can still see")
	assert.GreaterOrEqual(t, m.CursorRow(), 0, "and still points at a row that exists")
}

func TestViewedTogglesOnHunksOnly(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: testFiles()})

	_, ok := m.ToggleViewed()
	assert.False(t, ok, "a file heading has no hunk to mark")

	require.True(t, m.NextHunk())
	change, ok := m.ToggleViewed()
	require.True(t, ok)
	assert.Equal(t, ViewedChange{Digest: "da0", On: true}, change)
	assert.True(t, m.Viewed("da0"))
	assert.Contains(t, rowTextFor(m, RowHunk, 0), "[x]")
	assert.Contains(t, rowTextFor(m, RowFile, 0), "2 hunks, 1 read")

	change, ok = m.ToggleViewed()
	require.True(t, ok)
	assert.False(t, change.On, "the second press unmarks it")
	assert.False(t, m.Viewed("da0"))
	assert.Contains(t, rowTextFor(m, RowHunk, 0), "[ ]")
}

func TestViewedAdoptsThePersistedSet(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: testFiles(), Viewed: []string{"da1"}})
	assert.True(t, m.Viewed("da1"), "a mark made in another client is already read here")
	assert.Contains(t, rowTextFor(m, RowFile, 0), "2 hunks, 1 read")
}

func TestOverviewEntersAndReturns(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: testFiles()})
	require.True(t, m.NextHunk())

	m.ToggleOverview()
	require.True(t, m.Overview())
	assert.Equal(t, 0, m.OverviewCursor(), "it opens on the file being read")
	rows := m.OverviewRows()
	require.Len(t, rows, 3)
	assert.Equal(t, OverviewRow{Path: "a.go", HunkCount: 2, Read: 0, Rendered: "a.go  2 hunks, 0 read"}, rows[0])
	assert.True(t, rows[2].Generated)

	m.OverviewMove(-4)
	assert.Equal(t, 0, m.OverviewCursor(), "clamped at the top")
	m.OverviewMove(9)
	assert.Equal(t, 2, m.OverviewCursor(), "clamped at the bottom")

	m.OverviewEnter()
	assert.False(t, m.Overview())
	assert.Equal(t, at("gen/out.json", -1), m.Cursor(), "entering jumps to that file's heading")

	// Escaping back out leaves the cursor exactly where it was: the overview is a look, not
	// a move.
	m.ToggleOverview()
	m.OverviewMove(-2)
	m.ToggleOverview()
	assert.Equal(t, at("gen/out.json", -1), m.Cursor())
}

func TestScrollClampsAtBothEnds(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: testFiles()})
	m.Resize(4)
	maxTop := len(m.Rows()) - 4
	require.Positive(t, maxTop, "the fixture must be taller than the viewport for this to mean anything")

	m.Scroll(-10)
	assert.Equal(t, 0, m.Top())
	m.Scroll(1000)
	assert.Equal(t, maxTop, m.Top(), "scrolling past the end stops at the last row")
	m.Page(1)
	assert.Equal(t, maxTop, m.Top())
	m.Page(-1)
	assert.Equal(t, maxTop-4, m.Top())
	m.Page(-100)
	assert.Equal(t, 0, m.Top())
}

func TestShortViewportKeepsTheCursorInView(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: testFiles()})
	m.Resize(3)
	for range 4 {
		m.NextHunk()
		require.GreaterOrEqual(t, m.CursorRow(), m.Top())
		require.Less(t, m.CursorRow(), m.Top()+m.Height())
	}
	// And back, which is the direction a follow-forward-only implementation gets wrong.
	for range 4 {
		m.PrevHunk()
		require.GreaterOrEqual(t, m.CursorRow(), m.Top())
		require.Less(t, m.CursorRow(), m.Top()+m.Height())
	}
}

func TestCommentsAndPendingSuggestionsSitUnderTheirHunk(t *testing.T) {
	t.Parallel()
	m := New(Input{
		Files: testFiles(),
		Comments: []types.DiffComment{
			{ID: "c1", Path: "a.go", Hunk: 1, Author: types.DiffAuthorAgent, Body: "two\nlines"},
		},
		Suggestions: []types.DiffSuggestion{
			{ID: "s1", Path: "a.go", Hunk: 1, Reason: "look here"},
			{ID: "s2", Path: "b.go", Hunk: 0, Reason: "already answered", Declined: true},
		},
	})

	rows := m.Rows()
	var talk []Row
	for i, r := range rows {
		if r.Kind != RowComment && r.Kind != RowSuggestion {
			continue
		}
		talk = append(talk, r)
		assert.Equal(t, 1, r.Hunk, "row %d is anchored to the hunk it was written on", i)
	}
	require.Len(t, talk, 3, "two comment lines and one pending suggestion")
	assert.Contains(t, talk[0].Text, "agent: two")
	assert.Equal(t, "  | lines", talk[1].Text)
	assert.Contains(t, talk[2].Text, "SUGGESTION: look here")

	for _, r := range rows {
		assert.NotContains(t, r.Text, "already answered", "an answered suggestion is history, not an affordance")
	}
}

func TestLinkDecoratesOnlyTheFileHeading(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: testFiles(), Link: func(p string) string { return "<" + p + ">" }})
	assert.Contains(t, rowTextFor(m, RowFile, 0), "<a.go>")
	assert.Contains(t, m.OverviewRows()[0].Rendered, "<a.go>")
	assert.Equal(t, "a.go", m.OverviewRows()[0].Path, "the decoration is in Rendered and nowhere else")
	assert.NotContains(t, rowTextFor(m, RowHunk, 0), "<")
}

func TestFrameIsExactlyAsTallAsItClaims(t *testing.T) {
	t.Parallel()
	// InlineView redraws by erasing exactly the rows it drew last time, so a frame that does
	// not fill its declared height walks the cursor over the transcript above it. Styling must
	// not change that either: an escape is not a row, and a palette that wrapped one badly
	// would show up here first.
	for _, unranked := range []bool{false, true} {
		for _, color := range []bool{false, true} {
			m := New(Input{Files: testFiles(), Unranked: unranked})
			m.Resize(6)
			assert.Len(t, strings.Split(Frame(m, color), "\n"), Chrome(m)+m.Height())

			m.ToggleOverview()
			assert.Len(t, strings.Split(Frame(m, color), "\n"), Chrome(m)+m.Height())
		}
	}
}

func TestFrameMarksExactlyOneRow(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: testFiles()})
	m.Resize(8)
	require.True(t, m.NextHunk())
	marked := 0
	for _, line := range strings.Split(Frame(m, false), "\n") {
		if strings.HasPrefix(line, gutter(true)) {
			marked++
		}
	}
	assert.Equal(t, 1, marked)
}

func TestEmptyChangesetDrawsNothingAndRefusesEveryMove(t *testing.T) {
	t.Parallel()
	m := New(Input{})
	assert.Empty(t, m.Rows())
	assert.Equal(t, -1, m.CursorRow())
	assert.Equal(t, types.DiffCursor{Hunk: -1}, m.Cursor())
	assert.False(t, m.NextHunk())
	assert.False(t, m.PrevHunk())
	assert.False(t, m.NextFile())
	assert.False(t, m.PrevFile())
	_, ok := m.ToggleViewed()
	assert.False(t, ok)
	m.ToggleOverview()
	m.OverviewEnter()
	assert.False(t, m.Overview())
}

// The viewer no longer computes emphasis - the parser does, and hands it over in Hunk.Emph.
// What is left to check here is that a span it was GIVEN survives into the row it belongs to,
// including the "nothing to mark" case, since the slice may be short or absent entirely.
//
// The rule those numbers follow (which lines pair, and how a multi-byte prefix shifts the
// offsets) is pinned where it now lives, in internal/diff's TestEmphasisMarksOnlyThePairedRewrite.
func TestAGivenEmphasisSpanReachesItsRow(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{{
		Header: "@@ -1 +1 @@", NewStart: 1,
		Lines:  []string{" ctx", "-call(a, b)", "+call(a, c)"},
		Emph:   []changeset.Span{{}, {Start: 9, End: 10}, {Start: 9, End: 10}},
		Digest: "d0",
	}}}}})
	assert.Equal(t, []string{"", "b", "c"}, emphasisOf(m))
}

// A caller that hands over no spans at all renders every line plain rather than panicking on
// an index. That is the honest degradation, and it is what a hunk built by hand gets.
func TestNoEmphasisSliceIsNotAnIndexError(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{
		{Header: "@@ -1 +1 @@", NewStart: 1, Lines: []string{"-one", "+two"}, Digest: "d0"},
	}}}})
	assert.Equal(t, []string{"", ""}, emphasisOf(m))
}

func TestPlainFrameIsByteForByteTheUnstyledOne(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{
		{Header: "@@ -1 +1 @@", NewStart: 1, Lines: []string{" ctx", "-call(a, b)", "+call(a, c)"}, Digest: "d0"},
	}}}})
	m.Resize(5)

	// Spelled out rather than composed from the renderer's own parts: this is the output a
	// reader gets down a pipe, under NO_COLOR and on a dumb terminal, and the point of the
	// assertion is that no palette may move a single byte of it.
	want := strings.Join([]string{
		"▸ a.go  1 hunk, 0 read",
		"  [ ] line 1",
		"   ctx",
		"  -call(a, b)",
		"  +call(a, c)",
		"]/[ hunk   }/{ file   v read   n read-already   . generated   esc overview   q quit",
	}, "\n")
	assert.Equal(t, want, Frame(m, false))

	// And the property behind the golden, over the fixture that has every row kind in it:
	// colour is purely additive, so stripping it lands back on exactly the plain frame.
	big := New(Input{Files: testFiles(), Unranked: true})
	big.Resize(9)
	for range 2 {
		assert.NotContains(t, Frame(big, false), "\x1b", "the plain frame carries no escape at all")
		assert.Equal(t, Frame(big, false), stripSGR(Frame(big, true)))
		big.ToggleOverview()
	}
}

func TestNoColorSilencesTheWholeFrame(t *testing.T) {
	// Not parallel: it sets the environment the colour gate reads.
	out := termWriter{io.Discard}
	probe := tty.FixedProbe(80, 24)
	m := New(Input{Files: testFiles()})
	m.Resize(9)

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	require.False(t, tty.WantsColor(out, probe), "NO_COLOR is the gate the viewer asks")
	assert.NotContains(t, Frame(m, tty.WantsColor(out, probe)), "\x1b")

	t.Setenv("NO_COLOR", "")
	require.True(t, tty.WantsColor(out, probe))
	assert.Contains(t, Frame(m, tty.WantsColor(out, probe)), "\x1b[",
		"and with it unset the same frame is styled, so the test above is not vacuous")
}

func TestColourDrawsTheChangedPartHarderThanItsLine(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{{
		Header: "@@ -1 +1 @@", NewStart: 1,
		Lines:  []string{" ctx", "-call(a, b)", "+call(a, c)"},
		Emph:   []changeset.Span{{}, {Start: 9, End: 10}, {Start: 9, End: 10}},
		Digest: "d0",
	}}}}})
	m.Resize(5)
	lines := strings.Split(Frame(m, true), "\n")
	require.Len(t, lines, 6)

	assert.Equal(t, "▸ \x1b[1ma.go  1 hunk, 0 read\x1b[0m", lines[0], "a file heading is bold")
	assert.Equal(t, "  \x1b[2m[ ] line 1\x1b[0m", lines[1], "a hunk heading is dim")
	assert.Equal(t, "   ctx", lines[2], "a context line is left alone")
	assert.Equal(t, "  \x1b[31m-call(a, \x1b[0m\x1b[1;31mb\x1b[0m\x1b[31m)\x1b[0m", lines[3])
	assert.Equal(t, "  \x1b[32m+call(a, \x1b[0m\x1b[1;32mc\x1b[0m\x1b[32m)\x1b[0m", lines[4])
	assert.Equal(t, "]/[ hunk   }/{ file   v read   n read-already   . generated   esc overview   q quit", lines[5])

	// A whole-line change carries the row colour and nothing else: emphasising everything would
	// say no more than the colour already did.
	whole := New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{
		{Header: "@@ -1 +1 @@", NewStart: 1, Lines: []string{"-one", "+two"}, Digest: "d0"},
	}}}})
	whole.Resize(4)
	styled := strings.Split(Frame(whole, true), "\n")
	assert.Equal(t, "  \x1b[31m-one\x1b[0m", styled[2])
	assert.Equal(t, "  \x1b[32m+two\x1b[0m", styled[3])
}

func TestWheelScrollsTheViewportAndNothingElseDoes(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: testFiles()})
	m.Resize(4)
	where := m.Cursor()

	assert.False(t, apply(m, wheel(tty.MouseWheelDown), nil))
	assert.Equal(t, wheelRows, m.Top(), "a notch down moves the viewport by three rows")
	apply(m, wheel(tty.MouseWheelDown), nil)
	assert.Equal(t, 2*wheelRows, m.Top())
	assert.Equal(t, where, m.Cursor(), "scrolling is looking around, so the cursor stays put")

	apply(m, wheel(tty.MouseWheelUp), nil)
	assert.Equal(t, wheelRows, m.Top())
	apply(m, wheel(tty.MouseWheelUp), nil)
	apply(m, wheel(tty.MouseWheelUp), nil)
	assert.Equal(t, 0, m.Top(), "and it clamps at the top like every other scroll")

	// Every other mouse event is still dropped rather than guessed at.
	apply(m, wheel(tty.MouseWheelDown), nil)
	for _, ev := range []tty.Event{
		{Kind: tty.EventMouse, Button: tty.MouseLeft, Press: true, Row: 2},
		{Kind: tty.EventMouse, Button: tty.MouseRight, Press: true, Row: 2},
		{Kind: tty.EventMouse, Motion: true, Row: 2},
	} {
		assert.False(t, apply(m, ev, nil))
		assert.Equal(t, wheelRows, m.Top(), "a click is not a scroll")
	}
}

// wheel is one notch of the wheel, as the terminal reports it: a press with no release.
func wheel(b tty.MouseButton) tty.Event {
	return tty.Event{Kind: tty.EventMouse, Button: b, Press: true, Clicks: 1}
}

// termWriter looks like a terminal to tty.Fd so the colour gate can be asked without a pty.
type termWriter struct{ io.Writer }

func (termWriter) Fd() uintptr { return 1 }

// sgrPattern matches the only escape this package emits. A hyperlink would need more, and the
// fixtures here deliberately carry no Link for that reason.
var sgrPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripSGR(s string) string { return sgrPattern.ReplaceAllString(s, "") }

// emphasisOf is the text each hunk line's span selects, in line order, "" where there is none.
// Compared as text rather than as offsets because text is what the renderer draws.
func emphasisOf(m *Model) []string {
	var out []string
	for _, r := range m.Rows() {
		if r.Kind != RowLine {
			continue
		}
		if r.Emph.Empty() {
			out = append(out, "")
			continue
		}
		out = append(out, r.Text[r.Emph.Start:r.Emph.End])
	}
	return out
}

// countKind is how many rows of one kind are visible.
func countKind(m *Model, kind RowKind) int {
	n := 0
	for _, r := range m.Rows() {
		if r.Kind == kind {
			n++
		}
	}
	return n
}

// hunkRowsFor is how many hunk headings file i is showing.
func hunkRowsFor(m *Model, file int) int {
	n := 0
	for _, r := range m.Rows() {
		if r.Kind == RowHunk && r.File == file {
			n++
		}
	}
	return n
}

// rowTextFor returns the first row of a kind belonging to file i.
func rowTextFor(m *Model, kind RowKind, file int) string {
	for _, r := range m.Rows() {
		if r.Kind == kind && r.File == file {
			return r.Text
		}
	}
	return ""
}

// A colleague's remark reaches the TERMINAL, not only the browser. The reader chooses where to
// read and magus does not care which - read receipts already work both ways, and a review that
// showed the conversation in one surface and not the other would send half of them to a browser
// to find out what was asked.
func TestTheHostsThreadsRenderBesideTheCodeTheyAreAbout(t *testing.T) {
	t.Parallel()
	m := New(Input{
		Files: []File{{Path: "a.go", Hunks: []Hunk{
			{Index: 0, Header: "@@ -1 +1 @@", NewStart: 1, Lines: []string{"-old", "+new"}, Digest: "d0"},
		}}},
		Comments: []types.DiffComment{{Path: "a.go", Hunk: 0, Author: types.DiffAuthorHuman, Body: "mine"}},
		Threads:  []types.ReviewThread{{ID: "t1", Path: "a.go", Hunk: 0, Author: "priya", Body: "theirs"}},
	})

	text := everyRowText(m)
	// What a colleague already said precedes the remark it provoked, the same order the console
	// renders in.
	assert.Less(t, indexOfSubstring(text, "theirs"), indexOfSubstring(text, "mine"))
	// And it says the world has seen it, which is what separates it from a draft of your own.
	assert.Contains(t, text[indexOfSubstring(text, "theirs")], "on the review")
}

// A thread whose line this changeset no longer contains still belongs to its file. Dropping it
// would have the viewer telling the reader a colleague said nothing.
func TestAnUnplacedThreadRendersUnderItsFileRatherThanVanishing(t *testing.T) {
	t.Parallel()
	m := New(Input{
		Files:   []File{{Path: "a.go", Hunks: []Hunk{{Index: 0, Header: "@@ -1 +1 @@", NewStart: 1, Lines: []string{"-x", "+y"}, Digest: "d0"}}}},
		Threads: []types.ReviewThread{{ID: "t1", Path: "a.go", Hunk: -1, Author: "marcus", Body: "moved away"}},
	})
	assert.NotEqual(t, -1, indexOfSubstring(everyRowText(m), "moved away"))
}

// A pull request covers commits a working diff does not, so a colleague's remark can land on a
// file this changeset never touches. The console lists those; the viewer used to read m.unplaced
// only INSIDE its per-file loop, so a thread on a path it was not drawing reached no row at all
// and was discarded in silence - the one thing a review surface must never do.
func TestAThreadOutsideTheChangesetIsListedRatherThanDropped(t *testing.T) {
	t.Parallel()
	m := New(Input{
		Files: []File{{Path: "a.go", Hunks: []Hunk{{Index: 0, Header: "@@ -1 +1 @@", NewStart: 1, Lines: []string{"-x", "+y"}, Digest: "d0"}}}},
		Threads: []types.ReviewThread{
			{ID: "t1", Path: "elsewhere.go", Line: 12, Hunk: -1, Author: "priya", Body: "on another file"},
		},
	})

	text := everyRowText(m)
	assert.NotEqual(t, -1, indexOfSubstring(text, "on another file"))
	assert.NotEqual(t, -1, indexOfSubstring(text, "said on the review, elsewhere"))
	// Named, so the reader knows where to go and is not left with a remark about nothing.
	assert.NotEqual(t, -1, indexOfSubstring(text, "elsewhere.go:12"))
}

// A folded file draws one stand-in row instead of its hunks, so a remark anchored inside it has
// nowhere to sit either - and it was dropped for the same reason, one loop deeper.
func TestAThreadOnAFoldedFileIsListedRatherThanDropped(t *testing.T) {
	t.Parallel()
	m := New(Input{
		Files: []File{{
			Path:      "gen/api.go",
			Generated: true,
			Hunks:     []Hunk{{Index: 0, Header: "@@ -1 +1 @@", NewStart: 1, Lines: []string{"-x", "+y"}, Digest: "d0"}},
		}},
		Threads: []types.ReviewThread{
			{ID: "t1", Path: "gen/api.go", Line: 3, Hunk: 0, Author: "marcus", Body: "regenerate this"},
		},
	})

	assert.NotEqual(t, -1, indexOfSubstring(everyRowText(m), "regenerate this"))
}

// everyRowText is the visible text of every row, for asserting on order and presence. Named
// away from render.go's rowText, which renders ONE row and is the package's real one.
func everyRowText(m *Model) []string {
	out := make([]string, 0, len(m.Rows()))
	for _, r := range m.Rows() {
		out = append(out, r.Text)
	}
	return out
}

func indexOfSubstring(rows []string, want string) int {
	for i, r := range rows {
		if strings.Contains(r, want) {
			return i
		}
	}
	return -1
}

// TestSettledFilesFoldByDefault is what makes a second pass cost only the second pass: a reviewer
// who asked for changes comes back to a changeset that is mostly what they already read, and
// nothing distinguished that from what moved.
func TestSettledFilesFoldByDefault(t *testing.T) {
	m := New(Input{Files: []File{
		{Path: "read.go", Settled: true, Hunks: []Hunk{{Digest: "h1", Lines: []string{" ALREADY-WEIGHED"}}}},
		{Path: "moved.go", Hunks: []Hunk{{Digest: "h2", Lines: []string{" NEEDS-A-SECOND-LOOK"}}}},
	}})

	body := allRowText(m)
	assert.NotContains(t, body, "ALREADY-WEIGHED", "a file already read at this content is not what the reader is here for")
	assert.Contains(t, body, "NEEDS-A-SECOND-LOOK", "a file that moved is exactly what they came back for")
	assert.Contains(t, body, "you read this already and it has not changed since",
		"a folded file must say why, and how to see it")

	// n reveals them, and says nothing is hidden any more.
	m.ToggleSettled()
	assert.Contains(t, allRowText(m), "ALREADY-WEIGHED")
	assert.True(t, m.Unsettled())
}

// The control that keeps the fold honest. DiffReadStale - read, then EDITED - is the file that
// most needs a second look, and folding it would hide the change from the one person who would
// otherwise have caught it.
func TestAStaleFileIsNeverFolded(t *testing.T) {
	// Settled is set only from DiffReadRead; a stale or unread file arrives with it false.
	m := New(Input{Files: []File{
		{Path: "stale.go", Settled: false, Hunks: []Hunk{{Digest: "h1", Lines: []string{" changed since you read it"}}}},
	}})

	assert.Contains(t, allRowText(m), "changed since you read it")
}

// Generated wins where both apply: a generated file is not worth reading whether or not this
// reader got to it, so its reason is the more useful of the two.
func TestAGeneratedAndSettledFileNamesTheGeneratedReason(t *testing.T) {
	m := New(Input{Files: []File{
		{Path: "gen.go", Settled: true, Generated: true, Hunks: []Hunk{{Digest: "h1", Lines: []string{" x"}}}},
	}})

	assert.Contains(t, allRowText(m), "a target rewrites this")
}

// allRowText joins every visible row, for assertions about what a reader can and cannot see.
func allRowText(m *Model) string {
	var b strings.Builder
	for _, r := range m.Rows() {
		b.WriteString(r.Text)
		b.WriteString("\n")
	}
	return b.String()
}
