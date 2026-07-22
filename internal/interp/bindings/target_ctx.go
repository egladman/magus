package bindings

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

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
//   - Run (no discovery record): needs/needsGlob dispatch the dependency exactly as
//     magus.needs does today (deduped via the pool); inputs/outputs/skip_cache/
//     exclusive/slots are no-ops (they are declarations already read at discovery);
//     has_charm returns the live charm state.
//
// The value is stateless across invocations (the per-target state lives on the
// discovery record carried by ctx), so the session stashes a single instance and
// reuses it for every ctx-form target.
func buildTargetContext(obs buzz.DirectObserver, targets map[string]vm.Callable, exports map[string]vm.Value, ext *externalHandles) vm.Value {
	c := vm.NewMap()
	runNeeds := buildBuzzNeeds(targets, exports, ext)
	runNeedsGlob := buildBuzzNeedsGlob(targets)

	c.MapSet("needs", directVal(obs, "ctx.needs", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		rec := types.DiscoveryFromContext(ctx)
		if rec == nil {
			return runNeeds(ctx, args)
		}
		for _, arg := range args {
			if !arg.IsFun() {
				return vm.Null, fmt.Errorf("ctx.needs: each argument must be a target function (an exported target, or a project import member); use ctx.needsGlob for patterns")
			}
			if ref, ok := ext.lookup(arg); ok {
				cd := types.CrossTargetRef{Project: ref.Project, Target: strings.ToLower(ref.Target)}
				if !slices.Contains(rec.CrossDeps, cd) {
					rec.CrossDeps = append(rec.CrossDeps, cd)
				}
				continue
			}
			name, err := resolveTargetFun(targets, exports, arg)
			if err != nil {
				return vm.Null, fmt.Errorf("ctx.needs: %w", err)
			}
			rec.Needs = appendUniqStr(rec.Needs, name)
		}
		return vm.Null, nil
	}))

	c.MapSet("needsGlob", directVal(obs, "ctx.needsGlob", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		rec := types.DiscoveryFromContext(ctx)
		if rec == nil {
			return runNeedsGlob(ctx, args)
		}
		var patterns []string
		for _, arg := range args {
			if !arg.IsStr() {
				return vm.Null, fmt.Errorf("ctx.needsGlob: each argument must be a glob pattern string")
			}
			patterns = append(patterns, arg.AsString())
		}
		if len(patterns) == 0 {
			return vm.Null, fmt.Errorf("ctx.needsGlob: requires at least one glob pattern")
		}
		for _, m := range matchBuzzTargets(targets, patterns) {
			rec.Needs = appendUniqStr(rec.Needs, m)
		}
		return vm.Null, nil
	}))

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
