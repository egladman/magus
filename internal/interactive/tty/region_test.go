package tty

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ttyBuf wraps bytes.Buffer with a synthetic non-zero descriptor so a
// region treats it as a real terminal. Production writers are *os.File;
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
	r := newRegion(&buf, 0, terminal(80, 24))
	assert.False(t, r.isEnabled())
}

func TestRegionDisabledWhenNotTerminal(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 5+borderRows, notATerminal())
	assert.False(t, r.isEnabled(), "non-TTY writer must yield a disabled region")
}

func TestRegionDisabledWhenWriterHasNoDescriptor(t *testing.T) {
	t.Parallel()
	// A bytes.Buffer has no Fd(), so even a probe that calls everything a
	// terminal must not enable the region.
	var buf bytes.Buffer
	r := newRegion(&buf, 5+borderRows, terminal(80, 24))
	assert.False(t, r.isEnabled(), "a writer without a descriptor is never a terminal")
}

func TestRegionEnabledForLargeTerminal(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 5+borderRows, terminal(80, 24))
	assert.True(t, r.isEnabled(), "80x24 terminal with a 5-row region must enable")
}

func TestRegionDisabledWhenTerminalTooShort(t *testing.T) {
	t.Parallel()
	// minUsefulHeight is 8; 5 rows of region would leave only 2 to scroll.
	var buf ttyBuf
	r := newRegion(&buf, 5+borderRows, terminal(80, 7))
	assert.False(t, r.isEnabled(), "terminal too short for a useful scrolling region")
}

func TestRegionDisabledWhenTerminalTooNarrow(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 5+borderRows, terminal(10, 24))
	assert.False(t, r.isEnabled(), "terminal narrower than minUsefulWidth must disable")
}

func TestRegionDisabledWhenSizeQueryFails(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	p := fakeProbe{isTTY: true, width: 80, height: 24, sizeErr: errors.New("ioctl failed")}
	r := newRegion(&buf, 5+borderRows, p)
	assert.False(t, r.isEnabled(), "a failed size query must disable the region")

	require.NoError(t, r.render([]Line{{Text: "still visible"}}))
	assert.Empty(t, buf.String(), "a disabled region writes nothing; the caller decides what to print instead")
}

func TestRegionReserveOnDisabledIsNoOp(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 5+borderRows, notATerminal())
	require.NoError(t, r.reserve(), "Reserve on disabled must not error")
	assert.Empty(t, buf.String(), "Reserve on disabled must not write anything")
}

func TestRegionReserveEmitsScrollMargins(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 5+borderRows, terminal(80, 24))
	require.True(t, r.isEnabled())
	require.NoError(t, r.reserve())
	got := buf.String()
	// firstRow = 24 - 5 + 1 = 20, so scrolling covers rows [1,19] and the
	// region occupies rows [20,24].
	margins := fmt.Sprintf("\x1b[1;%dr", 24-5-borderRows)
	assert.Contains(t, got, margins, "margins must confine scrolling to the rows above the box")
	assert.Contains(t, got, cursorSave, "Reserve must save the cursor")
	assert.Contains(t, got, "\x1b[J", "Reserve must clear the reserved region")
	assert.Less(t, strings.Index(got, cursorSave), strings.Index(got, margins),
		"the cursor must be saved BEFORE the margins are set, since setting them homes it")
}

func TestRegionReserveIsIdempotent(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 5+borderRows, terminal(80, 24))
	require.NoError(t, r.reserve())
	first := buf.Len()
	require.NoError(t, r.reserve())
	assert.Equal(t, first, buf.Len(), "Reserve must not re-emit on second call")
}

func TestRegionReleaseOnDisabledIsNoOp(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 5+borderRows, notATerminal())
	assert.NoError(t, r.release(), "Release on disabled must not error")
	assert.Empty(t, buf.String(), "Release on disabled must not write anything")
}

func TestRegionReleaseRestoresTheTerminal(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 5+borderRows, terminal(80, 24))
	require.NoError(t, r.reserve())
	buf.Reset() // discard the Reserve bytes; assert against Release only
	require.NoError(t, r.release())
	got := buf.String()
	assert.Contains(t, got, decstbmReset, "Release must clear the scroll margins")
	// The restore here pairs the clearing paint's OWN save, within one write. It is
	// not a session-old position being reinstated: Release deliberately leaves the
	// caller's cursor alone, since nothing ever moved it. See
	// TestReleaseGivesTheRowsBackWithoutRepositioning.
	assert.Contains(t, got, cursorRestore, "the clearing paint restores the cursor it saved")
}

func TestRegionReleaseIsIdempotent(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 5+borderRows, terminal(80, 24))
	require.NoError(t, r.reserve())
	buf.Reset()
	require.NoError(t, r.release())
	first := buf.Len()
	require.NoError(t, r.release())
	assert.Equal(t, first, buf.Len(), "Release must not re-emit on second call")
}

// TestRegionReserveDisablesWhenTerminalShrank covers the resize case: the
// window is large at construction but too small by the time the first paint
// arrives. Applying the original margins would emit an inverted range, so the
// region stands down. What the caller prints instead is its own call - see
// TestZoneReportsNotRenderedWhenTheWindowShrinks.
func TestRegionReserveDisablesWhenTerminalShrank(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	p := &resizingProbe{width: 80, height: 24}
	r := newRegion(&buf, 5+borderRows, p)
	require.True(t, r.isEnabled(), "the region starts out viable")

	p.height = 6 // window shrank below minUsefulHeight + 5
	require.NoError(t, r.render([]Line{{Text: "boom"}}))

	assert.False(t, r.isEnabled(), "a shrunk terminal must stand the region down")
	assert.NotContains(t, buf.String(), "\x1b[1;", "no margins may be set for the shrunk size")
	assert.NotContains(t, buf.String(), "boom", "and nothing may be painted into a zone that does not fit")
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
	assert.Equal(t, cursorSave+decstbmReset+cursorRestore, buf.String())
}

// TestResetScrollMarginsLeavesTheCursorAlone is the `magus help` regression.
// This runs on every exit path, including commands that never opened a region,
// so on a plain `magus help` it is the only escape emitted at all. DECSTBM
// homes the cursor, so the bare reset left it at row 1; the shell drew its
// prompt there and erased the help text on its first redraw. The output
// appeared for a split second and vanished.
func TestResetScrollMarginsLeavesTheCursorAlone(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	require.NoError(t, ResetScrollMargins(&buf, terminal(80, 24)))
	got := buf.String()

	assert.Contains(t, got, decstbmReset, "the margins still have to be cleared")
	assert.True(t, strings.HasPrefix(got, cursorSave),
		"the cursor is saved before DECSTBM homes it")
	assert.True(t, strings.HasSuffix(got, cursorRestore),
		"and put back after - a reset emitted last would undo the restore")
	assert.Empty(t, stripANSI(got), "teardown prints no visible characters")
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

// TestRegionReflowsAfterAGrowingResize covers the resize case the scroll
// margins would otherwise get wrong: after the window grows, the margins
// must describe the new geometry, not the old.
func TestRegionReflowsAfterAGrowingResize(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	p := &resizingProbe{width: 80, height: 24}
	r := newRegion(&buf, 6+borderRows, p)
	require.NoError(t, r.render([]Line{{Text: "before"}}))
	require.Contains(t, buf.String(), fmt.Sprintf("\x1b[1;%dr", 24-6-borderRows), "margins for a 24-row terminal")

	p.height = 40
	buf.Reset()
	require.NoError(t, r.render([]Line{{Text: "after"}}))

	out := buf.String()
	assert.Contains(t, out, fmt.Sprintf("\x1b[1;%dr", 40-6-borderRows), "margins must be re-issued for the 40-row terminal")
	assert.Contains(t, out, fmt.Sprintf(cupFmt, 35, 1), "the zone restarts at the new first row")
	assert.Contains(t, out, "after")
}

// TestRegionReleasesRowsWhenAResizeMakesItUnviable makes sure standing
// down also hands the rows back: leaving the old margins set would keep
// the user's shell constrained after the run.
func TestRegionReleasesRowsWhenAResizeMakesItUnviable(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	p := &resizingProbe{width: 80, height: 24}
	r := newRegion(&buf, 6+borderRows, p)
	require.NoError(t, r.render([]Line{{Text: "before"}}))

	p.height = 9 // below minUsefulHeight + 6
	buf.Reset()
	require.NoError(t, r.render([]Line{{Text: "after"}}))

	out := buf.String()
	assert.False(t, r.isEnabled(), "a window too small for the region stands it down")
	assert.Contains(t, out, decstbmReset, "the reserved rows must be handed back")
	assert.NotContains(t, out, "after", "and nothing is painted into rows that no longer exist")
}

// balancedSaves reports how the save/restore register is used across out: how many
// times it is taken, and whether it was ever taken twice without an intervening
// release. Nesting is the specific failure this type had - a repaint taking the
// slot a reservation was still holding - so it is asserted directly rather than
// inferred from a byte comparison.
func balancedSaves(t *testing.T, out string) (saves int, nested bool) {
	t.Helper()
	depth := 0
	for i := 0; i < len(out); i++ {
		switch {
		case strings.HasPrefix(out[i:], cursorSave):
			depth++
			saves++
			if depth > 1 {
				nested = true
			}
		case strings.HasPrefix(out[i:], cursorRestore):
			depth--
		}
	}
	if depth != 0 {
		t.Fatalf("save/restore left unbalanced (depth %d) in %q", depth, out)
	}
	return saves, nested
}

// cursorModel is a minimal terminal that tracks only what this package's contract
// depends on: which row the cursor is on.
//
// It exists because the other tests here assert byte ORDER within one method, and
// that is exactly the shape of assertion the DECSTBM bug slipped past - the old
// Release emitted a correct-looking save/CUP/restore and then appended the margin
// reset, which was "last" as its test demanded and homed the cursor anyway. Order
// is a proxy; the contract is the cursor's final position. This measures that.
//
// It models only the sequences the package emits. Crucially it models DECSTBM as
// homing the cursor, which is the real terminal behaviour (VT100 and every emulator
// that follows it) and the thing the bug turned on.
type cursorModel struct {
	row      int
	saved    int
	hasSaved bool
}

func (m *cursorModel) feed(t *testing.T, s string) {
	t.Helper()
	for i := 0; i < len(s); {
		if s[i] == '\n' {
			m.row++
			i++
			continue
		}
		// DECSC / DECRC are two-byte sequences with no CSI introducer.
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '7' {
			m.saved, m.hasSaved = m.row, true
			i += 2
			continue
		}
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == 'D' {
			// IND: down one row, column untouched. This is how the region makes
			// room; a newline would also return the carriage and lose the
			// caller's column (see [ind]).
			m.row++
			i += 2
			continue
		}
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '8' {
			if !m.hasSaved {
				t.Fatal("cursor restore with nothing saved: the save register is a single global slot")
			}
			m.row = m.saved
			i += 2
			continue
		}
		if s[i] != 0x1b || i+1 >= len(s) || s[i+1] != '[' {
			i++
			continue
		}
		j := i + 2
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
			j++
		}
		if j >= len(s) {
			t.Fatalf("unterminated CSI in %q", s[i:])
		}
		params, final := s[i+2:j], s[j]
		switch final {
		case 's':
			m.saved, m.hasSaved = m.row, true
		case 'u':
			if !m.hasSaved {
				t.Fatal("cursor restore with nothing saved: the save register is a single global slot")
			}
			m.row = m.saved
		case 'H': // CUP: row;col
			m.row = csiParam(t, params, 0, 1)
		case 'A': // CUU: up n
			m.row -= csiParam(t, params, 0, 1)
		case 'r':
			// DECSTBM, set OR reset, homes the cursor. This single line is the whole
			// reason the bug existed and the whole reason this model is worth having.
			m.row = 1
		}
		i = j + 1
	}
}

// csiParam reads the nth semicolon-separated CSI parameter, or def when absent.
func csiParam(t *testing.T, params string, n, def int) int {
	t.Helper()
	if params == "" {
		return def
	}
	parts := strings.Split(params, ";")
	if n >= len(parts) || parts[n] == "" {
		return def
	}
	v, err := strconv.Atoi(parts[n])
	require.NoError(t, err, "CSI param %q", params)
	return v
}

// TestEveryRegionOperationLeavesTheCursorWhereItFoundIt is the regression guard for
// the whole class of bug, not just the two sites that had it.
//
// A caller is mid-transcript at some row; after ANY region operation it must still
// be there, or the shell prompt lands somewhere it did not write and its next
// redraw erases the run's output - which reads as output that flashed up and
// vanished. Each operation is fed to a fresh model at a known row and checked.
//
// Run against the pre-fix code this fails twice: Release ends at row 1 (margin
// reset after the restore) and ResetScrollMargins ends at row 1 (bare reset).
func TestEveryRegionOperationLeavesTheCursorWhereItFoundIt(t *testing.T) {
	const startRow = 12

	for _, tc := range []struct {
		name string
		run  func(t *testing.T, r *region)
	}{
		{"Reserve", func(t *testing.T, r *region) { require.NoError(t, r.reserve()) }},
		{"Render", func(t *testing.T, r *region) {
			require.NoError(t, r.render([]Line{{Text: "boom", Style: SGRBoldRed}}))
		}},
		{"Release", func(t *testing.T, r *region) {
			require.NoError(t, r.reserve())
			require.NoError(t, r.release())
		}},
		{"full lifecycle", func(t *testing.T, r *region) {
			require.NoError(t, r.reserve())
			require.NoError(t, r.render([]Line{{Text: "running", Style: SGRDim}}))
			require.NoError(t, r.render([]Line{{Text: "running", Style: SGRDim}, {Text: "first"}}))
			require.NoError(t, r.render([]Line{{Text: "running, 2 failed", Style: SGRDim}, {Text: "first"}, {Text: "second"}}))
			require.NoError(t, r.release())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf ttyBuf
			r := newRegion(&buf, 3+borderRows, terminal(80, 24))
			tc.run(t, r)

			m := &cursorModel{row: startRow}
			m.feed(t, buf.String())
			assert.Equal(t, startRow, m.row,
				"%s moved the caller's cursor from row %d to row %d; the shell prompt "+
					"would land there and its next redraw would erase the transcript",
				tc.name, startRow, m.row)
		})
	}

	// The process-exit path is the one `magus help` hits - a command that opens no
	// region at all, where this was the ONLY escape emitted for the whole run.
	t.Run("ResetScrollMargins", func(t *testing.T) {
		var buf ttyBuf
		require.NoError(t, ResetScrollMargins(&buf, terminal(80, 24)))

		m := &cursorModel{row: startRow}
		m.feed(t, buf.String())
		assert.Equal(t, startRow, m.row,
			"teardown moved the cursor to row %d; this runs on EVERY exit path, "+
				"including commands that never opened a region", m.row)
	})
}

// TestReserveMakesRoomWithoutMovingTheCallersCursor pins the reservation half of
// the transparency contract. The index sequences guarantee the zone exists; the
// matching cursor-up means the caller's cursor ends where its own text left it,
// so nothing jumps and no written row is clobbered.
func TestReserveMakesRoomWithoutMovingTheCallersCursor(t *testing.T) {
	var buf ttyBuf
	r := newRegion(&buf, 2+borderRows, terminal(80, 24))
	require.NoError(t, r.reserve())

	got := buf.String()
	assert.Contains(t, got, strings.Repeat(ind, 2+borderRows),
		"one index sequence per row, the box's included")
	assert.NotContains(t, got, "\n",
		"never a newline: ONLCR would return the carriage and lose the caller's column")
	back := fmt.Sprintf("\x1b[%dA", 2+borderRows)
	assert.Contains(t, got, back, "and the cursor steps back up over exactly those rows")
	assert.Less(t, strings.Index(got, back), strings.Index(got, cursorSave),
		"room is made before the cursor is saved, so the save records the real position")
	assert.Less(t, strings.Index(got, cursorSave), strings.Index(got, fmt.Sprintf("\x1b[1;%dr", 24-2-borderRows)),
		"and the save precedes DECSTBM, which homes the cursor in some terminals")
	assert.True(t, strings.HasSuffix(got, cursorRestore),
		"the reservation ends by putting the caller's cursor back")

	saves, nested := balancedSaves(t, got)
	assert.Equal(t, 1, saves)
	assert.False(t, nested)
}

// TestPaintingIsCursorTransparent is the invariant every consumer depends on and
// none of them could rely on before: a status repaint or a failure line must not
// move where the caller was writing.
//
// The cache handler interleaves paintStatus with its own scrolling output, and the
// REPL paints between printing a prompt and reading a line. Both were previously
// left with the cursor parked inside the region, so the next thing either printed
// landed in the footer.
func TestPaintingIsCursorTransparent(t *testing.T) {
	for name, paint := range map[string]func(r *region) error{

		"Render":  func(r *region) error { return r.render([]Line{{Text: "boom"}}) },
		"Release": func(r *region) error { return r.release() },
	} {
		t.Run(name, func(t *testing.T) {
			var buf ttyBuf
			r := newRegion(&buf, 2+borderRows, terminal(80, 24))
			require.NoError(t, r.reserve())

			buf.Reset()
			require.NoError(t, paint(r))
			got := buf.String()

			require.NotEmpty(t, got)
			assert.True(t, strings.HasPrefix(got, cursorSave), "the write opens by saving the cursor")
			saves, nested := balancedSaves(t, got)
			assert.Equal(t, 1, saves, "exactly one save per write, never held across calls")
			assert.False(t, nested, "the register is a single global slot; nesting loses a position")
		})
	}
}

// TestReleaseGivesTheRowsBackWithoutRepositioning pins the teardown. Nothing moved
// the caller's cursor, so it already sits after the caller's last line - which is
// where the shell prompt belongs. Restoring a position saved when the region opened
// (the old behaviour) put the prompt back into the middle of the transcript.
func TestReleaseGivesTheRowsBackWithoutRepositioning(t *testing.T) {
	var buf ttyBuf
	r := newRegion(&buf, 2+borderRows, terminal(80, 24))
	require.NoError(t, r.reserve())

	buf.Reset()
	require.NoError(t, r.release())
	got := buf.String()

	assert.Contains(t, got, ed, "the zone is cleared so the footer does not linger")
	assert.Contains(t, got, decstbmReset, "and the rows are given back")
	assert.True(t, strings.HasSuffix(got, cursorRestore),
		"the restore comes last: DECSTBM homes the cursor, so a reset emitted "+
			"after it would park the cursor at row 1 and let the shell prompt "+
			"paint over the run's transcript")
	assert.Equal(t, 1, strings.Count(got, cursorRestore),
		"one restore, pairing the clear's own save - not a session-old position")
}

// TestReserveThenPaintThenReleaseNeverNestsSaves is the sequence test: the whole
// lifecycle in order, asserting the register is taken and released cleanly at every
// step. This is what a byte-level assertion on any single method cannot show.
func TestReserveThenPaintThenReleaseNeverNestsSaves(t *testing.T) {
	var buf ttyBuf
	r := newRegion(&buf, 3+borderRows, terminal(80, 24))

	require.NoError(t, r.reserve())
	require.NoError(t, r.render([]Line{{Text: "running", Style: SGRDim}}))
	require.NoError(t, r.render([]Line{{Text: "running", Style: SGRDim}, {Text: "first failure"}}))
	require.NoError(t, r.render([]Line{{Text: "running, 1 failed", Style: SGRDim}, {Text: "first failure"}, {Text: "second failure"}}))
	require.NoError(t, r.release())

	saves, nested := balancedSaves(t, buf.String())
	assert.False(t, nested, "no paint may take the register while another holds it")
	assert.Equal(t, 5, saves, "one per write: reserve, 3 frames, release")
}

func TestRegionRenderOnDisabledDropsTheRows(t *testing.T) {
	t.Parallel()
	// A repainted view replayed into a pipe is noise, not information: unlike
	// WriteLine, Render has no plain-text fallback.
	var buf bytes.Buffer
	r := newRegion(&buf, 3+borderRows, notATerminal())
	require.NoError(t, r.render([]Line{{Text: "toast"}}))
	assert.Empty(t, buf.String())
}

func TestRegionRenderPaintsEveryRowInOneWrite(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 3+borderRows, terminal(80, 24))
	require.NoError(t, r.reserve())
	buf.Reset()

	require.NoError(t, r.render([]Line{{Text: "one"}, {Text: "two"}, {Text: "three"}}))
	out := buf.String()
	// One save/restore for the whole zone, not one per row: a per-row bracket
	// would let another writer's output land between two rows.
	assert.Equal(t, 1, strings.Count(out, cursorSave))
	assert.Equal(t, 1, strings.Count(out, cursorRestore))
	for i, want := range []string{"one", "two", "three"} {
		assert.Contains(t, out, fmt.Sprintf(cupFmt, r.firstRow()+i, 1))
		assert.Contains(t, out, want)
	}
}

func TestRegionRenderStylesPerRowAndClosesIt(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 2+borderRows, terminal(80, 24))
	require.NoError(t, r.render([]Line{{Text: "warn", Style: SGRYellow}, {Text: "plain"}}))
	out := buf.String()
	assert.Contains(t, out, fmt.Sprintf(sgrFmt, SGRYellow)+"warn"+sgrReset,
		"the style is closed on its own row, so the next cannot inherit it")
	// An unstyled row emits no SGR at all rather than an empty one.
	assert.NotContains(t, out, fmt.Sprintf(sgrFmt, "")+"plain")
}

func TestRegionRenderDropsRowsPastTheZone(t *testing.T) {
	t.Parallel()
	// Overflow is the caller's decision to make, because only it knows whether
	// the newest or the oldest entry is the one worth keeping.
	var buf ttyBuf
	r := newRegion(&buf, 2+borderRows, terminal(80, 24))
	require.NoError(t, r.render([]Line{{Text: "a"}, {Text: "b"}, {Text: "c"}}))
	assert.NotContains(t, buf.String(), "c")
}

func TestRegionRenderClipsToTheTerminalWidth(t *testing.T) {
	t.Parallel()
	// A row that overshot would wrap onto a second screen row and desynchronise
	// the zone's one-row-per-entry arithmetic.
	var buf ttyBuf
	r := newRegion(&buf, 1+borderRows, terminal(24, 24))
	require.NoError(t, r.render([]Line{{Text: strings.Repeat("x", 100)}}))
	assert.Contains(t, buf.String(), ellipsis)
	assert.NotContains(t, buf.String(), strings.Repeat("x", 24))
}

func TestRegionRenderAlignsSpansToBothEdges(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 1+borderRows, terminal(40, 24))
	require.NoError(t, r.render([]Line{{Spans: []Span{
		{Text: "pool 6/8 running", Style: SGRDim},
		{Text: "6.4s", Style: SGRDim, Align: AlignRight},
	}}}))

	// Width is 40, so the paintable row is 39 columns: 16 of text, 19 of gap,
	// 4 of elapsed.
	line := visibleText(t, buf.String())
	assert.True(t, strings.HasPrefix(line, "pool 6/8 running"))
	assert.True(t, strings.HasSuffix(line, "6.4s"))
	assert.Len(t, line, 40-1-2*borderCols, "the right span ends at the inner right edge")
}

func TestRegionRenderKeepsTheRightSpanWhenNarrow(t *testing.T) {
	t.Parallel()
	// The policy, and the bug it fixes: as one string the prompt's hint row was
	// 86 columns and an 80-column terminal clipped it to "...[esc] do" - the
	// only key that closes the prompt, gone at the width most people have.
	var buf ttyBuf
	r := newRegion(&buf, 1+borderRows, terminal(40, 24))
	require.NoError(t, r.render([]Line{{Spans: []Span{
		{Text: "click a failure, or [up/down] select   [enter] rerun stepped   [o] output"},
		{Text: "[esc] done", Align: AlignRight},
	}}}))

	line := visibleText(t, buf.String())
	assert.True(t, strings.HasSuffix(line, "[esc] done"), "the way out survives; the description is what gives")
	assert.Contains(t, line, "click a failure")
	assert.NotContains(t, line, "[o] output", "the left side clipped to make room")
	assert.LessOrEqual(t, len(line), 39)
}

func TestRegionRenderWritesNoTrailingPadWithoutARightSpan(t *testing.T) {
	t.Parallel()
	// A row that aligns nothing right must end where its text ends. Padding it
	// to the full width would be bytes on the wire every repaint, and would
	// make an unchanged row look changed to a terminal doing its own diffing.
	var buf ttyBuf
	r := newRegion(&buf, 1+borderRows, terminal(40, 24))
	require.NoError(t, r.render([]Line{{Text: "pool 6/8 running"}}))
	assert.Equal(t, "pool 6/8 running", visibleText(t, buf.String()))
}

func TestRegionRenderStylesEachSpanIndependently(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	r := newRegion(&buf, 1+borderRows, terminal(40, 24))
	require.NoError(t, r.render([]Line{{Spans: []Span{
		{Text: "waiting", Style: SGRYellow},
		{Text: "esc", Style: SGRDim, Align: AlignRight},
	}}}))
	out := buf.String()
	assert.Contains(t, out, fmt.Sprintf(sgrFmt, SGRYellow)+"waiting"+sgrReset)
	assert.Contains(t, out, fmt.Sprintf(sgrFmt, SGRDim)+"esc"+sgrReset,
		"each span closes its own style, so neither bleeds into the other")
}

func TestRegionRenderDiffsSpanRows(t *testing.T) {
	t.Parallel()
	// Line carries a slice now, so the frame diff cannot use ==. An unchanged
	// span row must still cost zero bytes.
	var buf ttyBuf
	r := newRegion(&buf, 1+borderRows, terminal(40, 24))
	row := Line{Spans: []Span{
		{Text: "pool 6/8 running", Style: SGRDim},
		{Text: "6.4s", Style: SGRDim, Align: AlignRight},
	}}
	require.NoError(t, r.render([]Line{row}))
	buf.Reset()
	require.NoError(t, r.render([]Line{row}))
	assert.Empty(t, buf.String())

	require.NoError(t, r.render([]Line{{Spans: []Span{
		{Text: "pool 6/8 running", Style: SGRDim},
		{Text: "6.5s", Style: SGRDim, Align: AlignRight},
	}}}))
	assert.Contains(t, buf.String(), "6.5s", "and a changed span still repaints")
}

// visibleText strips every escape sequence, leaving what a reader would see on
// the row. The zone paints one row here, so the result is that row.
// visibleText is the text a reader would see, with the region's box taken off.
//
// Unboxing here rather than in every assertion: the box is the region's own
// framing, and a test about what a CALLER's row says should not have to know it
// exists. A test that is about the box asserts on the raw buffer instead.
func visibleText(t *testing.T, out string) string {
	t.Helper()
	return strings.TrimRight(unbox(stripANSI(out)), " ")
}

// unbox removes the border glyphs and the rules drawn from them.
func unbox(s string) string {
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.Trim(line, boxH+boxTL+boxTR+boxBL+boxBR)
		if trimmed == "" && line != "" {
			continue // a rule row carries nothing else
		}
		keep = append(keep, strings.Trim(trimmed, boxV))
	}
	return strings.Join(keep, "\n")
}

// TestColsSkipsHyperlinkURI is the OSC regression. cols recognised CSI only and
// stepped two bytes past an OSC introducer, so a hyperlink's URI counted as
// visible columns: an 8-column ref measured ~70, fit clipped text that fitted,
// and the box's right edge landed short on exactly the rows carrying a link.
func TestColsSkipsHyperlinkURI(t *testing.T) {
	t.Parallel()

	linked := Hyperlink("out8518ac44", "file:///a/very/long/path/to/a/captured/log.log")
	assert.Equal(t, len("out8518ac44"), cols(linked),
		"a hyperlink is as wide as its visible text, not its URI")
	assert.Equal(t, cols("out8518ac44"), cols(linked),
		"linking text must not change how wide it measures")
}
