//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/termkit/notify"
	"github.com/egladman/magus/libs/termkit/pick"
	"github.com/egladman/magus/libs/termkit/progress"
	"github.com/egladman/magus/types"
)

// Term is the "term" host module: the terminal renderer magus already draws its
// own output with, exposed so a magusfile can use it too.
//
// None of this is new machinery. internal/interactive/tty carries the picker,
// the ANSI sequences and the terminal probe that status --watch is built
// from. Until now a magusfile could reach one piece, os\stdinIsTerminal.
//
// THE RULE THAT SHAPES THIS MODULE: magus does not ask. Its own design principles say
// the CLI reads context rather than prompting, because a prompt in a
// non-interactive run does not degrade: it HANGS, and a hung CI job reports as a
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
	Doc:  "Terminal interaction: capability probes, prompts (pick, pickMany, confirm), a spinner, and styled output. Renders to stderr; every prompt and the spinner raise rather than hanging when there is no terminal.",
	Methods: []Method{
		{
			Name:    "is_interactive",
			Doc:     "Report whether this run can prompt at all: both standard input and standard error are terminals. Branch on it before calling pick - in CI, behind a pipe, or under a server this is false, and pick would raise. It is the one call that makes an interactive step safe to add to a target that also runs unattended.",
			Returns: []Ret{{}},
			Impl:    TermIsInteractive,
		},
		{
			Name:    "wants_color",
			Doc:     "Report whether styled output should be emitted: standard error is a terminal and the environment does not ask for plain text (NO_COLOR, TERM=dumb). colorize already consults this, so a caller needs it only to make a wider rendering choice - a box-drawing table versus a plain one.",
			Returns: []Ret{{}},
			Impl:    TermWantsColor,
		},
		{
			Name:    "size",
			Doc:     "Return the terminal's {width, height} in character cells. Both are 0 when there is no terminal to measure - piped output, no controlling terminal - so check width rather than expecting a raise. Use it to wrap or truncate output to the reader's actual window instead of assuming 80 columns.",
			Returns: []Ret{{Object: "TermSize"}},
			Impl:    TermSize,
		},
		{
			Name: "colorize",
			Doc:  "Wrap s in the given style and close it again. Returns s UNCHANGED when the output is not a terminal or the environment asked for plain text, so a magusfile never has to guard the call and escape codes cannot leak into a CI log. A style of none is also pass-through, which lets a conditionally-computed style be passed without branching.",
			Args: []Arg{
				{Name: "s"},
				{Name: "style", Enum: "TermStyle"},
			},
			Returns: []Ret{{}},
			Impl:    TermColorize,
		},
		{
			Name: "pick",
			Doc:  "Prompt the reader to choose one of items and return its index. Type to filter (matching every whitespace-separated token), arrow keys or Ctrl-N/Ctrl-P to move, Enter to choose. RAISES when there is no terminal to prompt on - guard with is_interactive - and raises when the reader aborts with ESC, Ctrl-C or Ctrl-D, so a cancel ends the run rather than quietly returning a choice nobody made. Renders to stderr. query, when given, replaces the filter with a live lookup: it is called with the filter text on first paint and after every keystroke and returns the strings to show, best first, so a picker can search something too large to list up front. items may then be empty, the index returned is into the list query returned last, and a query that raises ends the prompt with its error.",
			Args: []Arg{
				{Name: "items"},
				{Name: "prompt", Optional: true},
				{Name: "initialFilter", Optional: true},
				{Name: "initial", Optional: true},
				{Name: "maxRows", Optional: true},
				{Name: "query", Optional: true},
			},
			Returns: []Ret{{}},
			Raises:  true,
			Impl:    TermPick,
		},
		{
			Name: "pick_many",
			Doc:  "Prompt the reader to mark any number of items and return the marked ones, in the order items lists them. Type to filter as pick does; Tab marks or unmarks the highlighted row, and a mark survives a change of filter. Enter confirms, and does nothing while nothing is marked, so the highlighted row is never taken for a choice. marked lists the items marked on first paint. Items must be distinct, since the answer is the items themselves, and an entry of marked that is not one of items raises. RAISES when there is no terminal to prompt on - guard with is_interactive - and when the reader aborts with ESC, Ctrl-C or Ctrl-D. Renders to stderr.",
			Args: []Arg{
				{Name: "items"},
				{Name: "prompt", Optional: true},
				{Name: "initialFilter", Optional: true},
				{Name: "marked", Optional: true},
				{Name: "maxRows", Optional: true},
			},
			Returns: []Ret{{}},
			Raises:  true,
			Impl:    TermPickMany,
		},
		{
			Name: "confirm",
			Doc:  "Ask the reader a yes or no question and return true only for yes. Ask once per run, just before a step that cannot be taken back or that reaches outside the machine (a delete, a push, an opened pull request), and print what the step will do before asking: a confirm between read-only steps trains the reader to answer without reading. no is highlighted first, so Enter alone declines. RAISES when there is no terminal to ask on - guard with is_interactive, and let the flag that already says yes (--apply, --push) answer for an unattended run - and raises when the reader aborts with ESC, Ctrl-C or Ctrl-D. Renders to stderr.",
			Args: []Arg{
				{Name: "question"},
			},
			Returns: []Ret{{}},
			Raises:  true,
			Impl:    TermConfirm,
		},
		{
			Name: "spin",
			Doc:  "Call run and return what it returns, while a loader turns beside label on standard error, so a person watching a long blocking step (a subprocess, a survey of the disk) can tell it from a hang. The loader is erased when run returns or raises, and run's raise is raised again unchanged. Anything run writes to standard error lands under the loader's line, so give it quiet work, such as a proc call whose output is captured. RAISES without calling run when there is no terminal to draw on - guard with is_interactive and call run directly for an unattended run.",
			Args: []Arg{
				{Name: "label"},
				{Name: "run"},
			},
			Returns: []Ret{{}},
			Raises:  true,
			Impl:    TermSpin,
		},
		{
			Name: "notify",
			Doc:  "Raise a notification into the band magus pins at the bottom of the terminal, where it shows for a few seconds and then disappears on its own. Unlike log.info it does not join the scrolling transcript: it is for something worth GLANCING at during a long run, not for the record. Returns immediately - the message expires on its own clock - and never raises: it is DROPPED when there is no terminal to show it on, or when the band has no room, so a piped or CI run is never given a repainted view it cannot use and no caller has to guard a notification. Log the same fact if it also needs recording. ttlMs defaults to 5000; a negative ttlMs pins the notification until newer ones push it out.",
			Args: []Arg{
				{Name: "message"},
				{Name: "level", Enum: "LogLevel", Optional: true},
				{Name: "ttlMs", Optional: true},
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
// terminal is the case that makes this concrete: stderr is a TTY, stdin is not,
// and a prompt would block forever on a read that can never arrive.
func TermIsInteractive(_ context.Context) (bool, error) {
	return termkit.StdinIsTerminal() && termkit.IsTerminalWriter(os.Stderr, termkit.SystemProbe), nil
}

// TermWantsColor reports whether styled output should be emitted to stderr.
func TermWantsColor(_ context.Context) (bool, error) {
	return termkit.WantsColor(os.Stderr, termkit.SystemProbe), nil
}

// TermSize returns the terminal's dimensions, or zeroes when unmeasurable.
func TermSize(_ context.Context) (types.TermSize, error) {
	fd, ok := termkit.Fd(os.Stderr)
	if !ok {
		return types.TermSize{}, nil
	}
	w, h, err := termkit.SystemProbe.Size(fd)
	if err != nil {
		// Not a raise: "there is no size" is an ordinary answer for a pipe, and a
		// caller that has to try/catch to lay out a line will not measure.
		return types.TermSize{}, nil //nolint:nilerr // documented above: an unmeasurable size is zero, not a raise; matches the size doc's "check width rather than expecting a raise"
	}
	return types.TermSize{Width: w, Height: h}, nil
}

// TermColorize wraps s in style, honoring the terminal's color capability.
func TermColorize(_ context.Context, s, style string) (string, error) {
	if !termkit.WantsColor(os.Stderr, termkit.SystemProbe) {
		return s, nil
	}
	return termkit.Colorize(s, termkit.SGR(style)), nil
}

// TermPick prompts for a choice among items and returns the chosen index.
func TermPick(ctx context.Context, items []string, prompt, initialFilter string, initial, maxRows int, query func(ctx context.Context, filter string) (any, error)) (int, error) {
	if types.Stubbed(ctx, "term.pick") {
		// Dry run: report the first item without painting anything. A record pass
		// must never block on a human, and every other host effect is skipped the
		// same way.
		return 0, nil
	}
	if len(items) == 0 && query == nil {
		return -1, errors.New("term.pick: no items to choose from")
	}
	if err := termPromptable("term.pick"); err != nil {
		return -1, err
	}
	opts := pick.Options{
		Prompt:        prompt,
		InitialFilter: initialFilter,
		Initial:       initial,
		MaxRows:       maxRows,
	}
	if query != nil {
		opts.Query = func(ctx context.Context, filter string) ([]string, error) {
			got, err := query(ctx, filter)
			if err != nil {
				return nil, err
			}
			return termQueryResult(got)
		}
	}
	idx, err := pick.One(ctx, os.Stdin, os.Stderr, termkit.SystemProbe, items, opts)
	if err != nil {
		return -1, termPromptError("term.pick", err)
	}
	return idx, nil
}

// termQueryResult reads a Buzz query's result as the labels to show. The
// callback crosses as any, so a wrong shape is named here rather than drawn as
// an empty list.
func termQueryResult(got any) ([]string, error) {
	if strs, ok := got.([]string); ok {
		return strs, nil
	}
	list, ok := got.([]any)
	if !ok {
		return nil, fmt.Errorf("query returned %T, want a list of strings", got)
	}
	out := make([]string, len(list))
	for i, v := range list {
		s, isStr := v.(string)
		if !isStr {
			return nil, fmt.Errorf("query returned a %T at index %d, want a list of strings", v, i)
		}
		out[i] = s
	}
	return out, nil
}

// TermPickMany prompts for any number of items and returns the marked ones.
func TermPickMany(ctx context.Context, items []string, prompt, initialFilter string, marked []string, maxRows int) ([]string, error) {
	if types.Stubbed(ctx, "term.pickMany") {
		// Nothing marked commits a recording pass to nothing.
		return []string{}, nil
	}
	if len(items) == 0 {
		return nil, errors.New("term.pickMany: no items to choose from")
	}
	start, err := termMarkedIndices(items, marked)
	if err != nil {
		return nil, err
	}
	if err := termPromptable("term.pickMany"); err != nil {
		return nil, err
	}
	got, err := pick.Many(ctx, os.Stdin, os.Stderr, termkit.SystemProbe, items, pick.Options{
		Prompt:        prompt,
		InitialFilter: initialFilter,
		MaxRows:       maxRows,
	}, start)
	if err != nil {
		return nil, termPromptError("term.pickMany", err)
	}
	out := make([]string, len(got))
	for i, idx := range got {
		out[i] = items[idx]
	}
	return out, nil
}

// termMarkedIndices maps marked onto indices into items, refusing a duplicate
// item (the answer could not say which one was meant) and a marked entry that is
// not an item.
func termMarkedIndices(items, marked []string) ([]int, error) {
	index := make(map[string]int, len(items))
	for i, it := range items {
		if _, dup := index[it]; dup {
			return nil, fmt.Errorf("term.pickMany: %q is listed twice, and the answer is the items themselves, so each must be distinct", it)
		}
		index[it] = i
	}
	out := make([]int, 0, len(marked))
	for _, m := range marked {
		i, ok := index[m]
		if !ok {
			return nil, fmt.Errorf("term.pickMany: marked names %q, which is not one of the items", m)
		}
		out = append(out, i)
	}
	return out, nil
}

// confirmAnswers lists no first so that Enter alone declines.
var confirmAnswers = []string{"no", "yes"}

// TermConfirm asks a yes or no question and reports whether the answer was yes.
//
// A pick over two rows rather than a y/N line read: the picker already owns raw
// mode, cancel and cleanup, and typing "y" filters to yes.
func TermConfirm(ctx context.Context, question string) (bool, error) {
	if types.Stubbed(ctx, "term.confirm") {
		// Decline: the step a confirm guards is the one a dry run exists not to take.
		return false, nil
	}
	if question == "" {
		return false, errors.New("term.confirm: no question to ask")
	}
	if err := termPromptable("term.confirm"); err != nil {
		return false, err
	}
	idx, err := pick.One(ctx, os.Stdin, os.Stderr, termkit.SystemProbe, confirmAnswers, pick.Options{Prompt: question})
	if err != nil {
		return false, termPromptError("term.confirm", err)
	}
	return confirmAnswers[idx] == "yes", nil
}

// termPromptable refuses a prompt when there is nothing to prompt on. The
// message names the guard: an author hitting this in CI needs to know what to
// write, not only what went wrong.
func termPromptable(member string) error {
	interactive, _ := TermIsInteractive(context.Background())
	if interactive {
		return nil
	}
	return fmt.Errorf("%s: nothing to prompt on (standard input and standard error are not both terminals), "+
		"guard the call with term.isInteractive() and choose a default for unattended runs", member)
}

// termPromptError names an abort as a cancel and wraps anything else.
//
// An abort RAISES rather than returning a sentinel. Ctrl-C means stop, and a
// sentinel would let a script that forgot to check it carry on with a choice
// the reader declined to make.
func termPromptError(member string, err error) error {
	if errors.Is(err, pick.ErrAborted) {
		return fmt.Errorf("%s: cancelled", member)
	}
	return fmt.Errorf("%s: %w", member, err)
}

// TermSpin calls run while a loader turns beside label on stderr.
func TermSpin(ctx context.Context, label string, run func(ctx context.Context) (any, error)) (any, error) {
	if run == nil {
		return nil, errors.New("term.spin: no function to run")
	}
	if types.Stubbed(ctx, "term.spin") {
		// The work still runs: its own effects are what a recording pass records.
		return run(ctx)
	}
	interactive, _ := TermIsInteractive(ctx)
	if !interactive {
		return nil, errors.New("term.spin: nothing to draw on (standard input and standard error are not both terminals), " +
			"guard the call with term.isInteractive() and call the function directly for unattended runs")
	}
	spinner, err := progress.StartSpinner(os.Stderr, termkit.SystemProbe, label)
	if err != nil {
		return nil, fmt.Errorf("term.spin: %w", err)
	}
	defer spinner.Stop()
	return run(ctx)
}

// defaultNotifyTTL is how long a notification shows when the caller does not
// say. Long enough to catch the eye of someone watching a build, short enough
// that three of them do not queue up behind each other.
const defaultNotifyTTL = 5 * time.Second

// TermNotify raises a notification into the process's terminal band.
//
// It does NOT take a scope. A magusfile target notifying in the middle of a run
// does not own the run, cannot wrap it, and would be nesting its scope inside
// magus's own. The band is owned by the process and released on the way out
// (tty.ReleaseStderr), so a caller just says the thing.
func TermNotify(ctx context.Context, message, level string, ttlMs int) error {
	if types.Stubbed(ctx, "term.notify") {
		// A record pass must not paint: a dry run reports what WOULD happen,
		// and a notification that flashed during it would be the one effect
		// that leaked.
		return nil
	}
	if message == "" {
		// A no-op rather than a raise. Everything else about this call is
		// best-effort (no terminal, no room, both silently drop), so failing
		// on one input would be the only way it could ever interrupt a run.
		return nil
	}
	// 0 is "the caller omitted it", because that is what the generated binding
	// passes for an absent optional int, so "pin this until something displaces
	// it" needs its own spelling, and a negative ttl is it.
	ttl := defaultNotifyTTL
	switch {
	case ttlMs > 0:
		ttl = time.Duration(ttlMs) * time.Millisecond
	case ttlMs < 0:
		ttl = 0
	}
	style := notifyStyle(types.LogLevel(level))
	if !termkit.WantsColor(os.Stderr, termkit.SystemProbe) {
		// NO_COLOR asks for plain text, and the band is text like any other.
		// Severity still reads from the message; it just is not colored.
		style = ""
	}
	// The write error is deliberately dropped, which is what lets this method
	// declare no raise. A notification is a VIEW: the same rule that discards
	// it on a pipe or a full band discards it when the terminal refuses the
	// bytes. Making every magusfile wrap a notification in try/catch to handle
	// "stderr write failed" would be a tax paid on every call site for a
	// condition no author can act on.
	_ = notify.Stderr().Notify(message, style, ttl)
	return nil
}

// notifyStyle maps a severity to the palette magus already renders with.
//
// LogLevel rather than a TermNotifyLevel of its own: a notification's severity
// is the same question log.at asks, and answering it twice with two enums would
// mean a magusfile author choosing between two spellings of "warn".
func notifyStyle(level types.LogLevel) termkit.SGR {
	switch level {
	case types.LogError:
		return termkit.SGRRed
	case types.LogWarn:
		return termkit.SGRYellow
	case types.LogTrace, types.LogDebug:
		return termkit.SGRDim
	case types.LogInfo:
	}
	// Info, and the empty level a caller who omitted the argument sends.
	// Unstyled: an ordinary notification should not compete with the
	// failures pinned above it.
	return ""
}

// TermClearScreen erases the screen, or does nothing when stderr is not a terminal.
func TermClearScreen(_ context.Context) error {
	if !termkit.IsTerminalWriter(os.Stderr, termkit.SystemProbe) {
		return nil
	}
	return termkit.ClearScreen(os.Stderr)
}
