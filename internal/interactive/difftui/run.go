package difftui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
)

// Sync is the human's half of the session: where they are looking, and what they have read.
//
// There is deliberately no read side. A cursor coming BACK from the session would be some
// other client moving this reader's viewport, and a viewport that moves on its own is the
// one thing the whole suggestion design exists to prevent - see types.DiffSuggestion. An
// agent suggests; only the person at the keyboard navigates.
//
// No method reports an error, and that is the contract rather than an omission: a
// coordination write that failed must not interrupt somebody reading a diff.
type Sync interface {
	SetCursor(c types.DiffCursor)
	SetViewed(digest string, on bool)
	// SetThreadsSeen records that these of the host's review threads have been in front of the
	// reader, which is the watermark deciding what still counts as NEW. A workspace whose
	// watermark nobody ever advances is one the forge-watching job reads as never reviewed in,
	// and it then reports no colleague's remark at all - so a terminal reader has to write it
	// for the same reason a browser one does.
	SetThreadsSeen(ids []string)
}

// Options is one interactive session.
type Options struct {
	// In is read for keys and Out is drawn on. Both must be terminals; the caller has
	// already refused otherwise, and OpenInput refuses again rather than trusting that.
	In    *os.File
	Out   io.Writer
	Probe tty.Probe
	Input Input
	// Sync is nil when nothing is listening - no daemon, no console, no agent.
	Sync Sync
	// Summary is the one line left behind in the scrollback when the reader quits, so the
	// session records what was read rather than vanishing without a trace. It is called at that
	// moment and handed the fold the reader left in, because `.` changes what the line has to
	// describe. Nil leaves nothing behind.
	Summary func(unfolded bool) string
}

// defaultHeight is what a terminal that will not report its size is assumed to be. The CLI's
// gate refuses a stdout that is not a terminal at all - the same descriptor this is drawn on -
// so what reaches here is a terminal whose size query failed, and a viewer that drew nothing
// would be indistinguishable from a hang.
const defaultHeight = 24

// Run draws the changeset and reads keys until the reader quits.
//
// It never touches the alternate screen buffer: the transcript above stays where it is and
// survives the session, which is the rule every interactive surface in magus follows.
func Run(ctx context.Context, opts Options) error {
	input, err := tty.OpenInput(opts.In, opts.Out, opts.Probe)
	if err != nil {
		return err
	}
	m := New(opts.Input)
	// Ordered so the terminal is handed back before anything is printed on it, and so both
	// happen on EVERY exit path - a return, a cancelled context, or a panic unwinding through
	// here. Leaving raw mode set would hand the reader a shell with no echo.
	defer func() {
		if opts.Summary != nil {
			fmt.Fprintln(opts.Out, opts.Summary(m.Unfolded()))
		}
	}()
	view := tty.NewInlineView(opts.Out, opts.Probe)
	defer func() { _ = view.Clear() }()
	defer func() { _ = input.Close() }()

	if opts.Sync != nil {
		opts.Sync.SetCursor(m.cursor())
	}

	// Asked once: nothing it reads - the descriptor, TERM, NO_COLOR - changes while the viewer
	// holds the terminal, and this is the single gate for every escape the frame carries.
	color := tty.WantsColor(opts.Out, opts.Probe)

	for {
		m.resize(viewportRows(opts.Out, opts.Probe, Chrome(m)))
		if !view.Paint(Frame(m, color)) {
			return errors.New("magus diff: the terminal is too short to draw the changeset")
		}
		// AFTER the paint, because the claim being made is that these remarks were on the reader's
		// screen. TakeShownThreads reports each one once, so this costs a keypress nothing on
		// every frame that exposed nothing new.
		if opts.Sync != nil {
			if ids := m.takeShownThreads(); len(ids) > 0 {
				opts.Sync.SetThreadsSeen(ids)
			}
		}
		ev, err := input.Read(ctx)
		if err != nil {
			// A cancelled context is the reader ending the session with Ctrl-C at the shell,
			// which is what they asked for rather than a failure - the same reading `magus diff
			// --watch` takes of its own interrupt.
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if quit := apply(m, ev, opts.Sync); quit {
			return nil
		}
	}
}

// wheelRows is how far one notch of the wheel moves the viewport. Three lines is what terminal
// emulators scroll by themselves, so the gesture costs the same here as it does in the
// transcript above.
const wheelRows = 3

// apply folds one event into the model and tells the session what moved. It reports whether the
// reader asked to leave.
func apply(m *Model, ev tty.Event, sync Sync) (quit bool) {
	if ev.Kind != tty.EventKey {
		// The wheel scrolls, which is a DELIBERATE divergence from the picker: that surface leaves
		// the wheel to the terminal so a reader keeps their own scrollback, and it can afford to
		// because it is open for seconds. This one holds the terminal - and therefore the
		// scrollback the wheel would otherwise reach - for as long as it takes to read a
		// changeset, so refusing the gesture does not preserve scrolling, it removes it.
		//
		// Every other mouse event is dropped rather than guessed at: they are reported only
		// because opening the input turns tracking on.
		switch ev.Button {
		case tty.MouseWheelUp:
			m.scroll(-wheelRows)
		case tty.MouseWheelDown:
			m.scroll(wheelRows)
		}
		return false
	}
	moved := false
	switch ev.Key {
	case tty.KeyRune:
		switch ev.Rune {
		case 'q':
			return true
		case ']':
			moved = m.nextHunk()
		case '[':
			moved = m.prevHunk()
		case '}':
			moved = m.nextFile()
		case '{':
			moved = m.prevFile()
		case 'v':
			if change, ok := m.toggleViewed(); ok && sync != nil {
				sync.SetViewed(change.Digest, change.On)
			}
		case '.':
			m.toggleGenerated()
		case 'n':
			// n for what is NEW to this reader: the files a receipt does not already cover at
			// their current content. Folded by default, so the second pass opens on the work.
			m.toggleSettled()
		}
	case tty.KeyCtrlC, tty.KeyCtrlD:
		return true
	case tty.KeyEscape:
		m.toggleOverview()
	case tty.KeyEnter:
		if m.Overview() {
			m.overviewEnter()
			moved = true
		}
	case tty.KeyUp:
		if m.Overview() {
			m.overviewMove(-1)
		} else {
			m.scroll(-1)
		}
	case tty.KeyDown:
		if m.Overview() {
			m.overviewMove(1)
		} else {
			m.scroll(1)
		}
	case tty.KeyPageUp, tty.KeyCtrlP:
		m.page(-1)
	case tty.KeyPageDown, tty.KeyCtrlN:
		m.page(1)
	}
	if moved && sync != nil {
		sync.SetCursor(m.cursor())
	}
	return false
}

// viewportRows is how many changeset rows fit, leaving the frame strictly shorter than the
// terminal - a block as tall as the screen has nowhere to walk back to and would eat the
// transcript above it on the next redraw.
func viewportRows(out io.Writer, p tty.Probe, chrome int) int {
	height := defaultHeight
	if fd, ok := tty.Fd(out); ok {
		if _, h, err := p.Size(fd); err == nil && h > 0 {
			height = h
		}
	}
	if rows := height - 1 - chrome; rows > 1 {
		return rows
	}
	return 1
}
