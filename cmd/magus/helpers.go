package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"

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
// and the driver runs inside the VCS's own index manipulation, once per conflicted file -
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

// loadMagus opens (once) the process's singleton workspace handle. extra Options apply only
// to the first, memoizing call - the daemon serve path passes magus.WithMetricsCollection()
// so the bridge Magus feeds the /dashboard, while one-shot CLI callers pass none and stay a
// true no-op.
func loadMagus(ctx context.Context, rootOverride string, extra ...magus.Option) (*magus.Magus, error) {
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
		opts := []magus.Option{magus.WithLoadedConfig(globalCfg), magus.WithVersion(version)}
		if lim := bootstrapLimiterFrom(ctx); lim != nil {
			opts = append(opts, workspace.WithLimiter(lim))
		}
		opts = append(opts, extra...)
		magusValue, magusErr = magus.Open(ctx, root, opts...)
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

// exitUsage is the exit code for a command-line misuse: a missing or unknown
// subcommand, a wrong argument count, an unrecognized value. It is deliberately
// distinct from 1:
//
//	0  the command did what was asked (an explicit -h/--help included)
//	1  the command was invoked correctly and the WORK failed
//	2  the invocation itself was wrong; nothing was attempted
//
// 2 matches Go's own flag package (flag.ExitOnError) and the long-standing Unix
// convention, and it is already what magus returns when a request cannot be carried
// out as stated (an unresolvable `path` node, an ambiguous `where`, a `tail` with no
// terminal). Before this was unified, the same "you forgot the subcommand" situation
// exited 0 from `config`, `self` and `merge-driver`, 1 from `man` and `completion`,
// and 2 from `self <unknown>` - so nothing scripting magus could branch on it.
const exitUsage = 2

// errUsage marks a command-line misuse so it exits exitUsage rather than the generic
// 1. Wrap the message a user needs to fix their invocation; the usage text itself is
// printed separately by the command, as it always was.
type errUsage struct{ msg string }

func (e errUsage) Error() string { return e.msg }

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

// splitOnThen splits args at the chain separator, returning the run's own args and
// the verbs to apply to what it produced.
//
// The separator is "--then" rather than "--" because "--" already forwards extra
// args to spells (`magus run test -- -run TestFoo`), and it is a separator rather
// than bare words because `magus run build web api` already takes trailing project
// args - `magus run build file x` would otherwise be indistinguishable from a
// project named "file".
// found distinguishes an ABSENT separator from one with no verb after it. Without
// it, `magus run build --then` silently ran as a plain build - the same
// accepted-and-ignored failure this grammar exists to avoid.
//
// "--" is split off FIRST and outranks it, because everything after "--" is the
// spell's verbatim argv - scanning the whole slice let `magus run test -- --then`
// hijack an argument the spell was meant to receive. The "--" and its arguments are
// re-attached to the run's own args, so `magus run test --then value -- -run TestFoo`
// still forwards -run TestFoo.
func splitOnThen(args []string) (before, after []string, found bool) {
	head, extra := splitOnDashDash(args)
	i := slices.Index(head, "--then")
	if i < 0 {
		return args, nil, false
	}
	before = slices.Clone(head[:i])
	if len(head) != len(args) {
		before = append(before, "--")
		before = append(before, extra...)
	}
	return before, head[i+1:], true
}

// hintCanonicalSpelling nudges toward the canonical name when the user typed
// another one. The fact comes off the parsed Target (Declared/DeclaredCharms), so
// this is presentation only - types.ParseTarget stays a pure function and the
// daemon and MCP paths get the same information without inheriting stderr output.
//
// Silent on canonical input, and deduped by interactive.Emit, so it teaches once
// rather than nagging.
func hintCanonicalSpelling(t types.Target) {
	if t.Declared != "" {
		interactive.Emit(os.Stderr, fmt.Sprintf("target %q is canonically %q - both work, %q is what magus reports", t.Declared, t.Name, t.Name))
	}
	for _, c := range t.DeclaredCharms {
		interactive.Emit(os.Stderr, fmt.Sprintf("charm %q is canonically %q", c, types.Normalize(c)))
	}
}
