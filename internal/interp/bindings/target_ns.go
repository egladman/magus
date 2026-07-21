package bindings

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// externalTarget names one target of another project: the {project, target}
// pair a cross-project handle stands for.
type externalTarget struct {
	Project string // project path as written after "project/" in the import
	Target  string // kebab-normalized target name
}

// externalHandles is a session's registry of cross-project target handles: the
// function values a `import "project/<path>"` module binds for each of the
// dependency's targets (see resolveProjectImport), paired with the target each
// dispatches. magus.needs matches a passed function against it by value
// identity to recover the {project, target} the handle stands for - the handle
// itself stays an ordinary callable, so `gopherbuzz.build()` also just works.
// A linear scan is fine: a magusfile imports a handful of projects at most.
type externalHandles struct {
	vals    []vm.Value
	targets []externalTarget
}

func (e *externalHandles) register(v vm.Value, dep externalTarget) {
	e.vals = append(e.vals, v)
	e.targets = append(e.targets, dep)
}

func (e *externalHandles) lookup(v vm.Value) (externalTarget, bool) {
	for i, hv := range e.vals {
		if hv.Equal(v) {
			return e.targets[i], true
		}
	}
	return externalTarget{}, false
}

// buildCacheNS assembles magus.cache for a magusfile. Today it exposes remote(),
// which wires an imported spell as the cross-shard remote cache backend:
//
//	import "spells/github/actions" as github
//	magus.cache.remote(github)
//
// The import already registered the spell (with handler op support, for a Buzz
// spell); remote() just records its name on the per-Open workspace registry, and
// magus.Open resolves it by name once the magusfile has been evaluated. The spell
// must expose get_artifact/put_artifact handler ops (and optionally enabled()).
func buildCacheNS(ctx context.Context, obs buzz.DirectObserver) vm.Value {
	ns := vm.NewMap()
	ns.MapSet("remote", directVal(obs, "magus.cache.remote", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsMap() {
			return vm.Null, fmt.Errorf("magus.cache.remote: expected an imported spell handle")
		}
		nv, ok := args[0].MapGet("name")
		if !ok || !nv.IsStr() || nv.AsString() == "" {
			return vm.Null, fmt.Errorf("magus.cache.remote: argument is not a spell handle (no name)")
		}
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.SetRemoteBackend(nv.AsString())
		}
		return vm.Null, nil
	}))
	return ns
}

// dispatchBuzzExternal runs the cross-project target an external handle names,
// through the run's CrossDispatch coordinator (run-once + cross-project cycle
// detection). The project path is resolved with file.Resolve against the caller's
// workspace-relative path — the same rule the static extractor uses, so the graph
// edge and the runtime dispatch agree, and a ..-escape or absolute path is rejected
// rather than running a magusfile outside the workspace. The dep's canonical dir
// comes from the workspace, keeping the coordinator's run-once/cycle key canonical.
// It yields the caller's concurrency slot for the duration (the remote run needs
// slots of its own), mirroring buzzDispatchViaPool. No-op when no coordinator/
// workspace is in ctx (describe/parse), so the handle stays graph-only.
func dispatchBuzzExternal(ctx context.Context, ref externalTarget) error {
	cd := interp.CrossDispatchFromContext(ctx)
	src := interp.SourceFromContext(ctx)
	ws := types.WorkspaceFromContext(ctx)
	if cd == nil || src == nil || ws == nil {
		return nil
	}
	callerRel, err := filepath.Rel(ws.Root(), src.Dir)
	if err != nil {
		return fmt.Errorf("magus: cross-project dependency: %w", err)
	}
	depPath, err := file.Resolve(ref.Project, filepath.ToSlash(callerRel))
	if err != nil {
		return err
	}
	dep := ws.Get(depPath)
	if dep == nil {
		return fmt.Errorf("magus: cross-project dependency: unknown project %q", depPath)
	}
	target := strings.ToLower(ref.Target)
	lim := cache.LimiterFromContext(ctx)
	return proc.RunChildSync(ctx, lim, func() error {
		return cd.Dispatch(cache.WithoutSlotHeld(ctx), dep.Dir, target)
	})
}

// buildBuzzNeeds returns magus.needs(...), the one dependency primitive. Every
// argument is a target function: a same-project exported target passed by
// reference (magus.needs(format)), or a cross-project handle a project import
// binds (magus.needs(gopherbuzz.build)). Nothing else is accepted - no strings,
// no query objects - so a dependency is always the target itself, checked at
// the call. Patterns go through magus.needsGlob instead. Same-project targets
// are awaited through the VM pool / TargetMemo path (dispatchBuzzDeps); a
// cross-project handle dispatches via CrossDispatch.
func buildBuzzNeeds(targets map[string]vm.Callable, exports map[string]vm.Value, ext *externalHandles) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		var names []string
		for _, arg := range args {
			if !arg.IsFun() {
				return vm.Null, fmt.Errorf("magus.needs: each argument must be a target function (an exported target, or a project import member); use magus.needsGlob for patterns")
			}
			if ref, ok := ext.lookup(arg); ok {
				if err := dispatchBuzzExternal(callCtx, ref); err != nil {
					return vm.Null, fmt.Errorf("magus.needs: %w", err)
				}
				continue
			}
			name, err := resolveTargetFun(targets, exports, arg)
			if err != nil {
				return vm.Null, fmt.Errorf("magus.needs: %w", err)
			}
			names = append(names, name)
		}
		if err := dispatchBuzzDeps(callCtx, targets, names); err != nil {
			return vm.Null, fmt.Errorf("magus.needs: %w", err)
		}
		return vm.Null, nil
	}
}

// resolveTargetFun maps a function value passed to magus.needs to its canonical
// target key. The declared name (vm.Value.FunName) is run through the same
// normalizer targetMap registration uses, so a handle gets the same
// many-spellings forgiveness as the CLI. When the session's export registry is
// available, the passed value must BE the exported function (value identity),
// so a local helper that merely shares a target's normalized name cannot
// silently stand in for it.
func resolveTargetFun(targets map[string]vm.Callable, exports map[string]vm.Value, arg vm.Value) (string, error) {
	name := arg.FunName()
	// The chunk compiler names an anonymous closure "<fun>"; a Go DirectValue can
	// legitimately carry an empty name too.
	if name == "" || name == "<fun>" {
		return "", fmt.Errorf("anonymous function is not a target; pass an exported target function")
	}
	key := types.DefaultTargetNameNormalizer.NormalizeTargetName(name)
	if _, ok := targets[key]; !ok {
		return "", fmt.Errorf("function %q does not name an exported target", name)
	}
	if exports != nil {
		exp, ok := exports[key]
		if !ok || !exp.Equal(arg) {
			return "", fmt.Errorf("function %q matches target name %q but is not the exported target function", name, key)
		}
	}
	return key, nil
}

// buildBuzzNeedsGlob returns magus.needsGlob(...), the pattern form of needs.
// Each argument is a glob pattern string matched against the project's target
// names (matchBuzzTargets semantics: "*" wildcards, and a pattern without "*"
// matches as "-<pattern>" suffix shorthand); every match is awaited like a
// magus.needs dependency. Patterns are a separate verb, not a needs overload,
// so needs stays monomorphic: a dependency is a target function, a pattern is
// a name query. A pattern matching nothing is a no-op, mirroring glob
// semantics elsewhere.
func buildBuzzNeedsGlob(targets map[string]vm.Callable) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		var patterns []string
		for _, arg := range args {
			if !arg.IsStr() {
				return vm.Null, fmt.Errorf("magus.needsGlob: each argument must be a glob pattern string")
			}
			patterns = append(patterns, arg.AsString())
		}
		if len(patterns) == 0 {
			return vm.Null, fmt.Errorf("magus.needsGlob: requires at least one glob pattern")
		}
		if err := dispatchBuzzDeps(callCtx, targets, matchBuzzTargets(targets, patterns)); err != nil {
			return vm.Null, fmt.Errorf("magus.needsGlob: %w", err)
		}
		return vm.Null, nil
	}
}

// dispatchBuzzDeps awaits the named same-project targets: via the Buzz VM pool
// when one is in ctx (parallel, TargetMemo-deduped), else inline sequential. It
// returns unprefixed errors so each caller attaches its own verb name.
func dispatchBuzzDeps(callCtx context.Context, targets map[string]vm.Callable, names []string) error {
	if len(names) == 0 {
		return nil
	}
	// These are dependencies (magus.needs), so a service op among them is supervised
	// in the background rather than blocked on (see runCommand). The directly-run
	// target is dispatched without this marker, so it still foregrounds.
	callCtx = service.WithSupervision(callCtx)
	names = dedupStrings(names)
	if src := interp.SourceFromContext(callCtx); src != nil {
		if reg := buzz.PoolRegistryFromContext(callCtx); reg != nil {
			key := src.Dir + "\x00buzz"
			p := reg.Get(key, interp.NewBuzzWorkerFunc(src))
			return buzzDispatchViaPool(callCtx, p, names)
		}
	}
	for _, name := range names {
		fn, ok := targets[name]
		if !ok {
			return fmt.Errorf("unknown target %q", name)
		}
		if fn == nil {
			continue
		}
		if _, err := fn(callCtx, nil); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// buzzDispatchViaPool fans names out via the Buzz pool, yielding the RunAll
// limiter slot (if held) for the duration so pool workers can acquire it.
func buzzDispatchViaPool(ctx context.Context, p *buzz.Pool, names []string) error {
	lim := cache.LimiterFromContext(ctx)
	ancestors := buzz.AncestorsFromContext(ctx)
	return proc.RunChildSync(ctx, lim, func() error {
		childCtx := cache.WithoutSlotHeld(ctx)
		return p.Dispatch(childCtx, names, ancestors)
	})
}

// matchBuzzTargets matches registered Buzz target names against glob/suffix patterns.
// Patterns without "*" match as suffix shorthand: "build" → ".*-build".
// Patterns with "*" are translated to regexps ("*" → ".*", anchored).
func matchBuzzTargets(targets map[string]vm.Callable, patterns []string) []string {
	res := compileTargetPatterns(patterns)
	seen := map[string]struct{}{}
	var matched []string
	for name := range targets {
		for _, re := range res {
			if re.MatchString(name) {
				if _, dup := seen[name]; !dup {
					seen[name] = struct{}{}
					matched = append(matched, name)
				}
				break
			}
		}
	}
	slices.Sort(matched)
	return matched
}
