package interp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/egladman/magus/internal/interp/engine"
	"github.com/fatih/color"
)

// ReplOptions configures a REPL session.
type ReplOptions struct {
	WorkDir string // working directory for the session; defaults to process cwd
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Banner  string                  // printed once before the first prompt
	Locals  map[string]engine.Value // injected as globals before the loop
}

// replDrivers returns the available REPL drivers for sess. The caller may use
// Language() to switch between them.
func replDrivers(sess engine.Session) []engine.ReplDriver {
	var drivers []engine.ReplDriver
	if dp, ok := sess.(engine.DriversProvider); ok {
		drivers = append(drivers, dp.Drivers()...)
	}
	return drivers
}

// defaultDriver picks the REPL-start driver: the first available.
func defaultDriver(drivers []engine.ReplDriver) engine.ReplDriver {
	if len(drivers) > 0 {
		return drivers[0]
	}
	return nil
}

// Repl runs an interactive REPL on sess until EOF or .exit.
func Repl(ctx context.Context, sess engine.Session, opts ReplOptions) error {
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.WorkDir == "" {
		opts.WorkDir, _ = os.Getwd()
	}

	for name, val := range opts.Locals {
		sess.SetGlobal(name, val)
	}

	if opts.Banner != "" {
		fmt.Fprintln(opts.Stdout, opts.Banner)
	}
	fmt.Fprintln(opts.Stdout, "Type .exit to quit, .help for commands.")

	drivers := replDrivers(sess)
	currentDriver := defaultDriver(drivers)

	scanner := bufio.NewScanner(opts.Stdin)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var pending strings.Builder
	depth := 0

	for {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // ctx cancelled: exit the REPL loop cleanly, not as an error
		}

		prompt := "> "
		if pending.Len() > 0 || depth > 0 {
			prompt = ">> "
		}
		fmt.Fprint(opts.Stdout, prompt)

		if !scanner.Scan() {
			fmt.Fprintln(opts.Stdout)
			return scanner.Err()
		}
		line := scanner.Text()

		// Meta-commands only on fresh input.
		if pending.Len() == 0 && depth == 0 {
			handled, exit := handleReplMeta(ctx, opts.Stdout, opts.Stderr, line, &currentDriver, drivers, sess)
			if exit {
				return nil
			}
			if handled {
				continue
			}
			if line == "" || strings.HasPrefix(line, "--") {
				continue
			}
		}

		if pending.Len() > 0 {
			pending.WriteByte('\n')
		}
		pending.WriteString(line)

		if currentDriver != nil {
			depth += currentDriver.LineDelta(line)
		}
		if depth < 0 {
			depth = 0
		}
		if depth > 0 {
			continue
		}

		if currentDriver == nil {
			fmt.Fprintln(opts.Stderr, "error: no REPL driver available")
			pending.Reset()
			continue
		}

		input := pending.String()
		vals, err := currentDriver.EvalLine(input)
		if err != nil {
			if currentDriver.IsIncomplete(err) {
				continue
			}
			fmt.Fprintf(opts.Stderr, "error: %v\n", err)
			pending.Reset()
		} else {
			for _, v := range vals {
				if !v.IsNil() {
					PrettyPrint(opts.Stdout, v, PrettyOpts{MaxDepth: 3})
				}
			}
			pending.Reset()
		}
	}
}

// handleReplMeta handles a dot-command. Returns (handled, exit).
func handleReplMeta(ctx context.Context, stdout, stderr io.Writer, line string, current *engine.ReplDriver, drivers []engine.ReplDriver, sess engine.Session) (handled, exit bool) {
	switch {
	case line == ".exit" || line == ".quit":
		return true, true
	case line == ".help":
		replHelp(stdout, drivers)
		return true, false
	case strings.HasPrefix(line, ".load "):
		path := strings.TrimSpace(strings.TrimPrefix(line, ".load "))
		loadFile(ctx, sess, path, stderr)
		return true, false
	}
	return false, false
}

// loadFile reads and executes path in sess via DoString.
func loadFile(ctx context.Context, sess engine.Session, path string, stderr io.Writer) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return
	}
	if err := sess.DoString(string(data)); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
	}
}

func replHelp(w io.Writer, drivers []engine.ReplDriver) {
	langs := make([]string, 0, len(drivers))
	for _, d := range drivers {
		langs = append(langs, "."+d.Language())
	}
	if len(langs) > 0 {
		fmt.Fprintf(w, "  %-16s switch input language (%s)\n", strings.Join(langs, " / "), strings.Join(langs, ", "))
	}
	fmt.Fprintln(w, "  .load <path>     execute a file")
	fmt.Fprintln(w, "  .exit / .quit    exit the REPL")
	fmt.Fprintln(w, "  .help            show this message")
}

// PryContext describes the call site and stack at a magus.pry() breakpoint.
type PryContext struct {
	File   string // source file of the pry() call
	Line   int
	Func   string         // enclosing function name when known
	Frames []engine.Frame // call stack, innermost first
}

// PryResume tells the debugger how to proceed after a breakpoint.
type PryResume int

const (
	ResumeContinue PryResume = iota
	ResumeStep               // step into next line
	ResumeNext               // step over current line
	ResumeFinish             // run until current frame returns
)

// Pry runs the pry REPL on sess. On .step/.next/.finish the REPL exits and
// the caller re-enters Pry when the engine's one-shot step hook fires.
func Pry(ctx context.Context, sess engine.Session, pctx PryContext, opts ReplOptions) (PryResume, error) {
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.WorkDir == "" {
		opts.WorkDir, _ = os.Getwd()
	}

	stdoutFile, _ := opts.Stdout.(*os.File)
	useColor := ColorEnabledForFile(stdoutFile)

	for name, val := range opts.Locals {
		sess.SetGlobal(name, val)
	}

	banner := pryBanner(pctx, useColor)
	fmt.Fprintln(opts.Stdout, banner)
	fmt.Fprintln(opts.Stdout, "Type .help for pry commands, .continue (or .exit) to resume.")

	hist, _ := Open(DefaultPath(), 0)
	debug, _ := sess.(engine.DebugReader)

	drivers := replDrivers(sess)
	currentDriver := defaultDriver(drivers)

	st := &pryState{
		currentFrame: 0,
		driver:       currentDriver,
		drivers:      drivers,
		useColor:     useColor,
		debug:        debug,
		pctx:         pctx,
		hist:         hist,
		sess:         sess,
	}

	scanner := bufio.NewScanner(opts.Stdin)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var pending strings.Builder
	depth := 0

	resume := ResumeContinue
	for {
		if ctx.Err() != nil {
			return resume, nil //nolint:nilerr // ctx cancelled: stop stepping cleanly, not as an error
		}
		prompt := pryPrompt(st.currentFrame, pending.Len() > 0 || depth > 0, useColor)
		fmt.Fprint(opts.Stdout, prompt)

		if !scanner.Scan() {
			fmt.Fprintln(opts.Stdout)
			return resume, scanner.Err()
		}
		line := scanner.Text()

		// Meta-commands only on fresh input.
		if pending.Len() == 0 && depth == 0 {
			if cmd, handled := handlePryMeta(ctx, opts.Stdout, opts.Stderr, line, st); handled {
				if cmd != pryMetaConsumed {
					return cmd, nil
				}
				continue
			}
			if line == "" || strings.HasPrefix(line, "--") {
				continue
			}
		}

		if pending.Len() > 0 {
			pending.WriteByte('\n')
		}
		pending.WriteString(line)

		if st.driver != nil {
			depth += st.driver.LineDelta(line)
		}
		if depth < 0 {
			depth = 0
		}
		if depth > 0 {
			continue
		}

		input := pending.String()
		vals, err := st.driver.EvalLine(input)
		if err != nil {
			if st.driver.IsIncomplete(err) {
				continue
			}
			fmt.Fprintf(opts.Stderr, "error: %v\n", err)
			pending.Reset()
		} else {
			for _, v := range vals {
				if !v.IsNil() {
					PrettyPrint(opts.Stdout, v, PrettyOpts{Color: useColor, MaxDepth: 3})
				}
			}
			if hist != nil {
				hist.Append(input)
			}
			pending.Reset()
		}
	}
}

// pryMetaConsumed signals that a command was handled but the REPL should keep prompting.
const pryMetaConsumed PryResume = -1

type pryState struct {
	currentFrame int
	driver       engine.ReplDriver
	drivers      []engine.ReplDriver
	useColor     bool
	debug        engine.DebugReader
	pctx         PryContext
	hist         *History
	sess         engine.Session
}

// handlePryMeta dispatches pry meta-commands. bool indicates whether the line was handled.
func handlePryMeta(ctx context.Context, stdout, stderr io.Writer, line string, st *pryState) (PryResume, bool) {
	trim := strings.TrimSpace(line)
	if !strings.HasPrefix(trim, ".") {
		return 0, false
	}

	switch trim {
	case ".exit", ".quit", ".continue":
		return ResumeContinue, true
	case ".step":
		if _, ok := st.sess.(engine.Stepper); !ok {
			fmt.Fprintln(stdout, "(stepping not supported on this engine — resuming)")
			return ResumeContinue, true
		}
		fmt.Fprintln(stdout, pryColorize(st.useColor, "(stepping into next line)", color.Faint))
		return ResumeStep, true
	case ".next":
		if _, ok := st.sess.(engine.Stepper); !ok {
			fmt.Fprintln(stdout, "(stepping not supported on this engine — resuming)")
			return ResumeContinue, true
		}
		fmt.Fprintln(stdout, pryColorize(st.useColor, "(stepping over current line)", color.Faint))
		return ResumeNext, true
	case ".finish":
		if _, ok := st.sess.(engine.Stepper); !ok {
			fmt.Fprintln(stdout, "(stepping not supported on this engine — resuming)")
			return ResumeContinue, true
		}
		fmt.Fprintln(stdout, pryColorize(st.useColor, "(running until current frame returns)", color.Faint))
		return ResumeFinish, true
	case ".help":
		pryHelp(stdout)
		return pryMetaConsumed, true
	case ".where", ".backtrace":
		printBacktrace(stdout, st.debug, st.pctx, st.currentFrame, st.useColor)
		return pryMetaConsumed, true
	case ".whereami":
		printWhereami(stdout, st.debug, st.pctx, st.currentFrame, st.useColor)
		return pryMetaConsumed, true
	case ".locals":
		printLocals(stdout, st.debug, st.currentFrame, st.useColor)
		return pryMetaConsumed, true
	case ".globals":
		printGlobals(stdout, st.driver, st.useColor)
		return pryMetaConsumed, true
	case ".up":
		frames := visibleFrames(st.debug, st.pctx)
		if st.currentFrame+1 >= len(frames) {
			fmt.Fprintln(stdout, "(already at outermost frame)")
			return pryMetaConsumed, true
		}
		st.currentFrame++
		printFrameSelected(stdout, frames, st.currentFrame, st.useColor)
		return pryMetaConsumed, true
	case ".down":
		if st.currentFrame == 0 {
			fmt.Fprintln(stdout, "(already at innermost frame)")
			return pryMetaConsumed, true
		}
		st.currentFrame--
		frames := visibleFrames(st.debug, st.pctx)
		printFrameSelected(stdout, frames, st.currentFrame, st.useColor)
		return pryMetaConsumed, true
	}

	switch {
	case strings.HasPrefix(trim, ".pp "):
		expr := strings.TrimSpace(strings.TrimPrefix(trim, ".pp "))
		evalAndPrettyPrint(st.driver, expr, stdout, stderr, st.useColor)
		if st.hist != nil {
			st.hist.Append(line)
		}
		return pryMetaConsumed, true
	case strings.HasPrefix(trim, ".history"):
		rest := strings.TrimSpace(strings.TrimPrefix(trim, ".history"))
		PrintHistory(stdout, st.hist, rest)
		return pryMetaConsumed, true
	case strings.HasPrefix(trim, ".load "):
		path := strings.TrimSpace(strings.TrimPrefix(trim, ".load "))
		loadFile(ctx, st.sess, path, stderr)
		return pryMetaConsumed, true
	}

	return 0, false
}

// pryBanner returns the coloured banner line printed on entry.
func pryBanner(pctx PryContext, useColor bool) string {
	loc := pctx.File
	if pctx.Line > 0 {
		loc = fmt.Sprintf("%s:%d", pctx.File, pctx.Line)
	}
	fn := pctx.Func
	if fn == "" {
		fn = "(top level)"
	}
	return pryColorize(useColor, fmt.Sprintf("*** magus.pry at %s in %s", loc, fn), color.Bold, color.FgYellow)
}

func pryPrompt(frame int, continuation bool, useColor bool) string {
	if continuation {
		return pryColorize(useColor, "pry>> ", color.FgGreen)
	}
	if frame > 0 {
		return pryColorize(useColor, fmt.Sprintf("pry[%d]> ", frame), color.FgGreen)
	}
	return pryColorize(useColor, "pry> ", color.FgGreen)
}

// visibleFrames returns current frames from the debug API or falls back to entry-captured frames.
func visibleFrames(debug engine.DebugReader, pctx PryContext) []engine.Frame {
	if debug != nil {
		if f := debug.Frames(); len(f) > 0 {
			return f
		}
	}
	return pctx.Frames
}

func printBacktrace(w io.Writer, debug engine.DebugReader, pctx PryContext, current int, useColor bool) {
	frames := visibleFrames(debug, pctx)
	if len(frames) == 0 {
		fmt.Fprintln(w, "(no frames — engine does not expose stack introspection)")
		return
	}
	for i, f := range frames {
		marker := "  "
		if i == current {
			marker = pryColorize(useColor, "=>", color.FgYellow, color.Bold)
		}
		name := f.Name
		if name == "" {
			name = "?"
		}
		fmt.Fprintf(w, "%s #%d %s:%d in %s\n", marker, i, displaySource(f.ShortSrc, f.Source), f.CurrentLine, name)
	}
}

func printWhereami(w io.Writer, debug engine.DebugReader, pctx PryContext, current int, useColor bool) {
	frames := visibleFrames(debug, pctx)
	var frame engine.Frame
	if current < len(frames) {
		frame = frames[current]
	} else {
		frame = engine.Frame{Source: pctx.File, CurrentLine: pctx.Line, Name: pctx.Func}
	}
	src := strings.TrimPrefix(frame.Source, "@")
	if src == "" {
		src = pctx.File
	}
	header := fmt.Sprintf("From: %s:%d in %s", displaySource(src, frame.Source), frame.CurrentLine, frame.Name)
	fmt.Fprintln(w, pryColorize(useColor, header, color.Bold))
	PrintSourceContext(w, src, frame.CurrentLine, 3, useColor)
}

func printLocals(w io.Writer, debug engine.DebugReader, frame int, useColor bool) {
	if debug == nil {
		fmt.Fprintln(w, "(engine does not expose locals — try .globals)")
		return
	}
	locals := debug.Locals(frame)
	if len(locals) == 0 {
		fmt.Fprintln(w, "(no locals)")
		return
	}
	names := make([]string, 0, len(locals))
	for k := range locals {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %s = ", pryColorize(useColor, name, color.FgCyan))
		PrettyPrint(w, locals[name], PrettyOpts{Color: useColor, MaxDepth: 3})
	}
	ups := debug.Upvalues(frame)
	if len(ups) > 0 {
		fmt.Fprintln(w, pryColorize(useColor, "upvalues:", color.Faint))
		unames := make([]string, 0, len(ups))
		for k := range ups {
			unames = append(unames, k)
		}
		sort.Strings(unames)
		for _, name := range unames {
			fmt.Fprintf(w, "  %s = ", pryColorize(useColor, name, color.FgCyan))
			PrettyPrint(w, ups[name], PrettyOpts{Color: useColor, MaxDepth: 3})
		}
	}
}

func printGlobals(w io.Writer, driver engine.ReplDriver, useColor bool) {
	if driver == nil {
		fmt.Fprintln(w, "(no REPL driver — cannot list globals)")
		return
	}
	globals := driver.UserGlobals()
	if globals == nil {
		fmt.Fprintln(w, "(globals not available on this engine)")
		return
	}
	names := make([]string, 0, len(globals))
	for k := range globals {
		names = append(names, k)
	}
	if len(names) == 0 {
		fmt.Fprintln(w, "(no user globals)")
		return
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %s = ", pryColorize(useColor, name, color.FgCyan))
		PrettyPrint(w, globals[name], PrettyOpts{Color: useColor, MaxDepth: 2})
	}
}

func evalAndPrettyPrint(driver engine.ReplDriver, expr string, stdout, stderr io.Writer, useColor bool) {
	if driver == nil {
		fmt.Fprintln(stderr, "error: no REPL driver available")
		return
	}
	vals, err := driver.EvalLine(expr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return
	}
	for _, v := range vals {
		PrettyPrint(stdout, v, PrettyOpts{Color: useColor, MaxDepth: 4})
	}
}

func printFrameSelected(w io.Writer, frames []engine.Frame, idx int, useColor bool) {
	if idx >= len(frames) {
		return
	}
	f := frames[idx]
	fmt.Fprintf(w, "%s #%d %s:%d in %s\n",
		pryColorize(useColor, "=>", color.FgYellow, color.Bold),
		idx,
		displaySource(f.ShortSrc, f.Source),
		f.CurrentLine,
		f.Name)
}

func pryHelp(w io.Writer) {
	fmt.Fprintln(w, "Pry commands:")
	fmt.Fprintln(w, "  .whereami        show source around the current line")
	fmt.Fprintln(w, "  .where           print the call stack (alias: .backtrace)")
	fmt.Fprintln(w, "  .up / .down      move between stack frames")
	fmt.Fprintln(w, "  .locals          list locals in the selected frame")
	fmt.Fprintln(w, "  .globals         list user-defined globals")
	fmt.Fprintln(w, "  .pp <expr>       evaluate and pretty-print <expr>")
	fmt.Fprintln(w, "  .step            single-step into the next line")
	fmt.Fprintln(w, "  .next            step over the current line")
	fmt.Fprintln(w, "  .finish          run until the current frame returns")
	fmt.Fprintln(w, "  .continue        resume execution (alias: .exit)")
	fmt.Fprintln(w, "  .history [N]     show last N (default 50) commands")
	fmt.Fprintln(w, "  .history!N       print the Nth-most-recent command")
	fmt.Fprintln(w, "  .load <path>     execute a file")
}

func displaySource(short, full string) string {
	if short != "" {
		return short
	}
	return strings.TrimPrefix(full, "@")
}
