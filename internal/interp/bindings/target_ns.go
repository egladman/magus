package bindings

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

func buildTargetNS(obs buzz.DirectObserver, targets map[string]vm.Callable) vm.Value {
	ns := vm.NewMap()

	ns.MapSet("expand_globs", directVal(obs, "magus.target.expand_globs", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.ListValue(nil), nil
		}
		matched := matchBuzzTargets(targets, buzzValToStringSlice(args[0]))
		return strSliceToBuzzList(matched), nil
	}))

	// literal/glob/regex return a TargetQuery (a map mirroring the magus/target
	// `object TargetQuery` fields — mode + pattern) consumed by magus.needs. The
	// pattern must be a string literal so the static extractor (internal/describe)
	// can recover the edge from source without evaluating the magusfile.
	ns.MapSet("literal", directVal(obs, "magus.target.literal", buildBuzzTargetHandle(types.QueryLiteral)))
	ns.MapSet("glob", directVal(obs, "magus.target.glob", buildBuzzTargetHandle(types.QueryGlob)))
	ns.MapSet("regex", directVal(obs, "magus.target.regex", buildBuzzTargetHandle(types.QueryRegex)))

	return ns
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

// buildBuzzTargetHandle returns the magus.target.<mode> constructor (mode is one of
// types.Query{Literal,Glob,Regex}). The argument is a required string literal (the
// literal-first-arg discipline: the static extractor recovers the edge from source
// without running the VM). It returns the TargetQuery's Buzz field shape.
func buildBuzzTargetHandle(mode string) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsStr() {
			return vm.Null, fmt.Errorf("magus.target.%s: argument must be a string literal", mode)
		}
		return targetQueryToBuzz(types.TargetQuery{Mode: mode, Pattern: args[0].AsString()}), nil
	}
}

// targetQueryToBuzz encodes q as the Buzz map magus.needs consumes — the same field
// shape as the magus/target `object TargetQuery`, so a constructor result and a
// magusfile-authored TargetQuery decode identically (see decodeTargetQuery).
func targetQueryToBuzz(q types.TargetQuery) vm.Value {
	m := vm.NewMap()
	m.MapSet("mode", vm.StrValue(q.Mode))
	m.MapSet("pattern", vm.StrValue(q.Pattern))
	m.MapSet("project", vm.StrValue(q.Project))
	return m
}

// decodeTargetQuery reads a magus.target.* value into a types.TargetQuery. It accepts
// both the map the constructors emit and a TargetQuery object instance a magusfile
// builds via `import "magus/target"` — MapView yields the field map for either. ok is
// false for any value without a valid mode, so magus.needs rejects bare strings,
// lists, and unrelated maps/objects.
func decodeTargetQuery(v vm.Value) (types.TargetQuery, bool) {
	mv, ok := v.MapView()
	if !ok {
		return types.TargetQuery{}, false
	}
	mode := ""
	if m, ok := mv.MapGet("mode"); ok && m.IsStr() {
		mode = m.AsString()
	}
	switch mode {
	case types.QueryLiteral, types.QueryGlob, types.QueryRegex:
	default:
		return types.TargetQuery{}, false
	}
	q := types.TargetQuery{Mode: mode}
	if p, ok := mv.MapGet("pattern"); ok && p.IsStr() {
		q.Pattern = p.AsString()
	}
	if pr, ok := mv.MapGet("project"); ok && pr.IsStr() {
		q.Project = pr.AsString()
	}
	return q, true
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
func dispatchBuzzExternal(ctx context.Context, q types.TargetQuery) error {
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
	depPath, err := file.Resolve(q.Project, filepath.ToSlash(callerRel))
	if err != nil {
		return err
	}
	dep := ws.Get(depPath)
	if dep == nil {
		return fmt.Errorf("magus: cross-project dependency: unknown project %q", depPath)
	}
	target := strings.ToLower(q.Pattern)
	lim := cache.LimiterFromContext(ctx)
	return proc.RunChildSync(ctx, lim, func() error {
		return cd.Dispatch(cache.WithoutSlotHeld(ctx), dep.Dir, target)
	})
}

// resolveTargetQuery expands a same-project query to matching target names: literal
// is an exact name run through the same normalizer targetMap registration uses (see
// execBuzzSrc), so a needs literal gets the same many-spellings forgiveness as the
// CLI regardless of the casing/separator convention it's written in; glob matches
// via matchBuzzTargets, regex matches registered names against the compiled
// pattern. External queries are dispatched separately and are not valid here.
func resolveTargetQuery(targets map[string]vm.Callable, q types.TargetQuery) ([]string, error) {
	switch q.Mode {
	case types.QueryLiteral:
		return []string{types.DefaultTargetNameNormalizer.NormalizeTargetName(q.Pattern)}, nil
	case types.QueryGlob:
		return matchBuzzTargets(targets, []string{q.Pattern}), nil
	case types.QueryRegex:
		re, err := regexp.Compile(q.Pattern)
		if err != nil {
			return nil, fmt.Errorf("target.regex %q: %w", q.Pattern, err)
		}
		var matched []string
		for name := range targets {
			if re.MatchString(name) {
				matched = append(matched, name)
			}
		}
		slices.Sort(matched)
		return matched, nil
	default:
		return nil, fmt.Errorf("target query: not a same-project query")
	}
}

// buildBuzzNeeds returns magus.needs(...), the one dependency primitive. Every
// argument must be a TargetQuery from magus.target.literal/glob/regex — bare strings
// and lists are not accepted, so a dependency is always a typed, statically-
// recoverable edge. Same-project queries resolve to target names awaited through the
// VM pool / TargetMemo path (dispatchBuzzDeps); an external query dispatches
// cross-project via CrossDispatch.
func buildBuzzNeeds(targets map[string]vm.Callable) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		var names []string
		for _, arg := range args {
			q, ok := decodeTargetQuery(arg)
			if !ok {
				return vm.Null, fmt.Errorf("magus.needs: each argument must be a magus.target.* query (literal/glob/regex)")
			}
			if q.IsExternal() {
				if err := dispatchBuzzExternal(callCtx, q); err != nil {
					return vm.Null, fmt.Errorf("magus.needs: %w", err)
				}
				continue
			}
			resolved, err := resolveTargetQuery(targets, q)
			if err != nil {
				return vm.Null, fmt.Errorf("magus.needs: %w", err)
			}
			names = append(names, resolved...)
		}
		if err := dispatchBuzzDeps(callCtx, targets, names); err != nil {
			return vm.Null, fmt.Errorf("magus.needs: %w", err)
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
