package bindings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interp"
	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// knownProjectOptionKeys are the recognized magus.project({...}) top-level keys.
var knownProjectOptionKeys = []string{
	"depends_on", "outputs", "sources", "exclusive", "spells", "watch_ignore", "targets",
}

// knownTargetPolicyKeys are the recognized per-target policy keys inside
// magus.project's "targets" map.
var knownTargetPolicyKeys = []string{"skipCache", "exclusive", "slots"}

// rejectUnknownKeys errors on the first key in m absent from known, so a typo
// like "skip_cache" or "depend_on" is a loud load error instead of a silently
// dropped option. context names the call site for the error message.
func rejectUnknownKeys(m vm.Value, known []string, context string) error {
	if !m.IsMap() {
		return nil
	}
	sortedKnown := slices.Sorted(slices.Values(known))
	for _, k := range m.MapKeys() {
		if slices.Contains(known, k) {
			continue
		}
		msg := fmt.Sprintf("%s: unknown option %q (known options: %s)",
			context, k, strings.Join(sortedKnown, ", "))
		if hint := interactive.SuggestNearest(k, known); hint != "" {
			msg = fmt.Sprintf("%s: unknown option %q; did you mean %q? (known options: %s)",
				context, k, hint, strings.Join(sortedKnown, ", "))
		}
		return errors.New(msg)
	}
	return nil
}

// buildProject returns magus.project, the callable that customizes the calling
// project's options. It is OPTIONAL: a magusfile's mere presence registers its
// directory as a project that runs on defaults — magus.project only layers on
// deps, spells, watch_ignore, and per-target policies. Two forms:
//
//	magus.project({...})        — customizes THIS project; its path comes from
//	                              context (the magusfile's own project).
//	magus.project(path, {...})  — customizes the discovered project at a workspace
//	                              path (the rare central/monorepo form, e.g. one
//	                              magusfile declaring options for several projects).
func buildProject(ctx context.Context, obs buzz.DirectObserver) vm.Value {
	return directVal(obs, "magus.project", func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Null, nil
		}
		var path string
		var optsVal vm.Value
		if args[0].IsStr() {
			path = args[0].AsString()
			if len(args) >= 2 {
				optsVal = args[1]
			}
		} else {
			optsVal = args[0]
			path, _ = interp.ProjectPathFromContext(ctx)
		}
		if !optsVal.IsMap() {
			return vm.Null, fmt.Errorf(
				"magus.project expects an options map `magus.project({...})`%s",
				configureFnHint(args[0]))
		}

		opts, err := parseBuzzProjectOpts(callCtx, optsVal)
		if err != nil {
			return vm.Null, err
		}
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.RegisterProject(path, opts...)
		}
		return vm.Null, nil
	})
}

// configureFnHint nudges a caller still passing the old configurator function
// toward the direct options map; empty for any other bad argument.
func configureFnHint(arg vm.Value) string {
	if arg.IsFun() {
		return "; pass the options map directly: magus.project({...})"
	}
	return ""
}

func parseBuzzProjectOpts(ctx context.Context, v vm.Value) ([]workspace.ProjectOption, error) {
	if !v.IsMap() {
		return nil, nil
	}
	if err := rejectUnknownKeys(v, knownProjectOptionKeys, "magus.project"); err != nil {
		return nil, err
	}
	var opts []workspace.ProjectOption

	if dv, ok := v.MapGet("depends_on"); ok {
		if paths := buzzValToStringSlice(dv); len(paths) > 0 {
			opts = append(opts, workspace.WithDependsOn(paths...))
		}
	}
	if ov, ok := v.MapGet("outputs"); ok {
		if paths := buzzValToStringSlice(ov); len(paths) > 0 {
			opts = append(opts, workspace.WithOutputs(paths...))
		}
	}
	if sv, ok := v.MapGet("sources"); ok {
		if paths := buzzValToStringSlice(sv); len(paths) > 0 {
			opts = append(opts, workspace.WithSources(paths...))
		}
	}
	if ev, ok := v.MapGet("exclusive"); ok {
		if ev.Bool() {
			opts = append(opts, workspace.WithExclusive())
		}
	}
	if sv, ok := v.MapGet("spells"); ok && sv.IsList() {
		// Each item is a spell handle. A local spell (.load) is registered by value
		// here, at bind time, from the resolved spec its handle carries; built-ins
		// and host spells are already registered, so they only need binding by name.
		for _, item := range sv.ListItems() {
			if !item.IsMap() {
				continue
			}
			nv, ok := item.MapGet("name")
			if !ok || !nv.IsStr() || nv.AsString() == "" {
				continue
			}
			name := nv.AsString()
			if _, exists := project.DefaultSpellRegistry().Lookup(name); !exists {
				m, err := ispell.DecodeHandle(item)
				if err != nil {
					return nil, fmt.Errorf("magus.project: spell %q: %w", name, err)
				}
				registerLocalSpell(m)
			}
			// A tool spell bound to contribute targets that exposes none almost always
			// means its mgs_listTargets was omitted or misnamed: the spell loads and
			// binds cleanly, then silently adds nothing to run. Warn (not error). A
			// declaration spell (the built-in magusfile spell, which registers
			// magusfile.buzz) legitimately has no ops, so a non-empty declaration set
			// is the signal to stay quiet; a pure in-VM cache backend is bound through
			// magus.cache.remote, not here.
			if sp, ok := project.DefaultSpellRegistry().Lookup(name); ok &&
				len(sp.Targets()) == 0 &&
				len(sp.DeclarationFiles()) == 0 &&
				len(sp.DeclarationDirGlobs()) == 0 {
				slog.WarnContext(ctx, "magus.project: bound spell exposes no targets; did its `mgs_listTargets` get omitted or misnamed?", "spell", name)
			}
			opts = append(opts, workspace.WithRegisteredSpell(name))
		}
	}
	if wv, ok := v.MapGet("watch_ignore"); ok && wv.IsMap() {
		var patterns []types.IgnorePattern
		if gv, ok := wv.MapGet("glob"); ok {
			for _, s := range buzzValToStringSlice(gv) {
				patterns = append(patterns, workspace.IgnoreGlob(s))
			}
		}
		if rv, ok := wv.MapGet("regex"); ok {
			for _, s := range buzzValToStringSlice(rv) {
				patterns = append(patterns, workspace.IgnoreRegex(s))
			}
		}
		if lv, ok := wv.MapGet("literal"); ok {
			for _, s := range buzzValToStringSlice(lv) {
				patterns = append(patterns, workspace.IgnoreLiteral(s))
			}
		}
		if len(patterns) > 0 {
			opts = append(opts, workspace.WithWatchIgnore(patterns...))
		}
	}
	// targets maps a target name to a per-target policy table: skipCache=true opts
	// the target out of the cache; exclusive=true runs it alone against the batch;
	// slots=N holds N concurrency slots while the target runs.
	if tv, ok := v.MapGet("targets"); ok && tv.IsMap() {
		for _, name := range tv.MapKeys() {
			pv, ok := tv.MapGet(name)
			if !ok || !pv.IsMap() {
				continue
			}
			if err := rejectUnknownKeys(pv, knownTargetPolicyKeys,
				fmt.Sprintf("magus.project: targets[%q]", name)); err != nil {
				return nil, err
			}
			// name is normalized by workspace.WithTarget, so a policy declared
			// under any spelling (skipCache/skip_cache aside) matches a target
			// invoked under any other.
			if sv, ok := pv.MapGet("skipCache"); ok && sv.Bool() {
				opts = append(opts, workspace.WithTarget(name, workspace.SkipCache()))
			}
			if ev, ok := pv.MapGet("exclusive"); ok && ev.Bool() {
				opts = append(opts, workspace.WithTarget(name, workspace.Exclusive()))
			}
			// A present-but-malformed slots value (non-int, or < 1) is a load
			// error, not a silent skip: AsInt reinterprets a float's bits as an
			// int, so slots=2.5 would otherwise yield garbage rather than
			// vanishing quietly.
			if sv, ok := pv.MapGet("slots"); ok {
				if !sv.IsInt() {
					return nil, fmt.Errorf(
						"magus.project: targets[%q].slots must be a whole number, got a %s",
						name, sv.Kind())
				}
				n := int(sv.AsInt())
				if n < 1 {
					return nil, fmt.Errorf(
						"magus.project: targets[%q].slots must be >= 1, got %d", name, n)
				}
				opts = append(opts, workspace.WithTarget(name, workspace.Slots(n)))
			}
		}
	}
	return opts, nil
}
