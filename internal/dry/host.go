package dry

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/vm"

	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/types"
)

// installHost wires a session for magusfile evaluation, layering host surfaces
// from least to most permissive: the Buzz std library (print captured into
// tr.out), then the pure-compute WASM-compatible host modules (`strings`, `json`,
// ...), then the tracing `magus` and `magus/spell/*` modules backed by tr.
// Every host effect is traced, not performed.
//
// spells is the set of spells to register as tracing `magus/spell/<name>` modules,
// keyed by import name with its op names. Callers pass the built-in registry
// (the SpellCatalog's BuiltinOps) plus any extra spells registered via WithSpells, so
// a workspace or third-party spell's example traces like a built-in's.
func installHost(ctx context.Context, sess *buzz.Session, tr *Tracer, spells map[string][]string) {
	buzzstd.RegisterWithOutput(sess, &tr.out)
	registerWASMCompatibleMagusModules(ctx, sess)

	// A native module, not a global: the playground must make you write
	// `import "magus"` exactly as a magusfile does. Bound as a global it resolved
	// without the import, so a snippet that ran here failed the moment it was pasted
	// into a real magusfile - a Run button validating syntax the language rejects
	// teaches worse than no Run button. Every other module beside it (the WASM set
	// above, the spells below) is already registered this way.
	sess.SetNativeModule("magus", buildMagus(sess, tr))
	// The DECLARATIONS beside it, so the playground checks a magus call against the
	// same signatures the real runtime does. Without them this host was untyped: a
	// snippet could read a field no return carries and the dry run would say nothing,
	// which is the opposite of what a dry run is for. The stubs above are shaped to
	// match, and TestMagusSurfaceMatchesBindings holds the member set in sync.
	if src, ok := spellruntime.ModuleDecls("magus"); ok {
		sess.SetModuleDecls("magus", src)
	}
	for name, ops := range spells {
		sess.SetNativeModule("magus/spell/"+name, buildSpell(name, ops, tr))
	}

	// Register the canonical value-type module as embedded declarations so a
	// SPELL buffer's or magusfile's `import "magus/spell"` resolves the
	// Target/Command/Service object types instead of failing with `undefined type
	// "Service"`. The real runtime (internal/interp/bindings) instead ships each
	// host-returned type (ExecResult, Commit, ...) with its OWNING module (os, fs,
	// vcs, ...) - but this sandbox never registers os/fs/http/vcs as real importable
	// modules at all (they're IO, excluded from WASMCompatibleMagusModules), so
	// there is no owning-module import for a probed buffer to reach those types
	// through. Bundling them here, under the one import path this sandbox does
	// wire, is this dry-only host's deliberate simplification - it keeps every
	// previously-typeable field (a magusfile's `> ExecResult`, `> Commit`, ...)
	// resolvable without also having to fake functional os/fs/http/vcs bindings.
	// The session's import lookup order (native, then declarations, then resolver)
	// means this is never shadowed by the catch-all resolver below.
	sess.SetModuleDecls(spellruntime.SpellModulePath, strings.Join([]string{
		spellruntime.TargetModuleSource,
		spellruntime.PatchOpSource,
		spellruntime.CharmTypeSource,
		spellruntime.CommandSource,
		spellruntime.ServiceSource,
		spellruntime.ExecResultSource,
		spellruntime.CommitAuthorSource,
		spellruntime.CommitSource,
		spellruntime.FileInfoSource,
		spellruntime.HTTPResponseSource,
		spellruntime.SemverVersionSource,
		spellruntime.URLSource,
	}, "\n"))
	sess.SetModuleDecls(spellruntime.CharmModulePath, spellruntime.CharmModuleSource)

	// A workspace-local `import "spells/foo"` that no caller registered can't be
	// resolved in the sandbox; return a stub instead of failing the whole evaluation
	// with a file-not-found. The declarations above resolve first, so this never
	// shadows them.
	sess.SetModuleResolver(func(importPath string) (vm.Value, bool) {
		m := vm.NewMap()
		m.MapSet("name", vm.StrValue(importPath))
		return m, true
	})
}

// fn is a small constructor alias matching the std package's helper.
func fn(name string, f func(context.Context, []vm.Value) (vm.Value, error)) vm.Value {
	return vm.DirectValue(name, f)
}

// buildMagus builds the tracing `magus` module. It MUST cover the same member
// surface the real bindings register (internal/interp/bindings: MagusModuleKeys) -
// a magusfile referencing a member this host omits would fail to evaluate. The guard
// test TestMagusSurfaceMatchesBindings enforces that parity. Members the dry run
// doesn't meaningfully act on are stubbed; only structure-declaring members
// (magus.project, and the ctx.needs/ctx.glob a target declares) are modeled into the graph.
func buildMagus(_ *buzz.Session, tr *Tracer) vm.Value {
	m := vm.NewMap()

	m.MapSet("project", fn("magus.project", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		path, opts := captureConfigure(args)
		if err := tr.traceProject(path, opts); err != nil {
			return vm.Null, err
		}
		return vm.Null, nil
	}))

	// Dependency and footprint declarations live only on the magus.Context a target
	// receives (ctx.needs / ctx.glob / ctx.readsFiles / ctx.writesFiles; see buildCtx), not on
	// the magus.* global - mirroring the real bindings.

	// magus.cache.<...>: a namespace in the real module (cache.remote, ...); stub as
	// a no-op so cache.remote(github) at magusfile top level doesn't blow up.
	cache := vm.NewMap()
	cache.MapSet("remote", fn("magus.cache.remote", retNull))
	m.MapSet("cache", cache)

	// magus.ci.<...>: selects the CI provider spell in the real module.
	// Stubbed no-op for the same reason as cache.remote - a magusfile calls
	// it at top level, and the dry playground evaluates that without a VM
	// able to resolve a spell.
	ci := vm.NewMap()
	ci.MapSet("provider", fn("magus.ci.provider", retNull))
	m.MapSet("ci", ci)

	// magus.secret.<...>: selects the secret provider spell and reads a credential
	// through it in the real module. provider() stubs to a no-op like the two above.
	//
	// read() returns a PLACEHOLDER rather than null or the real value. A dry run must
	// not resolve credentials at all - it would shell out to a vault, prompt for an
	// unlock, or fail the trace on a laptop that simply has no token exported, none of
	// which a structure-only pass has any business doing. But a magusfile commonly
	// feeds the result straight into a string, so null would make the trace die on a
	// concat rather than on anything real. The placeholder is deliberately not
	// credential-shaped, so a dry run that leaks it into output leaks nothing.
	secretNS := vm.NewMap()
	// Argument validation MATCHES the real namespace, unlike the cache/ci stubs above.
	// A dry run exists to catch structural mistakes before they cost a build, so a stub
	// that accepts what the real call rejects inverts its whole point: `read()` with no
	// reference, or `provider("onepassword")` passing a string where a spell handle
	// belongs, would pass the dry run and fail the real one.
	secretNS.MapSet("provider", fn("magus.secret.provider", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsMap() {
			return vm.Null, fmt.Errorf(`magus\secret.provider: expected an imported spell handle`)
		}
		if nv, ok := args[0].MapGet("name"); !ok || !nv.IsStr() || nv.AsString() == "" {
			return vm.Null, fmt.Errorf(`magus\secret.provider: argument is not a spell handle (no name)`)
		}
		return vm.Null, nil
	}))
	secretNS.MapSet("read", fn("magus.secret.read", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsStr() || args[0].AsString() == "" {
			return vm.Null, fmt.Errorf(`magus\secret.read: expected a non-empty reference string`)
		}
		return vm.StrValue("<secret>"), nil
	}))
	// grant() and endpoint() validate exactly like the real namespace and then do
	// nothing. A grant registers a rule rather than resolving anything, so there is no
	// credential work for a dry run to avoid here - only the validation, which is the
	// part a structure-only pass most wants to run.
	//
	// endpoint() returns a SYNTACTICALLY REAL but unroutable loopback URL. Null would
	// make a trace die on the string concat a magusfile does with it (ctx.with_env),
	// and a real listener has no business existing in a dry run; port 0 cannot be
	// connected to, so a trace that leaks this into a command leaks nothing that works.
	secretNS.MapSet("endpoint", fn("magus.secret.endpoint", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if err := secretGrantStubArg("endpoint", args); err != nil {
			return vm.Null, err
		}
		return vm.StrValue("http://127.0.0.1:0"), nil
	}))
	m.MapSet("secret", secretNS)

	// magus.workspace.<...>: wires a workspace-provider spell in the real module.
	// Stubbed no-op for the same reason as the two above; the playground has no
	// workspace to fold provided projects into, and no VM to run the provider.
	ws := vm.NewMap()
	ws.MapSet("provider", fn("magus.workspace.provider", retNull))
	m.MapSet("workspace", ws)

	// has_charm(name) reports whether name is in the active charm set (tr.charms), so
	// a `run t:charm` dry-run takes charm-gated branches. The same closure backs
	// ctx.has_charm (see buildCtx).
	m.MapSet("hasCharm", fn("magus.hasCharm", traceHasCharm(tr)))

	// magus.log.* - the emitting members, grouped as they are in the real bindings.
	// hint rides along here rather than with the runtime-only stubs below because it
	// emits, and a dry run should show it in target order like any other message.
	logNS := vm.NewMap()
	for _, level := range []string{"info", "warn", "error", "debug", "hint"} {
		logNS.MapSet(level, fn("magus.log."+level, func(_ context.Context, args []vm.Value) (vm.Value, error) {
			// Traced as a per-target op (attributed to tr.cur) so a dry-run shows
			// each target's logs in order; writing to the shared output buffer would
			// mix every probed target's logs together.
			tr.addOp("log", level, strArg(args, 0))
			return vm.Null, nil
		}))
	}
	m.MapSet("log", logNS)

	// magus.raise(code, message, cause?, url?) fails with a coded diagnostic. A dry run
	// must not actually fail, so it traces the code and message and returns - the point
	// of the probe is which branch a target would take, not that it aborts there.
	m.MapSet("raise", fn("magus.raise", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		tr.addOp("raise", strArg(args, 0), strArg(args, 1))
		return vm.Null, nil
	}))

	// magus.run(argv, opts?) recursively invokes `magus run <argv>`. The dry run
	// can't re-enter the runner, so it traces the invocation (the target and any
	// :charm suffix from argv[0]) as an op - the one imperative alternative to a
	// ctx.needs() DAG edge.
	m.MapSet("run", fn("magus.run", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if ref := firstListStr(args); ref != "" {
			target, charms := splitTargetRef(ref)
			display := target
			if len(charms) > 0 {
				display = target + ":" + strings.Join(charms, ",")
			}
			tr.addOp("run", display, "")
		}
		return emptyExecResult(), nil
	}))

	// magus.cmd/describe/insight return a captured-command result on the real
	// module; stub each as an empty success so `magus.describe(...).stdout` and the
	// like don't blow up in a dry run.
	for _, name := range []string{"cmd", "describe", "insight"} {
		m.MapSet(name, fn("magus."+name, func(_ context.Context, _ []vm.Value) (vm.Value, error) {
			return emptyExecResult(), nil
		}))
	}

	// The in-process read-only verbs return workspace data on the real module. A dry
	// run has no workspace, so each is stubbed with its result SHAPE - an empty but
	// correctly-keyed record - so field access (magus.ls().projects, .affected) still
	// resolves instead of blowing up on null.
	m.MapSet("ls", fn("magus.ls", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		res := vm.NewMap()
		res.MapSet("workspace", vm.StrValue(""))
		res.MapSet("count", vm.IntValue(0))
		res.MapSet("projects", vm.ListValue(nil))
		return res, nil
	}))
	m.MapSet("affected", fn("magus.affected", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		res := vm.NewMap()
		res.MapSet("base", vm.StrValue(""))
		res.MapSet("changed", vm.ListValue(nil))
		res.MapSet("seed", vm.ListValue(nil))
		res.MapSet("filesBySeed", vm.NewMap())
		res.MapSet("affected", vm.ListValue(nil))
		return res, nil
	}))
	// The reports magus returns as domain types (doctor, describeFile, insightReport,
	// affectedImpact) fork a real magus in the live host. Same rule as
	// ls/affected: stub each with its result shape so `magus.doctor().summary.fail`
	// and friends resolve. Field names track the Buzz mirrors in
	// internal/spellruntime/gen/types.
	m.MapSet("doctor", fn("magus.doctor", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		res := vm.NewMap()
		res.MapSet("workspace", vm.StrValue(""))
		res.MapSet("checks", vm.ListValue(nil))
		summary := vm.NewMap()
		summary.MapSet("ok", vm.IntValue(0))
		summary.MapSet("fail", vm.IntValue(0))
		summary.MapSet("advice", vm.IntValue(0))
		res.MapSet("summary", summary)
		return res, nil
	}))
	m.MapSet("describeFile", fn("magus.describeFile", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		res := vm.NewMap()
		res.MapSet("definition", vm.StrValue(""))
		res.MapSet("count", vm.IntValue(0))
		res.MapSet("files", vm.ListValue(nil))
		return res, nil
	}))
	m.MapSet("affectedImpact", fn("magus.affectedImpact", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		res := vm.NewMap()
		res.MapSet("base", vm.StrValue(""))
		res.MapSet("changedFileCount", vm.IntValue(0))
		res.MapSet("changedFiles", vm.ListValue(nil))
		res.MapSet("seedProjects", vm.ListValue(nil))
		res.MapSet("affectedProjects", vm.ListValue(nil))
		res.MapSet("changedSymbols", vm.ListValue(nil))
		res.MapSet("changedFileCoverage", vm.ListValue(nil))
		res.MapSet("notes", vm.ListValue(nil))
		return res, nil
	}))
	// A dry run performs no VCS probe, so nothing has drifted by construction. The
	// shape still has to match the real verdict, so a magusfile reading .files or
	// .drifted behaves the same under --dry-run as it does for real.
	m.MapSet("diagnoseDrift", fn("magus.diagnoseDrift", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		res := vm.NewMap()
		res.MapSet("drifted", vm.BoolValue(false))
		res.MapSet("code", vm.StrValue(""))
		res.MapSet("message", vm.StrValue(""))
		res.MapSet("url", vm.StrValue(""))
		res.MapSet("files", vm.ListValue(nil))
		return res, nil
	}))
	// insightReport nests a record per lens rather than a list, so each one is shaped
	// too: a null lens would break `.ownership.projects` where an empty list does not.
	m.MapSet("insightReport", fn("magus.insightReport", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		stats := vm.NewMap()
		stats.MapSet("definition", vm.StrValue(""))
		stats.MapSet("nodeCount", vm.IntValue(0))
		stats.MapSet("edgeCount", vm.IntValue(0))
		stats.MapSet("gods", vm.ListValue(nil))
		stats.MapSet("orphans", vm.ListValue(nil))
		stats.MapSet("coverage", vm.ListValue(nil))
		stats.MapSet("isolatedCount", vm.IntValue(0))
		stats.MapSet("componentCount", vm.IntValue(0))
		stats.MapSet("largestComponentSize", vm.IntValue(0))

		// Volatility is shaped like every other lens rather than null. It reads as the
		// "absent" case, but the mirror declares it non-optional (types.InsightReport
		// carries a VolatilityReport by value), so the checker types
		// `.volatility.targets` as always present while a null hands the run a member
		// access on nothing - which aborts the target body mid-trace and still reports
		// OK, the silent-truncation failure the shaped stubs above exist to avoid.
		volatility := vm.NewMap()
		volatility.MapSet("threshold", vm.FloatValue(0))
		volatility.MapSet("targets", vm.ListValue(nil))

		// Unreferenced carries a nested answer record, so the plain insightLens helper
		// (definition + list fields) is not enough: a script reading
		// `.unreferenced.answer.verdict` would hit a member access on nothing, which is
		// the same silent truncation the shaped stubs exist to avoid.
		answer := vm.NewMap()
		answer.MapSet("verdict", vm.StrValue(""))
		answer.MapSet("reason", vm.StrValue(""))
		answer.MapSet("uncovered", vm.ListValue(nil))
		unreferenced := vm.NewMap()
		unreferenced.MapSet("definition", vm.StrValue(""))
		unreferenced.MapSet("symbols", vm.ListValue(nil))
		unreferenced.MapSet("answer", answer)

		res := vm.NewMap()
		res.MapSet("hotspots", insightLens("nodes", "files"))
		res.MapSet("affinity", insightLens("pairs"))
		res.MapSet("ownership", insightLens("projects"))
		res.MapSet("trend", insightLens("projects"))
		res.MapSet("volatility", volatility)
		res.MapSet("unreferenced", unreferenced)
		res.MapSet("graphStats", stats)
		return res, nil
	}))
	m.MapSet("targets", fn("magus.targets", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		res := vm.NewMap()
		res.MapSet("projects", vm.ListValue(nil))
		return res, nil
	}))
	m.MapSet("where", fn("magus.where", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return vm.StrValue(""), nil
	}))
	m.MapSet("projectGraph", fn("magus.projectGraph", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		res := vm.NewMap()
		res.MapSet("nodes", vm.ListValue(nil))
		res.MapSet("dependsOn", vm.NewMap())
		res.MapSet("blastRadius", vm.NewMap())
		return res, nil
	}))

	addPureMagus(m)

	// magus.modules()/magus.module(name) introspect the real host module registry,
	// which the sandbox doesn't wire (pulling host/std in would bloat the playground).
	// Stub them as empty-but-shaped so a reference and field access (e.g.
	// magus.module(x).methods) resolve in a dry run.
	m.MapSet("describeModule", fn("magus.describeModule", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		// An empty-but-shaped LIST: the real member returns a collection whether or
		// not a name selects one, so a dry run must too or `describeModule(x)[0].name`
		// stops resolving.
		return vm.ListValue(nil), nil
	}))

	// Runtime-only members (a debugger, hints, fatal-abort, cache busting) have no
	// dry-run effect; stub them as no-ops so a reference resolves. They're here to
	// satisfy the surface parity guard, not because the dry run acts on them.
	for _, name := range []string{"fatal", "pry", "bustCache"} {
		m.MapSet(name, fn("magus."+name, retNull))
	}

	return m
}

func retNull(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil }

// insightLens shapes one VCS-history lens of magus.insightReport. All four share a
// definition/commits/since header and differ only in which lists they carry.
func insightLens(listKeys ...string) vm.Value {
	v := vm.NewMap()
	v.MapSet("definition", vm.StrValue(""))
	v.MapSet("commits", vm.IntValue(0))
	v.MapSet("since", vm.StrValue(""))
	for _, k := range listKeys {
		v.MapSet(k, vm.ListValue(nil))
	}
	return v
}

// traceNeeds backs ctx.needs: it traces a same-project edge per target
// function argument, keyed by the function's declared name (FunName) run through the
// same normalizer as the real binding's resolveTargetFun; a glob(...) list arg is
// flattened to its handles. Cross-project handles aren't modeled in the single-file dry
// run - there's no sibling project in the sandbox - so a non-function argument is
// skipped, best-effort.
func traceNeeds(tr *Tracer) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		var trace func(a vm.Value)
		trace = func(a vm.Value) {
			if a.IsList() {
				for _, el := range a.ListItems() {
					trace(el)
				}
				return
			}
			if a.IsFun() {
				if name := a.FunName(); name != "" {
					tr.addEdge(normalizeTarget(name))
				}
			}
		}
		for _, a := range args {
			trace(a)
		}
		return vm.Null, nil
	}
}

// traceGlob backs ctx.glob: it expands each pattern against the
// discovered target set (tr.targetKeys) and RETURNS the matches as target handles, so
// needs(glob("...")) traces an edge per match. Mirrors the real binding's buildBuzzGlob:
// a pattern resolves to handles, keeping needs monomorphic.
func traceGlob(tr *Tracer) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		// Every pattern is collected BEFORE matching: "!" negation subtracts from the
		// union, so it is a property of the whole list, not of one pattern in isolation.
		var patterns []string
		for _, a := range args {
			if !a.IsStr() {
				continue
			}
			patterns = append(patterns, a.AsString())
		}
		matched := types.MatchTargetPatterns(tr.targetKeys, patterns)
		handles := make([]vm.Value, 0, len(matched))
		for _, name := range matched {
			handles = append(handles, fn(name, retNull))
		}
		return vm.ListValue(handles), nil
	}
}

// traceHasCharm backs magus.has_charm and ctx.has_charm: it reports whether name is in
// the active charm set (tr.charms), so a `run t:charm` dry-run takes charm-gated
// branches. For a plain graph/ls load the set is empty, so every branch reads un-charmed.
func traceHasCharm(tr *Tracer) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		// Normalize the query the way types.HasCharm does on the real path: the
		// stored set is canonical, so a raw compare here answers differently than
		// the run being traced.
		name := types.Normalize(strArg(args, 0))
		for _, c := range tr.charms {
			if c == name {
				return vm.BoolValue(true), nil
			}
		}
		return vm.BoolValue(false), nil
	}
}

// buildCtx builds the magus.Context value a target receives as its first argument in a
// dry run. Its methods mirror the tracing magus.* members - needs/glob trace and expand
// graph edges, has_charm reads the active charm set - so a ctx-form body traces exactly
// as the old global form did. File declarations are inert: the dry graph reads the footprint
// statically (describe.Extract), never by tracing the body.
func buildCtx(tr *Tracer) vm.Value {
	c := vm.NewMap()
	c.MapSet("needs", fn("ctx.needs", traceNeeds(tr)))
	c.MapSet("glob", fn("ctx.glob", traceGlob(tr)))
	c.MapSet("hasCharm", fn("ctx.hasCharm", traceHasCharm(tr)))
	c.MapSet("readsFiles", fn("ctx.readsFiles", retNull))
	c.MapSet("writesFiles", fn("ctx.writesFiles", retNull))
	c.MapSet("modifiesExistingFiles", fn("ctx.modifiesExistingFiles", retNull))
	return c
}

// captureConfigure reads a magus.project call into the project path plus
// its options map. It mirrors the real binding: configure({...}) customizes this
// project (path defaults to "."), configure(path, {...}) an explicit one. Returns a
// null opts value (no-op) for a malformed call.
func captureConfigure(args []vm.Value) (string, vm.Value) {
	path := "."
	var opts = vm.Null
	if len(args) >= 1 && args[0].IsStr() {
		path = args[0].AsString()
		if len(args) >= 2 {
			opts = args[1]
		}
	} else if len(args) >= 1 {
		opts = args[0]
	}
	if !opts.IsMap() {
		return path, vm.Null
	}
	return path, opts
}

// dryKnownProjectOptionKeys / dryKnownTargetPolicyKeys mirror
// knownProjectOptionKeys / knownTargetPolicyKeys in the real binding
// (internal/interp/bindings/project_ns.go), so the playground/dry path rejects
// the same typos the real engine does instead of silently dropping them.
var (
	// The SAME list the engine rejects against (types.ProjectOptions), not a copy.
	// These were two hand-maintained slices and they had already drifted.
	dryKnownProjectOptionKeys = types.ProjectOptionKeys()
	dryKnownTargetPolicyKeys  = []string{"skip_cache", "exclusive", "slots"}
)

// rejectUnknownKeys errors on the first key in m absent from known. context
// names the call site for the error message.
func rejectUnknownKeys(m vm.Value, known []string, context string) error {
	if !m.IsMap() {
		return nil
	}
	for _, k := range m.MapKeys() {
		if slices.Contains(known, k) {
			continue
		}
		sortedKnown := append([]string(nil), known...)
		slices.Sort(sortedKnown)
		msg := fmt.Sprintf("%s: unknown option %q (known options: %s)",
			context, k, strings.Join(sortedKnown, ", "))
		if hint := suggestNearest(k, known); hint != "" {
			return fmt.Errorf("%s: unknown option %q; did you mean %q? (known options: %s)",
				context, k, hint, strings.Join(sortedKnown, ", "))
		}
		return errors.New(msg)
	}
	return nil
}

// suggestNearest returns the closest candidate to typed by Levenshtein
// distance, or "" if nothing is close enough. A small local copy (rather than
// importing internal/interactive) keeps this package a leaf, per the package
// doc: it must stay free of anything that would break the js/wasm build.
func suggestNearest(typed string, candidates []string) string {
	best, bestDist := "", 3
	for _, c := range candidates {
		if d := levenshtein(typed, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	row := make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}
	for i, ca := range a {
		prev := i + 1
		for j, cb := range b {
			cost := 1
			if ca == cb {
				cost = 0
			}
			cur := min(row[j]+cost, min(prev+1, row[j+1]+1))
			row[j] = prev
			prev = cur
		}
		row[len(b)] = prev
	}
	return row[len(b)]
}

// traceProject flattens the path and emitted options of a magus.project
// call into the graph model. It mirrors parseBuzzProjectOpts in the real binding,
// including its unknown-key and bad-slots-value validation.
func (r *Tracer) traceProject(path string, opts vm.Value) error {
	p := Project{Path: path}
	if opts.IsMap() {
		if err := rejectUnknownKeys(opts, dryKnownProjectOptionKeys, "magus.project"); err != nil {
			return err
		}
		if v, ok := opts.MapGet("depends_on"); ok {
			p.DependsOn = valToStrings(v)
		}
		if v, ok := opts.MapGet("outputs"); ok {
			p.Outputs = valToStrings(v)
		}
		if v, ok := opts.MapGet("sources"); ok {
			p.Sources = valToStrings(v)
		}
		if v, ok := opts.MapGet("exclusive"); ok {
			p.Exclusive = v.Bool()
		}
		if v, ok := opts.MapGet("spells"); ok && v.IsList() {
			for _, item := range v.ListItems() {
				if item.IsMap() {
					if nv, ok := item.MapGet("name"); ok && nv.IsStr() {
						p.Spells = append(p.Spells, nv.AsString())
					}
				}
			}
		}
		if v, ok := opts.MapGet("targets"); ok && v.IsMap() {
			for _, rawName := range v.MapKeys() {
				pv, ok := v.MapGet(rawName)
				if !ok || !pv.IsMap() {
					continue
				}
				if err := rejectUnknownKeys(pv, dryKnownTargetPolicyKeys,
					fmt.Sprintf("magus.project: targets[%q]", rawName)); err != nil {
					return err
				}
				name := types.Normalize(rawName)
				// Per-target policy mirrors the real binding (project_ns.go):
				// skip_cache opts the target out of the cache; exclusive runs it
				// alone against the batch.
				//
				// skip_cache is a REASON, not a flag, and this path must reject the
				// bare `true` for the same reason the real binding does. A Buzz string
				// is truthy, so the old `sv.Bool()` accepted both forms - which meant
				// the Playground and the editor's diagnostics stayed green on a
				// magusfile that `magus run` refuses to load.
				if sv, ok := pv.MapGet("skip_cache"); ok {
					var reason string
					if sv.IsStr() {
						reason = strings.TrimSpace(sv.AsString())
					}
					if reason == "" {
						return fmt.Errorf(
							"magus.project: targets[%q].skip_cache needs a reason string saying why REPLAYING this target would be wrong, e.g. \"signs a fresh artifact per invocation\". "+
								"If you only want a fresh run, use `--no-cache` instead; if the target simply produces no files, it caches correctly with no policy at all", rawName)
					}
					p.NoCache = append(p.NoCache, name)
				}
				if ev, ok := pv.MapGet("exclusive"); ok && ev.Bool() {
					p.ExclusiveTargets = append(p.ExclusiveTargets, name)
				}
				if sv, ok := pv.MapGet("slots"); ok {
					if !sv.IsInt() {
						return fmt.Errorf(
							"magus.project: targets[%q].slots must be a whole number, got a %s",
							rawName, sv.Kind())
					}
					n := sv.AsInt()
					if n < 1 {
						return fmt.Errorf(
							"magus.project: targets[%q].slots must be >= 1, got %d", rawName, n)
					}
					p.Slots = append(p.Slots, name+"="+strconv.FormatInt(n, 10))
				}
			}
		}
	}
	r.projects = append(r.projects, p)
	return nil
}

// buildSpell builds the object bound by `import "magus/spell/<name>"`: each op is
// a tracing callable reachable as spell["<name>-<verb>"](), plus listTargets()
// and the handle metadata fields the real spell handle exposes.
func buildSpell(name string, ops []string, tr *Tracer) vm.Value {
	h := vm.NewMap()
	h.MapSet("name", vm.StrValue(name))
	for _, op := range ops {
		h.MapSet(op, fn("spell."+op, func(_ context.Context, args []vm.Value) (vm.Value, error) {
			tr.addOp("spell", op, spellArgsDetail(args))
			return vm.Null, nil
		}))
	}
	opsCopy := append([]string(nil), ops...)
	h.MapSet("listTargets", fn("spell.listTargets", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return strsToList(opsCopy), nil
	}))
	return h
}

func strsToList(ss []string) vm.Value {
	items := make([]vm.Value, len(ss))
	for i, s := range ss {
		items[i] = vm.StrValue(s)
	}
	return vm.ListValue(items)
}

// valToStrings reads a Buzz str or [str] into a Go slice.
func valToStrings(v vm.Value) []string {
	if v.IsStr() {
		return []string{v.AsString()}
	}
	if v.IsList() {
		var out []string
		for _, item := range v.ListItems() {
			if item.IsStr() {
				out = append(out, item.AsString())
			}
		}
		return out
	}
	return nil
}

// strArg returns args[i] as a string, or fallback if it is absent or not a str.
func strArg(args []vm.Value, i int) string {
	if i < len(args) && args[i].IsStr() {
		return args[i].AsString()
	}
	return ""
}

func spellArgsDetail(args []vm.Value) string {
	if len(args) == 0 || !args[0].IsMap() {
		return ""
	}
	if av, ok := args[0].MapGet("args"); ok {
		return strings.Join(valToStrings(av), " ")
	}
	return ""
}

// emptyExecResult is the {stdout, stderr, code, success} trace the captured-command
// magus.* members return; a dry-run stub reports an empty success.
func emptyExecResult() vm.Value {
	res := vm.NewMap()
	res.MapSet("stdout", vm.StrValue(""))
	res.MapSet("stderr", vm.StrValue(""))
	res.MapSet("code", vm.IntValue(0))
	res.MapSet("success", vm.BoolValue(true))
	return res
}

// firstListStr returns the first string element of the first argument when it is a
// list (magus.run's argv), else "".
func firstListStr(args []vm.Value) string {
	if len(args) == 0 || !args[0].IsList() {
		return ""
	}
	items := args[0].ListItems()
	if len(items) == 0 || !items[0].IsStr() {
		return ""
	}
	return items[0].AsString()
}

// splitTargetRef splits a "target:charm,charm" reference into the normalized target
// key and its charms, mirroring the CLI's `magus run target:charm` suffix.
func splitTargetRef(ref string) (target string, charms []string) {
	i := strings.IndexByte(ref, ':')
	if i < 0 {
		return normalizeTarget(ref), nil
	}
	target = normalizeTarget(ref[:i])
	for _, c := range strings.Split(ref[i+1:], ",") {
		// Charms canonicalize exactly as the target does. ParseTarget normalizes
		// both halves on the real run path; normalizing only the target here made
		// the tracer disagree with the run it exists to predict - `--dry-run
		// lint:no_cache` took the un-charmed branch while the real `lint:no_cache`
		// took the charmed one.
		if c = strings.TrimSpace(c); c != "" {
			charms = append(charms, types.Normalize(c))
		}
	}
	return target, charms
}

// normalizeTarget maps an export name, a depends_on argument, or a name typed at
// the console to its canonical kebab-case target key (regen_pgo -> regen-pgo,
// goBuild -> go-build, HTTPServer -> http-server). It delegates to the real magus
// normalizer so the sandbox resolves names exactly like `magus run` does - any
// casing or separator lands on the same target.
func normalizeTarget(name string) string {
	return types.Normalize(name)
}

// addPureMagus installs the magus.* members that are pure computation - no
// workspace, no registry, no IO - so they answer honestly wherever they appear.
//
// Shared by both playground hosts on purpose. The tracer (buildMagus) needs them
// because a magusfile example may call them; PLAIN Eval needs them because that is
// the mode with a Run button, where a snippet's trailing value is what the reader
// sees. Stubbing a pure function in either would turn a live doc into a decorative
// one - docs/concepts/targets.md teaches normalization by running it.
//
// Everything else on the magus surface depends on a workspace and stays stubbed in
// the tracer / absent in plain mode, which is why this is a small explicit list
// rather than a share of the whole module.
func addPureMagus(m vm.Value) {
	// The one canonicalizer for every entity name: target, charm, spell op.
	m.MapSet("canonicalName", fn("magus.canonicalName", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsStr() {
			return vm.Null, fmt.Errorf("magus.canonicalName: expected a name string")
		}
		return vm.StrValue(types.Normalize(args[0].AsString())), nil
	}))
}

// pureMagus is the plain-playground `magus` global: only the members that work
// without a workspace. Plain Eval is the language playground, so a member that
// would need one is better absent than present-and-lying.
func pureMagus() vm.Value {
	m := vm.NewMap()
	addPureMagus(m)
	return m
}

// secretGrantStubArg is the dry-run counterpart to bindings.secretGrantArg: read a
// secret-grant object, reject what the real namespace would reject.
//
// Returns only an error - the dry run has no use for the grant itself, and both
// callers were discarding it.
//
// The field READ is duplicated (a dozen lines, and the packages cannot share a helper
// without internal/dry depending on the bindings layer it exists to stand in for); the
// RULES are not - both call types.SecretGrant.Normalize, which is where every
// judgement about a malformed grant lives. Duplicating the extraction is safe because
// it has no rules in it; duplicating the validation would not be. The message must
// stay in step with the real one, including its example: the dry run is the pass most
// likely to surface this error, so giving the user less to work with than the real
// namespace does inverts the point.
func secretGrantStubArg(method string, args []vm.Value) error {
	// MapView, not IsMap - see bindings.secretGrantArg. An object INSTANCE is tagObject,
	// not tagMap, so the documented spelling was rejected here too.
	// Length first: indexing args[0] to build the view before checking it exists
	// panics on a no-argument call instead of reporting the error below.
	if len(args) == 0 {
		return fmt.Errorf(`magus\secret.%s: expected an object with ref/host/header/prefix fields, e.g. SecretGrant{ ref = "...", host = "api.example.com", header = "Authorization", prefix = "Bearer " } declared in your magusfile`, method)
	}
	fields, viewOK := args[0].MapView()
	if !viewOK {
		return fmt.Errorf(`magus\secret.%s: expected an object with ref/host/header/prefix fields, e.g. SecretGrant{ ref = "...", host = "api.example.com", header = "Authorization", prefix = "Bearer " } declared in your magusfile`, method)
	}
	var bad error
	field := func(name string) string {
		v, ok := fields.MapGet(name)
		if !ok {
			return ""
		}
		if !v.IsStr() {
			if bad == nil {
				bad = fmt.Errorf(`magus\secret.%s: field %q must be a str`, method, name)
			}
			return ""
		}
		return v.AsString()
	}
	g := types.SecretGrant{
		Ref:    field("ref"),
		Host:   field("host"),
		Header: field("header"),
		Prefix: field("prefix"),
	}
	if bad != nil {
		return bad
	}
	if _, err := g.Normalize(); err != nil {
		return fmt.Errorf(`magus\secret.%s: %w`, method, err)
	}
	return nil
}
