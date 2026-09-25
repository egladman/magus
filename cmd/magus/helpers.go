package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// withDefaultCharms combines the workspace default charms (magus.yaml
// default_charms) with the per-run charms parsed from a target's :suffix:
// defaults first, per-run stacked on top, exact duplicates dropped. noDefault
// (the --no-default-charms flag) skips the defaults entirely. Only `magus run`
// and `magus x` call this; `magus affected` does not, and the ci anchor strips
// "rw" downstream in RunCI, so a defaulted rw never makes a ci run write.
func withDefaultCharms(perRun, defaults []string, noDefault bool) []string {
	if noDefault || len(defaults) == 0 {
		return perRun
	}
	out := slices.Clone(defaults)
	for _, c := range perRun {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

type traceCtxKey struct{}

func withTrace(ctx context.Context, t *startupTracer) context.Context {
	return context.WithValue(ctx, traceCtxKey{}, t)
}

func traceFromContext(ctx context.Context) *startupTracer {
	if t, ok := ctx.Value(traceCtxKey{}).(*startupTracer); ok {
		return t
	}
	return newStartupTracer(false) // no-op
}

// skipMergeDriverRefreshKey marks a workspace load that must not re-wire the merge driver.
type skipMergeDriverRefreshKey struct{}

// withoutMergeDriverRefresh suppresses the merge-driver registration refresh for loads made
// from inside the merge driver itself. EnsureMergeDriver writes the TRACKED .gitattributes,
// and the driver runs inside the VCS's own index manipulation, once per conflicted file,
// so refreshing there leaves the working tree dirty against what the VCS staged and stops
// `git rebase --continue` dead, which is the exact failure the driver was rewritten to stop
// causing. Any other caller still keeps the registration honest.
func withoutMergeDriverRefresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipMergeDriverRefreshKey{}, true)
}

func skipMergeDriverRefresh(ctx context.Context) bool {
	skip, _ := ctx.Value(skipMergeDriverRefreshKey{}).(bool)
	return skip
}

type magusCtxKey struct{}

// withMagus injects a per-workspace Magus for server-adopted handlers.
func withMagus(ctx context.Context, m *magus.Magus) context.Context {
	return context.WithValue(ctx, magusCtxKey{}, m)
}

func magusFromContext(ctx context.Context) (*magus.Magus, bool) {
	m, ok := ctx.Value(magusCtxKey{}).(*magus.Magus)
	return m, ok
}

// bootstrapLimiterKey carries a limiter shared between the proc server and workspace.
type bootstrapLimiterKey struct{}

func withBootstrapLimiter(ctx context.Context, lim *cache.Limiter) context.Context {
	return context.WithValue(ctx, bootstrapLimiterKey{}, lim)
}

func bootstrapLimiterFrom(ctx context.Context) *cache.Limiter {
	lim, _ := ctx.Value(bootstrapLimiterKey{}).(*cache.Limiter)
	return lim
}

var globalCfg config.Config

// Lazy singletons shared across subcommands.
var (
	magusOnce         sync.Once
	magusValue        *magus.Magus
	magusErr          error
	magusRootOverride string
	magusLoaded       atomic.Bool

	inspectOnce         sync.Once
	inspectValue        types.WorkspaceRepository
	inspectErr          error
	inspectRootOverride string
)

// loadMagus opens (once) the process's singleton workspace handle. extra Options apply only
// to the first, memoizing call: the server serve path passes magus.WithMetricsCollection()
// so the bridge Magus feeds the /dashboard, while one-shot CLI callers pass none and stay a
// true no-op.
func loadMagus(ctx context.Context, rootOverride string, extra ...magus.Option) (*magus.Magus, error) {
	if m, ok := magusFromContext(ctx); ok { // server-adopted handlers bypass the singleton
		return m, nil
	}
	t := traceFromContext(ctx)
	magusOnce.Do(func() {
		magusRootOverride = rootOverride
		defer t.phase("magus.find_root")()
		root, err := magus.FindRoot(rootOverride)
		if err != nil {
			magusErr = err
			return
		}
		stop := t.phase("magus.open")
		defer relaxGC()()
		opts := []magus.Option{magus.WithLoadedConfig(globalCfg), magus.WithVersion(version)}
		if lim := bootstrapLimiterFrom(ctx); lim != nil {
			opts = append(opts, workspace.WithLimiter(lim))
		}
		if globalCfg.Broker.Resolved() != types.BrokerOff {
			opts = append(opts, magus.WithBroker(processBrokerClient()))
		}
		opts = append(opts, extra...)
		magusValue, magusErr = magus.Open(ctx, root, opts...)
		magusLoaded.Store(true)
		stop()
		if magusErr == nil && !skipMergeDriverRefresh(ctx) {
			// Declared outputs are known only once the workspace is open, and they are
			// what the merge driver registers, so this is the first point that can keep
			// the registration honest. It is a no-op unless the globs actually moved.
			ensureMergeDriver(ctx, magusValue)
		}
	})
	if rootOverride != magusRootOverride {
		panic("loadMagus: called with different rootOverride on second call")
	}
	return magusValue, magusErr
}

// loadGCPercent is the GOGC a workspace load runs under. Evaluating every magusfile and
// local spell allocates heavily and retains little: measured on this repository, 400
// cut `magus ls` from 121ms to 110ms and its CPU by a quarter, for 37MB more peak RSS.
const loadGCPercent = 400

var (
	gcRelaxMu    sync.Mutex
	gcRelaxCount int
	gcRelaxPrev  int
)

// relaxGC raises GOGC for a workspace load and returns the restore. An explicit GOGC in
// the environment is the caller's choice and is left alone.
//
// Refcounted and shared across callers because loadMagus and inspectWorkspace can both
// first-load concurrently (the server bootstraps both in parallel goroutines): a plain
// save/restore pair races there, since either could observe the other's already-raised
// GOGC as "the prior value" and restore to it instead of the true original, or one
// restoring early could drop GOGC out from under the other's still-running load. Only
// the caller that takes the count from 0 saves the prior value; only the one that takes
// it back to 0 restores it.
func relaxGC() func() {
	if os.Getenv("GOGC") != "" {
		return func() {}
	}
	gcRelaxMu.Lock()
	if gcRelaxCount == 0 {
		gcRelaxPrev = debug.SetGCPercent(loadGCPercent)
	}
	gcRelaxCount++
	gcRelaxMu.Unlock()
	return func() {
		gcRelaxMu.Lock()
		gcRelaxCount--
		if gcRelaxCount == 0 {
			debug.SetGCPercent(gcRelaxPrev)
		}
		gcRelaxMu.Unlock()
	}
}

func inspectWorkspace(ctx context.Context, rootOverride string) (types.WorkspaceRepository, error) {
	t := traceFromContext(ctx)
	inspectOnce.Do(func() {
		inspectRootOverride = rootOverride
		// startup already opened this workspace for most subcommands, and an open
		// workspace answers everything an inspected one does; loading it twice doubled
		// the cost of `magus ls`.
		if magusLoaded.Load() && magusErr == nil && rootOverride == magusRootOverride {
			inspectValue = magusValue
			return
		}
		defer t.phase("workspace.find_root")()
		root, err := magus.FindRoot(rootOverride)
		if err != nil {
			inspectErr = err
			return
		}
		stop := t.phase("workspace.inspect")
		defer relaxGC()()
		inspectValue, inspectErr = magus.Inspect(ctx, root,
			magus.WithLoadedConfig(globalCfg), magus.WithVersion(version))
		stop()
	})
	if rootOverride != inspectRootOverride {
		panic("inspectWorkspace: called with different rootOverride on second call")
	}
	return inspectValue, inspectErr
}

func listTargets(scope string, targets []types.Target, source string) {
	var label string
	switch len(targets) {
	case 0:
		label = "no projects"
	case 1:
		label = targets[0].Path
	default:
		label = fmt.Sprintf("%d projects", len(targets))
	}
	if source != "" {
		label += " (" + source + ")"
	}
	slog.Info("listed targets", slog.String("scope", scope), slog.String("summary", label))
	for _, t := range targets {
		fmt.Println(t.Path)
	}
}

type errSilent struct{ exitCode int }

func (errSilent) Error() string { return "silent exit" }

// AlreadyReported says the failure has already been explained to the user, so no caller should
// print this error's text. exitCodeOf has always honored that locally; the method states
// it for the ADOPTED path too, where the error crosses a socket and the process that
// receives it cannot see this type. Without it a forwarded failure reports itself as
// "silent exit", which is a sentence about magus's internals and not about the failure.
func (errSilent) AlreadyReported() bool { return true }

// ExitCode states the process status this failure carries, for the ADOPTED path.
// exitCodeOf reads the field directly; the server holds the error as a plain `error`
// and cannot, so without the method every forwarded failure collapsed to 1 and the
// documented 1-vs-2 split existed only when no server was running.
func (e errSilent) ExitCode() int { return e.exitCode }

// exitUsage is the exit code for a command-line misuse: a missing or unknown
// subcommand, a wrong argument count, an unrecognized value. It is deliberately
// distinct from 1:
//
//	0   the command did what was asked (an explicit -h/--help included)
//	1   the command was invoked correctly and the WORK failed
//	2   the invocation itself was wrong; nothing was attempted
//	75  the machine could not seat the work; the same command succeeds later
//
// 2 matches Go's own flag package (flag.ExitOnError) and the long-standing Unix
// convention, and it is already what magus returns when a request cannot be carried
// out as stated (an unresolvable `path` node, an ambiguous `where`, an `x` with no
// terminal). Before this was unified, the same "you forgot the subcommand" situation
// exited 0 from `config`, `self` and `merge-driver`, 1 from `man` and `completion`,
// and 2 from `self <unknown>`, so nothing scripting magus could branch on it.
//
// 75 is EX_TEMPFAIL from sysexits.h, borrowed the same way. Two failures carry it: a
// contended no-wait project lock (lockContendedExit) and a step the machine's build
// budget could not seat (cache.ExitCodeMachineBusy, MGS3009). Neither is named here:
// each error states its own code and exitCodeOf reads it through proc.ExitCode, which
// is what lets the server report the same status for a run it executed on a client's
// behalf. A caller that cannot tell either from 1 reads a busy machine as a broken
// build: CI retries nothing, and an agent debugs a target that never ran.
const exitUsage = 2

// errUsage marks a command-line misuse so it exits exitUsage rather than the generic
// 1. Wrap the message a user needs to fix their invocation; the usage text itself is
// printed separately by the command, as it always was.
type errUsage struct{ msg string }

func (e errUsage) Error() string { return e.msg }

// ExitCode carries exitUsage across the server boundary; see errSilent.ExitCode.
func (errUsage) ExitCode() int { return exitUsage }

// usagef builds an errUsage with a formatted message.
func usagef(format string, a ...any) error {
	return errUsage{msg: fmt.Sprintf(format, a...)}
}

// reportedRunErr reports whether err was already surfaced to the user, per project,
// by the cache's pretty handler as a "[xx] <project> (error): ..." line — i.e. a
// spell/run failure during a fan-out. When true the top-level handler should exit
// non-zero via errSilent rather than reprinting the same text as a "[error] ..." line.
func reportedRunErr(err error) bool {
	var se *types.SpellErrors
	return errors.As(err, &se)
}

// canonicalTarget expands short target aliases at the CLI edge.
func canonicalTarget(name string) string {
	switch name {
	case "fmt":
		return "format"
	case "gen":
		return "generate"
	default:
		return name
	}
}

func splitOnDashDash(args []string) (before, after []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// hintCanonicalSpelling nudges toward the canonical name when the user typed
// another one. The fact comes off the parsed Target (Declared/DeclaredCharms), so
// this is presentation only: types.ParseTarget stays a pure function and the
// server and MCP paths get the same information without inheriting stderr output.
//
// Silent on canonical input, and deduped by interactive.Emit, so it teaches once
// rather than nagging.
func hintCanonicalSpelling(t types.Target) { hintCanonicalSpellingTo(os.Stderr, t) }

// hintCanonicalSpellingTo is hintCanonicalSpelling with the destination named, so a test
// can read what a run would print without capturing the process's stderr.
func hintCanonicalSpellingTo(w io.Writer, t types.Target) {
	if t.Declared != "" {
		interactive.Emit(w, fmt.Sprintf("target %q is canonically %q; both work, %q is what magus reports", t.Declared, t.Name, t.Name))
	}
	for _, c := range t.DeclaredCharms {
		interactive.Emit(w, fmt.Sprintf("charm %q is canonically %q", c, types.NormalizeCharm(c)))
	}
}
