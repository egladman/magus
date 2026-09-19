package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// buzzCmd runs Buzz source from a file, stdin, or an inline snippet using the
// Buzz interpreter with the full magus module surface (Buzz stdlib plus every
// magus host module: fs, os, http, markdown, template, ...) and the magus.*
// namespace. The magus.* members that declare into a workspace being loaded
// (magus\project and the provider selections) raise MGS1022 here, since a script
// has no magusfile to declare into. The REPL is the surface that does load one.
//
// This is the in-binary form of the former standalone magus-buzz tool, folded into
// the main command (like `kubectl kustomize`) so a clean Buzz runner is always
// present wherever magus is. The buzz spell's `run` op forks `magus buzz`, so the
// spell needs no separately-installed binary.
//
// With no arguments on an interactive terminal it opens a REPL, matching upstream
// `buzz`. A piped or redirected stdin still runs as a script (so `cat x | magus
// buzz` and heredocs keep working), and `magus buzz -` forces stdin.
// buzzLoadWorkspace opens the workspace a script reads. A variable so a test can count
// opens without the process singleton loadMagus memoizes.
var buzzLoadWorkspace = loadMagus

func warnWorkspaceNotAttached(err error) {
	slog.Warn("workspace not attached to this script; its workspace-reading members will raise MGS1022",
		slog.String("error", err.Error()))
}

// lazyWorkspaceContext opens a script's workspace on the first read instead of at
// startup: about 700ms in this repo, against 10ms for a script that never reads it.
//
// The readers are types.WorkspaceFromContext and trail.BaseFromContext, called from
// bindings throughout std and internal/interp. Answering their context keys here keeps
// every one of those call sites unchanged, where a lazy WorkspaceRepository would be
// non-nil before the open and so could not raise MGS1022 the way a nil one does, and
// would also have to forward the optional interfaces std type-asserts for. A read that
// cannot attach a workspace sees none, exactly as when the open ran at startup.
type lazyWorkspaceContext struct {
	context.Context
	root string

	once      sync.Once
	workspace types.WorkspaceRepository
	trailBase string
}

var (
	workspaceContextKey = contextKeyReadBy(func(ctx context.Context) { types.WorkspaceFromContext(ctx) })
	trailContextKey     = contextKeyReadBy(func(ctx context.Context) { trail.BaseFromContext(ctx) })
)

func newLazyWorkspaceContext(parent context.Context, root string) *lazyWorkspaceContext {
	return &lazyWorkspaceContext{Context: parent, root: root}
}

func (c *lazyWorkspaceContext) Value(key any) any {
	switch key {
	case workspaceContextKey:
		c.once.Do(c.attach)
		if c.workspace == nil {
			return nil
		}
		return c.workspace
	case trailContextKey:
		c.once.Do(c.attach)
		if c.trailBase == "" {
			return nil
		}
		return c.trailBase
	}
	return c.Context.Value(key)
}

// attach opens against the parent context, so the open cannot re-enter Value and
// deadlock on once.
func (c *lazyWorkspaceContext) attach() {
	m, err := buzzLoadWorkspace(c.Context, c.root)
	if err != nil {
		warnWorkspaceNotAttached(err)
		return
	}
	if m == nil {
		return
	}
	c.workspace = m
	c.trailBase = m.CacheDir()
}

// keyRecorder is a context that records the last key looked up in it.
type keyRecorder struct {
	context.Context
	key any
}

func (r *keyRecorder) Value(key any) any {
	r.key = key
	return nil
}

// contextKeyReadBy returns the key read looks up, so a context can answer for a
// package whose unexported key it cannot name.
func contextKeyReadBy(read func(context.Context)) any {
	r := &keyRecorder{Context: context.Background()}
	read(r)
	return r.key
}

func buzzCmd(ctx context.Context, root string, args []string) error {
	// `magus buzz lsp` is the Buzz language server (stdio LSP). It is a noun
	// subcommand of buzz, grouped with the rest of the Buzz-language tooling, rather
	// than a top-level `magus lsp`, so serving other languages later needs no new
	// top-level subcommand contract. Intercept it before flag parsing, which would
	// otherwise read "lsp" as a script filename.
	if len(args) > 0 && args[0] == "lsp" {
		return lspCmd(ctx, args[1:])
	}

	// Everything after `--` is the SCRIPT's argv, never magus's. The boundary is
	// needed because cmdParse reorders flags ahead of positionals, so a bare
	// `script.buzz --raw` would have magus parsing --raw and failing; it is also the
	// separator this CLI already uses to forward args to a tool (`run go::go-test . --
	// -run X`). A script reads them as main's [str], the way cmd/buzz and upstream do.
	args, sep, forwarded := splitScriptArgs(args)

	// Bound from the command registry rather than declared here. The -t/--test pair
	// is ONE switch, which a generated binder can only express because the registry
	// marks the second AliasOf the first; modeled as two flags they would get two
	// destinations and the shorthand would parse and then do nothing.
	var bf *gen.BuzzFlags
	rest, err := cmdParse("buzz", args, func(fs *flag.FlagSet) {
		bf = gen.BindBuzz(fs)
		fs.Usage = buzzUsage
	})
	if err != nil {
		return err
	}
	isRepl := bf.E == "" && !bf.Test && !bf.Check && len(rest) == 0
	if !isRepl && (bf.NoAutoload || bf.C != "") {
		return usagef("magus buzz: --%s and -%s apply to the REPL, not to a script, -%s, or -%s",
			gen.FlagBuzzNoAutoload, gen.FlagBuzzC, gen.FlagBuzzE, gen.FlagBuzzT)
	}
	if bf.Coverprofile != "" && !bf.Test {
		return usagef("magus buzz: --%s requires -%s", gen.FlagBuzzCoverprofile, gen.FlagBuzzT)
	}
	// Refused rather than ordered, because either order is a defensible reading and
	// picking one silently gives back an answer to a question nobody asked: -t runs the
	// file, --check is the mode that does not.
	if bf.Check && bf.Test {
		return usagef("magus buzz: --%s and -%s are different modes; --%s does not run the file",
			gen.FlagBuzzCheck, gen.FlagBuzzT, gen.FlagBuzzCheck)
	}
	if bf.Check {
		// -e and stdin are deliberately absent: a check reports positions, and both
		// name a source no reader can open at the position reported. `magus buzz -e`
		// already surfaces a compile error on its own, since it compiles before it runs.
		if bf.E != "" || len(rest) == 0 || (len(rest) == 1 && rest[0] == "-") {
			return usagef("magus buzz: --%s takes one or more file paths", gen.FlagBuzzCheck)
		}
		ctx, cerr := buzzScriptContext(ctx, root)
		if cerr != nil {
			return cerr
		}
		return buzzCheck(ctx, rest, bf.Embedded)
	}

	// No code, no file/stdin argument, and an interactive terminal: open the REPL,
	// which loads the magusfile at cwd when there is one: magus reads its context
	// rather than being told about it, and a REPL opened in a workspace is a REPL on
	// that workspace. Outside one it simply has nothing to autoload.
	// A non-terminal stdin (pipe, redirect, heredoc) falls through to script mode.
	// --embedded is a no-op on this path: a REPL is top-level statements by nature,
	// so the session is always embedded regardless of the flag.
	if isRepl && stdinIsTerminal() {
		return buzzRepl(ctx, bf.C, bf.NoAutoload)
	}

	code, name, scriptArgs, err := buzzSource(bf.E, rest)
	if err != nil {
		return err
	}
	if sep {
		scriptArgs = append(scriptArgs, forwarded...)
	}

	ctx, err = buzzScriptContext(ctx, root)
	if err != nil {
		return err
	}

	// Default is strict (upstream Buzz parity, what the buzz spell's `run` op forks).
	// --embedded opts into the relaxations the magusfile engine uses, so a magus
	// module like the docs generator (render) can be run or tested here.
	var opts []buzz.Option
	if bf.Embedded {
		opts = append(opts, buzz.WithEmbedded())
	}
	sess := buzz.NewSession(ctx, opts...)
	defer func() { _ = sess.Close() }()
	var cov *buzz.LineCoverage
	if bf.Coverprofile != "" {
		if path := buzzCoverPath(name); path != "" {
			sess.SetSourceFile(path)
		}
		cov = buzz.NewLineCoverage()
		sess.EnableLineCoverage(cov)
	}
	// Install the full magus module surface (Buzz stdlib + assert/suite + every
	// magus host module), the same one the magusfile engine uses. Sharing one
	// registration keeps `magus buzz` and magusfile execution in lock-step: any
	// module a script or test imports resolves the same way in both, with no
	// per-surface module list.
	bindings.RegisterModuleSurface(ctx, sess, bindings.WithScriptOutput(os.Stdout))
	// The magus.* namespace on top, so `import "magus"` resolves here too. The
	// members that declare into a workspace being loaded (magus\project,
	// magus\cache.remote, magus\ci.provider) raise MGS1022 on this surface; the rest
	// (magus\describe, magus\cmd, magus\run, ...) work.
	bindings.RegisterMagusNamespace(ctx, sess)
	// Install the magus/spell and magus/charm source modules too, so a spell file
	// (which imports them) and its `test "..." {}` blocks run here: `magus buzz -t`
	// is the spell test harness.
	bindings.RegisterSpellSourceModules(sess)

	if err := sess.Exec(ctx, code); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	// Warnings (e.g. BZZ3001 unused imports) never fail Exec, so they only reach
	// the user if something prints them after the fact; print to stderr, matching
	// how every other magus diagnostic (and the -t failure lines below) stays off
	// stdout, which carries structured output only. -q/-s suppress them like any
	// other non-error progress output.
	if !global.quiet && !global.silent {
		for _, w := range sess.Warnings() {
			w.File = name
			fmt.Fprintln(os.Stderr, w)
		}
	}
	var testErr error
	if bf.Test {
		testErr = runBuzzTests(ctx, sess, name)
	} else if mainFn := sess.GetGlobal("main"); mainFn.IsFun() {
		// Like upstream's Run flavor and cmd/buzz, an entry script's `main` runs once
		// its top level has. Without it a script had to call its own main, which strict
		// mode cannot wrap: a top-level `try` is rejected and a bare call is BZZ1006.
		// Everything after the script path is the script's own argv, the way upstream
		// and cmd/buzz hand it over, so a caller parameterizes a script with arguments
		// rather than an environment variable a shell has to set.
		items := make([]vm.Value, 0, len(scriptArgs))
		for _, a := range scriptArgs {
			items = append(items, vm.StrValue(a))
		}
		ret, err := sess.CallValue(ctx, mainFn, []vm.Value{vm.ListValue(items)})
		if err != nil {
			testErr = fmt.Errorf("%s: %w", name, err)
		} else if ret.IsInt() && ret.AsInt() != 0 {
			// `fun main() > int` is upstream's exit-status convention, and the checker
			// permits it; discarding the value made such a script always exit 0.
			testErr = errSilent{exitCode: int(ret.AsInt())}
		}
	}
	if cov != nil {
		if err := os.WriteFile(bf.Coverprofile, []byte(cov.Report()), 0o644); err != nil {
			return fmt.Errorf("magus buzz: write --%s: %w", gen.FlagBuzzCoverprofile, err)
		}
	}
	return testErr
}

// buzzCoverPath returns a stable, slash-separated path for LCOV SF: records.
// Absolute paths under the cwd become relative so a coverprofile is machine-
// stable; -e and stdin have no file to attribute and return "".
func buzzCoverPath(name string) string {
	switch name {
	case "", "-e", "<stdin>":
		return ""
	}
	path := name
	if abs, err := filepath.Abs(name); err == nil {
		if cwd, err := os.Getwd(); err == nil {
			if rel, err := filepath.Rel(cwd, abs); err == nil && !strings.HasPrefix(rel, "..") {
				path = rel
			}
		}
	}
	return filepath.ToSlash(path)
}

// stdinIsTerminal reports whether stdin is an interactive terminal rather than
// a pipe, file, or /dev/null. It gates the no-argument REPL so a piped script,
// or one run with stdin redirected from /dev/null, still runs as a whole
// instead of blocking on a REPL prompt no one can answer.
func stdinIsTerminal() bool {
	return tty.StdinIsTerminal()
}

// runBuzzTests executes every `test "..." {}` block registered while executing the
// file, printing one line per test and returning an error (non-zero exit) if any
// failed. A test fails when its body raises, typically an unmet std.assert.
func runBuzzTests(ctx context.Context, sess *buzz.Session, name string) error {
	tests := sess.Tests()
	if len(tests) == 0 {
		fmt.Printf("%s: no tests\n", name)
		return nil
	}
	failed := 0
	skipped := 0
	for _, tc := range tests {
		_, err := sess.CallValue(ctx, tc.Fn, []vm.Value(nil))
		if err == nil {
			fmt.Printf("ok    test %q\n", tc.Name)
			continue
		}
		if reason, ok := buzzstd.SkipMessage(err); ok {
			skipped++
			fmt.Printf("skip  test %q%s\n", tc.Name, skipReason(reason))
			continue
		}
		failed++
		fmt.Printf("FAIL  test %q\n      %v\n", tc.Name, err)
	}
	fmt.Printf("---\n%d passed, %d failed, %d skipped\n", len(tests)-failed-skipped, failed, skipped)
	if failed > 0 {
		return fmt.Errorf("%d of %d tests failed", failed, len(tests))
	}
	return nil
}

// skipReason formats a non-empty skip reason as " (reason)" for the test line.
func skipReason(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

// buzzSource resolves the program text (and a name for diagnostics) from the -e
// flag and positional args. Exactly one input is allowed: -e, a single file path,
// or stdin (no args, or "-").
//
// When a bare filename is given (no directory separator), BUZZ_INCLUDE_PATH is
// searched if the file is not found in the working directory, matching the
// upstream Buzz toolchain convention.
func buzzSource(eval string, args []string) (code, name string, scriptArgs []string, err error) {
	switch {
	case eval != "":
		if len(args) > 0 {
			return "", "", nil, fmt.Errorf("cannot combine -e with a file argument")
		}
		return eval, "-e", nil, nil
	case len(args) >= 1 && args[0] != "-":
		resolved := buzzResolveFile(args[0])
		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", "", nil, err
		}
		return string(data), resolved, args[1:], nil
	default: // no args, or "-": read stdin
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", "", nil, fmt.Errorf("read stdin: %w", err)
		}
		rest := args
		if len(rest) > 0 { // drop the "-" that named stdin
			rest = rest[1:]
		}
		return string(data), "<stdin>", rest, nil
	}
}

// splitScriptArgs cuts the command line at the first `--`: what precedes it is
// magus's to parse, what follows is the script's own argv. The bool reports whether
// a separator was present at all, so an empty tail after `--` stays distinguishable
// from no separator.
func splitScriptArgs(args []string) (before []string, sep bool, after []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], true, args[i+1:]
		}
	}
	return args, false, nil
}

// buzzResolveFile returns the path to use for reading a script. If the path
// contains a separator it is used as-is. Otherwise BUZZ_INCLUDE_PATH
// (colon-separated) is searched for the first match, falling back to the original
// path (which produces a clear "no such file" error).
func buzzResolveFile(path string) string {
	if filepath.Base(path) != path {
		return path
	}
	includePath := os.Getenv("BUZZ_INCLUDE_PATH")
	if includePath == "" {
		return path
	}
	for _, dir := range filepath.SplitList(includePath) {
		candidate := filepath.Join(dir, path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return path
}

func buzzUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus buzz              # open a REPL with the magusfile loaded")
	fmt.Fprintln(os.Stderr, "       magus buzz <file>       # run a script")
	fmt.Fprintln(os.Stderr, "       magus buzz -            # run a script from stdin")
	fmt.Fprintln(os.Stderr, "       magus buzz -e <code>    # run an inline snippet")
	fmt.Fprintln(os.Stderr, "       magus buzz -t <file>    # run its test \"...\" {} blocks")
	fmt.Fprintln(os.Stderr, "       magus buzz --check <file>...  # type-check without running")
	fmt.Fprintln(os.Stderr, "       magus buzz lsp          # language server over stdio (LSP)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Run Buzz source from a REPL, file, stdin, or an inline snippet. With no")
	fmt.Fprintln(os.Stderr, "argument on a terminal it opens a REPL with the magusfile at cwd loaded,")
	fmt.Fprintln(os.Stderr, "its targets and bindings ready; a piped or redirected stdin runs as a")
	fmt.Fprintln(os.Stderr, "script. The Buzz stdlib, every magus host module (fs, os, http, markdown,")
	fmt.Fprintln(os.Stderr, "...), and the magus.* namespace are available in both.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  -e <code>   execute code given on the command line instead of a file")
	fmt.Fprintln(os.Stderr, "  -t, -test   run the file's test \"...\" {} blocks and report pass/fail")
	fmt.Fprintln(os.Stderr, "  --check     parse and type-check the named files without running them")
	fmt.Fprintln(os.Stderr, "  --embedded  relax upstream strictness (top-level statements, optional")
	fmt.Fprintln(os.Stderr, "              argument labels) to match the magusfile engine")
	fmt.Fprintln(os.Stderr, "  --no-autoload  start the REPL without executing the magusfile")
	fmt.Fprintln(os.Stderr, "  -C <dir>    working directory for the REPL's import resolution")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Parsing is upstream-strict by default. A file written for the magusfile")
	fmt.Fprintln(os.Stderr, "engine needs --embedded, or it fails on rules upstream Buzz enforces and")
	fmt.Fprintln(os.Stderr, "magus does not (most often: \"argument N must be labeled\").")
}

// buzzScriptContext puts the workspace on a script's context when there is one.
//
// Without this every workspace-reading member of the magus module raised MGS1022 from
// a script (`magus\projects`, `affected`, `projectGraph`, `where`, `insight`), and the
// error told the reader to reach for the forking `magus\cmd` instead. That advice was
// sound only because nothing had put the workspace here: the process had already loaded
// one (loadMagus is a sync.Once singleton, so this is the same instance the dispatcher
// built, not a second load), and the script simply never saw it. A `magus buzz` script
// run inside a workspace is a script ON that workspace, the same way the REPL
// already autoloads the magusfile at cwd.
//
// Best-effort: outside a workspace, or when one fails to load, the script still runs
// and the workspace-reading members raise as before. A standalone script that touches
// none of them must not be blocked by a magusfile it never asked about.
//
// The load error is LOGGED rather than discarded. Silently dropping it made the two
// absences indistinguishable at the point a reader sees them: MGS1022 says "no
// workspace on the context" either way, so a script inside a workspace that simply
// failed to load reads as a script that was never in one, and the advice it gives
// ("fork instead") is then wrong. This is not hypothetical: it is what a green
// local run and a red CI run of the same script looked like, with nothing in
// between to tell them apart.
//
// The workspace's sandbox policy rides along, because a script reaches the same
// fs/proc/http bindings a target does and the guard cannot read a script body: it
// allows `magus buzz -` outright. Without the policy on ctx, sandbox.FromContext
// returns nil at every binding check and an ad-hoc script writes, execs and fetches
// with no policy at all in a workspace that asked for one. The trail base beside it
// is what lets a denial land as sandbox_denial, the way a target's does.
//
// --check reaches this too, and needs it for the same reason rather than a weaker
// one: resolving a file import executes that module's top level, so a check that
// skipped the policy would run code under no policy at all.
//
// Opening is most of a run's cost, so it waits for the first read unless the policy
// has to be in force before the first line runs. globalCfg is the config the open
// would load, and an adopted workspace (daemon, tests) is already open.
func buzzScriptContext(ctx context.Context, root string) (context.Context, error) {
	if _, adopted := magusFromContext(ctx); !adopted && !globalCfg.Sandbox.Enabled {
		return newLazyWorkspaceContext(ctx, root), nil
	}
	m, lerr := buzzLoadWorkspace(ctx, root)
	if lerr != nil {
		warnWorkspaceNotAttached(lerr)
		return ctx, nil
	}
	if m == nil {
		return ctx, nil
	}
	ctx = types.WithWorkspace(ctx, m)
	// Confined by the same policy a target run gets, lease narrowing included. A
	// script is the shortest way around a boundary the sandbox enforces everywhere
	// else: `magus buzz -e 'fs\write(...)'` writes through the same bindings a spell
	// does, and leaving it unpoliced would make the write grant advice.
	sctx, serr := m.ApplySandbox(ctx)
	if serr != nil {
		return nil, serr
	}
	return trail.ContextWithBase(sctx, m.CacheDir()), nil
}

// buzzCheck parses and type-checks each named file WITHOUT running it, printing
// every diagnostic and failing only on errors.
//
// Running a file is not a check of it. That is the gap this fills: a hook script
// whose whole job is a side effect (read the event on stdin, judge it, exec magus)
// cannot be validated by execution, so until this existed the only way to learn
// whether the shipped glue still compiled was a Go test that ran it. Buzz is also
// the one language where magus cannot defer to a spell-named tool, because magus is
// the toolchain; every other language pack names a checker that already exists.
//
// It takes several paths because the question is almost always asked about a set:
// the files just edited, or every glue script at once.
func buzzCheck(ctx context.Context, files []string, embedded bool) error {
	// Sessions are NOT shared across files. Session.Diagnostics mutates session state
	// (loadedPaths, env, importedTypes) and is documented as needing a fresh one, so a
	// reused session would report the second file against the first file's scope and
	// skip its imports as already-loaded.
	failed := 0
	noted := false
	for _, path := range files {
		diags, err := buzzCheckFile(ctx, path, embedded)
		if err != nil {
			return err
		}
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d)
			if !noted && buzzSpellImportUnresolved(d) {
				fmt.Fprintln(os.Stderr, buzzSpellImportNote)
				noted = true
			}
			if d.Severity != buzz.SeverityWarning {
				failed++
			}
		}
	}
	if failed > 0 {
		// errSilent: every diagnostic is already on stderr with its own position, so a
		// trailing "N errors" wrapper would be the only line without one.
		return errSilent{exitCode: 1}
	}
	return nil
}

// buzzCheckFile checks one path, returning its diagnostics with File set so each
// one renders as <file>:L:C, the position shape an editor can jump to.
func buzzCheckFile(ctx context.Context, path string, embedded bool) ([]buzz.Diagnostic, error) {
	resolved := buzzResolveFile(path)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, err
	}
	var opts []buzz.Option
	if embedded {
		opts = append(opts, buzz.WithEmbedded())
	}
	sess := buzz.NewSession(ctx, opts...)
	defer func() { _ = sess.Close() }()
	// Script output goes to STDERR on this path, unlike a run. A check prints no
	// program output of its own, so anything reaching stdout here came from an
	// imported module's top level, which Diagnostics executes; that is incidental to
	// the answer and must not be mixed into stdout with it.
	bindings.RegisterModuleSurface(ctx, sess, bindings.WithScriptOutput(os.Stderr))
	bindings.RegisterMagusNamespace(ctx, sess)
	bindings.RegisterSpellSourceModules(sess)

	diags := sess.Diagnostics(string(data))
	for i := range diags {
		diags[i].File = resolved
	}
	return diags, nil
}

// buzzSpellImportUnresolved reports whether d is the one diagnostic --check raises
// that does not mean what it says: a magusfile's `import "magus/spell/<name>"`.
//
// Those paths are bound by the workspace loader from the resolved spell registry
// (internal/interp/runtime.go), so a bare session has nothing to resolve them
// against and reports BZZ2001. The import is fine; this surface just cannot see it.
//
// The code is matched in EITHER position, because an unresolved import arrives by two
// routes that disagree about where it lands. A checker error carries it in Code; an
// import that fails during resolution surfaces as a parse error, and that path
// (Session.Diagnostics) renders the whole thing into Msg and leaves Code empty. Reading
// only Code silently missed every real instance, since resolution is the route this
// one actually takes.
func buzzSpellImportUnresolved(d buzz.Diagnostic) bool {
	if !strings.Contains(d.Msg, spellModulePrefix) {
		return false
	}
	return string(d.Code) == buzzUnresolvedImport || strings.Contains(d.Msg, buzzUnresolvedImport)
}

// buzzSpellImportNote follows an unresolved spell import.
//
// It is printed rather than suppressed, and the diagnostic still fails, because the
// same code covers a genuine typo (`magus/spell/gooo`) and this surface cannot tell
// the two apart. What it CAN do is name the check that does settle it.
//
// That check is reassuring, and it bounds what this verb is for. A file importing a
// spell module is loaded by magus itself, and loading reports exactly these errors, so
// a magusfile and a target definition are the Buzz files that already had a check. The
// ones that did not are the ones nothing loads: a standalone script, and hook glue
// whose whole job is a side effect.
const buzzSpellImportNote = "note: magus/spell/* is bound by the workspace loader, so --check cannot resolve it. " +
	"A file that imports one (a magusfile, a target definition) is checked by loading it: " +
	"any magus command reports its errors."

const (
	// buzzUnresolvedImport is gopherbuzz's unresolved-import code, spelled here rather
	// than imported: cmd/magus does not otherwise depend on the diagnostics registry,
	// and the code is the stable published identifier.
	buzzUnresolvedImport = "BZZ2001"
	spellModulePrefix    = "magus/spell/"
)
