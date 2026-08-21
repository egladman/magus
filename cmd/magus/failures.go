package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive/tty"
)

// The end-of-run failure prompt: the failures a run pinned are already on
// screen, in a band that did not scroll away, and this makes them ACTIONABLE
// rather than merely legible. Click one to select it, press enter to rerun it
// with stepping on, press o to open its captured output.
//
// It runs AFTER the run, deliberately: during a run a target's subprocess may own
// standard input, and two things in raw mode on one terminal corrupt it.
// Afterwards magus owns the terminal outright, which is also when a reader wants
// to act on a failure.
//
// The rules it keeps, in order of how badly getting them wrong hurts:
//
//   - THE WAY OUT IS ALWAYS ON SCREEN. The hint row is not decoration and is
//     not dismissable; it is the affordance. Nobody should have to remember a
//     key to leave, and nobody should end up killing the process because they
//     could not find the exit.
//   - Every plausible escape works: esc, q, and ctrl-c. Ctrl-C matters most and
//     is the easiest to get wrong: in raw mode it arrives as a byte rather than
//     a signal, so a loop that does not handle it explicitly leaves the one key
//     everybody reaches for doing nothing.
//   - A click outside the failures does nothing. It does not exit. A stray
//     click must never be the thing that ends a session.
//   - Nothing is taken that is not given back. Mouse reporting and raw mode are
//     released before any rerun and on every exit path, so the terminal a
//     reader is handed back is the one they had.

// failureBand is the part of the run's display this prompt drives.
//
// An interface for the same reason [tty.Probe] is one: the loop is interactive,
// and the alternative to a seam here is a test that needs a real terminal with
// a real run's failures already painted into it. *cache.PrettyHandler is the
// only production implementation and is expected to stay that way.
type failureBand interface {
	Failures() []cache.Failure
	HitFailure(row int) (cache.Failure, bool)
	SetSelection(n int)
	SetPreview(lines []string)
	ToggleFocus() cache.PaneFocus
}

// hintHitter resolves a click on the hint row to the action it names. Separate
// from failureBand because the band owns the failure rows and the ZONE owns the
// hint row's lease; a test drives them independently.
type hintHitter interface {
	HitSpan(row, col int) (string, bool)
}

// previewRows is how much of a failure's output the second column shows.
//
// The TAIL, not the head: a build that failed says why at the end. Ten lines is
// what fits beside a failure list on an ordinary terminal without the band
// eating the transcript it is supposed to sit under.
const previewRows = 10

// loadPreview reads the tail of a failure's captured output.
//
// Called when the selection MOVES, never from the paint path: resolving this
// opens a file, and the band repaints from a timer. An unreadable log is not an
// error - the column simply stays empty, and the ref is still printed in the
// transcript for anyone who wants the whole thing.
func loadPreview(f cache.Failure, width int) []string {
	if f.LogPath == "" {
		return nil
	}
	// The TAIL is read, not the whole file. This runs on every selection change,
	// including mouse motion across the band, and a failed build's captured log
	// is routinely tens of megabytes - so slurping it was a full read and a full
	// line-split per pointer movement, synchronously under the handler's mutex.
	fh, err := os.Open(f.LogPath)
	if err != nil {
		return nil
	}
	defer func() { _ = fh.Close() }()
	info, err := fh.Stat()
	if err != nil {
		return nil
	}
	const tailBytes = 64 << 10 // enough for previewRows of any sane line length
	at, size := int64(0), info.Size()
	if size > tailBytes {
		at = size - tailBytes
	}
	b := make([]byte, size-at)
	if _, err := fh.ReadAt(b, at); err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if at > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line is a fragment of whatever we cut into
	}
	if len(lines) > previewRows {
		lines = lines[len(lines)-previewRows:]
	}
	for i, l := range lines {
		lines[i] = tty.ClipCols(sanitizeLogLine(l), width)
	}
	return lines
}

// sanitizeLogLine makes one captured line safe to draw inside the band.
//
// The band is a BOX, so every character has to occupy the column the layout
// thinks it does. Captured output honors neither assumption:
//
//   - A TAB advances the terminal to the next 8-column stop while the layout
//     counts it as one, so the row overruns and loses its right border. This is
//     the common case, not an edge case: `go test` output is tab-delimited.
//   - A CARRIAGE RETURN returns the cursor to column 1 and the rest of the line
//     overwrites the tree, the divider and both edges. Any tool drawing a
//     progress bar emits them.
//   - Other C0 controls and stray escape sequences move the cursor or change
//     color inside a region the band believes it owns.
//
// So the line is flattened to printable text before it is ever a span. The full
// bytes remain reachable exactly where they always were: the captured log the
// output ref names.
func sanitizeLogLine(l string) string {
	var b strings.Builder
	b.Grow(len(l))
	col := 0
	for i := 0; i < len(l); {
		switch c := l[i]; {
		case c == '\t':
			// Expanded to the SAME stops the terminal would use, so the text
			// lines up as its author intended and the layout can count it.
			n := 8 - col%8
			b.WriteString(strings.Repeat(" ", n))
			col += n
			i++
		case c == 0x1b:
			// Skip an escape sequence whole rather than printing its letters.
			i++
			if i < len(l) && l[i] == '[' {
				for i++; i < len(l) && l[i] >= 0x20 && l[i] <= 0x3f; i++ {
				}
			}
			i++
		case c < 0x20 || c == 0x7f:
			i++ // CR, BS, BEL and friends: dropped, never forwarded
		default:
			r, size := utf8.DecodeRuneInString(l[i:])
			b.WriteRune(r)
			col++
			i += size
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// hintRows is the always-visible way out. One row, and it is never given up
// while the prompt is running.
const hintRows = 1

// failureAction is what the loop decided to do, returned so the caller can
// release the terminal before doing it.
type failureAction int

const (
	actionNone failureAction = iota
	actionRerun
	actionOutput
)

// promptFailures offers the run's pinned failures for rerun or inspection.
//
// It is a no-op when there is nothing to act on, when the terminal cannot be
// driven, or when the band never rendered, so a caller adds one call and does
// not branch. A non-TTY run reaches none of this.
func promptFailures(ctx context.Context, root string, h *cache.PrettyHandler) error {
	if h == nil || len(h.Failures()) == 0 || !h.RendersBand() {
		return nil
	}
	// The band was held past the summary so these failures would still be on
	// screen to act on; handing the rows back is this function's job now.
	defer func() { _ = h.ReleaseBand() }()

	selected := 0
	for {
		action, item, err := runFailurePrompt(ctx, h, &selected)
		if err != nil || action == actionNone {
			return err
		}
		// The terminal is already released here: the loop returns rather than
		// acting, so a rerun's own step gate gets a terminal nobody else is
		// holding raw.
		switch action {
		case actionRerun:
			if err := rerunStepped(ctx, root, item); err != nil {
				fmt.Fprintf(os.Stderr, "magus: %v\n", err)
			}
		case actionOutput:
			if err := showOutput(ctx, root, item); err != nil {
				fmt.Fprintf(os.Stderr, "magus: %v\n", err)
			}
		}
	}
}

// runFailurePrompt opens the terminal, drives one round of selection, and
// closes everything before returning what to do.
//
// Opening and closing per round rather than holding across an action is what
// keeps the guarantee above: whatever runs next finds a terminal in its normal
// state, and a panic anywhere in the action cannot leave mouse reporting on.
func runFailurePrompt(ctx context.Context, h failureBand, selected *int) (failureAction, cache.Failure, error) {
	in, err := tty.OpenInput(os.Stdin, os.Stderr, tty.SystemProbe)
	if err != nil {
		// Not a terminal on both ends: an ordinary answer, not a failure.
		return actionNone, cache.Failure{}, nil
	}
	hint := tty.StderrZone().Acquire(hintRows)
	defer func() {
		h.SetSelection(-1)
		_ = hint.Release()
		_ = in.Close()
	}()

	if err := showHint(hint, os.Stderr); err != nil {
		return actionNone, cache.Failure{}, err
	}
	action, item := dispatchFailureKeys(func() (tty.Event, error) { return in.Read(ctx) }, h, tty.StderrZone(), selected)
	return action, item, nil
}

// showHint puts the way out where the reader can see it, pinning it when the
// zone has room and printing it plainly when it does not.
//
// The fallback is the whole point. Everything else this package draws is a VIEW,
// correctly dropped when it cannot be pinned; this is not a view, it is the only
// statement of how to leave, and a prompt without it is what people describe
// killing the terminal to escape.
//
// A refused grant is not theoretical: the zone reserves rows only while a useful
// scrolling area remains, so a short window is enough to leave nothing for this.
//
// Printed to the scrolling transcript rather than skipped, because nothing else
// writes there while the prompt is open.
func showHint(l *tty.Lease, w io.Writer) error {
	// Composed by the package that owns the band, not here, so the row the
	// documentation renders and the row a reader sees are the same object.
	rendered, err := l.Set(cache.FailureHint())
	if err != nil {
		return err
	}
	if rendered {
		return nil
	}
	_, err = fmt.Fprintln(w, cache.FailureHintPlain())
	return err
}

// dispatchFailureKeys is the event loop, split from the terminal handling above
// so a test can feed it events without a pty.
// It returns no error on purpose. Every way this loop can end - an exhausted
// reader, a closed terminal, the user pressing escape - is an ANSWER, because
// the prompt is an offer made after the run already succeeded or failed on its
// own terms. An error return here would advertise a failure mode that cannot
// happen and force every caller to handle it.
func dispatchFailureKeys(next func() (tty.Event, error), h failureBand, hints hintHitter, selected *int) (failureAction, cache.Failure) {
	items := h.Failures()
	if len(items) == 0 {
		return actionNone, cache.Failure{}
	}
	*selected = clampIndex(*selected, len(items))
	h.SetSelection(*selected)
	showPreview(h, items, *selected)

	for {
		ev, err := next()
		if err != nil {
			return actionNone, cache.Failure{} // see the doc comment: ending is an answer
		}

		if ev.Kind == tty.EventMouse {
			// The wheel is deliberately NOT taken here, and that is not a gap in
			// the mouse support. The whole band is on screen at once, so there
			// is nothing to scroll WITHIN it - while the wheel is how a reader
			// scrolls back through the build output above, which magus never
			// takes. Binding it would trade the transcript for nothing.
			// TestFailurePromptIgnoresReleasesAndTheWheel pins this.
			if !ev.Motion && (!ev.Press || ev.Button != tty.MouseLeft) {
				continue
			}
			// A click on the row that NAMES an action takes it. One click, not
			// two: these are buttons, and the row-versus-button distinction is
			// the same one every list makes - items select, buttons activate.
			// Checked before the failure rows because the hint row is not one.
			if !ev.Motion && hints != nil {
				if key, ok := hints.HitSpan(ev.Row, ev.Col); ok {
					switch key {
					case cache.HintKeyDone:
						return actionNone, cache.Failure{}
					case cache.HintKeyRerun:
						return actionRerun, items[*selected]
					case cache.HintKeyFocus:
						h.ToggleFocus()
					case cache.HintKeyCopy:
						copyFailure(items[*selected])
					case cache.HintKeyOutput:
						return actionOutput, items[*selected]
					}
					continue
				}
			}
			hit, ok := h.HitFailure(ev.Row)
			if !ok {
				// Off the failures. Deliberately not an exit, and deliberately
				// not a change either: the highlight stays where it was rather
				// than clearing, so crossing the band on the way somewhere else
				// does not make it flicker.
				continue
			}
			i := indexOfFailure(items, hit)
			if i < 0 {
				continue
			}
			// Hovering moves the highlight, which is the whole affordance: it
			// shows what a click would act on. One highlight rather than a
			// separate hover state, so the mouse and the arrow keys cannot
			// disagree about what is selected.
			if i != *selected {
				*selected = i
				h.SetSelection(i)
				showPreview(h, items, i)
			}
			if !ev.Motion && ev.Clicks == 2 {
				return actionRerun, hit
			}
			continue
		}

		switch ev.Key {
		case tty.KeyEscape, tty.KeyCtrlC, tty.KeyCtrlD:
			return actionNone, cache.Failure{}
		case tty.KeyUp:
			*selected = clampIndex(*selected-1, len(items))
			h.SetSelection(*selected)
			showPreview(h, items, *selected)
		case tty.KeyDown:
			*selected = clampIndex(*selected+1, len(items))
			h.SetSelection(*selected)
			showPreview(h, items, *selected)
		case tty.KeyTab:
			// Focus resizes the two views: the one you are working in takes the
			// golden ratio's major share.
			h.ToggleFocus()
		case tty.KeyEnter:
			return actionRerun, items[*selected]
		case tty.KeyRune:
			switch ev.Rune {
			case 'q':
				return actionNone, cache.Failure{}
			case 'k':
				*selected = clampIndex(*selected-1, len(items))
				h.SetSelection(*selected)
				showPreview(h, items, *selected)
			case 'j':
				*selected = clampIndex(*selected+1, len(items))
				h.SetSelection(*selected)
				showPreview(h, items, *selected)
			case 'y':
				copyFailure(items[*selected])
			case 'o':
				return actionOutput, items[*selected]
			}
		}
	}
}

// copyFailure puts the selected failure's captured output on the system
// clipboard, and says so where the reader is looking.
//
// The answer to "the two columns cannot be drag-selected" - they cannot, and no
// arrangement of them can be, because terminals select linearly. So nobody is asked
// to select: the text goes to the clipboard exactly as the tool emitted it, through
// a sequence that survives ssh and tmux.
//
// A failed copy is a notification rather than an error: a terminal that refuses OSC
// 52 is not a broken run, and `o` still prints the whole thing into the transcript.
func copyFailure(f cache.Failure) {
	text := strings.Join(loadPreview(f, previewWidth), "\n")
	if raw, err := os.ReadFile(f.LogPath); err == nil {
		text = string(raw) // the WHOLE log, not the tail the band happens to show
	}
	n, err := tty.Copy(os.Stderr, text)
	msg := fmt.Sprintf("copied %d bytes of %s %s to the clipboard", n, f.Project, f.Target)
	if err != nil || n == 0 {
		msg = "could not reach the clipboard; press o to print the output instead"
	}
	_ = tty.StderrNotifier().Notify(msg, tty.SGRGreen, 4*time.Second)
}

// showPreview puts the selected failure's output in the band's second column.
// A band that cannot take one (no previewSetter) simply keeps one column.
func showPreview(h failureBand, items []cache.Failure, at int) {
	if at < 0 || at >= len(items) {
		return
	}
	h.SetPreview(loadPreview(items[at], previewWidth))
}

// previewWidth bounds a preview line before it reaches the band, so a log with
// very long lines cannot push the column arithmetic around.
const previewWidth = 120

// indexOfFailure finds a hit in the list the loop is driving, or -1. Matched on
// identity rather than position because the band's ring and this list are
// ordered differently once the ring has wrapped.
func indexOfFailure(items []cache.Failure, hit cache.Failure) int {
	for i, item := range items {
		if item.Target == hit.Target && item.Project == hit.Project {
			return i
		}
	}
	return -1
}

// clampIndex keeps a selection inside the list, stopping at the ends rather
// than wrapping. A list this short is read as a list, not a carousel: wrapping
// from the last failure back to the first reads as the selection having been
// lost.
func clampIndex(i, n int) int {
	if n == 0 {
		return 0
	}
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// rerunStepped reruns one failed target with stepping on, through the same
// entry point `magus run <target> <project> --step` uses.
//
// In-process rather than by forking a magus: a subprocess would need the
// terminal handed to it and its exit status interpreted, and the flags are
// already parsed by the function being called.
func rerunStepped(ctx context.Context, root string, f cache.Failure) error {
	args := []string{f.Target}
	if f.Project != "" {
		args = append(args, f.Project)
	}
	args = append(args, "--step")
	fmt.Fprintf(os.Stderr, "\n-> magus run %s --step\n", strings.Join(args[:len(args)-1], " "))
	return runTarget(ctx, root, runConfig{}, args)
}

// showOutput prints a failure's captured output into the SCROLLING transcript,
// rather than opening a view of it.
//
// That is the whole point. A pager or a pane is a place a reader can get stuck
// in and have to find their way out of; printed output is just output, and
// going back is scrolling, which needs no affordance and cannot trap anyone.
// The pinned band stays where it was the entire time, so the run's failures are
// still on screen underneath it.
func showOutput(ctx context.Context, root string, f cache.Failure) error {
	if f.OutputRef == "" {
		return fmt.Errorf("no captured output for %s", f.Target)
	}
	return queryOutputRef(ctx, root, f.OutputRef, outputRefOpts{})
}
