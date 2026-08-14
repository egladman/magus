//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module term -lang buzz -out ../internal/interp/bindings/gen/term.go

func init() { Register(Term) }

// Term is the "term" host module: the terminal surface magus already renders its
// own output with, exposed so a magusfile can use it too.
//
// None of this is new machinery. internal/interactive/tty carries the picker, the
// ANSI sequences and the terminal probe that `magus status --watch` and the
// project picker are built from; until now a magusfile could reach exactly one
// piece of it, os\stdinIsTerminal.
//
// THE RULE THAT SHAPES THIS MODULE: magus does not ask. Its own doctrine is that
// the CLI reads context rather than prompting, because a prompt in a
// non-interactive run does not degrade - it HANGS, and a hung CI job reports as a
// timeout half an hour later with nothing to read. tty.Pick's own doc says the
// caller is expected to have already checked that stdin and stderr are terminals.
// "Expected to" is not a guarantee, so this module does not rely on it: pick
// verifies, and RAISES when it cannot prompt.
//
// That makes the non-interactive path something an author has to answer for,
// which is the point. The shape is:
//
//	if (term\isInteractive()) { ... term\pick(...) ... } else { ... a declared default ... }
//
// EVERYTHING RENDERS TO STDERR, matching the rest of magus: stdout carries the
// structured answer (-o json|yaml|template) alone, so a magusfile that paints a
// picker still pipes cleanly.
var Term = Module{
	Name: "term",
	Doc:  "Terminal interaction: capability probes, an interactive picker, and styled output. Renders to stderr; pick raises rather than hanging when there is no terminal.",
	Methods: []Method{
		{
			Name:    "is_interactive",
			Doc:     "Report whether this run can prompt at all: both standard input and standard error are terminals. Branch on it before calling pick - in CI, behind a pipe, or under a daemon this is false, and pick would raise. It is the one call that makes an interactive step safe to add to a target that also runs unattended.",
			Returns: []Ret{{Type: TypeBool}},
			Impl:    TermIsInteractive,
		},
		{
			Name:    "wants_color",
			Doc:     "Report whether styled output should be emitted: standard error is a terminal and the environment does not ask for plain text (NO_COLOR, TERM=dumb). colorize already consults this, so a caller needs it only to make a wider rendering choice - a box-drawing table versus a plain one.",
			Returns: []Ret{{Type: TypeBool}},
			Impl:    TermWantsColor,
		},
		{
			Name:    "size",
			Doc:     "Return the terminal's {width, height} in character cells. Both are 0 when there is no terminal to measure - piped output, no controlling terminal - so check width rather than expecting a raise. Use it to wrap or truncate output to the reader's actual window instead of assuming 80 columns.",
			Returns: []Ret{{Type: TypeAnyMap, Object: "TermSize"}},
			Impl:    TermSizeOf,
		},
		{
			Name: "colorize",
			Doc:  "Wrap s in the given style and close it again. Returns s UNCHANGED when the output is not a terminal or the environment asked for plain text, so a magusfile never has to guard the call and escape codes cannot leak into a CI log. A style of none is also pass-through, which lets a conditionally-computed style be passed without branching.",
			Args: []Arg{
				{Name: "s", Type: TypeString},
				{Name: "style", Type: TypeString, Enum: "TermStyle"},
			},
			Returns: []Ret{{Type: TypeString}},
			Impl:    TermColorize,
		},
		{
			Name: "pick",
			Doc:  "Prompt the reader to choose one of items and return its index. Type to filter (matching every whitespace-separated token), arrow keys or Ctrl-N/Ctrl-P to move, Enter to choose. RAISES when there is no terminal to prompt on - guard with is_interactive - and raises when the reader aborts with ESC, Ctrl-C or Ctrl-D, so a cancel ends the run rather than quietly returning a choice nobody made. Renders to stderr.",
			Args: []Arg{
				{Name: "items", Type: TypeStringSlice},
				{Name: "prompt", Type: TypeString, Optional: true},
				{Name: "initial_filter", Type: TypeString, Optional: true},
				{Name: "initial", Type: TypeInt, Optional: true},
				{Name: "max_rows", Type: TypeInt, Optional: true},
			},
			Returns: []Ret{{Type: TypeIndex}},
			Raises:  true,
			Impl:    TermPick,
		},
		{
			Name: "notify",
			Doc:  "Raise a notification into the band magus pins at the bottom of the terminal, where it shows for a few seconds and then disappears on its own. Unlike log.info it does not join the scrolling transcript: it is for something worth GLANCING at during a long run, not for the record. Returns immediately - the message expires on its own clock - and never raises: it is DROPPED when there is no terminal to show it on, or when the band has no room, so a piped or CI run is never given a repainted view it cannot use and no caller has to guard a notification. Log the same fact if it also needs recording. ttl_ms defaults to 5000; a negative ttl_ms pins the notification until newer ones push it out.",
			Args: []Arg{
				{Name: "message", Type: TypeString},
				{Name: "level", Type: TypeString, Enum: "LogLevel", Optional: true},
				{Name: "ttl_ms", Type: TypeInt, Optional: true},
			},
			Returns: nil,
			Impl:    TermNotify,
		},
		{
			Name:    "clear_screen",
			Doc:     "Erase the screen and move the cursor home, the repaint a full-screen refresh loop issues before redrawing. Scrollback is preserved, so a reader who scrolls up after the loop ends still sees what came before. A no-op when there is no terminal, so a watch loop needs no guard.",
			Returns: nil,
			Raises:  true,
			Impl:    TermClearScreen,
		},
	},
}

// TermIsInteractive reports whether both stdin and stderr are terminals.
//
// BOTH, not either: the picker reads keys from stdin and paints to stderr, so one
// without the other is a half-usable prompt. `magus run x < /dev/null` on a
// terminal is the case that makes this concrete - stderr is a TTY, stdin is not,
// and a prompt would block forever on a read that can never arrive.
func TermIsInteractive(_ context.Context) (bool, error) {
	return tty.StdinIsTerminal() && tty.IsTerminalWriter(os.Stderr, tty.SystemProbe), nil
}

// TermWantsColor reports whether styled output should be emitted to stderr.
func TermWantsColor(_ context.Context) (bool, error) {
	return tty.WantsColor(os.Stderr, tty.SystemProbe), nil
}

// TermSizeOf returns the terminal's dimensions, or zeroes when unmeasurable.
func TermSizeOf(_ context.Context) (types.TermSize, error) {
	fd, ok := tty.Fd(os.Stderr)
	if !ok {
		return types.TermSize{}, nil
	}
	w, h, err := tty.SystemProbe.Size(fd)
	if err != nil {
		// Not a raise: "there is no size" is an ordinary answer for a pipe, and a
		// caller that has to try/catch to lay out a line will simply not measure.
		return types.TermSize{}, nil //nolint:nilerr // documented above: an unmeasurable size is zero, not a raise; matches the size doc's "check width rather than expecting a raise"
	}
	return types.TermSize{Width: w, Height: h}, nil
}

// TermColorize wraps s in style, honouring the terminal's colour capability.
func TermColorize(_ context.Context, s, style string) (string, error) {
	if !tty.WantsColor(os.Stderr, tty.SystemProbe) {
		return s, nil
	}
	return tty.Colorize(s, tty.SGR(style)), nil
}

// TermPick prompts for a choice among items and returns the chosen index.
func TermPick(ctx context.Context, items []string, prompt, initialFilter string, initial, maxRows int) (int, error) {
	if types.Tracing(ctx) {
		// Dry run: report the first item without painting anything. A record pass
		// must never block on a human, and every other host effect is skipped the
		// same way.
		return 0, nil
	}
	if len(items) == 0 {
		return -1, errors.New("term.pick: no items to choose from")
	}
	interactive, _ := TermIsInteractive(ctx)
	if !interactive {
		// The message names the guard rather than just the condition: an author
		// hitting this in CI needs to know what to write, not only what went wrong.
		return -1, fmt.Errorf("term.pick: nothing to prompt on (standard input and standard error are not both terminals). " +
			"Guard the call with term.isInteractive() and choose a default for unattended runs")
	}
	idx, err := tty.Pick(ctx, os.Stdin, os.Stderr, tty.SystemProbe, items, tty.PickOptions{
		Prompt:        prompt,
		InitialFilter: initialFilter,
		Initial:       initial,
		MaxRows:       maxRows,
	})
	if err != nil {
		if errors.Is(err, tty.ErrAborted) {
			// An abort RAISES rather than returning -1. Ctrl-C means stop, and a
			// sentinel return would let a magusfile that forgot to check it carry on
			// with a choice the reader explicitly declined to make.
			return -1, errors.New("term.pick: cancelled")
		}
		return -1, fmt.Errorf("term.pick: %w", err)
	}
	return idx, nil
}

// defaultNotifyTTL is how long a notification shows when the caller does not
// say. Long enough to catch the eye of someone watching a build, short enough
// that three of them do not queue up behind each other.
const defaultNotifyTTL = 5 * time.Second

// TermNotify raises a notification into the process's terminal band.
//
// It does NOT take a scope. An earlier shape wrapped the call in a
// term.withNotify(fn) that reserved the rows for the duration of a callback,
// which read well in a standalone script and was wrong for the case that
// matters: a magusfile target notifying in the middle of a run does not own the
// run, cannot wrap it, and would be nesting its scope inside magus's own. The
// band is owned by the process and released on the way out (tty.ReleaseStderr),
// so a caller just says the thing.
func TermNotify(ctx context.Context, message, level string, ttlMs int) error {
	if types.Tracing(ctx) {
		// A record pass must not paint: a dry run reports what WOULD happen,
		// and a notification that flashed during it would be the one effect
		// that leaked.
		return nil
	}
	if message == "" {
		// A no-op rather than a raise. Everything else about this call is
		// best-effort - no terminal, no room, both silently drop - so failing
		// on one input would be the only way it could ever interrupt a run.
		return nil
	}
	// 0 is "the caller omitted it", because that is what the generated binding
	// passes for an absent optional int - so "pin this until something displaces
	// it" needs its own spelling, and a negative ttl is it.
	ttl := defaultNotifyTTL
	switch {
	case ttlMs > 0:
		ttl = time.Duration(ttlMs) * time.Millisecond
	case ttlMs < 0:
		ttl = 0
	}
	style := notifyStyle(types.LogLevel(level))
	if !tty.WantsColor(os.Stderr, tty.SystemProbe) {
		// NO_COLOR asks for plain text, and the band is text like any other.
		// Severity still reads from the message; it just is not coloured.
		style = ""
	}
	// The write error is deliberately dropped, which is what lets this method
	// declare no raise. A notification is a VIEW: the same rule that discards
	// it on a pipe or a full band discards it when the terminal refuses the
	// bytes. Making every magusfile wrap a notification in try/catch to handle
	// "stderr write failed" would be a tax paid on every call site for a
	// condition no author can act on.
	_ = tty.StderrNotifier().Notify(message, style, ttl)
	return nil
}

// notifyStyle maps a severity to the palette magus already renders with.
//
// LogLevel rather than a TermNotifyLevel of its own: a notification's severity
// is the same question log.at asks, and answering it twice with two enums would
// mean a magusfile author choosing between two spellings of "warn".
func notifyStyle(level types.LogLevel) tty.SGR {
	switch level {
	case types.LogError:
		return tty.SGRRed
	case types.LogWarn:
		return tty.SGRYellow
	case types.LogTrace, types.LogDebug:
		return tty.SGRDim
	default:
		// Info, and the empty level a caller who omitted the argument sends.
		// Unstyled: an ordinary notification should not compete with the
		// failures pinned above it.
		return ""
	}
}

// TermClearScreen erases the screen, or does nothing when stderr is not a terminal.
func TermClearScreen(_ context.Context) error {
	if !tty.IsTerminalWriter(os.Stderr, tty.SystemProbe) {
		return nil
	}
	return tty.ClearScreen(os.Stderr)
}
