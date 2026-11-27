package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/pipe"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// ScriptHost installs the host modules a script can import. bindings imports this
// package, so the caller hands the installers over rather than this package
// importing them.
type ScriptHost struct {
	RegisterModules        func(ctx context.Context, sess *buzz.Session, scriptOut io.Writer)
	RegisterSpellDecls     func(sess *buzz.Session)
	RegisterMagusNamespace func(ctx context.Context, sess *buzz.Session)
	DeclareMagusTypes      func(sess *buzz.Session) error
	// RegisterSpellImports binds `magus/spell/*` to the spells this build ships, as a
	// magusfile load does, for a check that opens no workspace.
	RegisterSpellImports func(sess *buzz.Session)
}

// ScriptOptions are the switches that shape one script run.
type ScriptOptions struct {
	// Embedded takes the magusfile engine's relaxations in place of upstream strictness.
	Embedded bool
	// Inline marks an -e snippet, which takes the embedded rules whatever Embedded says.
	Inline bool
	// Test runs the file's test blocks in place of its main, and records the run.
	Test bool
	// Record keeps what the run printed in the output store.
	Record bool
	// Restart discards the run's replay log and starts from the top. It needs Record.
	Restart bool
}

// ScriptRecord is what a recorded run printed and how it ended.
type ScriptRecord struct {
	Name, Code string
	Args       []string
	Test       bool
	Out        []byte
	Started    time.Time
	Err        error
	// DiedIn names the call a failed run was inside, "#<seq> <name>", empty when
	// none explains the failure.
	DiedIn string
}

// ScriptRunner runs Buzz scripts for `magus buzz`: it resolves the source, opens the
// workspace the script reads, and runs, tests or checks it.
type ScriptRunner struct {
	// Open opens the workspace at a root; a script that reads none never calls it.
	Open ScriptOpener
	// Adopted reports whether ctx already carries an open workspace (server, tests).
	Adopted func(ctx context.Context) bool
	// Sandbox is whether the workspace's sandbox policy must be in force before the first line runs.
	Sandbox bool
	// Quiet drops the warnings a run would print.
	Quiet bool
	Host  ScriptHost
	// Phase times a named startup phase; nil times nothing.
	Phase func(name string) func()
	// Record keeps a recorded run; nil with Options.Record set is a refusal.
	Record func(ctx context.Context, root string, rec ScriptRecord) error
	// Replay opens a recorded run's replay log, discarding the record first when
	// restart is set. nil with Options.Record set is a refusal. A test run never asks.
	Replay func(ctx context.Context, root string, rec ScriptRecord, restart bool) (*ReplayLog, error)
}

// ReplayLog is an open replay log as the runner holds it. The wiring builds it
// so this package imports no record store.
type ReplayLog struct {
	// Interceptor is installed on the run's session.
	Interceptor vm.CallInterceptor
	// Attach hands the log the run's session, once it exists. nil when the log has no use for it.
	Attach func(sess *buzz.Session)
	// DiedIn names the call a run that failed with err was inside, "#<seq> <name>", or
	// returns "" when none explains the failure.
	DiedIn func(err error) string
	// Close ends the record. ended is false when the run's context cut it short, which a
	// rerun resumes. A refusal comes back as an error that errors.As reads as a
	// replayRefusal.
	Close func(ended bool, runErr error) error
}

func (r *ScriptRunner) phase(name string) func() {
	if r.Phase == nil {
		return func() {}
	}
	return r.Phase(name)
}

// Run runs the one script eval or rest names, with forwarded as its argv after sep.
func (r *ScriptRunner) Run(ctx context.Context, root string, o ScriptOptions, eval string, rest, forwarded []string, sep bool) error {
	code, name, scriptArgs, err := scriptSource(eval, rest)
	if err != nil {
		return err
	}
	if sep {
		scriptArgs = append(scriptArgs, forwarded...)
	}
	return r.RunSource(ctx, root, o, name, code, scriptArgs)
}

// RunSource gives host-owned source the file runner's sandbox and recording.
func (r *ScriptRunner) RunSource(ctx context.Context, root string, o ScriptOptions, name, code string, args []string) error {
	return r.runScript(ctx, root, o, coverage.BuzzScript{Code: code, Name: name, Args: args}, nil)
}

// Test runs the test blocks of each script, one session apiece, and writes one
// coverprofile across them when profile names a path.
func (r *ScriptRunner) Test(ctx context.Context, root string, o ScriptOptions, profile, eval string, rest, forwarded []string) error {
	scripts, err := coverage.BuzzTestSources(scriptSource, eval, rest, forwarded)
	if err != nil {
		return err
	}
	o.Test = true
	return coverage.BuzzTestScripts(ctx, profile, scripts, func(ctx context.Context, s coverage.BuzzScript, cov *buzz.LineCoverage) error {
		return r.runScript(ctx, root, o, s, cov)
	})
}

// runScript runs one script in a session of its own, recording the run under
// -t or --record. A non-nil cov collects the script's line coverage.
func (r *ScriptRunner) runScript(ctx context.Context, root string, o ScriptOptions, src coverage.BuzzScript, cov *buzz.LineCoverage) (retErr error) {
	if o.Restart && !o.Record {
		return errors.New("magus buzz: --restart discards a replay log, and only a --record run keeps one: add --record")
	}
	ctx, err := r.RunContext(ctx, root)
	if err != nil {
		return err
	}
	ctx, services := service.WithScope(ctx)
	defer releaseScriptServices(ctx, services)
	scriptOut, records := scriptWriters(ctx)
	var calls *ReplayLog
	// A test run is always recorded: a passing one is evidence a script check can cite.
	if o.Record || o.Test {
		var recorded bytes.Buffer
		scriptOut, records = io.MultiWriter(scriptOut, &recorded), io.MultiWriter(records, &recorded)
		started := time.Now()
		defer func() {
			rec := ScriptRecord{Name: src.Name, Code: src.Code, Args: src.Args, Test: o.Test,
				Out: recorded.Bytes(), Started: started, Err: retErr}
			if calls != nil && retErr != nil {
				rec.DiedIn = calls.DiedIn(retErr)
			}
			if err := r.record(ctx, root, rec); err != nil {
				retErr = errors.Join(retErr, err)
			}
		}()
		// A test run is already evidence and replays nothing, so it keeps no calls.
		if !o.Test {
			var err error
			if calls, err = r.openReplay(ctx, root, src, o.Restart); err != nil {
				return err
			}
			defer func() { retErr = closeReplayLog(calls, ctx.Err() == nil, retErr) }()
		}
	}
	in, _ := pipe.RecordsIn(ctx)
	ctx = report.WithStage(ctx, report.Stage{In: in, Out: records, Prose: scriptOut})
	return r.execScript(ctx, o, src, scriptOut, cov, calls)
}

func (r *ScriptRunner) openReplay(ctx context.Context, root string, src coverage.BuzzScript, restart bool) (*ReplayLog, error) {
	if r.Replay == nil {
		return nil, errors.New("magus buzz: --record: no replay store is wired here")
	}
	return r.Replay(ctx, root, ScriptRecord{Name: src.Name, Code: src.Code, Args: src.Args}, restart)
}

// replayRefusal is an error that says a rerun was refused. Naming it here keeps this
// package from importing the replay log's own.
type replayRefusal interface {
	error
	Refused()
}

// closeReplayLog ends the log and folds its error into runErr. A refusal replaces runErr,
// since the script's own error is only its echo.
func closeReplayLog(calls *ReplayLog, ended bool, runErr error) error {
	err := calls.Close(ended, runErr)
	var refusal replayRefusal
	switch {
	case err == nil:
		return runErr
	case errors.As(err, &refusal):
		return err
	}
	return errors.Join(runErr, err)
}

func (r *ScriptRunner) record(ctx context.Context, root string, rec ScriptRecord) error {
	if r.Record == nil {
		return errors.New("magus buzz: --record: no recorder is wired here")
	}
	return r.Record(ctx, root, rec)
}

// releaseScriptServices ends the script's magus\service leases. WithoutCancel: a
// Ctrl-C'd script still has to release what it holds.
func releaseScriptServices(ctx context.Context, services *service.Scope) {
	relCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.DefaultShutdownTimeout)
	defer cancel()
	services.ReleaseAll(relCtx)
}

// scriptWriters returns where the script's prose and records go. While a
// magus reads its stdout, stdout carries records alone and prose goes to stderr.
func scriptWriters(ctx context.Context) (prose, records io.Writer) {
	if pipe.StageFromContext(ctx).WritesRecords() {
		return os.Stderr, os.Stdout
	}
	return os.Stdout, os.Stdout
}

func (r *ScriptRunner) execScript(ctx context.Context, o ScriptOptions, src coverage.BuzzScript, scriptOut io.Writer, cov *buzz.LineCoverage, calls *ReplayLog) error {
	sess := newScriptSession(ctx, o, src.Name, calls)
	defer func() { _ = sess.Close() }() // the script has run; a close error has no reader
	if calls != nil && calls.Attach != nil {
		calls.Attach(sess)
	}
	addProfileObserver(ctx, sess)
	lateErr, err := r.installHost(ctx, sess, src.Code, scriptOut)
	if err != nil {
		return fmt.Errorf("%s: %w", src.Name, err)
	}
	enableScriptCoverage(sess, src.Name, cov)
	stopExec := r.phase("buzz.exec")
	err = sess.Exec(ctx, src.Code)
	stopExec()
	if err != nil {
		return scriptExecError(src.Name, err, lateErr)
	}
	r.printWarnings(ctx, sess, src.Name)
	if o.Test {
		return runScriptTests(ctx, sess, src.Name, scriptOut)
	}
	return r.callMain(ctx, sess, src.Name, src.Args)
}

// newScriptSession opens a strict session, upstream Buzz parity. --embedded
// opts into the magusfile engine's relaxations, so a magus module can run here.
func newScriptSession(ctx context.Context, o ScriptOptions, name string, calls *ReplayLog) *buzz.Session {
	var opts []buzz.Option
	if calls != nil {
		opts = append(opts, buzz.WithCallInterceptor(calls.Interceptor))
	}
	// -e is the one-off exception: a snippet takes the embedded rules, as python -c
	// needs no main. Files and stdin stay strict.
	if o.Embedded || o.Inline {
		opts = append(opts, buzz.WithEmbedded())
	}
	sess := buzz.NewSession(ctx, opts...)
	// A file script's imports resolve beside the file, not beside the process's
	// working directory. A hook runs in the host's session directory, which is
	// not the directory the script lives in, and `import "lib/hook"` has to
	// follow the script. Stdin and -e have no file to stand beside.
	if dir := scriptImportDir(name); dir != "" {
		sess.SetIncludeDirs(append(scriptIncludeDirs(dir), sess.IncludeDirs()...))
	}
	return sess
}

// scriptIncludeDirs adds the workspace root after the script's directory, for
// project-relative imports. The root is FindRoot's: docs/ has its own magusfile
// and is not the workspace.
func scriptIncludeDirs(dir string) []string {
	root, err := workspace.FindRoot(dir)
	if err != nil || root == dir {
		return []string{dir}
	}
	return []string{dir, root}
}

func enableScriptCoverage(sess *buzz.Session, name string, cov *buzz.LineCoverage) {
	if cov == nil {
		return
	}
	if path := scriptCoverPath(name); path != "" {
		sess.SetSourceFile(path)
	}
	sess.EnableLineCoverage(cov)
}

func scriptExecError(name string, err error, lateErr func() error) error {
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

// printWarnings prints warnings such as BZZ3001, which never fail Exec, to
// stderr: stdout carries structured output only. -q and -s suppress them.
func (r *ScriptRunner) printWarnings(ctx context.Context, sess *buzz.Session, name string) {
	if r.Quiet {
		return
	}
	for _, w := range sess.Warnings() {
		w.File = name
		slog.WarnContext(ctx, w.String(), attr.Notice(""))
	}
}

// callMain runs an entry script's `main` once its top level has, as upstream
// does, with the args after the script path as its argv. Strict mode cannot wrap
// a script's own call to main: a top-level `try` is rejected.
func (r *ScriptRunner) callMain(ctx context.Context, sess *buzz.Session, name string, args []string) error {
	mainFn := sess.GetGlobal("main")
	if !mainFn.IsFun() {
		return nil
	}
	stopMain := r.phase("buzz.main")
	ret, err := sess.CallValue(ctx, mainFn, []vm.Value{scriptStringList(args)})
	stopMain()
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	// `fun main() > int` is upstream's exit-status convention, and the checker
	// permits it; discarding the value made such a script always exit 0.
	if ret.IsInt() && ret.AsInt() != 0 {
		return exit.Reported(int(ret.AsInt()))
	}
	return nil
}

func scriptStringList(items []string) vm.Value {
	values := make([]vm.Value, 0, len(items))
	for _, s := range items {
		values = append(values, vm.StringValue(s))
	}
	return vm.ListValue(values)
}

// runScriptTests executes every `test "..." {}` block registered while executing the
// file, printing one line per test to out and returning an error (non-zero exit) if any
// failed. A test fails when its body raises, typically an unmet std.assert.
func runScriptTests(ctx context.Context, sess *buzz.Session, name string, out io.Writer) error {
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

// addProfileObserver records compile phases on the Buzz profile carried by ctx.
// -vvv puts the startup trace's own profile there, so a traced `magus shell` and
// a profiled `magus buzz` describe the same events. With no profile, nothing is
// installed and the session pays nothing for the clocks.
func addProfileObserver(ctx context.Context, sess *buzz.Session) {
	if p := buzz.ProfileFromContext(ctx); p != nil {
		sess.AddCompileObserver(p)
	}
}

// installHost registers the modules a script can import.
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
func (r *ScriptRunner) installHost(ctx context.Context, sess *buzz.Session, code string, scriptOut io.Writer) (lateErr func() error, err error) {
	stop := r.phase("buzz.register_modules")
	r.Host.RegisterModules(ctx, sess, scriptOut)
	stop()
	stop = r.phase("buzz.register_decls")
	r.Host.RegisterSpellDecls(sess)
	stop()
	if scriptReachesMagus(sess, code) {
		stop = r.phase("buzz.register_namespace")
		defer stop()
		r.Host.RegisterMagusNamespace(ctx, sess)
		if err := r.Host.DeclareMagusTypes(sess); err != nil {
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
			stop := r.phase("buzz.register_namespace")
			r.Host.RegisterMagusNamespace(ctx, sess)
			declErr = r.Host.DeclareMagusTypes(sess)
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

// Context puts the workspace on a script's context when there is one.
//
// Without this every workspace-reading member of the magus module raised MGS1022 from
// a script (`magus\projects`, `affected`, `projectGraph`, `where`, `insight`), and the
// error told the reader to reach for the forking `magus\cmd` instead. That advice was
// sound only because nothing had put the workspace here: the process had already loaded
// one, and the script never saw it. A `magus buzz` script
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
// has to be in force before the first line runs. Sandbox is the config the open
// would load, and an adopted workspace (server, tests) is already open.
func (r *ScriptRunner) Context(ctx context.Context, root string) (context.Context, error) {
	// A top-level `magus buzz` is an invocation like any other. Without its own entry a
	// magus its script spawns inherits MAGUS_LEVEL but no ancestry, and the machine gate
	// refuses it MGS3009 under a contended budget.
	ctx = types.AppendInvocationAncestor(ctx, os.Getpid(), recorder.NewInvocationID())
	// --root is the directory a script runs as if started in, as -C is for make: its
	// vcs calls, execs and relative paths resolve there, not in the process cwd.
	if _, set := std.CurrentWorkingDirectoryFromContext(ctx); root != "" && !set {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("magus buzz: --root: %w", err)
		}
		ctx = std.WithCurrentWorkingDirectory(ctx, abs)
	}
	if !r.Adopted(ctx) && !r.Sandbox {
		return &lazyWorkspaceContext{Context: ctx, root: root, open: r.Open}, nil
	}
	ws, lerr := r.Open(ctx, root)
	if lerr != nil {
		if r.Sandbox {
			return nil, types.WrapDiagnostic(types.WorkspaceLoadFailed, lerr,
				"the sandbox is on and the workspace failed to load, so there is no policy to run this script under: %v", lerr)
		}
		warnWorkspaceNotAttached(ctx, lerr)
		return ctx, nil
	}
	if ws == nil {
		return ctx, nil
	}
	ctx = types.WithWorkspace(ctx, ws.Repo)
	// Confined by the same policy a target run gets, lease narrowing included. A
	// script is the shortest way around a boundary the sandbox enforces everywhere
	// else: `magus buzz -e 'fs\write(...)'` writes through the same bindings a spell
	// does, and leaving it unpoliced would make the write grant advice.
	sctx, serr := ws.ApplySandbox(ctx)
	if serr != nil {
		return nil, serr
	}
	return recorder.ContextWithBase(sctx, ws.CacheDir), nil
}

// RunContext is Context plus one EvalOnce for the run.
// So every magus\graph() read answers from one build, as a spell's do.
// Without it each refs call rebuilt the symbol graph, about 12s apiece here.
func (r *ScriptRunner) RunContext(ctx context.Context, root string) (context.Context, error) {
	ctx, err := r.Context(ctx, root)
	if err != nil {
		return nil, err
	}
	return types.WithEvalOnce(ctx), nil
}
