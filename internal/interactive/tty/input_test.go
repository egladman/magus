package tty

import (
	"bufio"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestInput decodes from a fixed byte stream on a controllable clock, so
// the parser is exercised without a pty.
func newTestInput(stream string) (*Input, *fixedClock) {
	c := &fixedClock{t: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)}
	return &Input{r: bufio.NewReader(strings.NewReader(stream)), now: c.now}, c
}

func drain(t *testing.T, in *Input) []Event {
	t.Helper()
	var out []Event
	for {
		ev, err := in.Read()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		out = append(out, ev)
	}
}

func TestInputDecodesKeys(t *testing.T) {
	t.Parallel()
	in, _ := newTestInput("a\r\t\x7f\x03\x1b[A\x1b[B\x1b[C\x1b[D\x1b")
	got := drain(t, in)

	want := []Key{KeyRune, KeyEnter, KeyTab, KeyBackspace, KeyCtrlC, KeyUp, KeyDown, KeyRight, KeyLeft, KeyEscape}
	require.Len(t, got, len(want))
	for i, k := range want {
		assert.Equal(t, k, got[i].Key, "event %d", i)
		assert.Equal(t, EventKey, got[i].Kind)
	}
	assert.Equal(t, 'a', got[0].Rune)
}

func TestInputDecodesAMultiByteRune(t *testing.T) {
	t.Parallel()
	// A filter keystroke must not be split into mojibake.
	in, _ := newTestInput("é")
	got := drain(t, in)
	require.Len(t, got, 1)
	assert.Equal(t, KeyRune, got[0].Key)
	assert.Equal(t, 'é', got[0].Rune)
}

func TestInputDecodesAnSGRClick(t *testing.T) {
	t.Parallel()
	// ESC [ < button ; col ; row M  -- a left press at column 12, row 20.
	in, _ := newTestInput("\x1b[<0;12;20M\x1b[<0;12;20m")
	got := drain(t, in)
	require.Len(t, got, 2)

	assert.Equal(t, EventMouse, got[0].Kind)
	assert.Equal(t, MouseLeft, got[0].Button)
	assert.Equal(t, 20, got[0].Row, "row is the coordinate HitTest takes")
	assert.Equal(t, 12, got[0].Col)
	assert.True(t, got[0].Press)
	assert.Equal(t, 1, got[0].Clicks)

	assert.False(t, got[1].Press, "the release is reported too, as 'm'")
}

func TestInputDecodesColumnsPastTheLegacyLimit(t *testing.T) {
	t.Parallel()
	// The reason SGR mode is not optional: the original encoding cannot address
	// past column 223, so on a wide terminal the right-hand columns would
	// simply never report.
	in, _ := newTestInput("\x1b[<0;312;40M")
	got := drain(t, in)
	require.Len(t, got, 1)
	assert.Equal(t, 312, got[0].Col)
	assert.Equal(t, 40, got[0].Row)
}

func TestInputDecodesTheWheel(t *testing.T) {
	t.Parallel()
	in, _ := newTestInput("\x1b[<64;5;5M\x1b[<65;5;5M")
	got := drain(t, in)
	require.Len(t, got, 2)
	assert.Equal(t, MouseWheelUp, got[0].Button)
	assert.Equal(t, MouseWheelDown, got[1].Button)
}

func TestInputIgnoresClickModifiers(t *testing.T) {
	t.Parallel()
	// 16 is the ctrl bit. A user holding ctrl must still get an ordinary click
	// rather than nothing at all.
	in, _ := newTestInput("\x1b[<16;3;9M")
	got := drain(t, in)
	require.Len(t, got, 1)
	assert.Equal(t, MouseLeft, got[0].Button)
}

func TestInputTimesADoubleClick(t *testing.T) {
	t.Parallel()
	// The terminal reports presses, never double clicks, so this is measured
	// here or it does not exist.
	in, clock := newTestInput("\x1b[<0;12;20M\x1b[<0;12;20M")

	first, err := in.Read()
	require.NoError(t, err)
	assert.Equal(t, 1, first.Clicks)

	clock.advance(200 * time.Millisecond)
	second, err := in.Read()
	require.NoError(t, err)
	assert.Equal(t, 2, second.Clicks)
}

func TestInputDoesNotDoubleClickAcrossCellsOrTime(t *testing.T) {
	t.Parallel()
	t.Run("too slow", func(t *testing.T) {
		in, clock := newTestInput("\x1b[<0;12;20M\x1b[<0;12;20M")
		_, err := in.Read()
		require.NoError(t, err)
		clock.advance(doubleClickWindow + time.Millisecond)
		second, err := in.Read()
		require.NoError(t, err)
		assert.Equal(t, 1, second.Clicks)
	})
	t.Run("different cell", func(t *testing.T) {
		in, clock := newTestInput("\x1b[<0;12;20M\x1b[<0;12;21M")
		_, err := in.Read()
		require.NoError(t, err)
		clock.advance(50 * time.Millisecond)
		second, err := in.Read()
		require.NoError(t, err)
		assert.Equal(t, 1, second.Clicks, "a click on the row below is a new click, not a double")
	})
	t.Run("triple press is not two doubles", func(t *testing.T) {
		in, clock := newTestInput("\x1b[<0;12;20M\x1b[<0;12;20M\x1b[<0;12;20M")
		_, err := in.Read()
		require.NoError(t, err)
		clock.advance(50 * time.Millisecond)
		second, err := in.Read()
		require.NoError(t, err)
		require.Equal(t, 2, second.Clicks)
		clock.advance(50 * time.Millisecond)
		third, err := in.Read()
		require.NoError(t, err)
		assert.Equal(t, 1, third.Clicks, "the pair resets, or every press past the first reports a double")
	})
}

func TestInputSkipsAnUnknownSequenceWhole(t *testing.T) {
	t.Parallel()
	// A sequence magus does not act on must be consumed to its final byte, or
	// its tail is read as fresh keystrokes.
	in, _ := newTestInput("\x1b[200~x")
	got := drain(t, in)
	require.Len(t, got, 2)
	assert.Equal(t, KeyUnknown, got[0].Key)
	assert.Equal(t, KeyRune, got[1].Key)
	assert.Equal(t, 'x', got[1].Rune, "the byte after the sequence survives intact")
}

func TestOpenInputRefusesWithoutATerminal(t *testing.T) {
	t.Parallel()
	// An ordinary answer, not a failure: the caller skips the interactive step.
	var buf ttyBuf
	_, err := OpenInput(os.Stdin, &buf, notATerminal())
	assert.ErrorIs(t, err, ErrNotATerminal)
}

func TestZoneHitTestResolvesAClickToItsLease(t *testing.T) {
	t.Parallel()
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	failures := z.Acquire(6)
	toasts := z.Acquire(3)
	_, err := failures.Set([]Line{{Text: "pool"}})
	require.NoError(t, err)

	// 9 leased rows on a 24-row terminal: the zone starts at row 16.
	l, i, ok := z.HitTest(16)
	require.True(t, ok)
	assert.Same(t, failures, l)
	assert.Equal(t, 0, i, "the first row of the first band")

	l, i, ok = z.HitTest(18)
	require.True(t, ok)
	assert.Same(t, failures, l)
	assert.Equal(t, 2, i)

	l, i, ok = z.HitTest(22)
	require.True(t, ok)
	assert.Same(t, toasts, l, "row 22 is the second band")
	assert.Equal(t, 0, i)

	l, i, ok = z.HitTest(24)
	require.True(t, ok)
	assert.Same(t, toasts, l)
	assert.Equal(t, 2, i, "the last row of the zone")
}

func TestZoneHitTestRejectsScrollingRows(t *testing.T) {
	t.Parallel()
	// A click in the transcript belongs to the terminal's own selection, not to
	// magus.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	l := z.Acquire(6)
	_, err := l.Set([]Line{{Text: "pool"}})
	require.NoError(t, err)

	// One 6-row lease on a 24-row terminal reserves rows 19-24; everything
	// above that scrolls.
	for _, row := range []int{1, 15, 18} {
		_, _, ok := z.HitTest(row)
		assert.False(t, ok, "row %d is scrolling output", row)
	}
	_, _, ok := z.HitTest(19)
	assert.True(t, ok, "row 19 is the first reserved row")
}

func TestZoneHitTestRejectsEverythingBeforeAnythingIsDrawn(t *testing.T) {
	t.Parallel()
	// No rows are reserved until a paint happens, so no coordinate is ours.
	var buf ttyBuf
	z := NewZone(&buf, terminal(80, 24))
	z.Acquire(6)
	_, _, ok := z.HitTest(20)
	assert.False(t, ok)
}

func TestInputDecodesHoverMotion(t *testing.T) {
	t.Parallel()
	// Bit 32 is motion; 35 is motion with no button held, which is what
	// any-event tracking sends as the pointer crosses a cell. Hover is the
	// affordance that makes a clickable row look clickable, so this has to be
	// distinguishable from a press.
	in, _ := newTestInput("\x1b[<35;12;20M")
	got := drain(t, in)
	require.Len(t, got, 1)
	assert.Equal(t, EventMouse, got[0].Kind)
	assert.True(t, got[0].Motion)
	assert.False(t, got[0].Press, "motion is not a press; acting on it would fire on a mouse-over")
	assert.Equal(t, 20, got[0].Row)
	assert.Equal(t, 12, got[0].Col)
}

func TestInputMotionDoesNotArmADoubleClick(t *testing.T) {
	t.Parallel()
	// Moving onto a cell and then clicking it once must report one click. If
	// motion updated the double-click state, every hover-then-click would read
	// as a double and act twice.
	in, clock := newTestInput("\x1b[<35;12;20M\x1b[<0;12;20M")
	_, err := in.Read()
	require.NoError(t, err)
	clock.advance(50 * time.Millisecond)
	press, err := in.Read()
	require.NoError(t, err)
	assert.Equal(t, 1, press.Clicks)
}

func TestMouseTrackingDropsHoverOverSSH(t *testing.T) {
	// Hover reports an event per cell crossed. Over a link with latency that is
	// a stream of round trips for a highlight, and the pointer starts to feel
	// heavy - a worse first impression than having no hover at all. Clicks are
	// unaffected.
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	assert.Equal(t, mouseTrackAny, mouseTrackFor())

	t.Setenv("SSH_TTY", "/dev/pts/3")
	assert.Equal(t, mouseTrackClick, mouseTrackFor(), "ssh gets clicks without hover")
}

func TestMouseTrackingOffDisablesBothModes(t *testing.T) {
	// Whichever mode was enabled has to be the one turned off, and the process
	// exit path cannot know which was chosen - so the reset disables both. A
	// mode that was never on is a no-op to disable.
	assert.Contains(t, mouseTrackOff, "1003l")
	assert.Contains(t, mouseTrackOff, "1000l")
	assert.Contains(t, mouseTrackOff, "1006l")
}

func TestInputParsesACursorReply(t *testing.T) {
	t.Parallel()
	// The reply arrives in the same stream as keystrokes, so it has to be
	// recognised rather than swallowed as an unknown sequence.
	in, _ := newTestInput("\x1b[12;40R")
	ev, err := in.read()
	require.NoError(t, err)
	assert.Equal(t, eventCursor, ev.Kind)
	assert.Equal(t, 12, ev.Row)
	assert.Equal(t, 40, ev.Col)
}

func TestInputNeverSurfacesACursorReplyAsInput(t *testing.T) {
	t.Parallel()
	// A stray reply - one nobody asked for, or one that outlived its query -
	// is not something the user did. Handing it to a caller would look like a
	// phantom keypress.
	in, _ := newTestInput("\x1b[12;40Rx")
	ev, err := in.Read()
	require.NoError(t, err)
	assert.Equal(t, KeyRune, ev.Key)
	assert.Equal(t, 'x', ev.Rune, "the reply was dropped and the real keystroke came through")
}

func TestInputQueuesKeystrokesThatOvertakeAReply(t *testing.T) {
	t.Parallel()
	// A keystroke is not less real for having arrived at an awkward moment. It
	// must survive the query and be delivered in order afterwards.
	in, _ := newTestInput("ab\x1b[7;3R")

	// The same loop CursorPosition runs, minus the deadline a string reader
	// cannot carry. It uses decode rather than read: a loop that appends to the
	// queue must not also read from it, or it consumes its own output forever.
	var row, col int
	for {
		ev, err := in.decode()
		require.NoError(t, err)
		if ev.Kind == eventCursor {
			row, col = ev.Row, ev.Col
			break
		}
		in.pending = append(in.pending, ev)
	}
	assert.Equal(t, 7, row)
	assert.Equal(t, 3, col)

	first, err := in.Read()
	require.NoError(t, err)
	assert.Equal(t, 'a', first.Rune)
	second, err := in.Read()
	require.NoError(t, err)
	assert.Equal(t, 'b', second.Rune, "queued keystrokes keep their order")
}

func TestCursorPositionRefusesWhenItCannotBoundTheWait(t *testing.T) {
	// The failure mode being guarded is a HANG: a terminal that does not
	// implement the query says nothing at all, and a blocking read on it never
	// returns. Without a deadline to bound the wait, refuse outright - the
	// caller keeps its keyboard handling and goes without mouse support.
	var out ttyBuf
	in, _ := newTestInput("")
	in.out = &out
	_, _, ok := in.CursorPosition()
	assert.False(t, ok)
	assert.Contains(t, out.String(), dsrCursor, "the query is sent before the wait is given up on")
}

// TestCursorPositionTimesOutWhenNothingAnswers is the one claim in this file
// that cannot be checked by feeding bytes to a string reader, and it is also
// the highest-consequence one: a terminal that does not implement the query
// says NOTHING, and a blocking read on it never returns. If the deadline does
// not work, an interactive surface that asks freezes with no output and no way
// out.
//
// A pipe stands in for the terminal because the property under test is whether
// the descriptor is pollable enough for a read deadline to fire, which it is
// for both.
func TestCursorPositionTimesOutWhenNothingAnswers(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

	var out ttyBuf
	in := &Input{r: bufio.NewReader(r), in: r, out: &out, now: time.Now}

	start := time.Now()
	_, _, ok := in.CursorPosition()
	elapsed := time.Since(start)

	assert.False(t, ok, "silence must report as unsupported, not as a position")
	assert.GreaterOrEqual(t, elapsed, cprTimeout, "it waited for the reply before giving up")
	assert.Less(t, elapsed, 5*time.Second, "and gave up rather than hanging")
	assert.Contains(t, out.String(), dsrCursor)
}

// TestCursorPositionReadsARealReply is the other half: when the terminal does
// answer, over a real descriptor, the position comes back.
func TestCursorPositionReadsARealReply(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	go func() {
		// A keystroke racing ahead of the reply, which is the case that would
		// otherwise lose it.
		_, _ = w.WriteString("k\x1b[14;7R")
		_ = w.Close()
	}()

	var out ttyBuf
	in := &Input{r: bufio.NewReader(r), in: r, out: &out, now: time.Now}
	row, col, ok := in.CursorPosition()
	require.True(t, ok)
	assert.Equal(t, 14, row)
	assert.Equal(t, 7, col)

	ev, err := in.Read()
	require.NoError(t, err)
	assert.Equal(t, 'k', ev.Rune, "the keystroke that overtook the reply survived")
}

func TestInputDecodesABareEscapeWithoutWaiting(t *testing.T) {
	t.Parallel()
	// Escape is the key a reader presses when they want out, so it must not
	// wait for a second keypress. Peek on an empty buffer issues a read and
	// blocks; the decision has to be made on what has already arrived.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

	_, err = w.WriteString("\x1b")
	require.NoError(t, err)

	in := &Input{r: bufio.NewReader(r), in: r, now: time.Now}
	done := make(chan Event, 1)
	go func() {
		ev, readErr := in.Read()
		if readErr == nil {
			done <- ev
		}
	}()

	select {
	case ev := <-done:
		assert.Equal(t, KeyEscape, ev.Key)
	case <-time.After(2 * time.Second):
		t.Fatal("a bare escape must decode immediately, not wait for the next key")
	}
}

func TestCursorPositionDropsTheRemainsOfATimedOutReply(t *testing.T) {
	t.Parallel()
	// The deadline can fire part-way through a sequence. Its tail must not be
	// left for the next decode to read as fresh keystrokes - a half-arrived
	// reply would surface as a stray bracket or letter the user never typed.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

	// The front of a CPR reply, and nothing more: the terminal stalled.
	_, err = w.WriteString("\x1b[12;")
	require.NoError(t, err)

	var out ttyBuf
	in := &Input{r: bufio.NewReader(r), in: r, out: &out, now: time.Now}
	_, _, ok := in.CursorPosition()
	require.False(t, ok)
	assert.Zero(t, in.r.Buffered(), "the partial reply is discarded, not left to be misread")
}
