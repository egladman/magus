package bindings

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// magusfileSpellName is the internal driver that dispatches a magusfile's own
// exported targets. It is bound implicitly for every project (see the "spells"
// handling below), so a magusfile's targets run whether or not the author lists
// it - and since listing it therefore changes nothing, listing it is now an
// error rather than a no-op that implies otherwise. See magusfileNotASpellErr.
const magusfileSpellName = "magusfile"

// magusfileNotASpellErr is the migration diagnostic for a magusfile that still
// declares the magusfile driver as one of its spells, or imports it.
//
// A spell is a library of tool-native ops for ONE TOOLCHAIN - go-build,
// cargo-clippy, eslint (docs/concepts/spells.md, whose built-in table has never
// listed magusfile). The magusfile driver adapts no toolchain and contributes no
// ops; it is what makes the file you are writing runnable at all. Binding it was
// made implicit precisely because "listing it should never have been the author's
// job", which left the import and the list entry doing nothing while still
// teaching every reader that magusfile is a spell. This finishes that change.
func magusfileNotASpellErr(what string) error {
	return fmt.Errorf(
		"[%s] magusfile is not a spell, so %s does nothing: magus binds it to every "+
			"project automatically (it is what makes a magusfile's targets runnable, "+
			"not a toolchain adapter).\nfix: delete the `import \"magus/spell/magusfile\"` "+
			"line and drop `magusfile` from the project's \"spells\" list; a project with "+
			"no toolchain spell needs no list at all.\nsee: %s",
		types.MagusfileIsNotASpell, what, types.CodeURL(types.MagusfileIsNotASpell))
}

// knownProjectOptionKeys are the recognized magus.project({...}) top-level keys, from
// the one shared table in types so the engine and the dry-run host cannot drift.
var knownProjectOptionKeys = types.ProjectOptionKeys()

// knownToolBoundKeys are the recognized keys inside one entry of magus.project's
// "tools" map.
var knownToolBoundKeys = []string{"min", "below"}

// knownTargetPolicyKeys are the recognized per-target policy keys inside
// magus.project's "targets" map.
var knownTargetPolicyKeys = []string{"skip_cache", "exclusive", "slots", "memory_mb", "cache", "drift", "drift_reason"}

// rejectUnknownKeys errors on the first key in m absent from known, so a typo
// like "skip_cache" or "depend_on" is a loud load error instead of a silently
// dropped option. context names the call site for the error message.
//
// Use rejectUnknownSchemaKeys instead for a vocabulary that grows across releases.
func rejectUnknownKeys(m vm.Value, known []string, context string) error {
	if !m.IsMap() {
		return nil
	}
	sortedKnown := slices.Sorted(slices.Values(known))
	for _, k := range m.MapKeys() {
		if slices.Contains(known, k) {
			continue
		}
		if sug := hint.Nearest(k, known); sug != "" {
			return fmt.Errorf("%s: unknown option %q; did you mean %q? (known options: %s)",
				context, k, sug, strings.Join(sortedKnown, ", "))
		}
		return fmt.Errorf("%s: unknown option %q (known options: %s)",
			context, k, strings.Join(sortedKnown, ", "))
	}
	return nil
}

// rejectUnknownSchemaKeys is rejectUnknownKeys for a vocabulary that GROWS across
// releases, so an unrecognized key there has a second possible cause.
//
// Only the top-level magus.project map qualifies. A key with no near match in a
// vocabulary that gains members is more likely one this binary is too old to know than
// a misspelling, and by far the more expensive reading to get wrong: a magusfile schema
// key added upstream makes EVERY magus command fail at workspace load, including the one
// that would build a binary that understands it, and the bare "unknown option" text
// sends the reader hunting a typo that is not there.
//
// A closed sub-map (a tool's min/below, a target's policy keys) keeps the plain
// rejection. Those vocabularies do not grow, so "your magus may predate it" is wrong
// advice there - `minimum` for `min` is a typo the suggester cannot match on distance,
// not a key from the future.
func rejectUnknownSchemaKeys(m vm.Value, known []string, context string) error {
	err := rejectUnknownKeys(m, known, context)
	if err == nil {
		return nil
	}
	for _, k := range m.MapKeys() {
		if slices.Contains(known, k) || hint.Nearest(k, known) != "" {
			continue
		}
		return fmt.Errorf("%s\nhint: nothing here is close to %q, so this magus may predate it."+
			" Upgrade with `%s`, or delete the key if the workspace does not need it",
			err.Error(), k, hint.SelfUpdate)
	}
	return err
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
	if err := rejectUnknownSchemaKeys(v, knownProjectOptionKeys, "magus.project"); err != nil {
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
	if nv, ok := v.MapGet("name"); ok && nv.IsStr() {
		if name := strings.TrimSpace(nv.AsString()); name != "" {
			opts = append(opts, workspace.WithName(name))
		}
	}
	if ev, ok := v.MapGet("exclusive"); ok {
		if ev.Bool() {
			opts = append(opts, workspace.WithExclusive())
		}
	}
	// A reason, not a flag. `"no_language": true` would silence doctor's language-coverage
	// check anonymously; requiring prose means the next reader learns why this project has
	// no toolchain spell instead of finding a switch someone flipped.
	if nv, ok := v.MapGet("no_language"); ok {
		var reason string
		if nv.IsStr() {
			reason = strings.TrimSpace(nv.AsString())
		}
		if reason == "" {
			return nil, fmt.Errorf(`magus.project: "no_language" needs a reason string explaining why this project binds no toolchain spell, e.g. "polyglot harness; no single language pack describes it"`)
		}
		opts = append(opts, workspace.WithNoLanguage(reason))
	}
	// Globs, not a bool. Declaring WHERE reading matters is what keeps the report quiet
	// everywhere else; a project-wide switch would make every changed file a finding, which
	// is the shape people learn to skip.
	if rv, ok := v.MapGet("review_required"); ok && rv.IsList() {
		var globs []string
		for _, item := range rv.ListItems() {
			if item.IsStr() && strings.TrimSpace(item.AsString()) != "" {
				globs = append(globs, strings.TrimSpace(item.AsString()))
			}
		}
		if len(globs) == 0 {
			return nil, fmt.Errorf(`magus.project: "review_required" needs at least one glob naming where an unread change matters, e.g. ["internal/cache/**", "internal/secret/**"]`)
		}
		opts = append(opts, workspace.WithReviewRequired(globs...))
	}
	// An EMPTY list is legal here, unlike review_required: [] is the spelling that
	// turns the prose class of the gate redundancy check off, and any declaration
	// replaces the built-in markdown defaults rather than adding to them.
	if gv, ok := v.MapGet("gate_low_risk"); ok {
		if !gv.IsList() {
			return nil, fmt.Errorf(`magus.project: "gate_low_risk" takes a list of globs the ci-gate redundancy check classifies as prose, e.g. ["**/*.md", "notes/**"]; [] disables the prose class`)
		}
		var globs []string
		for _, item := range gv.ListItems() {
			if !item.IsStr() || strings.TrimSpace(item.AsString()) == "" {
				return nil, fmt.Errorf(`magus.project: "gate_low_risk" entries must be non-empty glob strings`)
			}
			globs = append(globs, strings.TrimSpace(item.AsString()))
		}
		opts = append(opts, workspace.WithGateLowRisk(globs...))
	}
	// Only false declares anything: true is the default, and accepting it keeps a
	// workspace that spells the default out from failing to load.
	if iv, ok := v.MapGet("gate_inherit"); ok {
		if !iv.IsBool() {
			return nil, fmt.Errorf(`magus.project: "gate_inherit" takes a bool; false turns CI verdict inheritance off workspace-wide`)
		}
		if !iv.AsBool() {
			opts = append(opts, workspace.WithGateInheritOff())
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
				m, err := spellruntime.DecodeHandle(item)
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
			if name == magusfileSpellName {
				return nil, magusfileNotASpellErr("listing it in \"spells\"")
			}
			opts = append(opts, workspace.WithRegisteredSpell(name))
		}
		// The magusfile spell is bound whether or not it was listed. It is not a
		// language adapter you opt into like go or buf - it is what makes the file
		// you are writing runnable at all, so listing it should never have been the
		// author's job. Before this, declaring ANY spell replaced the default set and
		// dropped it, and the project's own targets stopped dispatching: the run
		// resolved each name against the bound spells, found no op, treated the miss
		// as a fan-out skip, and reported [pass] having executed nothing. This repo's
		// own proto project (spells: [buf]) had three no-op targets that way,
		// including the ci anchor that `magus affected ci` gates on.
		//
		// Unconditional: declaring it is now an error (see magusfileNotASpellErr), so
		// there is no author-listed case left to avoid double-binding against. Order
		// no longer matters for the primary slot either - an internal registration
		// never claims it (spells.WithInternal).
		opts = append(opts, workspace.WithRegisteredSpell(magusfileSpellName))
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
	// tools maps a bin name to the version window this project requires of it:
	// {"node": {"min": "22", "below": "25"}}. A plain nested map rather than a
	// VersionBounds literal, matching watch_ignore above - a magusfile does not import
	// magus/spell, so the spell-side type is not nameable here.
	//
	// Inherited only by explicit import: a project writes `import "project/.." as root`
	// and passes root's exported map. Nothing is inherited by position in the tree.
	if tv, ok := v.MapGet("tools"); ok && tv.IsMap() {
		bounds := map[string]spells.VersionBounds{}
		for _, bin := range tv.MapKeys() {
			bv, ok := tv.MapGet(bin)
			if !ok || !bv.IsMap() {
				continue
			}
			if err := rejectUnknownKeys(bv, knownToolBoundKeys,
				fmt.Sprintf("magus.project: tools[%q]", bin)); err != nil {
				return nil, err
			}
			var b spells.VersionBounds
			if mv, ok := bv.MapGet("min"); ok {
				if !mv.IsStr() {
					return nil, fmt.Errorf("magus.project: tools[%q].min must be a string version, e.g. \"22\"", bin)
				}
				b.Min = mv.AsString()
			}
			if lv, ok := bv.MapGet("below"); ok {
				if !lv.IsStr() {
					return nil, fmt.Errorf("magus.project: tools[%q].below must be a string version, e.g. \"25\"", bin)
				}
				b.Below = lv.AsString()
			}
			if !b.IsZero() {
				bounds[bin] = b
			}
		}
		if len(bounds) > 0 {
			opts = append(opts, workspace.WithToolBounds(bounds))
		}
	}
	// targets maps a target name to a per-target policy table: skip_cache=true opts
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
			// under any spelling matches a target invoked under any other.
			// A reason, not a flag. Opting out claims that REPLAYING this target
			// would be wrong; wanting a fresh run is `--no-cache`. A bare `true`
			// could not tell those apart, and six of these turned out to be
			// workarounds for a snapshot error the engine no longer raises.
			if sv, ok := pv.MapGet("skip_cache"); ok {
				var reason string
				if sv.IsStr() {
					reason = strings.TrimSpace(sv.AsString())
				}
				if reason == "" {
					return nil, fmt.Errorf(
						"magus.project: targets[%q].skip_cache needs a reason string saying why REPLAYING this target would be wrong, e.g. \"signs a fresh artifact per invocation\". "+
							"If you only want a fresh run, use `--no-cache` instead; if the target simply produces no files, it caches correctly with no policy at all", name)
				}
				opts = append(opts, workspace.WithTarget(name, workspace.SkipCache(reason)))
			}
			if ev, ok := pv.MapGet("exclusive"); ok && ev.Bool() {
				opts = append(opts, workspace.WithTarget(name, workspace.Exclusive()))
			}
			// drift says what happens when this target's declared output moves under a
			// read-only run. Absent means the default, which already gates a target that
			// declares output - so this key exists to downgrade or switch off, not to
			// switch on.
			//
			// An unknown value is a load error rather than the default: "of" or "warm"
			// would silently gate or silently not, and the author would learn which from
			// a merge rather than from the load.
			if dv, ok := pv.MapGet("drift"); ok {
				policy := types.DriftPolicy(strings.TrimSpace(dv.AsString()))
				if !types.ValidDriftPolicy(policy) {
					return nil, fmt.Errorf(
						"magus.project: targets[%q].drift is %q; it takes \"fail\" (the default for a target that declares output), \"warn\" to report without failing, or \"off\"",
						name, dv.AsString())
				}
				var reason string
				if rv, ok := pv.MapGet("drift_reason"); ok && rv.IsStr() {
					reason = strings.TrimSpace(rv.AsString())
				}
				// Same bar skip_cache sets: switching a check off is a claim about this
				// target, and the next reader cannot tell a considered exemption from an
				// abandoned workaround unless the claim is written down.
				if policy == types.DriftOff && reason == "" {
					return nil, fmt.Errorf(
						"magus.project: targets[%q].drift is \"off\" but drift_reason is empty; say why this target's output cannot be checked, e.g. \"writes a timestamped artifact no two runs can match\"", name)
				}
				opts = append(opts, workspace.WithTarget(name, workspace.Drift(policy, reason)))
			}
			// Nested to mirror magus.yaml's cache.include.*.enabled exactly, so the
			// same decision reads the same way wherever it is written:
			//
			//	"image": { "cache": { "include": { "arch": { "enabled": false } } } }
			//
			// An unrecognized shape is a load error rather than a silent skip: a
			// misspelled nesting level would leave the target inheriting the
			// workspace answer, which looks identical to a cache that works.
			if cv, ok := pv.MapGet("cache"); ok {
				inc, ok := cv.MapGet("include")
				if !ok {
					return nil, fmt.Errorf(
						"magus.project: targets[%q].cache has no `include`; the only cache key a target may set is cache.include.os/arch.enabled", name)
				}
				for _, axis := range []string{"os", "arch"} {
					av, ok := inc.MapGet(axis)
					if !ok {
						continue
					}
					ev, ok := av.MapGet("enabled")
					if !ok {
						return nil, fmt.Errorf(
							"magus.project: targets[%q].cache.include.%s needs an `enabled` bool", name, axis)
					}
					on := ev.Bool()
					if axis == "os" {
						opts = append(opts, workspace.WithTarget(name, workspace.IncludeOS(on)))
					} else {
						opts = append(opts, workspace.WithTarget(name, workspace.IncludeArch(on)))
					}
				}
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
			// memory_mb declares a memory budget and is validated like slots above.
			// magus converts it to slots per host; see types.Target.MemoryMB.
			if mv, ok := pv.MapGet("memory_mb"); ok {
				if !mv.IsInt() {
					return nil, fmt.Errorf(
						"magus.project: targets[%q].memory_mb must be a whole number of megabytes, got a %s",
						name, mv.Kind())
				}
				n := int(mv.AsInt())
				if n < 1 {
					return nil, fmt.Errorf(
						"magus.project: targets[%q].memory_mb must be >= 1, got %d", name, n)
				}
				opts = append(opts, workspace.WithTarget(name, workspace.MemoryMB(n)))
			}
		}
	}
	return opts, nil
}
