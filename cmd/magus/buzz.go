package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// buzzCommand runs Buzz source from a file, stdin, or an inline snippet using the
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
	// judge is the one ApplySandbox installs, set once attached.
	judge    std.EditJudge
	judgeErr error
}

var (
	workspaceContextKey = contextKeyReadBy(func(ctx context.Context) { types.WorkspaceFromContext(ctx) })
	trailContextKey     = contextKeyReadBy(func(ctx context.Context) { trail.BaseFromContext(ctx) })
	editJudgeContextKey = contextKeyReadBy(func(ctx context.Context) { std.EditJudgeFromContext(ctx) })
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
	case editJudgeContextKey:
		return c.editJudge()
	}
	return c.Context.Value(key)
}

// editJudge defers the open to the first write into the workspace,
// so a write to a temp or env file opens nothing.
// A workspace that fails to open refuses the write.
func (c *lazyWorkspaceContext) editJudge() std.EditJudge {
	return std.EditJudge{
		Covers: func(ctx context.Context, path string) bool {
			root, err := workspace.FindRoot(c.root)
			if err != nil || !inDir(root, path) {
				return false
			}
			c.once.Do(c.attach)
			return c.judge.Covers == nil || c.judge.Covers(ctx, path)
		},
		Grade: func(ctx context.Context, path string, before, after []byte) error {
			c.once.Do(c.attach)
			if c.judge.Grade == nil {
				return fmt.Errorf("fs write %s: refused: the workspace that grades it did not open: %w", path,
					cmp.Or(c.judgeErr, errors.New("it attached no edit judge")))
			}
			return c.judge.Grade(ctx, path, before, after)
		},
	}
}

// inDir reports whether path, made absolute, lies under dir.
func inDir(dir, path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dir, abs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// attach opens against the parent context, so the open cannot re-enter Value and
// deadlock on once.
func (c *lazyWorkspaceContext) attach() {
	m, err := buzzLoadWorkspace(c.Context, c.root)
	if err != nil {
		c.judgeErr = err
		warnWorkspaceNotAttached(err)
		return
	}
	if m == nil {
		return
	}
	c.workspace = viewInternals(m)
	c.trailBase = m.CacheDir()
	// The judge the eager door gets, the sandbox being off here.
	sctx, err := m.ApplySandbox(c.Context)
	if err != nil {
		c.judgeErr = err
		return
	}
	c.judge, _ = std.EditJudgeFromContext(sctx)
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

func buzzCommand(ctx context.Context, root string, args []string) (retErr error) {
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
		return lspCommand(ctx, args[1:])
	}

	// Everything after `--` is the SCRIPT's argv, never magus's. The boundary is
	// needed because commandParse reorders flags ahead of positionals, so a bare
	// `script.buzz --raw` would have magus parsing --raw and failing; it is also the
	// separator this CLI already uses to forward args to a tool (`run go::go-test . --
	// -run X`). A script reads them as main's [str], the way cmd/buzz and upstream do.
	args, sep, forwarded := splitScriptArgs(args)

	// Bound from the command registry rather than declared here. The -t/--test pair
	// is ONE switch, which a generated binder can only express because the registry
	// marks the second AliasOf the first; modeled as two flags they would get two
	// destinations and the shorthand would parse and then do nothing.
	var bf *flaggen.BuzzFlags
	rest, err := commandParse("buzz", args, func(fs *flag.FlagSet) {
		bf = flaggen.BindBuzz(fs)
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
	if !isRepl && !bf.Autoload {
		return exit.Usagef("magus buzz: --%s applies to the REPL, not to a script, -%s, or -%s",
			"no-"+flaggen.FlagBuzzAutoload, flaggen.FlagBuzzE, flaggen.FlagBuzzT)
	}
	if bf.Coverprofile != "" && !bf.Test {
		return exit.Usagef("magus buzz: --%s requires -%s", flaggen.FlagBuzzCoverprofile, flaggen.FlagBuzzT)
	}
	// Refused rather than ordered, because either order is a defensible reading and
	// picking one silently gives back an answer to a question nobody asked: -t runs the
	// file, --check is the mode that does not.
	if bf.Check && bf.Test {
		return exit.Usagef("magus buzz: --%s and -%s are different modes; --%s does not run the file",
			flaggen.FlagBuzzCheck, flaggen.FlagBuzzT, flaggen.FlagBuzzCheck)
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
			return exit.Usagef("magus buzz: --%s takes one or more file paths", flaggen.FlagBuzzCheck)
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
		return buzzRepl(ctx, !bf.Autoload)
	}

	if bf.Test {
		scripts, err := buzzTestSources(bf.E, rest, forwarded)
		if err != nil {
			return err
		}
		return buzzTestScripts(ctx, root, bf, scripts)
	}
	code, name, scriptArgs, err := buzzSource(bf.E, rest)
	if err != nil {
		return err
	}
	if sep {
		scriptArgs = append(scriptArgs, forwarded...)
	}
	return buzzRunScript(ctx, root, bf, buzzScript{code: code, name: name, args: scriptArgs}, nil)
}

// buzzScript is one program a run executes, with the name its diagnostics carry.
type buzzScript struct {
	code, name string
	args       []string
}

// buzzTestSources resolves what -t tests: an -e snippet, piped stdin, or every
// path given. Args after -- go to a single script, never to several.
func buzzTestSources(eval string, paths, forwarded []string) ([]buzzScript, error) {
	if len(paths) > 1 && len(forwarded) > 0 {
		return nil, exit.Usagef("magus buzz: args after -- go to one script, and -%s was given %d", flaggen.FlagBuzzT, len(paths))
	}
	if eval != "" {
		code, name, _, err := buzzSource(eval, paths)
		return []buzzScript{{code: code, name: name, args: forwarded}}, err
	}
	return buzzReadScripts(paths, forwarded)
}

func buzzReadScripts(paths, forwarded []string) ([]buzzScript, error) {
	if len(paths) == 0 {
		return buzzStdinScript(forwarded)
	}
	scripts := make([]buzzScript, 0, len(paths))
	for _, p := range paths {
		code, name, _, err := buzzSource("", []string{p})
		if err != nil {
			return nil, err
		}
		scripts = append(scripts, buzzScript{code: code, name: name, args: forwarded})
	}
	return scripts, nil
}

// buzzStdinScript reads the script from stdin only when one is piped. A terminal
// would block, and /dev/null would pass having tested nothing.
func buzzStdinScript(forwarded []string) ([]buzzScript, error) {
	if !stdinIsPiped() {
		return nil, exit.Usagef("magus buzz: -%s <file>... names the scripts to test; none was given and nothing is piped on stdin", flaggen.FlagBuzzT)
	}
	code, name, _, err := buzzSource("", nil)
	return []buzzScript{{code: code, name: name, args: forwarded}}, err
}

// stdinIsPiped reports whether stdin is a pipe or a regular file, the two that
// carry a script. A terminal and /dev/null are character devices.
func stdinIsPiped() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeNamedPipe != 0 || info.Mode().IsRegular()
}

// buzzTestScripts tests every script, each in its own session, so a failing one
// does not hide the next. One coverprofile holds every script's lines.
func buzzTestScripts(ctx context.Context, root string, bf *flaggen.BuzzFlags, scripts []buzzScript) error {
	var cov *buzz.LineCoverage
	if bf.Coverprofile != "" {
		cov = buzz.NewLineCoverage()
	}
	errs := make([]error, 0, len(scripts)+1)
	for _, s := range scripts {
		errs = append(errs, buzzRunScript(ctx, root, bf, s, cov))
	}
	return errors.Join(append(errs, writeBuzzCoverage(bf.Coverprofile, cov))...)
}

func writeBuzzCoverage(path string, cov *buzz.LineCoverage) error {
	if cov == nil {
		return nil
	}
	if err := os.WriteFile(path, []byte(cov.Report()), 0o644); err != nil {
		return fmt.Errorf("magus buzz: write --%s: %w", flaggen.FlagBuzzCoverprofile, err)
	}
	return nil
}

// buzzRunScript runs one script in a session of its own, recording the run under
// -t or --record. A non-nil cov collects the script's line coverage.
func buzzRunScript(ctx context.Context, root string, bf *flaggen.BuzzFlags, src buzzScript, cov *buzz.LineCoverage) (retErr error) {
	ctx, err := buzzRunContext(ctx, root)
	if err != nil {
		return err
	}
	ctx, services := service.WithScope(ctx)
	defer releaseBuzzServices(ctx, services)
	scriptOut, records := buzzScriptWriters(ctx)
	// A test run is always recorded: a passing one is evidence a script check can cite.
	if bf.Record || bf.Test {
		var recorded bytes.Buffer
		scriptOut, records = io.MultiWriter(scriptOut, &recorded), io.MultiWriter(records, &recorded)
		started := time.Now()
		defer func() {
			if err := recordBuzzRun(ctx, root, src.name, src.code, src.args, bf.Test, recorded.Bytes(), started, retErr); err != nil {
				retErr = errors.Join(retErr, err)
			}
		}()
	}
	in, _ := pipeRecordsIn(ctx)
	ctx = report.WithStage(ctx, report.Stage{In: in, Out: records, Prose: scriptOut})
	return buzzExecScript(ctx, bf, src, scriptOut, cov)
}

// releaseBuzzServices ends the script's magus\service leases. WithoutCancel: a
// Ctrl-C'd script still has to release what it holds.
func releaseBuzzServices(ctx context.Context, services *service.Scope) {
	relCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.DefaultShutdownTimeout)
	defer cancel()
	services.ReleaseAll(relCtx)
}

// buzzScriptWriters returns where the script's prose and records go. While a
// magus reads its stdout, stdout carries records alone and prose goes to stderr.
func buzzScriptWriters(ctx context.Context) (prose, records io.Writer) {
	if pipeStageFromContext(ctx).writesRecords() {
		return os.Stderr, os.Stdout
	}
	return os.Stdout, os.Stdout
}

func buzzExecScript(ctx context.Context, bf *flaggen.BuzzFlags, src buzzScript, scriptOut io.Writer, cov *buzz.LineCoverage) error {
	sess := newBuzzScriptSession(ctx, bf, src.name)
	defer func() { _ = sess.Close() }()
	tr := traceFromContext(ctx)
	addProfileObserver(ctx, sess)
	lateErr, err := installBuzzHost(ctx, sess, src.code, scriptOut, tr)
	if err != nil {
		return fmt.Errorf("%s: %w", src.name, err)
	}
	buzzEnableCoverage(sess, src.name, cov)
	stopExec := tr.phase("buzz.exec")
	err = sess.Exec(ctx, src.code)
	stopExec()
	if err != nil {
		return buzzExecError(src.name, err, lateErr)
	}
	printBuzzWarnings(ctx, sess, src.name)
	if bf.Test {
		return runBuzzTests(ctx, sess, src.name, scriptOut)
	}
	return buzzCallMain(ctx, sess, src, tr)
}

// newBuzzScriptSession opens a strict session, upstream Buzz parity. --embedded
// opts into the magusfile engine's relaxations, so a magus module can run here.
func newBuzzScriptSession(ctx context.Context, bf *flaggen.BuzzFlags, name string) *buzz.Session {
	var opts []buzz.Option
	// -e is the one-off exception: a snippet takes the embedded rules, as python -c
	// needs no main. Files and stdin stay strict.
	if bf.Embedded || bf.E != "" {
		opts = append(opts, buzz.WithEmbedded())
	}
	sess := buzz.NewSession(ctx, opts...)
	// A file script's imports resolve beside the file, not beside the process's
	// working directory. A hook runs in the host's session directory, which is
	// not the directory the script lives in, and `import "lib/hook"` has to
	// follow the script. Stdin and -e have no file to stand beside.
	if dir := scriptImportDir(name); dir != "" {
		sess.SetIncludeDirs(append([]string{dir}, sess.IncludeDirs()...))
	}
	return sess
}

func buzzEnableCoverage(sess *buzz.Session, name string, cov *buzz.LineCoverage) {
	if cov == nil {
		return
	}
	if path := buzzCoverPath(name); path != "" {
		sess.SetSourceFile(path)
	}
	sess.EnableLineCoverage(cov)
}

func buzzExecError(name string, err error, lateErr func() error) error {
	if declErr := lateErr(); declErr != nil {
		return fmt.Errorf("%s: %w", name, declErr)
	}
	// Strict mode refuses both a raising call and a try at the top level, so the
	// fix is the one place a raise may go, which -e can hold as well as a file.
	if d := (*diagnostics.Error)(nil); errors.As(err, &d) && d.Code == buzz.UnhandledRaise {
		return fmt.Errorf("%s: a raising call belongs in `fun main(args: [str]) > void !> any { ... }`, which magus buzz calls after the top level, and -e takes that form too: %w", name, err)
	}
	return fmt.Errorf("%s: %w", name, err)
}

// printBuzzWarnings prints warnings such as BZZ3001, which never fail Exec, to
// stderr: stdout carries structured output only. -q and -s suppress them.
func printBuzzWarnings(ctx context.Context, sess *buzz.Session, name string) {
	if global.quiet || global.silent {
		return
	}
	for _, w := range sess.Warnings() {
		w.File = name
		slog.WarnContext(ctx, w.String(), attr.Notice(""))
	}
}

// buzzCallMain runs an entry script's `main` once its top level has, as upstream
// does, with the args after the script path as its argv. Strict mode cannot wrap
// a script's own call to main: a top-level `try` is rejected.
func buzzCallMain(ctx context.Context, sess *buzz.Session, src buzzScript, tr *startupTracer) error {
	mainFn := sess.GetGlobal("main")
	if !mainFn.IsFun() {
		return nil
	}
	stopMain := tr.phase("buzz.main")
	ret, err := sess.CallValue(ctx, mainFn, []vm.Value{buzzStringList(src.args)})
	stopMain()
	if err != nil {
		return fmt.Errorf("%s: %w", src.name, err)
	}
	// `fun main() > int` is upstream's exit-status convention, and the checker
	// permits it; discarding the value made such a script always exit 0.
	if ret.IsInt() && ret.AsInt() != 0 {
		return exit.Reported(int(ret.AsInt()))
	}
	return nil
}

func buzzStringList(items []string) vm.Value {
	values := make([]vm.Value, 0, len(items))
	for _, s := range items {
		values = append(values, vm.StringValue(s))
	}
	return vm.ListValue(values)
}

// buzzRunKey is the output-store key of a --record run: the script's source and its
// arguments, so rerunning one probe adds an attempt under the same ref and a changed probe
// gets a new one.
func buzzRunKey(name, code, lease string, args []string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("buzz\x00%s\x00%s\x00%s\x00%s", name, code, lease, strings.Join(args, "\x00"))))
	return hex.EncodeToString(sum[:])
}

// recordBuzzRun keeps what a recorded run printed in the output store and prints its ref
// on stderr, so a plan, a review or a job's notes can cite the run instead of describing it.
// A test run is stored as `buzz -t <script>`, apart from the script's main.
// The record holds the args and the lease, so a check binds to both.
// A failure to record is returned. The ref skips slog, which silent mode drops.
func recordBuzzRun(ctx context.Context, root, name, code string, args []string, test bool, out []byte, started time.Time, runErr error) error {
	if root == "" {
		root = "."
	}
	dir, err := magus.ResolveCacheDir(root, cliOptions()...)
	if err != nil {
		return fmt.Errorf("magus buzz: --record: no output store here: %w", err)
	}
	lease, err := recordLease(ctx, root, name)
	if err != nil {
		return fmt.Errorf("magus buzz: --record: %w", err)
	}
	target, key := "buzz "+name, buzzRunKey(name, code, lease, args)
	if test {
		target, key = "buzz -t "+name, buzzRunKey(name, code, lease, append([]string{"-t"}, args...))
	}
	d := cache.OutputDescriptor{
		Project:     ".",
		Target:      target,
		Failed:      runErr != nil,
		TimestampMs: started.UnixMilli(),
		DurationMs:  time.Since(started).Milliseconds(),
		ExtraArgs:   slices.Clone(args),
		Lease:       lease,
	}
	if runErr != nil {
		d.ErrMsg = runErr.Error()
	}
	stored, err := cache.NewOutputStore(dir).Persist(ctx, key, out, d)
	if err != nil {
		return fmt.Errorf("magus buzz: --record: store the run: %w", err)
	}
	_, err = fmt.Fprintln(os.Stderr, "ref  "+stored.Ref)
	return err
}

// recordLease is the lease a recorded run of script acts under.
func recordLease(ctx context.Context, root, script string) (string, error) {
	here, err := resolvedDir(root)
	if err != nil {
		return "", err
	}
	lease, _, err := checkoutLease(ctx, here)
	if err != nil || lease != "" {
		return lease, err
	}
	rows, err := readJobRows(ctx, here)
	if err != nil {
		return "", err
	}
	return scriptCheckHolder(rows, here, script), nil
}

// scriptCheckHolder names the one live row here whose goal checks script.
// A CLI process cannot see the host session a hook bound its worker by.
// Two such rows leave it empty: the run cannot say whose it was.
func scriptCheckHolder(rows []types.Job, root, script string) string {
	here, err := resolvedDir(root)
	if err != nil {
		return ""
	}
	holder := ""
	for _, row := range rows {
		there, err := resolvedDir(row.CheckoutRoot)
		if err != nil || there != here || !job.Editing(row) || !slices.ContainsFunc(row.EffectiveGoals(), func(g types.Goal) bool {
			return g.Check.Script != "" && path.Clean(g.Check.Script) == path.Clean(script)
		}) {
			continue
		}
		if holder != "" {
			return ""
		}
		holder = row.ID
	}
	return holder
}

// resolvedDir is dir absolute with symlinks followed: exec records the
// resolved checkout, and a shell's cwd may name it through a link.
func resolvedDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
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
// file, printing one line per test to out and returning an error (non-zero exit) if any
// failed. A test fails when its body raises, typically an unmet std.assert.
func runBuzzTests(ctx context.Context, sess *buzz.Session, name string, out io.Writer) error {
	tests := sess.Tests()
	if len(tests) == 0 {
		fmt.Fprintf(out, "%s: no tests\n", name)
		return nil
	}
	failed := 0
	skipped := 0
	for _, tc := range tests {
		_, err := sess.CallValue(ctx, tc.Fn, []vm.Value(nil))
		if err == nil {
			fmt.Fprintf(out, "ok    test %q\n", tc.Name)
			continue
		}
		if reason, ok := buzzstd.SkipMessage(err); ok {
			skipped++
			fmt.Fprintf(out, "skip  test %q%s\n", tc.Name, skipReason(reason))
			continue
		}
		failed++
		fmt.Fprintf(out, "FAIL  test %q\n      %v\n", tc.Name, err)
	}
	fmt.Fprintf(out, "---\n%d passed, %d failed, %d skipped\n", len(tests)-failed-skipped, failed, skipped)
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
	return fmt.Sprintf(" (%s)", reason)
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
//
// The eager path returns a declaration failure at once. The lazy resolver
// cannot return one, so lateErr reports it once the script has run.
func installBuzzHost(ctx context.Context, sess *buzz.Session, code string, scriptOut io.Writer, tr *startupTracer) (lateErr func() error, err error) {
	stop := tr.phase("buzz.register_modules")
	bindings.RegisterModules(ctx, sess, bindings.WithScriptOutput(scriptOut))
	stop()
	stop = tr.phase("buzz.register_decls")
	bindings.RegisterSpellDecls(sess)
	stop()
	if buzzReachesMagus(sess, code) {
		stop = tr.phase("buzz.register_namespace")
		defer stop()
		bindings.RegisterMagusNamespace(ctx, sess)
		if err := bindings.DeclareMagusTypes(sess); err != nil {
			return nil, err
		}
		return func() error { return nil }, nil
	}
	var once sync.Once
	var declErr error
	sess.SetModuleResolver(func(importPath string) (vm.Value, bool) {
		if importPath != "magus" {
			var zero vm.Value
			return zero, false
		}
		once.Do(func() {
			stop := tr.phase("buzz.register_namespace")
			bindings.RegisterMagusNamespace(ctx, sess)
			declErr = bindings.DeclareMagusTypes(sess)
			stop()
		})
		if declErr != nil {
			var zero vm.Value
			return zero, false
		}
		v, ok := sess.NativeModule("magus")
		return v, ok
	})
	return func() error { return declErr }, nil
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
	case a == "-"+flaggen.FlagBuzzC || a == "--"+flaggen.FlagBuzzC:
		if len(args) < 2 {
			return "", args, false
		}
		return args[1], args[2:], true
	case strings.HasPrefix(a, "-"+flaggen.FlagBuzzC+"="), strings.HasPrefix(a, "--"+flaggen.FlagBuzzC+"="):
		_, v, _ := strings.Cut(a, "=")
		return v, args[1:], true
	}
	return "", args, false
}

// buzzChdir moves the process to dir. `magus buzz` is never forwarded to a server, so
// the directory it changes is its own.
func buzzChdir(dir string) error {
	if err := os.Chdir(dir); err != nil {
		return exit.Usagef("magus buzz: -%s %s: %v", flaggen.FlagBuzzC, dir, errors.Unwrap(err))
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
	fmt.Fprintln(os.Stderr, "       magus buzz -t <file>... # run each file's test \"...\" {} blocks")
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
	fmt.Fprintln(os.Stderr, "  -e <code>   execute code given on the command line; it takes the --embedded rules, so no main")
	fmt.Fprintln(os.Stderr, "  -t, -test   run each file's test \"...\" {} blocks and report pass/fail; each run is")
	fmt.Fprintln(os.Stderr, "              recorded as `buzz -t <file>` and its ref printed, as --record does")
	fmt.Fprintln(os.Stderr, "  --record    keep what the run printed in the output store and print its ref")
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
	// A top-level `magus buzz` is an invocation like any other. Without its own entry a
	// magus its script spawns inherits MAGUS_LEVEL but no ancestry, and the machine gate
	// refuses it MGS3009 under a contended budget.
	ctx = types.AppendInvocationAncestor(ctx, os.Getpid(), journal.NewInvocationID())
	// --root is the directory a script runs as if started in, as -C is for make: its
	// vcs calls, execs and relative paths resolve there, not in the process cwd.
	if _, set := std.CurrentWorkingDirectoryFromContext(ctx); root != "" && !set {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("magus buzz: --root: %w", err)
		}
		ctx = std.WithCurrentWorkingDirectory(ctx, abs)
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
	ctx = types.WithWorkspace(ctx, viewInternals(m))
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

// buzzRunContext is buzzScriptContext's context plus one memo for the run.
// So every magus\graph() read answers from one build, as a spell's do.
// Without it each refs call rebuilt the symbol graph, about 12s apiece here.
func buzzRunContext(ctx context.Context, root string) (context.Context, error) {
	ctx, err := buzzScriptContext(ctx, root)
	if err != nil {
		return nil, err
	}
	return types.WithEvalMemo(ctx), nil
}

// buzzCheck parses and type-checks each named file WITHOUT running it, printing
// every diagnostic and failing on any of them, warnings included.
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
			}
			failed++
			slog.LogAttrs(ctx, level, d.String(), attr.Notice(""))
			if !noted && buzzSpellImportUnresolved(d) {
				slog.InfoContext(ctx, buzzSpellImportNote, attr.Notice("note"))
				noted = true
			}
		}
	}
	if failed > 0 {
		// Reported: every diagnostic is already on stderr with its own position, so a
		// trailing "N errors" wrapper would be the only line without one.
		return exit.Reported(exit.Failure)
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
	lateErr, err := installBuzzHost(ctx, sess, string(data), os.Stderr, traceFromContext(ctx))
	if err != nil {
		return nil, err
	}

	diags := sess.Diagnostics(string(data))
	if err := lateErr(); err != nil {
		return nil, err
	}
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
