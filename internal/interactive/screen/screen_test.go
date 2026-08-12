package screen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The emulator is the witness every terminal test leans on, so its own
// semantics need checking directly. These cover the sequences magus emits and
// the two behaviours the rest of the system is designed around: a scroll region
// that leaves rows below it alone, and a cursor that can be put back exactly
// where it was.

func TestScrollRegionLeavesPinnedRowsAlone(t *testing.T) {
	t.Parallel()
	s := New(20, 10)
	fmt.Fprint(s, "\x1b[1;7r") // scroll rows 1-7; 8-10 are pinned
	fmt.Fprint(s, "\x1b[8;1H") // paint the pinned zone
	fmt.Fprint(s, "pinned row")
	fmt.Fprint(s, "\x1b[1;1H")

	for i := range 20 {
		fmt.Fprintf(s, "line %d\n", i)
	}

	assert.Equal(t, "pinned row", s.Row(8), "output scrolling above must not disturb a pinned row")
	assert.Positive(t, s.Scrolled())
	top, bottom := s.ScrollRegion()
	assert.Equal(t, 1, top)
	assert.Equal(t, 7, bottom)
}

func TestIndexMovesDownWithoutReturningTheCarriage(t *testing.T) {
	t.Parallel()
	// The distinction that cost a real bug: a newline on a cooked terminal also
	// returns the carriage, and IND does not.
	s := New(20, 10)
	fmt.Fprint(s, "abc\x1bD")
	row, col := s.Cursor()
	assert.Equal(t, 2, row)
	assert.Equal(t, 4, col, "IND keeps the column")

	s2 := New(20, 10)
	fmt.Fprint(s2, "abc\n")
	row, col = s2.Cursor()
	assert.Equal(t, 2, row)
	assert.Equal(t, 1, col, "a newline returns the carriage")
}

func TestCursorMovesAndSaveRestore(t *testing.T) {
	t.Parallel()
	s := New(20, 10)
	fmt.Fprint(s, "\x1b[5;3H")
	fmt.Fprint(s, "\x1b7")   // save
	fmt.Fprint(s, "\x1b[2A") // up 2
	row, _ := s.Cursor()
	require.Equal(t, 3, row)
	fmt.Fprint(s, "\x1b[4B") // down 4
	row, _ = s.Cursor()
	require.Equal(t, 7, row)
	fmt.Fprint(s, "\x1b8") // restore
	row, col := s.Cursor()
	assert.Equal(t, 5, row)
	assert.Equal(t, 3, col)
}

func TestEraseLineAndScreen(t *testing.T) {
	t.Parallel()
	s := New(20, 4)
	fmt.Fprint(s, "keep\nwipe me\nalso wiped\n")
	fmt.Fprint(s, "\x1b[2;1H\x1b[2K") // erase line 2 whole
	assert.Equal(t, "keep", s.Row(1))
	assert.Equal(t, "", s.Row(2))
	assert.Equal(t, "also wiped", s.Row(3))

	fmt.Fprint(s, "\x1b[1;1H\x1b[J") // erase to end of screen
	assert.Equal(t, "", s.Row(1))
	assert.Equal(t, "", s.Row(3))
}

func TestStyleIsRecordedPerCell(t *testing.T) {
	t.Parallel()
	s := New(20, 4)
	fmt.Fprint(s, "\x1b[33mwarn\x1b[0mplain")
	assert.Equal(t, "33", s.StyleAt(1, 1))
	assert.Equal(t, "", s.StyleAt(1, 5), "the reset closes the run")
}

func TestUnknownSequencesAreDroppedWhole(t *testing.T) {
	t.Parallel()
	// The rule that makes this a model of magus rather than of terminals in
	// general - and the one that hid a missing CUD until it corrupted a screen.
	s := New(20, 4)
	fmt.Fprint(s, "\x1b[?25lvisible")
	assert.Equal(t, "visible", s.Row(1), "the sequence vanished; its payload did not")
}

func TestFindRowLocatesText(t *testing.T) {
	t.Parallel()
	s := New(20, 4)
	fmt.Fprint(s, "alpha\nbravo\ncharlie\n")
	assert.Equal(t, 2, s.FindRow("bravo"))
	assert.Zero(t, s.FindRow("absent"))
}

func TestHyperlinksDoNotLandInTheGrid(t *testing.T) {
	t.Parallel()
	// OSC 8 is in the emitted vocabulary, so the model has to consume it. It
	// used to fall through to the two-byte default and type the URI into the
	// cells as ordinary text.
	s := New(40, 4)
	fmt.Fprint(s, "\x1b]8;;file:///tmp/a.log\x1b\\out-42\x1b]8;;\x1b\\ done")
	assert.Equal(t, "out-42 done", s.Row(1))
}

func TestInvalidBytesAdvanceByOne(t *testing.T) {
	t.Parallel()
	// A hand-rolled decoder reported RuneError as three bytes wide, so one bad
	// byte made the model swallow the two valid ones after it.
	s := New(40, 4)
	fmt.Fprint(s, "a\xffbc")
	assert.Equal(t, 4, len([]rune(s.Row(1))), "every byte accounted for: a, replacement, b, c")
}

// TestSnapshotKeepsStyles is the capability a recording needs, and the one its
// absence quietly broke: rendering String() into a fresh screen loses every
// colour, because String is plain text.
func TestSnapshotKeepsStyles(t *testing.T) {
	t.Parallel()
	s := New(20, 6)
	fmt.Fprint(s, "\x1b[32mgreen\x1b[0m\n\x1b[1;31mbold red\x1b[0m\n")

	shot := s.Snapshot()
	assert.Equal(t, "32", shot.StyleAt(1, 1), "colour survives the copy")
	assert.Equal(t, "1;31", shot.StyleAt(2, 1))
	assert.Equal(t, "green", shot.Row(1))

	// And it is INDEPENDENT: the live screen keeps being written to, and a
	// sequence of pointers to one screen would all show its final state.
	fmt.Fprint(s, "\x1b[33mlater\x1b[0m\n")
	assert.Equal(t, "", shot.Row(3), "the snapshot does not follow the screen forward")
	assert.Equal(t, "later", s.Row(3))
}

func TestSnapshotCarriesTheWholeTerminal(t *testing.T) {
	t.Parallel()
	s := New(20, 10)
	fmt.Fprint(s, "\x1b[1;7r\x1b[4;2H")
	shot := s.Snapshot()
	row, col := shot.Cursor()
	assert.Equal(t, 4, row)
	assert.Equal(t, 2, col)
	top, bottom := shot.ScrollRegion()
	assert.Equal(t, 1, top)
	assert.Equal(t, 7, bottom, "a snapshot is a terminal, not a picture of one")
}

// TestWriteExpandsTabs pins the emulator against a real terminal rather than
// against itself.
//
// This shipped: `go test` prints "ok  \tacme/admin\t0.531s", the tab was put in
// ONE cell, and the rendered SVG in the README carried a literal tab that no
// renderer expands - so the columns after it sat up to seven cells left of where
// the reader's terminal actually put them. The drift gate structurally cannot
// catch this class, because it compares the renderer to the renderer.
func TestWriteExpandsTabs(t *testing.T) {
	s := New(40, 3)
	fmt.Fprint(s, "ok\tacme/admin\t0.5s")
	// Tab stops every 8 columns: "ok" ends at column 3, the tab moves to 9, and
	// "acme/admin" (10 wide) ends at 19, so the second tab moves to 25.
	if got, want := s.Row(1), "ok      acme/admin      0.5s"; strings.TrimRight(got, " ") != want {
		t.Errorf("Row(1) = %q, want %q", strings.TrimRight(got, " "), want)
	}
}

// TestWriteTabAtEndOfLineStaysOnScreen: a tab near the right margin advances to
// the margin and stops. HT never wraps, so it must not push the cursor into the
// pending-wrap column that put uses (see put: col may reach width+1, and the
// NEXT character is what wraps).
func TestWriteTabAtEndOfLineStaysOnScreen(t *testing.T) {
	s := New(10, 2)
	fmt.Fprint(s, "abcdefghi\t")
	row, col := s.Cursor()
	if row != 1 || col != 10 {
		t.Errorf("cursor = row %d col %d, want row 1 col 10 (the right margin)", row, col)
	}
}

// TestCropClampsEveryRowCoordinate guards the returned Screen against being
// written to, which it exposes as an io.Writer.
func TestCropClampsEveryRowCoordinate(t *testing.T) {
	s := New(20, 20)
	// Park the cursor and the saved cursor low, and pin a scroll region that
	// lives entirely below the crop.
	fmt.Fprint(s, "\x1b[15;1H\x1b7\x1b[12;18r")
	c := s.Crop(5)
	if _, err := c.Write([]byte("x\ny\n\x1b8z")); err != nil {
		t.Fatalf("write to a cropped screen: %v", err)
	}
	row, col := c.Cursor()
	if row < 1 || row > 5 || col < 1 || col > 20 {
		t.Errorf("cursor outside the cropped screen: row=%d col=%d", row, col)
	}
	top, bot := c.ScrollRegion()
	if top > bot || bot > 5 {
		t.Errorf("scroll region outside the cropped screen: top=%d bot=%d", top, bot)
	}
}
