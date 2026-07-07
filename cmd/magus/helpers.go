package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
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

// withMagus injects a per-workspace Magus for daemon-adopted handlers.
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

	inspectOnce         sync.Once
	inspectValue        types.WorkspaceRepository
	inspectErr          error
	inspectRootOverride string
)

func loadMagus(ctx context.Context, rootOverride string) (*magus.Magus, error) {
	if m, ok := magusFromContext(ctx); ok { // daemon-adopted handlers bypass the singleton
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
		opts := []magus.Option{magus.WithLoadedConfig(globalCfg)}
		if lim := bootstrapLimiterFrom(ctx); lim != nil {
			opts = append(opts, workspace.WithLimiter(lim))
		}
		magusValue, magusErr = magus.Open(ctx, root, opts...)
		stop()
	})
	if rootOverride != magusRootOverride {
		panic("loadMagus: called with different rootOverride on second call")
	}
	return magusValue, magusErr
}

func inspectWorkspace(ctx context.Context, rootOverride string) (types.WorkspaceRepository, error) {
	t := traceFromContext(ctx)
	inspectOnce.Do(func() {
		inspectRootOverride = rootOverride
		defer t.phase("workspace.find_root")()
		root, err := magus.FindRoot(rootOverride)
		if err != nil {
			inspectErr = err
			return
		}
		stop := t.phase("workspace.inspect")
		inspectValue, inspectErr = magus.Inspect(ctx, root, magus.WithLoadedConfig(globalCfg))
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
