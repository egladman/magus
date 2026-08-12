package tty

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Key names a non-printing key. A printing key arrives as [KeyRune] with the
// rune in Event.Rune.
type Key int

// The keys magus acts on. Deliberately not an exhaustive terminal keymap:
// anything not listed decodes to KeyUnknown and callers ignore it, which is
// how an unrecognised escape sequence stays inert instead of being guessed at.
const (
	KeyUnknown Key = iota
	KeyRune
	KeyEnter
	KeyEscape
	KeyTab
	KeyBackspace
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyCtrlC
	KeyCtrlD
)

// MouseButton names which button an [Event] carries. Wheel movement arrives as
// a button because that is how the terminal reports it.
type MouseButton int

const (
	MouseNone MouseButton = iota
	MouseLeft
	MouseMiddle
	MouseRight
	MouseWheelUp
	MouseWheelDown
)

// EventKind distinguishes the two things an [Input] reports.
type EventKind int

const (
	EventKey EventKind = iota
	EventMouse
)

// Event is one thing the user did.
type Event struct {
	Kind EventKind

	// Key and Rune carry a keyboard event.
	Key  Key
	Rune rune

	// Button, Row, Col, Press and Clicks carry a mouse event. Row and Col are
	// 1-based absolute terminal coordinates, the same space [Zone.HitTest]
	// takes, so a click maps to a leased row without any translation.
	Button MouseButton
	Row    int
	Col    int
	Press  bool
	// Motion reports that the pointer MOVED to Row/Col rather than that a
	// button changed state. Button is MouseNone unless one is held.
	Motion bool
	// Clicks is 1 for a single click and 2 for a double click. The TERMINAL
	// does not report double clicks - it reports presses - so this is timed
	// here, from the interval and cell of the previous press.
	Clicks int
}

// doubleClickWindow is how long after a press a second press on the same cell
// still counts as a double click. 400ms is the common desktop default; longer
// makes two deliberate clicks merge, shorter makes a real double click miss.
const doubleClickWindow = 400 * time.Millisecond

// Mouse reporting modes, enabled together and disabled together.
//
// 1003 is any-event tracking: press, release, AND motion with no button held.
// The bare motion is the point - it is what lets a row highlight as the pointer
// crosses it, so a clickable thing LOOKS clickable. Without that feedback a
// mouse-driven surface is a guessing game, which defeats the reason for having
// one. (1002 is the middle mode and reports motion only while a button is down,
// which is drag, not hover.)
//
// The cost is real and is why this is scoped: 1003 sends an event for every
// cell the pointer crosses. It is only ever on while magus owns the terminal,
// and a motion that does not change what is highlighted repaints nothing, so
// the traffic ends at the parser.
//
// 1006 is SGR extended coordinates, and it is not optional. The original
// encoding packs each coordinate into one byte offset by 32, so it cannot
// address past column 223 - on any modern wide terminal the right-hand columns
// simply do not report. 1006 sends decimal parameters instead and has no limit.
const (
	mouseTrackAny   = "\x1b[?1003h\x1b[?1006h"
	mouseTrackClick = "\x1b[?1000h\x1b[?1006h"
	mouseTrackOff   = "\x1b[?1006l\x1b[?1003l\x1b[?1000l"
)

// mouseTrackFor picks how much of the mouse to ask for.
//
// Over ssh it drops to 1000, press and release only. Hover reports an event for
// every cell the pointer crosses, and on a link with any latency that is a
// stream of round trips for a highlight - the pointer starts to feel heavy,
// which is a worse first impression than not having hover at all. Clicking
// still works exactly the same; only the preview of what a click would hit is
// given up, and it is given up precisely where it would cost the most.
//
// Same signal the hyperlink gate reads, and for a related reason: ssh is where
// assumptions about the terminal being local stop holding.
func mouseTrackFor() string {
	if os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "" {
		return mouseTrackClick
	}
	return mouseTrackAny
}

// ResetMouseTracking turns mouse reporting off unconditionally.
//
// [Input.Close] restores a session this process knows it opened. This is the
// process-exit counterpart, and it is not redundant: a deferred Close does not
// run on os.Exit, on a SIGKILL, or on a signal handler that exits without
// unwinding - and a process that dies with tracking still on leaves the user
// unable to select text in their own shell, with nothing on screen explaining
// why. That is a broken terminal until they think to run `reset`, which is the
// single worst thing this package could do to somebody.
//
// Disabling a mode that was never enabled is a no-op in every terminal, so this
// is safe on every exit path including commands that never read a key. Unlike
// DECSTBM these sequences do not move the cursor, so they need no save/restore
// bracket.
//
// No-op when w is not a terminal, so callers do not branch.
func ResetMouseTracking(w io.Writer, p Probe) error {
	if !IsTerminalWriter(w, p) {
		return nil
	}
	_, err := io.WriteString(w, mouseTrackOff)
	return err
}

// Input owns the terminal's keyboard and mouse for as long as it is open.
//
// IT IS SCOPED ON PURPOSE, and that is the whole design. Enabling mouse
// reporting takes the mouse away from the terminal emulator's own text
// selection: while tracking is on, drag-to-select and the scroll wheel belong
// to this process instead of to the user. magus's contract is that ordinary
// output stays ordinary - real scrollback, selectable with the mouse - so
// tracking is switched on only at the moments magus genuinely owns the
// terminal (an end-of-run prompt, a step gate, a dashboard) and switched off
// the instant it does not.
//
// Raw mode is set on the descriptor that is actually READ. That sounds obvious
// and was not: the step gate calls MakeRaw on stderr and then reads stdin,
// which happens to work only because both usually point at the same device.
//
// Not safe for concurrent use: one goroutine reads.
type Input struct {
	r       *bufio.Reader
	out     io.Writer
	restore func() error
	closed  bool

	// lastPress is the previous press, for timing a double click.
	lastPress  time.Time
	lastRow    int
	lastCol    int
	lastButton MouseButton
	// now is the clock, injected so double-click timing is testable.
	now func() time.Time
}

// ErrNotATerminal is returned by OpenInput when either end is not a terminal.
// It is an ordinary answer rather than a failure: a caller checks it and skips
// the interactive step.
var ErrNotATerminal = errors.New("tty: input needs a terminal on both standard input and the display")

// OpenInput puts in into raw mode, turns on mouse reporting on out, and
// returns the session that reads from it. Close puts everything back.
//
// Both ends must be terminals: keys are read from in and the modes are written
// to out, and one without the other is a session that can capture but not
// respond, or respond but never hear anything.
func OpenInput(in *os.File, out io.Writer, p Probe) (*Input, error) {
	fd, ok := Fd(in)
	if !ok || !p.IsTerminal(fd) || !CanRender(out, p) {
		return nil, ErrNotATerminal
	}
	restore, err := MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(out, mouseTrackFor()); err != nil {
		// Leaving the terminal raw because tracking failed would hand the user
		// back a shell with no echo, which looks like a hang.
		_ = restore()
		return nil, fmt.Errorf("tty: enable mouse reporting: %w", err)
	}
	return &Input{r: bufio.NewReader(in), out: out, restore: restore, now: time.Now}, nil
}

// Close turns mouse reporting off and restores the terminal. Idempotent and
// safe to defer.
//
// Both halves are attempted even if the first fails: leaving tracking on would
// leave the user unable to select text in their own shell, and leaving raw mode
// on would leave them with no echo. Neither is acceptable as a consequence of
// the other going wrong.
func (i *Input) Close() error {
	if i.closed {
		return nil
	}
	i.closed = true
	_, trackErr := io.WriteString(i.out, mouseTrackOff)
	restoreErr := i.restore()
	if restoreErr != nil {
		return restoreErr
	}
	return trackErr
}

// Read blocks until the user does something, and reports it.
//
// It blocks, rather than offering a channel or a deadline, because every
// caller so far is a prompt waiting on a person. A caller that needs to do
// something else meanwhile runs this in its own goroutine and closes the Input
// to release it.
func (i *Input) Read() (Event, error) {
	b, err := i.r.ReadByte()
	if err != nil {
		return Event{}, err
	}
	if b != 0x1b {
		return i.decodeByte(b)
	}

	// An ESC that is not followed by anything is the Escape key. Everything
	// else is a sequence; peeking avoids blocking forever on a bare ESC only
	// when the rest has already arrived, which for a real terminal it has.
	rest, err := i.r.Peek(1)
	if err != nil || len(rest) == 0 || rest[0] != '[' {
		return Event{Kind: EventKey, Key: KeyEscape}, nil
	}
	_, _ = i.r.ReadByte() // consume '['

	next, err := i.r.Peek(1)
	if err != nil {
		return Event{Kind: EventKey, Key: KeyEscape}, nil
	}
	if next[0] == '<' {
		_, _ = i.r.ReadByte()
		return i.decodeMouse()
	}
	return i.decodeCSI()
}

// decodeByte handles a single non-escape byte.
func (i *Input) decodeByte(b byte) (Event, error) {
	switch b {
	case '\r', '\n':
		return Event{Kind: EventKey, Key: KeyEnter}, nil
	case '\t':
		return Event{Kind: EventKey, Key: KeyTab}, nil
	case 0x7f, 0x08:
		return Event{Kind: EventKey, Key: KeyBackspace}, nil
	case 0x03:
		return Event{Kind: EventKey, Key: KeyCtrlC}, nil
	case 0x04:
		return Event{Kind: EventKey, Key: KeyCtrlD}, nil
	}
	if b < 0x20 {
		return Event{Kind: EventKey, Key: KeyUnknown}, nil
	}
	if b < utf8Self {
		return Event{Kind: EventKey, Key: KeyRune, Rune: rune(b)}, nil
	}
	// A multi-byte rune: put the lead byte back and let the reader decode it
	// whole, so a non-ASCII filter keystroke is not split into mojibake.
	if err := i.r.UnreadByte(); err != nil {
		return Event{Kind: EventKey, Key: KeyUnknown}, nil
	}
	r, _, err := i.r.ReadRune()
	if err != nil {
		return Event{}, err
	}
	return Event{Kind: EventKey, Key: KeyRune, Rune: r}, nil
}

// utf8Self is the first byte value that begins a multi-byte UTF-8 sequence.
const utf8Self = 0x80

// decodeCSI handles the arrow keys, the only CSI sequences magus acts on.
func (i *Input) decodeCSI() (Event, error) {
	b, err := i.r.ReadByte()
	if err != nil {
		return Event{}, err
	}
	switch b {
	case 'A':
		return Event{Kind: EventKey, Key: KeyUp}, nil
	case 'B':
		return Event{Kind: EventKey, Key: KeyDown}, nil
	case 'C':
		return Event{Kind: EventKey, Key: KeyRight}, nil
	case 'D':
		return Event{Kind: EventKey, Key: KeyLeft}, nil
	}
	// Some other sequence. Consume to its final byte so the next Read starts
	// clean rather than interpreting this one's tail as fresh keystrokes.
	for b < 0x40 || b > 0x7e {
		if b, err = i.r.ReadByte(); err != nil {
			return Event{}, err
		}
	}
	return Event{Kind: EventKey, Key: KeyUnknown}, nil
}

// decodeMouse parses an SGR mouse report, the part after "ESC [ <":
// "button;col;row" then 'M' for a press or 'm' for a release.
func (i *Input) decodeMouse() (Event, error) {
	var body strings.Builder
	var final byte
	for {
		b, err := i.r.ReadByte()
		if err != nil {
			return Event{}, err
		}
		if b == 'M' || b == 'm' {
			final = b
			break
		}
		if b != ';' && (b < '0' || b > '9') {
			// Malformed. Report nothing rather than guessing at coordinates.
			return Event{Kind: EventKey, Key: KeyUnknown}, nil
		}
		body.WriteByte(b)
	}
	parts := strings.Split(body.String(), ";")
	if len(parts) != 3 {
		return Event{Kind: EventKey, Key: KeyUnknown}, nil
	}
	code, err1 := strconv.Atoi(parts[0])
	col, err2 := strconv.Atoi(parts[1])
	row, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return Event{Kind: EventKey, Key: KeyUnknown}, nil
	}

	ev := Event{Kind: EventMouse, Row: row, Col: col, Press: final == 'M', Clicks: 1}
	if code&mouseMotionBit != 0 {
		// Motion. A held button still rides along in the low bits, but nothing
		// here acts on a drag, so only the position is reported.
		ev.Motion = true
		ev.Press = false
		ev.Clicks = 0
		return ev, nil
	}
	switch {
	case code&mouseWheelBit != 0:
		// Wheel events report as a press with no matching release.
		if code&1 == 0 {
			ev.Button = MouseWheelUp
		} else {
			ev.Button = MouseWheelDown
		}
		return ev, nil
	case code&mouseButtonMask == 0:
		ev.Button = MouseLeft
	case code&mouseButtonMask == 1:
		ev.Button = MouseMiddle
	case code&mouseButtonMask == 2:
		ev.Button = MouseRight
	}
	if !ev.Press {
		return ev, nil
	}

	now := i.now()
	if ev.Button == i.lastButton && ev.Row == i.lastRow && ev.Col == i.lastCol &&
		now.Sub(i.lastPress) <= doubleClickWindow {
		ev.Clicks = 2
		// Reset, so a third press starts a new pair rather than reporting a
		// double click for every press in a fast series.
		i.lastPress = time.Time{}
		return ev, nil
	}
	i.lastPress, i.lastRow, i.lastCol, i.lastButton = now, ev.Row, ev.Col, ev.Button
	return ev, nil
}

// SGR button-code bits. The modifier bits (shift 4, meta 8, ctrl 16) are
// deliberately ignored: magus has no modified-click actions, and masking them
// off means a user holding ctrl still gets an ordinary click rather than
// nothing at all.
const (
	mouseButtonMask = 0b11
	mouseWheelBit   = 64
	mouseMotionBit  = 32
)
