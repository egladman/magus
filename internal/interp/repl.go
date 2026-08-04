package interp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/interp/engine"
	"github.com/fatih/color"
	"golang.org/x/term"
)

// ReplOptions configures a REPL session.
type ReplOptions struct {
	WorkDir string // working directory for the session; defaults to process cwd
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Banner  string                  // printed once before the first prompt
	Locals  map[string]engine.Value // injected as globals before the loop
	// Candidates supplies completion candidates the interpreter cannot know:
	// workspace target names, project paths, host module names. Optional.
	//
	// It is a callback rather than a slice because the workspace is the caller's to
	// read, and reading it once at startup would go stale in a session long enough
	// to matter - which is most of them, since a REPL is where you sit while editing
	// the magusfile it is describing.
	Candidates func() []string
}

// replInput is the REPL's line source. It is one type with two behaviours because
// every caller wants the same thing - give me the next line - and only the
// implementation differs:
//
//   - On a terminal: an x/term line editor, so arrow keys recall history, Ctrl-A
//     and friends edit the line, and Tab completes. This is the whole point; a REPL
//     without history is one where every typo is retyped from scratch.
//   - Off a terminal: the bufio.Scanner this used to be, unconditionally. A piped
//     or scripted session must stay byte-identical, which also means it must not
//     enter raw mode - there is no terminal to restore.
type replInput struct {
	editor  *term.Terminal
	scanner *bufio.Scanner
	restore func() error
	out     io.Writer
}

// newReplInput builds the line source for opts, installing the editor only when
// BOTH streams are a terminal: the editor writes its own prompt and echoes
// keystrokes, so it cannot drive a session whose output goes somewhere else.
//
// A failure to enter raw mode is not fatal. It degrades to the scanner, because a
// REPL that refuses to start over a missing line editor is strictly worse than one
// with no history.
func newReplInput(opts ReplOptions, complete func(line string, pos int, key rune) (string, int, bool)) *replInput {
	in := &replInput{out: opts.Stdout}
	stdin, isFile := opts.Stdin.(*os.File)
	if !isFile || !tty.StdinIsTerminal() || !tty.IsTerminalWriter(opts.Stdout, tty.SystemProbe) {
		in.scanner = bufio.NewScanner(opts.Stdin)
		in.scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		return in
	}
	restore, err := tty.MakeRaw(stdin.Fd())
	if err != nil {
		in.scanner = bufio.NewScanner(opts.Stdin)
		in.scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		return in
	}
	in.restore = restore
	in.editor = term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{opts.Stdin, opts.Stdout}, "")
	in.editor.AutoCompleteCallback = complete
	// Without a size the editor assumes 80 columns and wraps a longer line in the
	// wrong place, which makes editing the tail of a long expression unusable.
	if w, h, err := tty.SystemProbe.Size(stdin.Fd()); err == nil {
		_ = in.editor.SetSize(w, h)
	}
	// Output must go through the editor once it owns the line: writing to the raw
	// stdout underneath it lands mid-prompt and leaves the editor's idea of the
	// cursor wrong for the rest of the session.
	in.out = in.editor
	return in
}

// readLine returns the next line. The editor draws prompt itself; the scanner path
// prints it, which is what keeps a piped transcript identical to the old behaviour.
func (in *replInput) readLine(prompt string) (string, error) {
	if in.editor != nil {
		in.editor.SetPrompt(prompt)
		return in.editor.ReadLine()
	}
	fmt.Fprint(in.out, prompt)
	if !in.scanner.Scan() {
		if err := in.scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return in.scanner.Text(), nil
}

func (in *replInput) close() {
	if in.restore != nil {
		_ = in.restore()
	}
}

// replCompleter answers Tab from what magus already knows about itself.
//
// Every source here is an existing structured surface, which is the reason this is
// worth doing at all: completion is a PROJECTION of the session and the workspace,
// not a second list to keep in sync. Nothing here has to be updated when a target,
// module or meta-command is added.
//
//   - meta commands, including one per available language driver
//   - the session's own user globals, so a value you just bound completes
//   - whatever the caller supplies: workspace targets, projects, host modules
type replCompleter struct {
	meta       []string
	driver     func() engine.ReplDriver
	candidates func() []string
	out        io.Writer
}

// complete is x/term's per-keypress hook. It acts on Tab and declines everything
// else, so ordinary typing is untouched.
//
// The three outcomes follow readline, because that is what hands already expect:
// one match completes; several insert their longest common prefix, and when that
// adds nothing the list is printed; none does nothing at all rather than beeping.
func (c *replCompleter) complete(line string, pos int, key rune) (string, int, bool) {
	if key != '\t' {
		return "", 0, false
	}
	start := wordStart(line, pos)
	word := line[start:pos]
	matches := c.matching(word)
	if len(matches) == 0 {
		return "", 0, false
	}

	insert := matches[0]
	if len(matches) > 1 {
		insert = commonPrefix(matches)
		if insert == word {
			// No progress to make, so show the alternatives instead of silently
			// doing nothing - the state where a user presses Tab repeatedly.
			c.list(matches)
			return "", 0, false
		}
	}
	return line[:start] + insert + line[pos:], start + len(insert), true
}

// matching gathers every candidate with the given prefix, deduplicated and sorted
// so the order is stable between presses.
func (c *replCompleter) matching(word string) []string {
	var pool []string
	// A word starting with "." can only be a meta command, so the noise of every
	// global and target is excluded rather than ranked below.
	if strings.HasPrefix(word, ".") {
		pool = c.meta
	} else {
		if d := c.driver(); d != nil {
			for name := range d.UserGlobals() {
				pool = append(pool, name)
			}
		}
		if c.candidates != nil {
			pool = append(pool, c.candidates()...)
		}
	}
	seen := make(map[string]bool, len(pool))
	var out []string
	for _, p := range pool {
		if p != "" && strings.HasPrefix(p, word) && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// list prints the alternatives in columns, capped. An unbounded dump on Tab can
// scroll the transcript away, which costs the reader more than the list gives them.
func (c *replCompleter) list(matches []string) {
	const maxRows = 40
	shown := matches
	trailer := ""
	if len(shown) > maxRows {
		shown = shown[:maxRows]
		trailer = fmt.Sprintf("  ... and %d more", len(matches)-maxRows)
	}
	fmt.Fprintln(c.out)
	width := 0
	for _, m := range shown {
		if len(m) > width {
			width = len(m)
		}
	}
	perRow := max(1, 76/(width+2))
	for i, m := range shown {
		fmt.Fprintf(c.out, "%-*s", width+2, m)
		if (i+1)%perRow == 0 {
			fmt.Fprintln(c.out)
		}
	}
	if len(shown)%perRow != 0 {
		fmt.Fprintln(c.out)
	}
	if trailer != "" {
		fmt.Fprintln(c.out, trailer)
	}
}

// wordStart finds where the word under the cursor begins. Buzz member access is a
// dot and namespaces are a backslash, so neither breaks a word: completing
// "fs.wri" has to see the whole thing to know it is a member of fs, and a meta
// command is a leading dot.
func wordStart(line string, pos int) int {
	i := pos
	for i > 0 {
		ch := line[i-1]
		if ch == ' ' || ch == '\t' || ch == '(' || ch == ',' || ch == '[' || ch == '{' || ch == '"' {
			break
		}
		i--
	}
	return i
}

// commonPrefix returns the longest prefix shared by every match, which is how much
// of an ambiguous completion can be inserted without choosing for the user.
func commonPrefix(items []string) string {
	if len(items) == 0 {
		return ""
	}
	p := items[0]
	for _, s := range items[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// metaCommands lists the dot-commands Tab offers.
//
// It is exactly what handleReplMeta accepts, and deliberately does NOT include a
// `.<language>` entry per driver. replHelp advertises language switching, but
// handleReplMeta discards its driver argument and has no case for it, so `.buzz`
// falls through and is evaluated as Buzz source. Completing to a command that errors
// is worse than not offering it; if the switch is implemented, add it here and to
// handleReplMeta together.
func metaCommands(drivers []engine.ReplDriver) []string {
	_ = drivers
	out := []string{".exit", ".help", ".load", ".quit"}
	slices.Sort(out)
	return out
}

// stickyFooterRows is the height of the REPL's pinned status region.
//
// One row, and status only: a REPL's errors belong inline in the transcript where
// the input that caused them is, not in a region that overwrites its oldest entry.
// That is the opposite of the cache handler's use, where failures scroll away
// during a long run and the region is what keeps them on screen.
const stickyFooterRows = 1

// replFooter pins one dim row at the bottom of the terminal with the session state
// a REPL otherwise makes you remember: which language you are in, where the working
// directory is, and whether the parser is mid-expression.
//
// The last of those is the reason it is worth the row. A continuation prompt says
// ">>" and nothing else, so an unclosed brace looks identical to a REPL that has
// simply stopped responding - the state that makes people kill the process. The
// footer names the depth instead.
//
// It degrades to nothing off a TTY: Region reports disabled, and every method here
// is a no-op, so a piped or scripted session behaves exactly as before.
type replFooter struct {
	region *tty.Region
}

func newReplFooter(out io.Writer) *replFooter {
	return &replFooter{region: tty.NewRegion(out, stickyFooterRows, tty.SystemProbe)}
}

// paint redraws the footer. It needs no cursor bookkeeping: Region painting is
// cursor-transparent, so the prompt printed next - and the characters the terminal
// echoes as the user types - land wherever the transcript had reached.
func (f *replFooter) paint(state string) {
	if f == nil || !f.region.Enabled() {
		return
	}
	// The error is deliberately dropped rather than surfaced. A footer is
	// decoration on an interactive session: a terminal that refuses the escape
	// sequence should cost the reader a status line, never an aborted REPL or a
	// diagnostic interleaved with their own typing.
	_ = f.region.SetStatus(state)
}

func (f *replFooter) release() {
	if f == nil {
		return
	}
	_ = f.region.Release()
}

// replState renders the footer's one line. Ordering is fixed so the eye lands in
// the same place every repaint: what is running, then where, then what it is
// waiting for.
func replState(lang, workDir string, depth int, pending bool) string {
	parts := []string{"magus buzz"}
	if lang != "" {
		parts = append(parts, lang)
	}
	if workDir != "" {
		parts = append(parts, shortenPath(workDir))
	}
	switch {
	case depth > 0:
		// The count, not just the fact: it tells you how many closers to type.
		parts = append(parts, fmt.Sprintf("continuing (depth %d)", depth))
	case pending:
		parts = append(parts, "continuing")
	}
	parts = append(parts, ".help")
	return strings.Join(parts, "  |  ")
}

// shortenPath keeps the tail of a long path, which is the half that identifies the
// project; a footer clipped from the left would show the same home prefix for every
// session.
func shortenPath(p string) string {
	const max = 40
	if len(p) <= max {
		return p
	}
	tail := p[len(p)-max:]
	if i := strings.IndexByte(tail, '/'); i >= 0 {
		tail = tail[i:]
	}
	return "..." + tail
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

	var pending strings.Builder
	depth := 0

	completer := &replCompleter{
		meta:       metaCommands(drivers),
		driver:     func() engine.ReplDriver { return currentDriver },
		candidates: opts.Candidates,
	}
	input := newReplInput(opts, completer.complete)
	defer input.close()
	// The completer prints its candidate list through the same writer the editor
	// owns, so the list cannot land mid-prompt.
	completer.out = input.out
	// With the editor active, output routes through it; off a TTY these stay the
	// caller's streams and the transcript is unchanged.
	stdout, stderr := input.out, opts.Stderr
	if input.editor != nil {
		stderr = input.out
	}

	footer := newReplFooter(opts.Stdout)
	// Released on every exit path, including the ctx-cancelled one below: leaving
	// the scroll margins set would hand the shell back a terminal that only
	// scrolls its top rows, and the user has no obvious way to undo that.
	defer footer.release()

	for {
		if ctx.Err() != nil {
			// ctx cancelled: exit the REPL loop cleanly, not as an error.
			return nil
		}

		prompt := "> "
		if pending.Len() > 0 || depth > 0 {
			prompt = ">> "
		}
		// Painted before the prompt, not after: paint leaves the cursor on the
		// transcript's last row, so the prompt and the user's echoed keystrokes
		// land there rather than in the region.
		lang := ""
		if currentDriver != nil {
			lang = currentDriver.Language()
		}
		footer.paint(replState(lang, opts.WorkDir, depth, pending.Len() > 0))

		line, err := input.readLine(prompt)
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(stdout)
				return nil
			}
			return err
		}

		// Meta-commands only on fresh input.
		if pending.Len() == 0 && depth == 0 {
			handled, exit := handleReplMeta(ctx, stdout, stderr, line, &currentDriver, drivers, sess)
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
			fmt.Fprintln(stderr, "error: no REPL driver available")
			pending.Reset()
			continue
		}

		// Named src, not input: input is the line source now, and shadowing it here
		// would hide the editor from the rest of the loop.
		src := pending.String()
		vals, evalErr := currentDriver.EvalLine(src)
		if evalErr != nil {
			if currentDriver.IsIncomplete(evalErr) {
				continue
			}
			fmt.Fprintf(stderr, "error: %v\n", evalErr)
			pending.Reset()
		} else {
			for _, v := range vals {
				if !v.IsNil() {
					PrettyPrint(stdout, v, PrettyOpts{MaxDepth: 3})
				}
			}
			pending.Reset()
		}
	}
}

// handleReplMeta handles a dot-command. Returns (handled, exit).
func handleReplMeta(ctx context.Context, stdout, stderr io.Writer, line string, _ *engine.ReplDriver, drivers []engine.ReplDriver, sess engine.Session) (handled, exit bool) {
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
func loadFile(_ context.Context, sess engine.Session, path string, stderr io.Writer) {
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
			fmt.Fprintln(stdout, "(stepping not supported on this engine - resuming)")
			return ResumeContinue, true
		}
		fmt.Fprintln(stdout, pryColorize(st.useColor, "(stepping into next line)", color.Faint))
		return ResumeStep, true
	case ".next":
		if _, ok := st.sess.(engine.Stepper); !ok {
			fmt.Fprintln(stdout, "(stepping not supported on this engine - resuming)")
			return ResumeContinue, true
		}
		fmt.Fprintln(stdout, pryColorize(st.useColor, "(stepping over current line)", color.Faint))
		return ResumeNext, true
	case ".finish":
		if _, ok := st.sess.(engine.Stepper); !ok {
			fmt.Fprintln(stdout, "(stepping not supported on this engine - resuming)")
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
		fmt.Fprintln(w, "(no frames - engine does not expose stack introspection)")
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
		fmt.Fprintln(w, "(engine does not expose locals - try .globals)")
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
		fmt.Fprintln(w, "(no REPL driver - cannot list globals)")
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
