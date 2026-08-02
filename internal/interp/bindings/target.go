package bindings

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
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
// dispatches. ctx.needs matches a passed function against it by value
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
			return vm.Null, fmt.Errorf(`magus\cache.remote: expected an imported spell handle`)
		}
		nv, ok := args[0].MapGet("name")
		if !ok || !nv.IsStr() || nv.AsString() == "" {
			return vm.Null, fmt.Errorf(`magus\cache.remote: argument is not a spell handle (no name)`)
		}
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.SetRemoteBackend(nv.AsString())
		}
		return vm.Null, nil
	}))
	return ns
}

// buildCINS assembles magus.ci for a magusfile. It exposes provider(),
// which wires an imported spell as this workspace's CI provider:
//
//	import "spells/github/actions" as github
//	magus.ci.provider(github)
//
// The spell supplies job-log structure for whatever CI system it targets:
// fold markers around failure output, annotations that surface on a pull
// request, and a suggested concurrency for that provider's runners. Every
// op is optional, because providers differ in what they support at all
// (see internal/ci/annotate).
//
// A declared provider wins over magus's built-ins, so a workspace can
// override the bundled GitHub Actions support with its own spell.
func buildCINS(_ context.Context, obs buzz.DirectObserver) vm.Value {
	ns := vm.NewMap()
	ns.MapSet("provider", directVal(obs, "magus.ci.provider", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsMap() {
			return vm.Null, fmt.Errorf(`magus\ci.provider: expected an imported spell handle`)
		}
		nv, ok := args[0].MapGet("name")
		if !ok || !nv.IsStr() || nv.AsString() == "" {
			return vm.Null, fmt.Errorf(`magus\ci.provider: argument is not a spell handle (no name)`)
		}
		SetCIProvider(nv.AsString())
		return vm.Null, nil
	}))
	return ns
}

// dispatchBuzzExternal runs the cross-project target an external handle names,
// through the run's CrossDispatch coordinator (run-once + cross-project cycle
// detection). The project path is resolved with file.ResolveImport against the caller's
// workspace-relative path, the same rule describe.go applies to the extracted ref, so
// the graph edge and the runtime dispatch agree, and a ..-escape or absolute path is rejected
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
	depPath, err := file.ResolveImport(ref.Project, filepath.ToSlash(callerRel))
	if err != nil {
		return err
	}
	dep := ws.Get(depPath)
	if dep == nil {
		return fmt.Errorf("magus: cross-project dependency: unknown project %q", depPath)
	}
	// The real normalizer, not ToLower. Today's only producer of a cross ref already
	// normalized it, so the two agree by luck; a producer that hands over a raw name
	// would get goBuild -> gobuild here and dispatch a target that does not exist.
	target := types.Normalize(ref.Target)
	lim := cache.LimiterFromContext(ctx)
	return proc.RunChildSync(ctx, lim, func() error {
		return cd.Dispatch(cache.WithoutSlotHeld(ctx), dep.Dir, target)
	})
}

// buildBuzzNeeds returns ctx.needs(...), the one dependency primitive. Every
// argument is a target function - a same-project exported target passed by
// reference (ctx.needs(format)), a cross-project handle a project import binds
// (ctx.needs(gopherbuzz.build)), or a LIST of target functions produced by
// ctx.glob (ctx.needs(ctx.glob("*-generate"))). A string is never accepted:
// a name pattern becomes handles through ctx.glob, so needs only ever sees target
// functions and stays monomorphic. Same-project targets are awaited through the VM
// pool / TargetMemo path (dispatchBuzzDeps); a cross-project handle dispatches via
// CrossDispatch.
func buildBuzzNeeds(targets map[string]vm.Callable, exports map[string]vm.Value, ext *externalHandles) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		var names []string
		// collect resolves one argument to its target name(s): a target function to
		// its name, or a ctx.glob(...) list to each element's name. A cross-project
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
				return fmt.Errorf("each argument must be a target function (an exported target, a project import member, or a ctx.glob(...) result)")
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
				return vm.Null, fmt.Errorf("ctx.needs: %w", err)
			}
		}
		if err := dispatchBuzzDeps(callCtx, targets, names); err != nil {
			return vm.Null, fmt.Errorf("ctx.needs: %w", err)
		}
		return vm.Null, nil
	}
}

// resolveTargetFun maps a function value passed to ctx.needs to its canonical
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
	key := types.Normalize(name)
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

// buildBuzzGlob returns ctx.glob(...), the pattern resolver that FEEDS
// ctx.needs. Each argument is a glob pattern string matched against the project's
// target names (matchBuzzTargets semantics: "*" wildcards, and a pattern without "*"
// matches as "-<pattern>" suffix shorthand); it RETURNS the list of matching target
// function handles, so ctx.needs(ctx.glob("*-generate")) depends on every
// matching target. glob is the ONE place a pattern (a string) enters the dependency
// surface: it turns a name query into handles, keeping ctx.needs monomorphic - it
// only ever receives target functions. A pattern matching nothing yields an empty
// list (needs of it is a no-op). Only exported-function targets carry a handle, so a
// pattern that would match a spell-provided op yields no handle for it - depend on
// such a target directly.
func buildBuzzGlob(targets map[string]vm.Callable, exports map[string]vm.Value) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		var patterns []string
		for _, arg := range args {
			if !arg.IsStr() {
				return vm.Null, fmt.Errorf("ctx.glob: each argument must be a glob pattern string")
			}
			patterns = append(patterns, arg.AsString())
		}
		if len(patterns) == 0 {
			return vm.Null, fmt.Errorf("ctx.glob: requires at least one glob pattern")
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
	// These are dependencies (ctx.needs), so a service op among them is supervised
	// in the background rather than blocked on (see runCommand). The directly-run
	// target is dispatched without this marker, so it still foregrounds.
	callCtx = service.WithSupervision(callCtx)
	// `--` args belong to the target the USER named, not to whatever that target
	// pulls in. They ride the context, and runBuzzCommand hands them to any op that
	// declared no explicit args, so without this every dependency got them too:
	// `magus run test <p> -- -run TestX` reached the format dependency, and gofmt
	// tried to lstat "-run" and "TestX" as paths. That made the documented way to
	// narrow a run unusable on any target with a ctx.needs, which is most of them.
	callCtx = project.WithExtraArgs(callCtx, nil)
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

// ctxMarker identifies a value as a magus.Context, base or derived. An op call needs
// to tell a leading context from a leading opts table, and with a map-based value model
// and no protocol conformance in gopherbuzz on this base there is no type to ask. Not
// part of the authored surface; it disappears when the context becomes a real type.
const ctxMarker = "__magus_context"

// buildTargetContext assembles the shared magus.Context value every target receives
// as its first argument. Its methods are the injected, per-target form of what used to
// be global magus.* declarations: `ctx.needs(format)` binds on the context the function
// received rather than a floating `magus.needs` attributed by lexical position.
//
//   - needs(...) dispatches the named dependencies - a target function, or a
//     ctx.glob(...) list of them - deduped through the pool.
//   - glob(pattern) resolves a pattern to matching target handles, feeding needs.
	//   - readsFiles(...) / writesFiles(...) / modifiesExistingFiles(...) declare the cache footprint. They are no-ops at run
//     time: the footprint is read STATICALLY from the source by describe.Extract (both
//     arms of any branch), the sole graph source - the body is never run to learn it. A
//     non-literal argument is caught there as MGS-level DynamicIO at load, not here.
//   - has_charm(name) returns the live charm state.
//
// The value is stateless, so the session stashes one instance and reuses it for every
// target.
func buildTargetContext(obs buzz.DirectObserver, targets map[string]vm.Callable, exports map[string]vm.Value, ext *externalHandles) vm.Value {
	c := vm.NewMap()
	c.MapSet("needs", directVal(obs, "ctx.needs", buildBuzzNeeds(targets, exports, ext)))
	// ctx.glob(...): resolve glob patterns to matching target function handles, the
	// pattern resolver that feeds ctx.needs (ctx.needs(ctx.glob("*-generate"))). It
	// returns handles; ctx.needs dispatches them, so needs stays monomorphic.
	c.MapSet("glob", directVal(obs, "ctx.glob", buildBuzzGlob(targets, exports)))
	// File declarations are read statically by describe.Extract; at run time
	// they do nothing.
	c.MapSet(ctxMarker, vm.BoolValue(true))
	// ctx.withEnv({...}) / ctx.withCwd(".."): a magus\Exec, the EXECUTION-only context,
	// carrying overrides for the op calls made with it -
	// go["go-test"](ctx.withEnv({"CGO_ENABLED": "0"})).
	//
	// Named for WHAT DIFFERS, not for the act of making it, following context.WithValue /
	// WithCancel / WithTimeout: at a call site you want to read the change. (Go's docs
	// call the result a "derived context"; the API never says derive, and neither should
	// this.) Temporal's workflow.WithActivityOptions(ctx, ao) is the same shape - a
	// derived context carrying options for the calls made with it - and its
	// workflow.Context / context.Context split is the same separation magus\Exec makes.
	//
	// magus\Exec deliberately carries no declaration methods, so
	// ctx.withEnv({...}).inputs("x") fails loudly instead of silently no-op'ing. That is
	// the guarantee a checked type would give once gopherbuzz has protocol conformance;
	// until then the names are bound to an explaining error.
	var execCtx func(env, cwd vm.Value) vm.Value
	execCtx = func(env, cwd vm.Value) vm.Value {
		e := vm.NewMap()
		e.MapSet(ctxMarker, vm.BoolValue(true))
		if !env.IsNull() {
			e.MapSet("env", env)
		}
		if !cwd.IsNull() {
			e.MapSet("cwd", cwd)
		}
		// Chainable: ctx.withEnv({...}).withCwd(".."). Each returns a fresh Exec, so a
		// derivation hoisted into a variable is never mutated by a later one.
		e.MapSet("withEnv", directVal(obs, "ctx.withEnv", func(_ context.Context, args []vm.Value) (vm.Value, error) {
			if len(args) == 0 || !args[0].IsMap() {
				return vm.Null, fmt.Errorf("ctx.withEnv: requires a {NAME: value} map")
			}
			return execCtx(args[0], cwd), nil
		}))
		e.MapSet("withCwd", directVal(obs, "ctx.withCwd", func(_ context.Context, args []vm.Value) (vm.Value, error) {
			if len(args) == 0 || !args[0].IsStr() {
				return vm.Null, fmt.Errorf("ctx.withCwd: requires a directory string")
			}
			return execCtx(env, args[0]), nil
		}))
		for _, decl := range []string{"needs", "glob", "readsFiles", "writesFiles", "modifiesExistingFiles", "envInputs", "has_charm"} {
			e.MapSet(decl, directVal(obs, "ctx."+decl, func(_ context.Context, _ []vm.Value) (vm.Value, error) {
				return vm.Null, fmt.Errorf(
					"ctx.%s: magus\\Exec carries execution overrides only; declare on the magus\\Context the target received", decl)
			}))
		}
		return e
	}
	// The base context's derivation pair IS the empty derivation's, so take it from
	// execCtx rather than writing the two closures (and their two error strings) a
	// second time. Only those keys are copied: the rest of an Exec is the refusal to
	// declare, which the base context must not inherit.
	for _, k := range []string{"withEnv", "withCwd"} {
		if v, ok := execCtx(vm.Null, vm.Null).MapGet(k); ok {
			c.MapSet(k, v)
		}
	}
	footprintDecl := func(_ context.Context, _ []vm.Value) (vm.Value, error) { return vm.Null, nil }
	c.MapSet("readsFiles", directVal(obs, "ctx.readsFiles", footprintDecl))
	c.MapSet("writesFiles", directVal(obs, "ctx.writesFiles", footprintDecl))
	// modifiesExistingFiles is outputs' explicit counterpart: the target changes an
	// existing file but does not own its whole contents, so magus neither deletes it
	// nor restores it from a snapshot. See types.UpdateRef.
	c.MapSet("modifiesExistingFiles", directVal(obs, "ctx.modifiesExistingFiles", footprintDecl))
	for old, replacement := range map[string]string{
		"inputs": "readsFiles", "outputs": "writesFiles", "updates": "modifiesExistingFiles",
	} {
		old, replacement := old, replacement
		c.MapSet(old, directVal(obs, "ctx."+old, func(_ context.Context, _ []vm.Value) (vm.Value, error) {
			return vm.Null, fmt.Errorf("ctx.%s was removed in v0.4; use ctx.%s instead", old, replacement)
		}))
	}
	// env names variables whose PROCESS value folds into the key - the counterpart to
	// withEnv, which carries a value written in the magusfile. Declaration only: the
	// static read collects the names, and hashing reads the values.
	// envInputs, not env: "env" is already the key carrying the Exec's actual
	// environment map, which spell dispatch reads back - a declaration under that name
	// silently replaced the environment with a no-op and dropped every withEnv override.
	c.MapSet("envInputs", directVal(obs, "ctx.envInputs", footprintDecl))
	c.MapSet("has_charm", directVal(obs, "ctx.has_charm", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		return vm.BoolValue(types.HasCharm(ctx, argStr(args, 0))), nil
	}))
	return c
}
