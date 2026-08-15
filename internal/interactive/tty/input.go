package tty

import (
	"bufio"
	"context"
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
	// KeyCtrlN and KeyCtrlP are the emacs-style down/up an incremental
	// selector is expected to answer to, by anyone who has used one.
	KeyCtrlN
	KeyCtrlP
	// KeyCtrlU clears a line of input.
	KeyCtrlU
	// KeyPageUp and KeyPageDown page a bounded viewport. Appended rather than filed beside
	// the arrows so the constants above keep the values they already have.
	KeyPageUp
	KeyPageDown
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
	// eventCursor is a terminal REPLY, not something the user did: the answer
	// to a cursor-position query. It never reaches a Read caller.
	eventCursor
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
// The bare motion is what lets a row highlight as the pointer crosses it, so a
// clickable thing LOOKS clickable. (1002 reports motion only while a button is
// down, which is drag, not hover.)
//
// The cost is why this is scoped: 1003 sends an event per cell crossed. It is
// only on while magus owns the terminal, and a motion that changes no highlight
// repaints nothing, so the traffic ends at the parser.
//
// 1006 is SGR extended coordinates and is NOT optional: the original encoding
// packs each coordinate into one byte offset by 32, so it cannot address past
// column 223 and the right-hand columns of a wide terminal never report.
const (
	mouseTrackAny   = "\x1b[?1003h\x1b[?1006h"
	mouseTrackClick = "\x1b[?1000h\x1b[?1006h"
	mouseTrackOff   = "\x1b[?1006l\x1b[?1003l\x1b[?1000l"
	// DSR 6 - ask the terminal to report the cursor position, answered with
	// CPR ("ESC [ row ; col R").
	dsrCursor = "\x1b[6n"
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
// [Input.Close] restores a session this process knows it opened; this is the
// process-exit counterpart, and it is not redundant - a deferred Close does not
// run on os.Exit or a SIGKILL, and a process that dies with tracking on leaves
// the user unable to select text in their own shell with nothing explaining why.
//
// Disabling a mode that was never enabled is a no-op in every terminal, so this
// is safe on every exit path. Unlike DECSTBM these sequences do not move the
// cursor, so they need no save/restore bracket.
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
// IT IS SCOPED ON PURPOSE: enabling mouse reporting takes drag-to-select and the
// scroll wheel away from the terminal emulator. magus's contract is that ordinary
// output stays ordinary, so tracking is on only at the moments magus genuinely
// owns the terminal and off the instant it does not.
//
// Raw mode is set on the descriptor that is actually READ - the step gate calls
// MakeRaw on stderr then reads stdin, which works only because both usually point
// at the same device.
//
// Not safe for concurrent use: one goroutine reads.
type Input struct {
	r       *bufio.Reader
	in      *os.File
	out     io.Writer
	restore func() error
	closed  bool
	// pending holds user events decoded while waiting for a query reply. A
	// keystroke that arrives mid-query is still a keystroke and must not be
	// dropped just because it overtook the terminal's answer.
	pending []Event

	// lastPress is the previous press, for timing a double click.
	lastPress  time.Time
	lastRow    int
	lastCol    int
	lastButton MouseButton
	// now is the clock, injected so double-click timing is testable.
	now func() time.Time
	// deadlines records whether in accepts a read deadline, probed once at
	// open. A pipe (every test that is not a pty) does not, and Read falls back
	// to a blocking wait there rather than refusing to work.
	deadlines bool
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
	// Probed once rather than per Read: the answer is a property of the
	// descriptor, and a failed SetReadDeadline on every poll would be its own
	// cost. Clearing it immediately leaves no deadline in force.
	deadlines := in.SetReadDeadline(time.Time{}) == nil
	return &Input{r: bufio.NewReader(in), in: in, out: out, restore: restore,
		now: time.Now, deadlines: deadlines}, nil
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

// readPoll is how long Read parks in one syscall before looking at ctx again.
// Short enough that a cancelled run gives the terminal back promptly, long
// enough that an idle prompt is not a spin loop.
const readPoll = 50 * time.Millisecond

// Read blocks until the user does something, and reports it. It returns
// ctx.Err() when ctx is cancelled first.
//
// Cancellation is by DEADLINE, not by closing the descriptor: standard input
// belongs to the process, and a prompt that closed it would take the shell's
// stdin with it. So the wait for the first byte is a poll under a short read
// deadline, and ctx is checked between polls - the mechanism [Input.CursorPosition]
// already relies on, applied to the wait a person can sit in indefinitely.
//
// The deadline covers ONLY the wait for a sequence's first byte. Once one has
// arrived the rest is read without one, because a terminal writes an escape
// sequence in a single write and a deadline firing mid-sequence would leave its
// tail to be decoded as stray keystrokes - an arrow key arriving as a bracket.
//
// On a descriptor that cannot take a deadline the wait is an ordinary blocking
// read, so behaviour is unchanged where cancellation was never available.
func (i *Input) Read(ctx context.Context) (Event, error) {
	for {
		if err := i.waitForInput(ctx); err != nil {
			return Event{}, err
		}
		ev, err := i.read()
		if err != nil {
			return Event{}, err
		}
		if ev.Kind == eventCursor {
			// A reply nobody asked for, or one that outlived its query. It is
			// not input; drop it rather than handing a caller a phantom key.
			continue
		}
		return ev, nil
	}
}

// waitForInput blocks until a byte is available to decode, ctx is done, or the
// descriptor fails. It is a no-op when something is already queued or buffered,
// which is what keeps the deadline off the middle of an escape sequence.
func (i *Input) waitForInput(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(i.pending) > 0 || i.r.Buffered() > 0 {
			return nil
		}
		if !i.deadlines {
			// No deadline support, so there is nothing to poll with: fall back
			// to the blocking read this method replaced. ctx was checked above,
			// which is all that can be offered here.
			return nil
		}
		if err := i.in.SetReadDeadline(time.Now().Add(readPoll)); err != nil {
			// The descriptor stopped accepting deadlines. Record it and take
			// the no-deadline path on the next turn rather than failing a
			// prompt over a capability probe - losing cancellation is the
			// documented degradation here, and it is not this caller's error.
			i.deadlines = false
			continue
		}
		_, err := i.r.Peek(1)
		_ = i.in.SetReadDeadline(time.Time{})
		if err == nil {
			return nil
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			continue // nobody typed; look at ctx and wait again
		}
		return err
	}
}

// read returns the next event, taking queued ones first. Includes replies;
// callers of the exported Read never see those.
func (i *Input) read() (Event, error) {
	if len(i.pending) > 0 {
		ev := i.pending[0]
		i.pending = i.pending[1:]
		return ev, nil
	}
	return i.decode()
}

// decode reads one event straight off the terminal, bypassing the queue.
//
// The distinction matters exactly once, and getting it wrong hangs: a caller
// that queues what it reads must not read from the queue, or it re-consumes
// its own output forever. [Input.CursorPosition] is that caller.
func (i *Input) decode() (Event, error) {
	b, err := i.r.ReadByte()
	if err != nil {
		return Event{}, err
	}
	if b != 0x1b {
		return i.decodeByte(b)
	}

	// An ESC with nothing behind it is the Escape key. The check is on what has
	// ALREADY ARRIVED, because Peek on an empty buffer issues a read and blocks
	// - so a lone ESC would sit there doing nothing until the user pressed
	// another key, which is the one keypress a reader tries when they want out.
	// A real escape sequence arrives from the terminal in a single write, so
	// its remainder is buffered by the time the ESC is decoded.
	if i.r.Buffered() == 0 {
		return Event{Kind: EventKey, Key: KeyEscape}, nil
	}
	// The error is discarded rather than suppressed with a lint directive: the
	// check above established Buffered() > 0, and bufio.Reader.Peek(1) with a
	// byte already buffered returns it without filling, so it cannot fail here.
	// The length guard stays as the cheap way to keep the index below safe if
	// that precondition ever moves.
	rest, _ := i.r.Peek(1)
	if len(rest) == 0 {
		return Event{Kind: EventKey, Key: KeyEscape}, nil
	}
	// SS3 - "ESC O <final>" - is how F1 to F4 and the application-mode arrows
	// arrive. It has to be recognised even though magus binds none of them,
	// because the alternative is not "the key does nothing": ESC followed by
	// anything used to fall through to KeyEscape below, and KeyEscape is the
	// key that QUITS. Pressing F1 in the picker closed it, then its "O" and "P"
	// were read as two filter keystrokes.
	if rest[0] == 'O' {
		_, _ = i.r.ReadByte() // consume 'O'
		// Consume the final byte too, or it becomes a stray keystroke.
		if _, err := i.r.ReadByte(); err != nil {
			return Event{}, err
		}
		return Event{Kind: EventKey, Key: KeyUnknown}, nil
	}
	if rest[0] != '[' {
		// Alt-<key> is sent as ESC then the key. magus binds no Alt chord, so
		// the keystroke is unknown - which callers IGNORE - rather than escape,
		// which would QUIT.
		//
		// The key is consumed rather than left to decode as itself, because
		// Alt-x is not x and typing an "x" into the picker's filter is a
		// surprise the user cannot connect to what they pressed. That follows
		// the assumption the Buffered() check above already makes: bytes
		// sitting behind an ESC came from the terminal as one sequence.
		if _, err := i.r.ReadByte(); err != nil {
			return Event{}, err
		}
		return Event{Kind: EventKey, Key: KeyUnknown}, nil
	}
	_, _ = i.r.ReadByte() // consume '['

	next, err := i.r.Peek(1)
	if err != nil {
		// PROPAGATED, not decoded as a keystroke. Past the '[' the buffer may be
		// empty, so this Peek can issue a real read against CursorPosition's
		// 250ms deadline, and a reply truncated at "ESC [" arrives as
		// os.ErrDeadlineExceeded.
		//
		// Returning KeyEscape queued a fabricated keystroke in i.pending, which
		// discardBuffered does not drop, so the phantom outlived the query and
		// closed the picker with the user having pressed nothing.
		//
		// KeyEscape is never a safe default for "we do not know": callers IGNORE
		// KeyUnknown and QUIT on KeyEscape.
		return Event{}, err
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
	case 0x0e:
		return Event{Kind: EventKey, Key: KeyCtrlN}, nil
	case 0x10:
		return Event{Kind: EventKey, Key: KeyCtrlP}, nil
	case 0x15:
		return Event{Kind: EventKey, Key: KeyCtrlU}, nil
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
		// Propagated. This cannot happen today - the only caller reaches here
		// straight after a successful ReadByte with no intervening Peek - but
		// if it ever does, the lead byte is already consumed and its
		// continuation bytes are still in the stream, so the next decode reads
		// them as fresh input. That is a parser desync, not a keystroke, and
		// reporting it as one would hide it.
		return Event{}, err
	}
	r, _, err := i.r.ReadRune()
	if err != nil {
		return Event{}, err
	}
	return Event{Kind: EventKey, Key: KeyRune, Rune: r}, nil
}

// utf8Self is the first byte value that begins a multi-byte UTF-8 sequence.
const utf8Self = 0x80

// decodeCSI handles the arrow keys and the two paging keys, the only CSI sequences magus
// acts on.
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
	// Collect the rest of the sequence up to its final byte, so the next read
	// starts clean rather than interpreting this one's tail as fresh keystrokes.
	var params []byte
	for b < 0x40 || b > 0x7e {
		params = append(params, b)
		if b, err = i.r.ReadByte(); err != nil {
			return Event{}, err
		}
	}
	if b == 'R' {
		// CPR: the reply to a cursor-position query, "row;col".
		if row, col, ok := twoInts(string(params)); ok {
			return Event{Kind: eventCursor, Row: row, Col: col}, nil
		}
	}
	if b == '~' {
		// The tilde family is numbered rather than lettered. A MODIFIED page key carries its
		// modifier in a second parameter ("5;2~") and stays unknown: magus binds no chord, and
		// an unknown key is ignored rather than acted on as the bare one.
		switch string(params) {
		case "5":
			return Event{Kind: EventKey, Key: KeyPageUp}, nil
		case "6":
			return Event{Kind: EventKey, Key: KeyPageDown}, nil
		}
	}
	return Event{Kind: EventKey, Key: KeyUnknown}, nil
}

// discardBuffered drops whatever has already arrived but not been decoded.
//
// Only correct after abandoning a query: the bytes in flight are the reply that
// timed out, or the remains of one, and interpreting them later is worse than
// losing them. Never call it while ordinary input is expected.
func (i *Input) discardBuffered() {
	if n := i.r.Buffered(); n > 0 {
		_, _ = i.r.Discard(n)
	}
}

// twoInts parses "a;b".
func twoInts(s string) (int, int, bool) {
	a, b, ok := strings.Cut(s, ";")
	if !ok {
		return 0, 0, false
	}
	x, err1 := strconv.Atoi(a)
	y, err2 := strconv.Atoi(b)
	return x, y, err1 == nil && err2 == nil
}

// cprTimeout bounds the wait for a cursor-position reply.
//
// It exists because the failure mode is a HANG. A terminal that does not
// implement the query simply says nothing, and a blocking read on it never
// returns - so an interactive surface that asked would freeze with no output
// and no way out, which is the worst thing in this package's power to do. A
// terminal that does answer does so in microseconds, so this is generous.
const cprTimeout = 250 * time.Millisecond

// CursorPosition asks the terminal where the cursor is, in absolute 1-based
// terminal coordinates.
//
// This is what makes a mouse usable on an INLINE view: a [Zone] band can be
// hit-tested because magus chose the rows it drew on, but a view rendered where
// the cursor happened to be knows its shape and not its position. The terminal is
// the only thing that knows.
//
// Returns ok=false when the terminal does not answer in time, which is an
// ordinary answer: the caller keeps its keyboard handling and goes without
// mouse support rather than failing.
func (i *Input) CursorPosition() (row, col int, ok bool) {
	if _, err := io.WriteString(i.out, dsrCursor); err != nil {
		return 0, 0, false
	}
	if err := i.in.SetReadDeadline(time.Now().Add(cprTimeout)); err != nil {
		// Deadlines are unavailable on this descriptor, so a query could block
		// forever. Refuse rather than risk it.
		return 0, 0, false
	}
	defer func() { _ = i.in.SetReadDeadline(time.Time{}) }()

	for {
		// decode, not read: this loop APPENDS to the queue, so reading from it
		// would consume its own output and never terminate.
		ev, err := i.decode()
		if err != nil {
			// The deadline can fire part-way through a sequence, leaving its
			// tail in the buffer for the next decode to read as fresh
			// keystrokes - an arrow key arriving as a stray bracket. Whatever
			// is buffered belongs to a reply nobody is waiting for any more.
			i.discardBuffered()
			return 0, 0, false
		}
		if ev.Kind == eventCursor {
			return ev.Row, ev.Col, true
		}
		// Something the user did, which overtook the reply. Queue it: a
		// keystroke is not less real for having arrived at an awkward moment.
		i.pending = append(i.pending, ev)
	}
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
		//nolint:nilerr // a malformed mouse report is an unknown key; the stream is still fine
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
