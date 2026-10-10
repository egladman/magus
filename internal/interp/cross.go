package interp

import (
	"context"
	"log/slog"
	"slices"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/log/attr"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

type crossDispatchCtxKey struct{}
type crossAncestorCtxKey struct{}

// CrossDispatch runs cross-project target dependencies (declared via a project import,
// then referenced as <alias>.<target>) and detects cross-project cycles. It records runs
// in the run's TargetRuns, so a remote target runs at most once whoever reaches it first.
// Dispatch is safe for concurrent use.
type CrossDispatch struct {
	runs    *cache.TargetRuns
	run     func(ctx context.Context, dir, target string) error // RunDir; swappable in tests
	runStep func(ctx context.Context, p *types.Project, step cache.Step) error
}

// RunStepsWith sets how DispatchStep runs a step a reader keyed itself. The run scheduler
// passes its own step runner, so the step runs inside this invocation, under the locks it
// already holds.
func (c *CrossDispatch) RunStepsWith(fn func(ctx context.Context, p *types.Project, step cache.Step) error) {
	c.runStep = fn
}

// DispatchStep runs step, a target of p keyed by its caller, through the run that owns
// ctx: at most once per invocation, cached, and without the project lock a nested run would
// be refused (MGS3007). It reports false when no run scheduler installed a runner, and the
// caller then runs the target on its own.
//
// It exists for a reader that freshens what it reads, such as a symbol index, from inside a
// target body: the outer run holds the project's lock for its whole invocation.
func (c *CrossDispatch) DispatchStep(ctx context.Context, p *types.Project, step cache.Step) (bool, error) {
	if c == nil || c.runStep == nil {
		return false, nil
	}
	return true, c.runStep(ctx, p, step)
}

// NewCrossDispatch returns a coordinator that records its runs in runs.
func NewCrossDispatch(runs *cache.TargetRuns) *CrossDispatch {
	return &CrossDispatch{
		runs: runs,
		// A cross-project dependency is run for its EFFECT (its outputs feed the
		// dependent), so its return value has no consumer here and is dropped.
		run: func(ctx context.Context, dir, target string) error {
			_, err := RunDir(ctx, dir, target, nil)
			return err
		},
	}
}

// WithCrossDispatch stores c in ctx; bindings retrieve it to run external deps.
func WithCrossDispatch(ctx context.Context, c *CrossDispatch) context.Context {
	return context.WithValue(ctx, crossDispatchCtxKey{}, c)
}

// CrossDispatchFromContext returns the coordinator stored by WithCrossDispatch, or
// nil — e.g. in describe/parse, where external deps stay graph-only and must not run.
func CrossDispatchFromContext(ctx context.Context) *CrossDispatch {
	c, _ := ctx.Value(crossDispatchCtxKey{}).(*CrossDispatch)
	return c
}

func withCrossAncestor(ctx context.Context, key string) context.Context {
	prev, _ := ctx.Value(crossAncestorCtxKey{}).([]string)
	next := append(append([]string(nil), prev...), key)
	return context.WithValue(ctx, crossAncestorCtxKey{}, next)
}

func crossAncestors(ctx context.Context) []string {
	a, _ := ctx.Value(crossAncestorCtxKey{}).([]string)
	return a
}

// Dispatch runs target in project dep, at most once per invocation. A second caller for
// the same target shares the first run's outcome; one already on the current call stack is
// a cross-project cycle and errors instead of deadlocking.
//
// The caller is responsible for yielding any concurrency slot it holds before
// calling Dispatch (the remote run needs slots of its own); see the binding's use
// of proc.RunChildSync.
func (c *CrossDispatch) Dispatch(ctx context.Context, dep *types.Project, target string) error {
	ref := types.TargetRef{Project: dep.Path, Target: target}
	if slices.Contains(crossAncestors(ctx), ref.Ref()) {
		return types.DiagnosticErrorf(types.TargetDependencyCycle, "cross-project cycle: %s", ref.Ref())
	}
	return c.runs.Once(ctx, ref, func() error { return c.runRemote(ctx, dep, ref) })
}

func (c *CrossDispatch) runRemote(ctx context.Context, dep *types.Project, ref types.TargetRef) error {
	slog.With(attr.Component("interp")).DebugContext(ctx, "cross-project dispatch", "target", ref.Ref())
	// Mark before running: the parent's audit diffs after its body returns, and by then
	// this child has already written its own outputs.
	types.ActiveDispatchFromContext(ctx).Mark(dep.Dir)

	// A fresh body record, since target names are per-project, seeded with what this
	// run already ran there.
	rctx := buzz.WithTargetRuns(ctx, buzz.NewTargetRuns(c.runs.Passed(dep.Path)...))
	// Same reason the memo is fresh, applied to the dispatch ancestor stack: its
	// entries are bare target names, and a name only means something within one
	// project. Carried across, a sub-project target that merely SHARES a name with
	// one of the caller's ancestors read as a cycle, deterministically, and for a
	// graph that has none. The cross-project cycle it might otherwise have caught is
	// the key below's job.
	rctx = buzz.WithAncestors(rctx, nil)
	// And the interceptor, for the third time the same reason: it is bound to ONE project.
	// The closure the caller installed captured its own project, so a needs inside the
	// remote target minted a step against the CALLER: the remote project's work cached
	// under the caller's key, re-run by the caller's edits and never by its own. Nothing
	// errors; buildStep is map lookups, so it mints a plausible entry and moves on.
	//
	// Stripped rather than rebuilt for the remote project: without one, that target's own
	// needs run inline and uncached, which is what run.go does for a cacheable member too.
	rctx = buzz.WithoutTargetInterceptor(rctx)
	// `--` args belong to the target the user named, so a remote dependency drops them as
	// a same-project one does in runBuzzDependencies; ops read them from the context.
	rctx = project.WithExtraArgs(rctx, nil)
	rctx = withCrossAncestor(rctx, ref.Ref())
	return c.run(rctx, dep.Dir, ref.Target)
}
