package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/gcpolicy"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/observability"
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

func bootstrapLimiterFromContext(ctx context.Context) *cache.Limiter {
	lim, _ := ctx.Value(bootstrapLimiterKey{}).(*cache.Limiter)
	return lim
}

var globalCfg config.Config

// Lazy singletons shared across subcommands.
var (
	magusOnce   sync.Once
	magusValue  *magus.Magus
	magusErr    error
	magusOpened openedRoot
	magusLoaded atomic.Bool

	inspectOnce   sync.Once
	inspectValue  types.WorkspaceRepository
	inspectErr    error
	inspectOpened openedRoot
)

// openedRoot is the workspace a lazy singleton loaded: the --root spelling it was first
// asked for, and the root that spelling resolved to ("" when resolution failed, which
// leaves the singleton holding that error).
type openedRoot struct {
	override, root string
}

// check answers whether a later caller's override names the workspace this singleton
// already loaded. The flag as given ("") and the path it resolves to are one workspace,
// and a command often holds both. A second, different workspace in one process is a
// programming error and panics naming both; an override that no longer resolves is
// returned as the caller's error.
func (o openedRoot) check(singleton, override string) error {
	if override == o.override || o.root == "" {
		return nil
	}
	root, err := workspace.FindRoot(override)
	if err != nil {
		return err
	}
	if root != o.root {
		panic(fmt.Sprintf("%s: asked for the workspace at %s after loading %s", singleton, root, o.root))
	}
	return nil
}

// loadedMagus returns the singleton workspace handle when startup, or an earlier call,
// already opened it successfully, and false without opening anything: the question a lazy
// graph read asks before deciding whether it must defer the open at all.
func loadedMagus() (*magus.Magus, bool) {
	if magusLoaded.Load() && magusErr == nil && magusValue != nil {
		return magusValue, true
	}
	return nil, false
}

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
		magusOpened.override = rootOverride
		defer t.phase("magus.find_root")()
		root, err := workspace.FindRoot(rootOverride)
		if err != nil {
			magusErr = err
			return
		}
		magusOpened.root = root
		stop := t.phase("magus.open")
		defer relaxGC()()
		opts := append(cliOptions(), workspace.WithLoadedConfig(globalCfg), magus.WithVersion(version))
		if lim := bootstrapLimiterFromContext(ctx); lim != nil {
			opts = append(opts, workspace.WithLimiter(lim))
		}
		if globalCfg.Broker.Resolved() != types.BrokerOff {
			opts = append(opts, magus.WithBroker(processBrokerClient()))
		}
		opts = append(opts, extra...)
		magusValue, magusErr = magus.Open(ctx, root, opts...)
		magusLoaded.Store(true)
		stop()
	})
	if err := magusOpened.check("loadMagus", rootOverride); err != nil {
		return nil, err
	}
	return magusValue, magusErr
}

// loadGCPercent is the GOGC a workspace load runs under. Evaluating every magusfile and
// local spell allocates heavily and retains little: measured on this repository, 400
// cut `magus ls` from 121ms to 110ms and its CPU by a quarter, for 37MB more peak RSS.
const loadGCPercent = 400

// optimization: run the first second of every process under loadGCPercent, not only the load.
//
//	measured: median wall, interleaved runs, n=60: commit-msg hook 74.1->61.6ms, tool-call hook
//	          89.7->78.1ms, describe targets 240.8->233.9ms; one fewer GC each.
//	trade-off: a long-lived process grows its heap 5x live for its first second.
//	assumes:  darwin arm64, 10 P; the GCs a short process pays are the 4MB-minimum-heap ones.
//
// GC off under a 512MiB limit ran level with 400 and peaked 100MB higher on describe targets.
// GOGC 50, 100 and 200, GOMAXPROCS 2 and 4, and a freed 64 to 256MB ballast all lost to 400.
const startupGCWindow = time.Second

// relaxStartupGC holds relaxGC for window, so a command that exits sooner never restores it.
// It takes over gcpolicy's init raise, restoring the GOGC that raise replaced.
func relaxStartupGC(window time.Duration) {
	restore := relaxGC()
	if prior, ok := gcpolicy.Prior(); ok {
		gcRelaxMu.Lock()
		if gcRelaxCount == 1 && gcRelaxPrev == gcpolicy.Percent {
			gcRelaxPrev = prior
		}
		gcRelaxMu.Unlock()
	}
	time.AfterFunc(window, restore)
}

var (
	gcRelaxMu    sync.Mutex
	gcRelaxCount int
	gcRelaxPrev  int
)

// relaxGC raises GOGC for a workspace load and returns the restore. An explicit GOGC in
// the environment is the caller's choice and is left alone.
//
// Refcounted and shared across callers because loadMagus and inspectWorkspace can both
// first-load concurrently (the server bootstraps both in parallel goroutines). A plain
// save/restore pair races there: either could observe the other's already-raised GOGC
// as "the prior value" and restore to it instead of the true original, or one
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
		inspectOpened.override = rootOverride
		// startup already opened this workspace for most subcommands, and an open
		// workspace answers everything an inspected one does; loading it twice doubled
		// the cost of `magus ls`.
		opened := magusLoaded.Load() && magusErr == nil && magusValue != nil
		if opened && rootOverride == magusOpened.override {
			inspectOpened.root, inspectValue = magusOpened.root, magusValue
			return
		}
		defer t.phase("workspace.find_root")()
		root, err := workspace.FindRoot(rootOverride)
		if err != nil {
			inspectErr = err
			return
		}
		inspectOpened.root = root
		if opened && root == magusOpened.root {
			inspectValue = magusValue
			return
		}
		stop := t.phase("workspace.inspect")
		defer relaxGC()()
		inspectValue, inspectErr = magus.Inspect(ctx, root,
			append(cliOptions(), workspace.WithLoadedConfig(globalCfg), magus.WithVersion(version))...)
		stop()
	})
	if err := inspectOpened.check("inspectWorkspace", rootOverride); err != nil {
		return nil, err
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
		label += fmt.Sprintf(" (%s)", source)
	}
	slog.Info("listed targets", slog.String("scope", scope), slog.String("summary", label))
	for _, t := range targets {
		fmt.Println(t.Path)
	}
}

// reportedRunError reports whether err was already surfaced to the user, per project,
// by the cache's pretty handler as a "[xx] <project> (error): ..." line — i.e. a
// spell/run failure during a fan-out. When true the top-level handler should exit
// non-zero via exit.Reported rather than reprinting the same text as a "[error] ..." line.
func reportedRunError(err error) bool {
	var se *types.SpellErrors
	var logged cache.ReportedError
	return errors.As(err, &se) || errors.As(err, &logged)
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
// Silent on canonical input, and deduped by interactive.Hint, so it teaches once
// rather than nagging.
func hintCanonicalSpelling(ctx context.Context, t types.Target) {
	if t.Declared != "" {
		interactive.Hint(ctx, fmt.Sprintf("target %q is canonically %q; both work, %q is what magus reports", t.Declared, t.Name, t.Name))
	}
	for _, c := range t.DeclaredCharms {
		interactive.Hint(ctx, fmt.Sprintf("charm %q is canonically %q", c, types.NormalizeCharm(c)))
	}
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ", ") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// cliOptions are what every workspace the CLI opens is given: this process's environment,
// stderr and command line, which a workspace the library opens reads none of unless
// given, and the reference spawn rule, since a workspace has no default one.
func cliOptions() []magus.Option {
	return []magus.Option{
		magus.WithEnv(os.Environ()), magus.WithStderr(os.Stderr), magus.WithCommandLine(os.Args),
		magus.WithSpawnRule(magus.TierCeiling),
	}
}

// magusInternals is what workspace.ViewAs adds to an opened *magus.Magus for magus's own
// packages: the methods whose signatures name internal types, which the public API leaves
// off.
type magusInternals interface {
	types.WorkspaceRepository
	KnowledgeGraph(ctx context.Context, req types.GraphRequest) (*knowledge.Graph, error)
	LastRecordedRun(ctx context.Context, projectPath, target string) (cache.RecordedRun, error)
	Telemetry() observability.Provider
}

// viewInternals is m as workspace.ViewAs shows it to magus's own packages. It is also what a
// context carries as the workspace (types.WithWorkspace), so std and the Buzz bindings
// find the methods they assert for.
func viewInternals(m *magus.Magus) magusInternals { return workspace.ViewAs[magusInternals](m) }
