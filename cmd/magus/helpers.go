package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/gcpolicy"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
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
	t := observability.TraceFromContext(ctx)
	magusOnce.Do(func() {
		magusOpened.override = rootOverride
		defer t.Phase("magus.find_root")()
		root, err := workspace.FindRoot(rootOverride)
		if err != nil {
			magusErr = err
			return
		}
		magusOpened.root = root
		stop := t.Phase("magus.open")
		defer gcpolicy.Relax()()
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

func inspectWorkspace(ctx context.Context, rootOverride string) (types.WorkspaceRepository, error) { //nolint:ireturn // the workspace backing is chosen at runtime
	t := observability.TraceFromContext(ctx)
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
		defer t.Phase("workspace.find_root")()
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
		stop := t.Phase("workspace.inspect")
		defer gcpolicy.Relax()()
		inspectValue, inspectErr = magus.Inspect(ctx, root,
			append(cliOptions(), workspace.WithLoadedConfig(globalCfg), magus.WithVersion(version))...)
		stop()
	})
	if err := inspectOpened.check("inspectWorkspace", rootOverride); err != nil {
		return nil, err
	}
	return inspectValue, inspectErr
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

// hintCanonicalSpelling shows the nudges hint.CanonicalSpelling offers once each (interactive.Hint dedupes),
// so a run teaches a spelling once rather than once per project.
func hintCanonicalSpelling(ctx context.Context, t types.Target) {
	for _, msg := range hint.CanonicalSpelling(t) {
		interactive.Hint(ctx, msg)
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
	CheckSpawn(parent, child agent.ModelTier) error
	SpawnCeiling(ctx context.Context) (builtin.Setting, error)
}

// viewInternals is m as workspace.ViewAs shows it to magus's own packages. It is also what a
// context carries as the workspace (types.WithWorkspace), so std and the Buzz bindings
// find the methods they assert for.
func viewInternals(m *magus.Magus) magusInternals { return workspace.ViewAs[magusInternals](m) } //nolint:ireturn // the workspace backing is chosen at runtime
