package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive/tty"
)

// The end-of-run failure prompt: the failures a run pinned are already on
// screen, in a band that did not scroll away, and this makes them ACTIONABLE
// rather than merely legible. Click one to select it, press enter to rerun it
// with stepping on, press o to open its captured output.
//
// It runs AFTER the run, deliberately, and that is not a limitation dressed up
// as a decision. During a run a target's subprocess may own standard input, and
// two things in raw mode on one terminal corrupt it - the step gate already
// documents that in its own comment. Afterwards magus owns the terminal
// outright, which is also exactly when a reader wants to act on a failure.
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
		action, item, err := runFailurePrompt(h, &selected)
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
func runFailurePrompt(h failureBand, selected *int) (failureAction, cache.Failure, error) {
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
	return dispatchFailureKeys(in.Read, h, selected)
}

// The instruction, in the two forms it may have to take.
const (
	hintLeft  = "click a failure, or [up/down] select   [enter] rerun stepped   [o] output"
	hintRight = "[esc] done"
)

// showHint puts the way out where the reader can see it, pinning it when the
// zone has room and printing it plainly when it does not.
//
// The fallback is the whole point, and its absence was a bug. Every other thing
// this package draws is a VIEW, and a view that cannot be pinned is correctly
// dropped - replaying it into a pipe would be noise. This is not a view. It is
// the only statement of how to leave, and a prompt that opens without it is the
// exact situation people describe killing the terminal to escape.
//
// A refused grant is not rare or theoretical: the zone reserves rows only while
// a useful scrolling area remains, so a short window, or a run that already
// pinned failures and a notification, is enough to leave nothing for this.
//
// Printed to the scrolling transcript rather than skipped, because nothing else
// writes there while the prompt is open, so it stays on screen regardless.
func showHint(l *tty.Lease, w io.Writer) error {
	// Pinned, the way out sits at the RIGHT edge so it is the last thing
	// clipped rather than the first. As one string this row was 86 columns and
	// an 80-column terminal cut it to exactly "[esc] do".
	rendered, err := l.Set([]tty.Line{{Spans: []tty.Span{
		{Text: hintLeft, Style: tty.SGRDim},
		{Text: hintRight, Style: tty.SGRDim, Align: tty.AlignRight},
	}}})
	if err != nil {
		return err
	}
	if rendered {
		return nil
	}
	_, err = fmt.Fprintf(w, "%s   %s\n", hintLeft, hintRight)
	return err
}

// dispatchFailureKeys is the event loop, split from the terminal handling above
// so a test can feed it events without a pty.
func dispatchFailureKeys(next func() (tty.Event, error), h failureBand, selected *int) (failureAction, cache.Failure, error) {
	items := h.Failures()
	if len(items) == 0 {
		return actionNone, cache.Failure{}, nil
	}
	*selected = clampIndex(*selected, len(items))
	h.SetSelection(*selected)

	for {
		ev, err := next()
		if err != nil {
			// A closed or exhausted terminal ends the prompt rather than
			// failing the command: the run itself already succeeded or failed
			// on its own terms, and this was an offer.
			return actionNone, cache.Failure{}, nil
		}

		if ev.Kind == tty.EventMouse {
			if !ev.Motion && (!ev.Press || ev.Button != tty.MouseLeft) {
				continue
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
			}
			if !ev.Motion && ev.Clicks == 2 {
				return actionRerun, hit, nil
			}
			continue
		}

		switch ev.Key {
		case tty.KeyEscape, tty.KeyCtrlC, tty.KeyCtrlD:
			return actionNone, cache.Failure{}, nil
		case tty.KeyUp:
			*selected = clampIndex(*selected-1, len(items))
			h.SetSelection(*selected)
		case tty.KeyDown:
			*selected = clampIndex(*selected+1, len(items))
			h.SetSelection(*selected)
		case tty.KeyEnter:
			return actionRerun, items[*selected], nil
		case tty.KeyRune:
			switch ev.Rune {
			case 'q':
				return actionNone, cache.Failure{}, nil
			case 'k':
				*selected = clampIndex(*selected-1, len(items))
				h.SetSelection(*selected)
			case 'j':
				*selected = clampIndex(*selected+1, len(items))
				h.SetSelection(*selected)
			case 'o':
				return actionOutput, items[*selected], nil
			}
		}
	}
}

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
