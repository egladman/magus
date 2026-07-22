package bindings

import (
	"context"
	"fmt"
	"log/slog"
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
// argument is a target function - a same-project exported target passed by
// reference (magus.needs(format)), a cross-project handle a project import binds
// (magus.needs(gopherbuzz.build)), or a LIST of target functions produced by
// magus.glob (magus.needs(magus.glob("*-generate"))). A string is never accepted:
// a name pattern becomes handles through magus.glob, so needs only ever sees target
// functions and stays monomorphic. Same-project targets are awaited through the VM
// pool / TargetMemo path (dispatchBuzzDeps); a cross-project handle dispatches via
// CrossDispatch.
func buildBuzzNeeds(targets map[string]vm.Callable, exports map[string]vm.Value, ext *externalHandles) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		var names []string
		// collect resolves one argument to its target name(s): a target function to
		// its name, or a magus.glob(...) list to each element's name. A cross-project
		// handle dispatches immediately (awaited via CrossDispatch, not the same-project
		// pool). Errors are returned unprefixed; the caller adds the verb.
		var collect func(arg vm.Value) error
		collect = func(arg vm.Value) error {
			if arg.IsList() {
				for _, el := range arg.ListItems() {
					if err := collect(el); err != nil {
						return err
					}
				}
				return nil
			}
			if !arg.IsFun() {
				return fmt.Errorf("each argument must be a target function (an exported target, a project import member, or a magus.glob(...) result)")
			}
			if ref, ok := ext.lookup(arg); ok {
				return dispatchBuzzExternal(callCtx, ref)
			}
			name, err := resolveTargetFun(targets, exports, arg)
			if err != nil {
				return err
			}
			names = append(names, name)
			return nil
		}
		for _, arg := range args {
			if err := collect(arg); err != nil {
				return vm.Null, fmt.Errorf("magus.needs: %w", err)
			}
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

// buildBuzzGlob returns magus.glob(...), the pattern resolver that FEEDS
// magus.needs. Each argument is a glob pattern string matched against the project's
// target names (matchBuzzTargets semantics: "*" wildcards, and a pattern without "*"
// matches as "-<pattern>" suffix shorthand); it RETURNS the list of matching target
// function handles, so magus.needs(magus.glob("*-generate")) depends on every
// matching target. glob is the ONE place a pattern (a string) enters the dependency
// surface: it turns a name query into handles, keeping magus.needs monomorphic - it
// only ever receives target functions. A pattern matching nothing yields an empty
// list (needs of it is a no-op). Only exported-function targets carry a handle, so a
// pattern that would match a spell-provided op yields no handle for it - depend on
// such a target directly.
func buildBuzzGlob(targets map[string]vm.Callable, exports map[string]vm.Value) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		var patterns []string
		for _, arg := range args {
			if !arg.IsStr() {
				return vm.Null, fmt.Errorf("magus.glob: each argument must be a glob pattern string")
			}
			patterns = append(patterns, arg.AsString())
		}
		if len(patterns) == 0 {
			return vm.Null, fmt.Errorf("magus.glob: requires at least one glob pattern")
		}
		var handles []vm.Value
		for _, name := range matchBuzzTargets(targets, patterns) {
			if h, ok := exports[name]; ok {
				handles = append(handles, h)
			}
		}
		return vm.ListValue(handles), nil
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
			return types.DiagnosticErrorf(types.UnknownTarget, "unknown target %q", name)
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

// buildTargetContext assembles the shared magus.Context value a ctx-form target
// receives as its first argument. Its methods are the honest, injected form of the
// old global magus.* declarations: `ctx.needs(format)` is a binding on the context
// the function received, not a floating `magus.needs` the static reader attributes
// by lexical position.
//
// One value serves BOTH modes, branching on whether ctx carries a discovery record:
//
//   - Discovery (types.DiscoveryFromContext != nil): every method RECORDS onto the
//     record and returns a benign value. needs records the dependency by name (a
//     cross-project handle distinctly), inputs/outputs/charms/skip_cache/exclusive/
//     slots record the footprint and policy. Nothing dispatches or executes, so the
//     graph is learned without doing work.
//   - Run (no discovery record): needs dispatches the dependency (a target function,
//     or a ctx.glob(...) list of them) exactly as magus.needs does today (deduped via
//     the pool); glob resolves a pattern to handles; inputs/outputs/skip_cache/
//     exclusive/slots are no-ops (they are declarations already read at discovery);
//     has_charm returns the live charm state.
//
// The value is stateless across invocations (the per-target state lives on the
// discovery record carried by ctx), so the session stashes a single instance and
// reuses it for every ctx-form target.
func buildTargetContext(obs buzz.DirectObserver, targets map[string]vm.Callable, exports map[string]vm.Value, ext *externalHandles) vm.Value {
	c := vm.NewMap()
	runNeeds := buildBuzzNeeds(targets, exports, ext)

	c.MapSet("needs", directVal(obs, "ctx.needs", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		rec := types.DiscoveryFromContext(ctx)
		if rec == nil {
			return runNeeds(ctx, args)
		}
		// record resolves one argument to its declared dependency: a target function
		// records its name (a cross-project handle records a CrossDep), and a
		// ctx.glob(...) list records each element. A string is never accepted - a
		// pattern becomes handles through ctx.glob, so needs stays monomorphic.
		var record func(arg vm.Value) error
		record = func(arg vm.Value) error {
			if arg.IsList() {
				for _, el := range arg.ListItems() {
					if err := record(el); err != nil {
						return err
					}
				}
				return nil
			}
			if !arg.IsFun() {
				return fmt.Errorf("each argument must be a target function (an exported target, a project import member, or a ctx.glob(...) result)")
			}
			if ref, ok := ext.lookup(arg); ok {
				cd := types.CrossTargetRef{Project: ref.Project, Target: strings.ToLower(ref.Target)}
				if !slices.Contains(rec.CrossDeps, cd) {
					rec.CrossDeps = append(rec.CrossDeps, cd)
				}
				return nil
			}
			name, err := resolveTargetFun(targets, exports, arg)
			if err != nil {
				return err
			}
			rec.Needs = appendUniqStr(rec.Needs, name)
			return nil
		}
		for _, arg := range args {
			if err := record(arg); err != nil {
				return vm.Null, fmt.Errorf("ctx.needs: %w", err)
			}
		}
		return vm.Null, nil
	}))

	// ctx.glob(...): resolve glob patterns to matching target function handles, the
	// pattern resolver that feeds ctx.needs (ctx.needs(ctx.glob("*-generate"))). A pure
	// resolver, identical in discovery and run mode - it records nothing itself; the
	// handles it returns are recorded/dispatched by ctx.needs.
	c.MapSet("glob", directVal(obs, "ctx.glob", buildBuzzGlob(targets, exports)))

	c.MapSet("inputs", directVal(obs, "ctx.inputs", recordGlobs("ctx.inputs", func(rec *types.DiscoveryRecord, g string) {
		rec.Inputs = appendUniqStr(rec.Inputs, g)
	})))
	c.MapSet("outputs", directVal(obs, "ctx.outputs", recordGlobs("ctx.outputs", func(rec *types.DiscoveryRecord, g string) {
		rec.Outputs = appendUniqStr(rec.Outputs, g)
	})))

	c.MapSet("has_charm", directVal(obs, "ctx.has_charm", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		name := argStr(args, 0)
		if rec := types.DiscoveryFromContext(ctx); rec != nil {
			// Record the charm so it shows on the node; return false so discovery
			// takes the charm-absent branch. Discovery sees only the branch it takes
			// (an accepted tradeoff of running the body vs a static two-arm read).
			if name != "" {
				rec.Charms = appendUniqStr(rec.Charms, name)
			}
			return vm.BoolValue(false), nil
		}
		return vm.BoolValue(types.HasCharm(ctx, name)), nil
	}))

	c.MapSet("skip_cache", directVal(obs, "ctx.skip_cache", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		if rec := types.DiscoveryFromContext(ctx); rec != nil {
			rec.SkipCache = true
		}
		return vm.Null, nil
	}))
	c.MapSet("exclusive", directVal(obs, "ctx.exclusive", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		if rec := types.DiscoveryFromContext(ctx); rec != nil {
			rec.Exclusive = true
		}
		return vm.Null, nil
	}))
	c.MapSet("slots", directVal(obs, "ctx.slots", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		rec := types.DiscoveryFromContext(ctx)
		if rec == nil {
			return vm.Null, nil
		}
		if len(args) == 0 || !args[0].IsInt() {
			return vm.Null, fmt.Errorf("ctx.slots: expects a whole number of slots")
		}
		n := int(args[0].AsInt())
		if n < 1 {
			return vm.Null, fmt.Errorf("ctx.slots: must be >= 1, got %d", n)
		}
		rec.Slots = n
		return vm.Null, nil
	}))

	return c
}

// recordGlobs builds a ctx.inputs / ctx.outputs method: in discovery it records
// each string-literal glob via set; in run mode it is a no-op (the footprint was
// already read at discovery). A non-string argument is a computed footprint the
// static cache key cannot see, so it is warned-and-excluded rather than hard-errored
// - the honest position the discovery-run model takes over today's DynamicIO error.
func recordGlobs(name string, set func(*types.DiscoveryRecord, string)) vm.Callable {
	return func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		rec := types.DiscoveryFromContext(ctx)
		if rec == nil {
			return vm.Null, nil
		}
		for _, a := range args {
			if !a.IsStr() {
				slog.WarnContext(ctx, "non-literal glob excluded from the cache footprint; use a string-literal glob",
					"method", name)
				continue
			}
			set(rec, a.AsString())
		}
		return vm.Null, nil
	}
}

// appendUniqStr appends v to s unless already present, preserving order.
func appendUniqStr(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}
