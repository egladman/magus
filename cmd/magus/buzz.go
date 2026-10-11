package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/token"
	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// buzzCmd runs Buzz source from a file, stdin, or an inline snippet using the
// Buzz interpreter with the full set of magus modules (Buzz stdlib plus every
// magus host module: fs, os, http, markdown, template, ...) and the magus.*
// namespace. The magus.* members that declare into a workspace being loaded
// (magus\project and the provider selections) raise MGS1022 here, since a script
// has no magusfile to declare into. The REPL is the mode that does load one.
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
		attr.Error(err))
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

func buzzCmd(ctx context.Context, root string, args []string) (retErr error) {
	// A relative --root names a directory from where magus started, so it is pinned
	// before -C moves the process.
	if root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return fmt.Errorf("magus buzz: --root: %w", err)
		}
		root = abs
	}
	// A leading -C is taken here so it reaches `lsp`, which is intercepted before the
	// flag parse; anywhere else it arrives through bf.C below.
	if dir, rest, ok := cutBuzzWorkDir(args); ok {
		if err := buzzChdir(dir); err != nil {
			return err
		}
		args = rest
	}

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
	if bf.C != "" {
		if err := buzzChdir(bf.C); err != nil {
			return err
		}
	}
	isRepl := bf.E == "" && !bf.Test && !bf.Check && len(rest) == 0
	if !isRepl && bf.NoAutoload {
		return usagef("magus buzz: --%s applies to the REPL, not to a script, -%s, or -%s",
			gen.FlagBuzzNoAutoload, gen.FlagBuzzE, gen.FlagBuzzT)
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
	// A trace (-vvv) already carries a profile and prints it when the process
	// exits. --profile is the switch for a run that is not otherwise traced, and
	// it prints once, here, so the two do not each emit the same report.
	if bf.Profile && buzz.ProfileFromContext(ctx) == nil {
		p := buzz.NewProfile()
		ctx = buzz.WithProfile(ctx, p)
		defer func() {
			if rep := p.Report(); rep != "" {
				slog.InfoContext(ctx, strings.TrimSuffix(rep, "\n"), attr.Notice(""))
			}
		}()
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
		return buzzRepl(ctx, bf.NoAutoload)
	}

	code, name, scriptArgs, err := buzzSource(bf.E, rest)
	if err != nil {
		return err
	}
	if sep {
		scriptArgs = append(scriptArgs, forwarded...)
	}

	ctx, err = buzzRunContext(ctx, root)
	if err != nil {
		return err
	}
	// The script's magus\service leases end with it. WithoutCancel: a Ctrl-C'd script
	// still has to release what it holds.
	ctx, services := service.WithScope(ctx)
	defer func() {
		relCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.DefaultShutdownTimeout)
		defer cancel()
		services.ReleaseAll(relCtx)
	}()
	// A script is a pipe stage: it reads the records a magus stage upstream writes and
	// emits records downstream. While a magus reads its stdout, stdout carries records
	// alone and what the script prints goes to stderr, as a run's prose does.
	scriptOut := io.Writer(os.Stdout)
	if pipeStageFromContext(ctx).writesRecords() {
		scriptOut = os.Stderr
	}
	records := io.Writer(os.Stdout)
	var recorded bytes.Buffer
	if bf.Record {
		scriptOut = io.MultiWriter(scriptOut, &recorded)
		records = io.MultiWriter(records, &recorded)
		started := time.Now()
		defer func() { recordBuzzRun(ctx, root, name, code, scriptArgs, recorded.Bytes(), started, retErr) }()
	}
	in, upstream := pipeRecordsIn(ctx)
	ctx = std.WithPipe(ctx, std.PipeIO{In: in, Upstream: upstream, Out: records, Prose: scriptOut})

	// Default is strict (upstream Buzz parity, what the buzz spell's `run` op forks).
	// --embedded opts into the relaxations the magusfile engine uses, so a magus
	// module like the docs generator (render) can be run or tested here.
	var opts []buzz.Option
	if bf.Embedded {
		opts = append(opts, buzz.WithEmbedded())
	}
	sess := buzz.NewSession(ctx, opts...)
	defer func() { _ = sess.Close() }()
	// A file script's imports resolve beside the file, not beside the process's
	// working directory. A hook runs in the host's session directory, which is
	// not the directory the script lives in, and `import "lib/hook"` has to
	// follow the script. Stdin and -e have no file to stand beside.
	if dir := scriptImportDir(name); dir != "" {
		sess.SetIncludeDirs(append([]string{dir}, sess.IncludeDirs()...))
	}
	tr := traceFromContext(ctx)
	addProfileObserver(ctx, sess)
	installBuzzHost(ctx, sess, code, scriptOut, tr)
	var cov *buzz.LineCoverage
	if bf.Coverprofile != "" {
		if path := buzzCoverPath(name); path != "" {
			sess.SetSourceFile(path)
		}
		cov = buzz.NewLineCoverage()
		sess.EnableLineCoverage(cov)
	}
	stopExec := tr.phase("buzz.exec")
	if err := sess.Exec(ctx, code); err != nil {
		stopExec()
		// Strict mode refuses both a raising call and a try at the top level, so the
		// fix is the one place a raise may go, which -e can hold as well as a file.
		if d := (*diagnostics.Error)(nil); errors.As(err, &d) && d.Code == buzz.UnhandledRaise {
			return fmt.Errorf("%s: a raising call belongs in `fun main(args: [str]) > void !> any { ... }`, which magus buzz calls after the top level, and -e takes that form too: %w", name, err)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	stopExec()
	// Warnings (e.g. BZZ3001 unused imports) never fail Exec, so they only reach
	// the user if something prints them after the fact; print to stderr, matching
	// how every other magus diagnostic (and the -t failure lines below) stays off
	// stdout, which carries structured output only. -q/-s suppress them like any
	// other non-error progress output.
	if !global.quiet && !global.silent {
		for _, w := range sess.Warnings() {
			w.File = name
			slog.WarnContext(ctx, w.String(), attr.Notice(""))
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
		stopMain := tr.phase("buzz.main")
		ret, err := sess.CallValue(ctx, mainFn, []vm.Value{vm.ListValue(items)})
		stopMain()
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

// buzzRunKey is the output-store key of a --record run: the script's source and its
// arguments, so rerunning one probe adds an attempt under the same ref and a changed probe
// gets a new one.
func buzzRunKey(name, code string, args []string) string {
	sum := sha256.Sum256([]byte("buzz\x00" + name + "\x00" + code + "\x00" + strings.Join(args, "\x00")))
	return hex.EncodeToString(sum[:])
}

// recordBuzzRun keeps what a --record run printed in the output store and prints its ref
// on stderr, so a plan, a review or a job's notes can cite the run instead of describing it.
// A store it cannot reach is reported and leaves the run's own outcome alone.
func recordBuzzRun(ctx context.Context, root, name, code string, args []string, out []byte, started time.Time, runErr error) {
	if root == "" {
		root = "."
	}
	dir, err := magus.ResolveCacheDir(root)
	if err != nil {
		slog.WarnContext(ctx, "--record: no output store here", attr.Notice(""), attr.Component("magus buzz"), attr.Error(err))
		return
	}
	d := cache.OutputDescriptor{
		Project:     ".",
		Target:      "buzz " + name,
		Failed:      runErr != nil,
		TimestampMs: started.UnixMilli(),
		DurationMs:  time.Since(started).Milliseconds(),
	}
	if runErr != nil {
		d.ErrMsg = runErr.Error()
	}
	stored, err := cache.NewOutputStore(dir).Persist(ctx, buzzRunKey(name, code, args), out, d)
	if err != nil {
		slog.WarnContext(ctx, "--record", attr.Notice(""), attr.Component("magus buzz"), attr.Error(err))
		return
	}
	slog.InfoContext(ctx, "ref  "+stored.Ref, attr.Notice(""))
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

// addProfileObserver records compile phases on the Buzz profile carried by ctx.
// -vvv puts the startup trace's own profile there, so a traced `magus shell` and
// a profiled `magus buzz` describe the same events. With no profile, nothing is
// installed and the session pays nothing for the clocks.
func addProfileObserver(ctx context.Context, sess *buzz.Session) {
	if p := buzz.ProfileFromContext(ctx); p != nil {
		sess.AddCompileObserver(p)
	}
}

// buzzReachesMagus reports whether code, or any file or source module it imports
// transitively, imports the magus namespace, a magus/* module or a spell. An import
// found nowhere counts as not reaching magus, and so does one inside a comment or a
// string: the lexer, not a pattern, finds them.
func buzzReachesMagus(sess *buzz.Session, code string) bool {
	sources := map[string]string{}
	for _, m := range std.AllSource() {
		sources[m.ImportPath()] = m.Source
	}
	seen := map[string]bool{}
	var reaches func(code, dir string) bool
	reaches = func(code, dir string) bool {
		for _, path := range buzzImportPaths(code) {
			p := strings.TrimPrefix(path, "buzz:")
			if p == "magus" || strings.HasPrefix(p, "magus/") || strings.HasPrefix(p, "spells/") {
				return true
			}
			if _, native := sess.NativeModule(p); native {
				continue
			}
			if src, ok := sources[p]; ok {
				if !seen["source:"+p] {
					seen["source:"+p] = true
					if reaches(src, "") {
						return true
					}
				}
				continue
			}
			path := buzzFindImport(p, dir, sess.IncludeDirs())
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			data, err := os.ReadFile(path)
			if err == nil && reaches(string(data), filepath.Dir(path)) {
				return true
			}
		}
		return false
	}
	return reaches(code, "")
}

// buzzImportPaths lists the paths code's import statements name, or none when code
// does not lex; the session reports that error itself.
func buzzImportPaths(code string) []string {
	toks, err := token.Tokenize(code)
	if err != nil {
		return nil
	}
	var paths []string
	for i, t := range toks {
		if t.Kind != token.Import {
			continue
		}
		for _, next := range toks[i+1:] {
			if next.Kind == token.String {
				paths = append(paths, next.Val)
				break
			}
			if next.Kind != token.Ident && next.Kind != token.Comma {
				break
			}
		}
	}
	return paths
}

// buzzFindImport resolves p the way the session does for a file import: beside the
// importing file first, then each include dir.
func buzzFindImport(p, dir string, includeDirs []string) string {
	dirs := includeDirs
	if dir != "" {
		dirs = append([]string{dir}, includeDirs...)
	}
	for _, d := range dirs {
		candidate := filepath.Join(d, p+".buzz")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// installBuzzHost registers the modules a script can import.
//
// The stdlib and the spell declaration text are always installed: the first is
// what a hook imports, and the second is a few string stores. The magus
// namespace and its type mirrors are not. Building the namespace walks every
// member, and a guard hook does not import it. Measured 2026-09-28, in process:
// the namespace was 1.3ms and the closed script's own exec was 27µs. The
// workspace open these hooks already skip is the ~700ms figure; this is the
// part that was still on the tool-call path for nothing.
//
// A script whose import closure reaches magus gets the mirrors up front, so a later
// file's type of the same name still wins and an aliased import sees them. The
// resolver below covers an import the closure scan could not follow.
func installBuzzHost(ctx context.Context, sess *buzz.Session, code string, scriptOut io.Writer, tr *startupTracer) {
	stop := tr.phase("buzz.register_modules")
	bindings.RegisterModules(ctx, sess, bindings.WithScriptOutput(scriptOut))
	stop()
	stop = tr.phase("buzz.register_decls")
	bindings.RegisterSpellDecls(sess)
	stop()
	if buzzReachesMagus(sess, code) {
		stop = tr.phase("buzz.register_namespace")
		bindings.RegisterMagusNamespace(ctx, sess)
		bindings.DeclareMagusTypes(sess)
		stop()
		return
	}
	var once sync.Once
	sess.SetModuleResolver(func(importPath string) (vm.Value, bool) {
		if importPath != "magus" {
			var zero vm.Value
			return zero, false
		}
		once.Do(func() {
			stop := tr.phase("buzz.register_namespace")
			bindings.RegisterMagusNamespace(ctx, sess)
			bindings.DeclareMagusTypes(sess)
			stop()
		})
		v, ok := sess.NativeModule("magus")
		return v, ok
	})
}

// scriptImportDir is the directory a file script's imports resolve against.
// Stdin and -e have none: there is no file for a sibling import to stand beside.
func scriptImportDir(name string) string {
	switch name {
	case "", "-e", "<stdin>":
		return ""
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return filepath.Dir(name)
	}
	return filepath.Dir(abs)
}

// cutBuzzWorkDir takes a -C that leads args, in any spelling the flag package accepts.
func cutBuzzWorkDir(args []string) (dir string, rest []string, ok bool) {
	if len(args) == 0 {
		return "", args, false
	}
	switch a := args[0]; {
	case a == "-"+gen.FlagBuzzC || a == "--"+gen.FlagBuzzC:
		if len(args) < 2 {
			return "", args, false
		}
		return args[1], args[2:], true
	case strings.HasPrefix(a, "-"+gen.FlagBuzzC+"="), strings.HasPrefix(a, "--"+gen.FlagBuzzC+"="):
		_, v, _ := strings.Cut(a, "=")
		return v, args[1:], true
	}
	return "", args, false
}

// buzzChdir moves the process to dir. `magus buzz` is never forwarded to a server, so
// the directory it changes is its own.
func buzzChdir(dir string) error {
	if err := os.Chdir(dir); err != nil {
		return usagef("magus buzz: -%s %s: %v", gen.FlagBuzzC, dir, errors.Unwrap(err))
	}
	return nil
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
	fmt.Fprintln(os.Stderr, "Usage: magus buzz [-C dir] ... # every form below takes -C")
	fmt.Fprintln(os.Stderr, "       magus buzz              # open a REPL with the magusfile loaded")
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
	fmt.Fprintln(os.Stderr, "  --profile   print where compile and import time went, after the script runs")
	fmt.Fprintln(os.Stderr, "  --embedded  relax upstream strictness (top-level statements, optional")
	fmt.Fprintln(os.Stderr, "              argument labels) to match the magusfile engine")
	fmt.Fprintln(os.Stderr, "  --no-autoload  start the REPL without executing the magusfile")
	fmt.Fprintln(os.Stderr, "  -C <dir>    change to dir before anything else, as go -C does; script")
	fmt.Fprintln(os.Stderr, "              paths and imports resolve from it (lsp: only as the first flag)")
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
// built, not a second load), and the script never saw it. A `magus buzz` script
// run inside a workspace is a script ON that workspace, the same way the REPL
// already autoloads the magusfile at cwd.
//
// Best-effort: outside a workspace, or when one fails to load, the script still runs
// and the workspace-reading members raise as before. A standalone script that touches
// none of them must not be blocked by a magusfile it never asked about.
//
// Except under the sandbox: a workspace that failed to load has no policy to apply, and
// running the script anyway runs it confined by nothing. That is refused, so breaking a
// magusfile is not a way out of the sandbox.
//
// The load error is LOGGED rather than discarded. Dropping it made the two absences
// indistinguishable at the point a reader sees them: MGS1022 says "no workspace on the
// context" either way. A script inside a workspace that failed to load then reads as a
// script that was never in one, and the advice it gives ("fork instead") is wrong. A
// green local run and a red CI run of the same script looked exactly like that.
//
// The workspace's sandbox policy rides along, because a script reaches the same
// fs/proc/http bindings a target does and the guard cannot read a script body: it
// allows `magus buzz -` outright. Without the policy on ctx, sandbox.PolicyFromContext
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
// would load, and an adopted workspace (server, tests) is already open.
func buzzScriptContext(ctx context.Context, root string) (context.Context, error) {
	// --root is the directory a script runs as if started in, as -C is for make: its
	// vcs calls, execs and relative paths resolve there, not in the process cwd.
	if _, set := std.CwdFromContext(ctx); root != "" && !set {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("magus buzz: --root: %w", err)
		}
		ctx = std.WithCwd(ctx, abs)
	}
	if _, adopted := magusFromContext(ctx); !adopted && !globalCfg.Sandbox.Mode.Enabled() {
		return newLazyWorkspaceContext(ctx, root), nil
	}
	m, lerr := buzzLoadWorkspace(ctx, root)
	if lerr != nil {
		if globalCfg.Sandbox.Mode.Enabled() {
			return nil, types.WrapDiagnostic(types.WorkspaceLoadFailed, lerr,
				"the sandbox is on and the workspace failed to load, so there is no policy to run this script under: %v", lerr)
		}
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

// buzzRunContext is the context a script runs under: buzzScriptContext's, plus one
// evaluation memo for the whole run, so every graph read (magus\refs, magus\dir, ...)
// answers from one build of the graph the way a spell invocation's do. Without it each
// magus\refs call rebuilt the symbol graph, about 12s apiece on this repository.
func buzzRunContext(ctx context.Context, root string) (context.Context, error) {
	ctx, err := buzzScriptContext(ctx, root)
	if err != nil {
		return nil, err
	}
	return types.WithEvalMemo(ctx), nil
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
			level := slog.LevelWarn
			if d.Severity != buzz.SeverityWarning {
				level = slog.LevelError
				failed++
			}
			slog.LogAttrs(ctx, level, d.String(), attr.Notice(""))
			if !noted && buzzSpellImportUnresolved(d) {
				slog.InfoContext(ctx, buzzSpellImportNote, attr.Notice("note"))
				noted = true
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
// one renders as <file>:L:C, the position shape an editor can jump to. A diagnostic
// inside an imported file names that file, relative to the working directory when
// it sits beneath it.
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
	if dir := scriptImportDir(resolved); dir != "" {
		sess.SetIncludeDirs(append([]string{dir}, sess.IncludeDirs()...))
	}
	// Script output goes to STDERR on this path, unlike a run. A check prints no
	// program output of its own, so anything reaching stdout here came from an
	// imported module's top level, which Diagnostics executes; that is incidental to
	// the answer and must not be mixed into stdout with it.
	addProfileObserver(ctx, sess)
	installBuzzHost(ctx, sess, string(data), os.Stderr, traceFromContext(ctx))

	diags := sess.Diagnostics(string(data))
	wd, _ := os.Getwd()
	for i := range diags {
		switch f := diags[i].File; {
		case f == "":
			diags[i].File = resolved
		case !filepath.IsAbs(f):
			diags[i].File = filepath.Clean(f)
		default:
			if rel, err := filepath.Rel(wd, f); err == nil && !strings.HasPrefix(rel, "..") {
				diags[i].File = rel
			}
		}
	}
	return diags, nil
}

// buzzSpellImportUnresolved reports whether d is the one diagnostic --check raises
// that does not mean what it says: a magusfile's `import "magus/spell/<name>"`.
//
// Those paths are bound by the workspace loader from the resolved spell registry
// (internal/interp/runtime.go), so a bare session has nothing to resolve them
// against and reports BZZ2001. The import is fine; this mode just cannot see it.
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
// same code covers a genuine typo (`magus/spell/gooo`) and this mode cannot tell
// the two apart. What it CAN do is name the check that does settle it.
//
// That check is reassuring, and it bounds what this verb is for. A file importing a
// spell module is loaded by magus itself, and loading reports exactly these errors, so
// a magusfile and a target definition are the Buzz files that already had a check. The
// ones that did not are the ones nothing loads: a standalone script, and hook glue
// whose whole job is a side effect.
const buzzSpellImportNote = "magus/spell/* is bound by the workspace loader, so --check cannot resolve it. " +
	"A file that imports one (a magusfile, a target definition) is checked by loading it: " +
	"any magus command reports its errors."

const (
	// buzzUnresolvedImport is gopherbuzz's unresolved-import code, spelled here rather
	// than imported: cmd/magus does not otherwise depend on the diagnostics registry,
	// and the code is the stable published identifier.
	buzzUnresolvedImport = "BZZ2001"
	spellModulePrefix    = "magus/spell/"
)
