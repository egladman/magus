package screen

import (
	"fmt"
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
