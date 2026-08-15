package difftui

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
)

// testFiles is the shape that exercises every boundary at once: a multi-hunk file, a
// single-hunk file, and a generated file that is folded away by default. Index is spelled out
// because it is the coordinate talk is anchored by, and the caller supplies it from the patch.
func testFiles() []File {
	return []File{
		{Path: "a.go", Facts: []string{"PUBLIC SURFACE", "in root"}, Hunks: []Hunk{
			{Index: 0, Header: "@@ -1 +1 @@", Lines: []string{"-one", "+two"}, Digest: "da0"},
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
	assert.Equal(t, OverviewRow{Path: "a.go", Hunks: 2, Read: 0, Rendered: "a.go  2 hunks, 0 read"}, rows[0])
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

func TestEmphasisMarksOnlyThePairedRewrite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		lines []string
		// want is the text each hunk line's span selects, in line order, "" for no emphasis.
		want []string
	}{
		{"one changed argument is the only part emphasised",
			[]string{" ctx", "-call(a, b)", "+call(a, c)"},
			[]string{"", "b", "c"}},
		{"a wholly different line has nothing to point at",
			[]string{"-one", "+two"},
			[]string{"", ""}},
		{"runs of unequal length are not paired at all",
			[]string{"-one", "+two", "+three"},
			[]string{"", "", ""}},
		{"an insertion has no partner to differ from",
			[]string{" ctx", "+added"},
			[]string{"", ""}},
		{"a two-line rewrite pairs positionally",
			[]string{"-let a = 1", "-let b = 2", "+let a = 9", "+let b = 8"},
			[]string{"1", "2", "9", "8"}},
		{"a second run pairs on its own",
			[]string{"-call(a, b)", "+call(a, c)", " ctx", "-x := 1", "+x := 2"},
			[]string{"b", "c", "", "1", "2"}},
		{"spans are BYTES, so a multi-byte prefix does not shift what they select",
			[]string{`-x = "` + "αβγ" + ` one"`, `+x = "` + "αβγ" + ` two"`},
			[]string{"one", "two"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{
				{Header: "@@ -1 +1 @@", Lines: tc.lines, Digest: "d0"},
			}}}})
			assert.Equal(t, tc.want, emphasisOf(m))
		})
	}
}

func TestEmphasisSpansAreByteOffsetsIntoTheRawLine(t *testing.T) {
	t.Parallel()
	// The renderer slices Text with these numbers, so a span counted in RUNES would not merely
	// highlight the wrong word - it would cut a rune in half and hand the terminal invalid
	// UTF-8. Three two-byte runes sit ahead of the change, so byte and rune offsets differ.
	m := New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{
		{Header: "@@ -1 +1 @@", Lines: []string{`-x = "` + "αβγ" + ` one"`}, Digest: "d0"},
	}}}})
	require.Len(t, m.Rows(), 3)
	// One line has nobody to pair with, which is the run-length rule and not a byte question.
	assert.True(t, m.Rows()[2].Emph.Empty())

	m = New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{
		{Header: "@@ -1 +1 @@", Lines: []string{
			`-x = "` + "αβγ" + ` one"`,
			`+x = "` + "αβγ" + ` two"`,
		}, Digest: "d0"},
	}}}})
	del := m.Rows()[2]
	assert.Equal(t, 13, del.Emph.Start, "the marker plus five ASCII bytes plus six bytes of Greek")
	assert.Equal(t, 16, del.Emph.End)
	assert.Equal(t, "one", del.Text[del.Emph.Start:del.Emph.End])
}

func TestPlainFrameIsByteForByteTheUnstyledOne(t *testing.T) {
	t.Parallel()
	m := New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{
		{Header: "@@ -1 +1 @@", Lines: []string{" ctx", "-call(a, b)", "+call(a, c)"}, Digest: "d0"},
	}}}})
	m.Resize(5)

	// Spelled out rather than composed from the renderer's own parts: this is the output a
	// reader gets down a pipe, under NO_COLOR and on a dumb terminal, and the point of the
	// assertion is that adding a palette did not move a single byte of it.
	want := strings.Join([]string{
		"▸ a.go  1 hunk, 0 read",
		"  [ ] @@ -1 +1 @@",
		"   ctx",
		"  -call(a, b)",
		"  +call(a, c)",
		"]/[ hunk   }/{ file   v read   . generated   esc overview   q quit",
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
	m := New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{
		{Header: "@@ -1 +1 @@", Lines: []string{" ctx", "-call(a, b)", "+call(a, c)"}, Digest: "d0"},
	}}}})
	m.Resize(5)
	lines := strings.Split(Frame(m, true), "\n")
	require.Len(t, lines, 6)

	assert.Equal(t, "▸ \x1b[1ma.go  1 hunk, 0 read\x1b[0m", lines[0], "a file heading is bold")
	assert.Equal(t, "  \x1b[2m[ ] @@ -1 +1 @@\x1b[0m", lines[1], "a hunk heading is dim")
	assert.Equal(t, "   ctx", lines[2], "a context line is left alone")
	assert.Equal(t, "  \x1b[31m-call(a, \x1b[0m\x1b[1;31mb\x1b[0m\x1b[31m)\x1b[0m", lines[3])
	assert.Equal(t, "  \x1b[32m+call(a, \x1b[0m\x1b[1;32mc\x1b[0m\x1b[32m)\x1b[0m", lines[4])
	assert.Equal(t, "]/[ hunk   }/{ file   v read   . generated   esc overview   q quit", lines[5])

	// A whole-line change carries the row colour and nothing else: emphasising everything would
	// say no more than the colour already did.
	whole := New(Input{Files: []File{{Path: "a.go", Hunks: []Hunk{
		{Header: "@@ -1 +1 @@", Lines: []string{"-one", "+two"}, Digest: "d0"},
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

// sgrPattern matches the only escape difftui emits. A hyperlink would need more, and the
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
