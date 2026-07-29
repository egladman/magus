package tty

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ttyBuf wraps bytes.Buffer with a synthetic non-zero descriptor so a
// Region treats it as a real terminal. Production writers are *os.File;
// this exists so enabled-region tests need no pty.
type ttyBuf struct {
	bytes.Buffer
}

func (*ttyBuf) Fd() uintptr { return 2 }

// terminal returns a Probe describing a terminal of the given size.
func terminal(width, height int) Probe {
	return fakeProbe{isTTY: true, width: width, height: height}
}

// notATerminal returns a Probe that reports every descriptor as a pipe.
func notATerminal() Probe { return fakeProbe{} }

func TestRegionDisabledWhenHeightIsZero(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 0, terminal(80, 24))
	assert.False(t, r.Enabled())
}

func TestRegionDisabledWhenNotTerminal(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, notATerminal())
	assert.False(t, r.Enabled(), "non-TTY writer must yield a disabled Region")
}

func TestRegionDisabledWhenWriterHasNoDescriptor(t *testing.T) {
	t.Parallel()
	// A bytes.Buffer has no Fd(), so even a probe that calls everything a
	// terminal must not enable the region.
	var buf bytes.Buffer
	r := NewRegion(&buf, 5, terminal(80, 24))
	assert.False(t, r.Enabled(), "a writer without a descriptor is never a terminal")
}

func TestRegionEnabledForLargeTerminal(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(80, 24))
	assert.True(t, r.Enabled(), "80x24 terminal with a 5-row region must enable")
}

func TestRegionDisabledWhenTerminalTooShort(t *testing.T) {
	t.Parallel()
	// minUsefulHeight is 8; 5 rows of region would leave only 2 to scroll.
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(80, 7))
	assert.False(t, r.Enabled(), "terminal too short for a useful scrolling region")
}

func TestRegionDisabledWhenTerminalTooNarrow(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(10, 24))
	assert.False(t, r.Enabled(), "terminal narrower than minUsefulWidth must disable")
}

func TestRegionDisabledWhenSizeQueryFails(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	p := fakeProbe{isTTY: true, width: 80, height: 24, sizeErr: errors.New("ioctl failed")}
	r := NewRegion(&buf, 5, p)
	assert.False(t, r.Enabled(), "a failed size query must disable the region")

	require.NoError(t, r.WriteLine("still visible"))
	assert.Contains(t, buf.String(), "still visible", "writes must fall back to plain text")
}

func TestRegionReserveOnDisabledIsNoOp(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, notATerminal())
	require.NoError(t, r.Reserve(), "Reserve on disabled must not error")
	assert.Empty(t, buf.String(), "Reserve on disabled must not write anything")
}

func TestRegionWriteLineOnDisabledIsPlainText(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, notATerminal())
	require.NoError(t, r.WriteLine("first failure"))
	require.NoError(t, r.WriteLine("second failure"))
	got := buf.String()
	assert.Equal(t, "first failure\nsecond failure\n", got,
		"a disabled Region emits plain newline-terminated lines and no escapes")
}

func TestRegionReserveEmitsScrollMargins(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(80, 24))
	require.True(t, r.Enabled())
	require.NoError(t, r.Reserve())
	got := buf.String()
	// firstRow = 24 - 5 + 1 = 20, so scrolling covers rows [1,19] and the
	// region occupies rows [20,24].
	assert.Contains(t, got, "\x1b[1;19r", "margins must confine scrolling to rows 1-19")
	assert.Contains(t, got, cursorSave, "Reserve must save the cursor")
	assert.Contains(t, got, "\x1b[J", "Reserve must clear the reserved region")
	assert.Less(t, strings.Index(got, cursorSave), strings.Index(got, "\x1b[1;19r"),
		"the cursor must be saved BEFORE the margins are set, since setting them homes it")
}

func TestRegionReserveIsIdempotent(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(80, 24))
	require.NoError(t, r.Reserve())
	first := buf.Len()
	require.NoError(t, r.Reserve())
	assert.Equal(t, first, buf.Len(), "Reserve must not re-emit on second call")
}

func TestRegionWriteLineAutoReserves(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(80, 24))
	require.NoError(t, r.WriteLine("first failure"))
	assert.Contains(t, buf.String(), "\x1b[1;19r", "the first WriteLine must auto-reserve")
	assert.Contains(t, buf.String(), "first failure", "and emit the message")
}

func TestRegionWriteLineEmitsBoldRed(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(80, 24))
	require.NoError(t, r.WriteLine("boom"))
	got := buf.String()
	assert.Contains(t, got, sgrBoldRed, "a region line must be bold red")
	assert.Contains(t, got, "boom")
	assert.Contains(t, got, sgrReset, "the SGR must be closed so the next line does not inherit it")
	assert.Contains(t, got, el, "the row must be cleared before writing")
}

// TestRegionWriteLineAddressesEachRow is the regression test for lines
// piling onto one row. Every line must be preceded by a cursor-position
// escape naming its own row, or the terminal simply continues where the
// previous line left the cursor and the region renders as one long
// concatenated line.
func TestRegionWriteLineAddressesEachRow(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(80, 24))
	require.NoError(t, r.Reserve())
	buf.Reset() // assert against the writes only

	for _, msg := range []string{"AAA", "BBB", "CCC"} {
		require.NoError(t, r.WriteLine(msg))
	}
	got := buf.String()

	// Rows 20, 21, 22 - one per line, in order.
	for i, msg := range []string{"AAA", "BBB", "CCC"} {
		row := 20 + i
		want := fmt.Sprintf(cupFmt, row, 1)
		assert.Contains(t, got, want, "line %q must be addressed to row %d", msg, row)
		assert.Less(t, strings.Index(got, want), strings.Index(got, msg),
			"the cursor move must precede the text for %q", msg)
	}
}

// TestRegionWriteLineUsesEveryRowBeforeWrapping pins the wrap boundary. A
// 5-row region must write 5 distinct rows before reusing the first; an
// off-by-one here silently costs a row of capacity.
func TestRegionWriteLineUsesEveryRowBeforeWrapping(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(80, 24))
	require.NoError(t, r.Reserve())
	buf.Reset()

	for range 5 {
		require.NoError(t, r.WriteLine("line"))
	}
	got := buf.String()
	for row := 20; row <= 24; row++ {
		assert.Contains(t, got, fmt.Sprintf(cupFmt, row, 1),
			"row %d must be used before the region wraps", row)
	}

	// The sixth line wraps back to the top of the region.
	buf.Reset()
	require.NoError(t, r.WriteLine("wrapped"))
	assert.Contains(t, buf.String(), fmt.Sprintf(cupFmt, 20, 1),
		"the sixth line must wrap to the first row of the region")
}

func TestRegionWriteLineClipsToWidth(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(20, 24))
	require.NoError(t, r.WriteLine(strings.Repeat("x", 200)))
	got := buf.String()
	assert.Contains(t, got, ellipsis, "a clipped line must be marked with an ellipsis")

	start := strings.Index(got, sgrBoldRed) + len(sgrBoldRed)
	end := strings.Index(got, sgrReset)
	require.Greater(t, end, start, "SGR markers missing from output")
	body := stripANSI(got[start:end])
	assert.LessOrEqual(t, utf8.RuneCountInString(body), 20,
		"a clipped line must fit within the terminal width")
}

func TestRegionReleaseOnDisabledIsNoOp(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, notATerminal())
	assert.NoError(t, r.Release(), "Release on disabled must not error")
	assert.Empty(t, buf.String(), "Release on disabled must not write anything")
}

func TestRegionReleaseRestoresTheTerminal(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(80, 24))
	require.NoError(t, r.Reserve())
	buf.Reset() // discard the Reserve bytes; assert against Release only
	require.NoError(t, r.Release())
	got := buf.String()
	assert.Contains(t, got, decstbmReset, "Release must clear the scroll margins")
	assert.Contains(t, got, cursorRestore, "Release must restore the saved cursor")
}

func TestRegionReleaseIsIdempotent(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 5, terminal(80, 24))
	require.NoError(t, r.Reserve())
	buf.Reset()
	require.NoError(t, r.Release())
	first := buf.Len()
	require.NoError(t, r.Release())
	assert.Equal(t, first, buf.Len(), "Release must not re-emit on second call")
}

// TestRegionReserveDisablesWhenTerminalShrank covers the resize case: the
// window is large at construction but too small by the time the first
// failure arrives. Applying the original margins would emit an inverted
// range, so the region stands down and the caller gets plain output.
func TestRegionReserveDisablesWhenTerminalShrank(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	p := &resizingProbe{width: 80, height: 24}
	r := NewRegion(&buf, 5, p)
	require.True(t, r.Enabled(), "the region starts out viable")

	p.height = 6 // window shrank below minUsefulHeight + 5
	require.NoError(t, r.WriteLine("boom"))

	assert.False(t, r.Enabled(), "a shrunk terminal must stand the region down")
	assert.NotContains(t, buf.String(), "\x1b[1;", "no margins may be set for the shrunk size")
	assert.Contains(t, buf.String(), "boom", "the failure must still reach the user")
}

// resizingProbe reports dimensions that callers can mutate between calls,
// standing in for a terminal the user resized mid-run.
type resizingProbe struct {
	width, height int
}

func (p *resizingProbe) IsTerminal(uintptr) bool { return true }
func (p *resizingProbe) Size(uintptr) (int, int, error) {
	return p.width, p.height, nil
}

func TestClipFitsWithinBudget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		msg  string
		n    int
		want string
	}{
		{"short message untouched", "abc", 10, "abc"},
		{"exact fit untouched", "abcde", 5, "abcde"},
		{"clipped with ellipsis", "abcdefghij", 8, "abcde..."},
		{"budget equals ellipsis", "abcdefghij", 3, "..."},
		{"budget below ellipsis", "abcdefghij", 2, ".."},
		{"zero budget", "abc", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Clip(tc.msg, tc.n)
			assert.Equal(t, tc.want, got)
			assert.LessOrEqual(t, len(got), tc.n, "Clip must never exceed its byte budget")
		})
	}
}

// TestClipNeverSplitsARune guards the UTF-8 walk-back: a multi-byte rune
// straddling the cut is dropped whole rather than emitted as a broken
// fragment the terminal would render as a replacement character.
func TestClipNeverSplitsARune(t *testing.T) {
	t.Parallel()
	// Four 3-byte runes; a budget of 8 leaves 5 bytes for content, which
	// lands mid-rune and must walk back to 3.
	got := Clip("日本語文", 8)
	assert.True(t, utf8.ValidString(got), "clip must not emit a partial rune: %q", got)
	assert.LessOrEqual(t, len(got), 8)
	assert.True(t, strings.HasSuffix(got, ellipsis))
}

func TestResetScrollMarginsIsNoOpOffTerminal(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, ResetScrollMargins(&buf, terminal(80, 24)))
	assert.Empty(t, buf.String(), "a writer with no descriptor must not be written to")

	var tbuf ttyBuf
	require.NoError(t, ResetScrollMargins(&tbuf, notATerminal()))
	assert.Empty(t, tbuf.String(), "a non-terminal descriptor must not be written to")
}

func TestResetScrollMarginsClearsMargins(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	require.NoError(t, ResetScrollMargins(&buf, terminal(80, 24)))
	assert.Equal(t, decstbmReset, buf.String())
}

// stripANSI removes every ANSI escape sequence from s: CSI sequences
// introduced by ESC [ ... letter, plus the bare two-character ESC
// sequences (save/restore cursor, etc.). Visible characters pass through.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) {
			if s[i+1] == '[' {
				// CSI: ESC [ ... <final byte 0x40-0x7E>
				j := i + 2
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
					j++
				}
				i = j + 1
				continue
			}
			i += 2 // two-character escape: ESC X
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func TestRegionSetStatusPinsTheFirstRow(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 6, terminal(80, 24))
	require.NoError(t, r.Reserve())
	buf.Reset()

	require.NoError(t, r.SetStatus("pool 3/8 running"))
	out := buf.String()
	// firstRow = 24 - 6 + 1 = 19.
	assert.Contains(t, out, fmt.Sprintf(cupFmt, 19, 1), "the status line owns the region's first row")
	assert.Contains(t, out, "pool 3/8 running")
	assert.Contains(t, out, sgrDim, "the status line is dim, not the failures' red")
	assert.NotContains(t, out, sgrBoldRed, "the status line must not read as a failure")
}

// TestRegionSetStatusShiftsFailuresDown is the contract that keeps the
// status line from being overwritten: once it claims row one, failures
// start below it.
func TestRegionSetStatusShiftsFailuresDown(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 6, terminal(80, 24))
	require.NoError(t, r.SetStatus("pool 1/8 running"))
	buf.Reset()

	require.NoError(t, r.WriteLine("boom"))
	assert.Contains(t, buf.String(), fmt.Sprintf(cupFmt, 20, 1),
		"the first failure lands one row below the status line, not on it")
}

// TestRegionWithoutStatusUsesEveryRow proves the status row is not paid
// for unless it is used: a caller that never sets one gets the whole
// region for failures.
func TestRegionWithoutStatusUsesEveryRow(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 6, terminal(80, 24))
	require.NoError(t, r.Reserve())
	buf.Reset()

	require.NoError(t, r.WriteLine("boom"))
	assert.Contains(t, buf.String(), fmt.Sprintf(cupFmt, 19, 1),
		"with no status line the first failure uses the region's first row")
}

func TestRegionSetStatusIsDroppedWhenDisabled(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := NewRegion(&buf, 6, notATerminal())
	require.NoError(t, r.SetStatus("pool 3/8 running"))
	assert.Empty(t, buf.String(),
		"a status line is a repainted view; replaying it into a pipe would be noise")
}

// TestRegionReflowsAfterAGrowingResize covers the resize case the scroll
// margins would otherwise get wrong: after the window grows, the margins
// must describe the new geometry, not the old.
func TestRegionReflowsAfterAGrowingResize(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	p := &resizingProbe{width: 80, height: 24}
	r := NewRegion(&buf, 6, p)
	require.NoError(t, r.WriteLine("before"))
	require.Contains(t, buf.String(), "\x1b[1;18r", "margins for a 24-row terminal")

	p.height = 40
	buf.Reset()
	require.NoError(t, r.WriteLine("after"))

	out := buf.String()
	assert.Contains(t, out, "\x1b[1;34r", "margins must be re-issued for the 40-row terminal")
	assert.Contains(t, out, fmt.Sprintf(cupFmt, 35, 1), "the failure zone restarts at the new first row")
	assert.Contains(t, out, "after")
}

// TestRegionReleasesRowsWhenAResizeMakesItUnviable makes sure standing
// down also hands the rows back: leaving the old margins set would keep
// the user's shell constrained after the run.
func TestRegionReleasesRowsWhenAResizeMakesItUnviable(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	p := &resizingProbe{width: 80, height: 24}
	r := NewRegion(&buf, 6, p)
	require.NoError(t, r.WriteLine("before"))

	p.height = 9 // below minUsefulHeight + 6
	buf.Reset()
	require.NoError(t, r.WriteLine("after"))

	out := buf.String()
	assert.False(t, r.Enabled(), "a window too small for the region stands it down")
	assert.Contains(t, out, decstbmReset, "the reserved rows must be handed back")
	assert.Contains(t, out, "after", "the failure still reaches the user, plainly")
}

// TestReturnCursorParksAboveTheRegion pins the interactive contract. SetStatus
// leaves the cursor inside the region; a caller holding a prompt needs it back on
// the transcript's last row, or the prompt and every echoed keystroke land in the
// footer. Row is termHeight-height, i.e. the row just above firstRow.
func TestReturnCursorParksAboveTheRegion(t *testing.T) {
	var buf ttyBuf
	r := NewRegion(&buf, 1, terminal(80, 24))
	require.True(t, r.Enabled())
	require.NoError(t, r.Reserve())

	buf.Reset()
	require.NoError(t, r.ReturnCursor())
	assert.Equal(t, "\x1b[23;1H", buf.String(),
		"parks at column 1 of the last scrolling row, one above the 1-row region")
}

// TestReturnCursorDoesNotTouchTheSaveSlot is the reason ReturnCursor exists at all
// rather than SetStatus saving and restoring around its repaint: Reserve holds the
// terminal's single cursor-save slot for the region's whole life so Release can use
// it, and a per-repaint save would clobber it.
func TestReturnCursorDoesNotTouchTheSaveSlot(t *testing.T) {
	var buf ttyBuf
	r := NewRegion(&buf, 2, terminal(80, 24))
	require.NoError(t, r.Reserve())

	buf.Reset()
	require.NoError(t, r.SetStatus("running"))
	require.NoError(t, r.ReturnCursor())
	assert.NotContains(t, buf.String(), cursorSave,
		"a repaint must not re-save; Release would then restore the repaint position")

	buf.Reset()
	require.NoError(t, r.Release())
	assert.Contains(t, buf.String(), cursorRestore, "Release still restores Reserve's save")
}

// TestReturnCursorIsSafeWhenDisabled keeps the caller branch-free: off a TTY every
// region method is a no-op, so a piped REPL session emits no escape sequences.
func TestReturnCursorIsSafeWhenDisabled(t *testing.T) {
	var buf ttyBuf
	r := NewRegion(&buf, 1, notATerminal())
	require.False(t, r.Enabled())
	require.NoError(t, r.ReturnCursor())
	assert.Empty(t, buf.String(), "a disabled region writes nothing")
}
