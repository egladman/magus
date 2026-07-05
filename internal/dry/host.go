package dry

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
	"github.com/egladman/gopherbuzz/vm"

	ispell "github.com/egladman/magus/internal/spell"
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
// (builtinSpellOps) plus any extra spells registered via WithSpells, so a workspace
// or third-party spell's example traces like a built-in's.
func installHost(ctx context.Context, sess *buzz.Session, tr *Tracer, spells map[string][]string) {
	buzzstd.RegisterWithOutput(sess, &tr.out)
	registerWASMCompatibleHostModules(ctx, sess)

	sess.SetGlobal("magus", buildMagus(sess, tr))
	for name, ops := range spells {
		sess.SetSyntheticModule("magus/spell/"+name, buildSpell(name, ops, tr))
	}

	// Register the canonical value-type modules as embedded SOURCE modules, mirroring
	// the real runtime (internal/interp/bindings.registerHostModules), so a SPELL
	// buffer's `import "magus/target"` resolves the Target/Command/Service object types
	// instead of failing with `undefined type "Service"`. The session's import lookup
	// order (synthetic, then source, then resolver) means these are never shadowed by
	// the catch-all resolver below.
	sess.SetSourceModule(ispell.TargetModulePath, strings.Join([]string{
		ispell.TargetModuleSource,
		ispell.PatchOpSource,
		ispell.CharmTypeSource,
		ispell.CommandSource,
		ispell.ServiceSource,
		ispell.TargetQuerySource,
		ispell.ExecResultSource,
		ispell.CommitAuthorSource,
		ispell.CommitSource,
		ispell.FileInfoSource,
		ispell.HTTPResponseSource,
		ispell.SemverVersionSource,
		ispell.URLSource,
	}, "\n"))
	sess.SetSourceModule(ispell.CharmModulePath, ispell.CharmModuleSource)

	// A workspace-local `import "spells/foo"` that no caller registered can't be
	// resolved in the sandbox; return a stub instead of failing the whole evaluation
	// with a file-not-found. The source modules above resolve first, so this never
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
// (magus.project, target.*, needs) are modeled into the graph.
func buildMagus(_ *buzz.Session, tr *Tracer) vm.Value {
	m := vm.NewMap()

	m.MapSet("project", fn("magus.project", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		path, opts := captureConfigure(args)
		tr.traceProject(path, opts)
		return vm.Null, nil
	}))

	// magus.target.<mode>(...) return handles (mode-tagged maps) consumed by needs;
	// expand_globs returns an empty list (its standalone, non-dependency use).
	// Cross-project deps (import "project/...") aren't modeled in the single-file dry
	// run - there's no sibling project to enumerate in the sandbox.
	target := vm.NewMap()
	target.MapSet("literal", targetHandle("literal"))
	target.MapSet("glob", targetHandle("glob"))
	target.MapSet("regex", targetHandle("regex"))
	target.MapSet("expand_globs", fn("magus.target.expand_globs", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return vm.ListValue(nil), nil
	}))
	m.MapSet("target", target)

	// magus.needs(handle, ...): trace a same-project edge per handle. A literal
	// handle is one exact edge; a glob/regex handle expands against the discovered
	// target set (tr.targetKeys) and traces an edge to each match, mirroring the real
	// binding's resolveTargetQuery. Cross-project handles aren't modeled here.
	m.MapSet("needs", fn("magus.needs", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		for _, a := range args {
			if !a.IsMap() {
				continue
			}
			modeV, _ := a.MapGet("mode")
			if !modeV.IsStr() {
				continue
			}
			pattern := mapStr(a, "pattern").AsString()
			if pattern == "" {
				continue
			}
			switch modeV.AsString() {
			case "literal":
				tr.addEdge(normalizeTarget(pattern))
			case "glob":
				for _, name := range tr.matchTargets(globToRegexp(pattern)) {
					tr.addEdge(name)
				}
			case "regex":
				if re, err := regexp.Compile(pattern); err == nil {
					for _, name := range tr.matchTargets(re) {
						tr.addEdge(name)
					}
				}
			}
		}
		return vm.Null, nil
	}))

	// magus.cache.<...>: a namespace in the real module (cache.remote, ...); stub as
	// a no-op so cache.remote(github) at magusfile top level doesn't blow up.
	cache := vm.NewMap()
	cache.MapSet("remote", fn("magus.cache.remote", retNull))
	m.MapSet("cache", cache)

	// has_charm(name) reports whether name is in the active charm set (tr.charms), so
	// a `run t:charm` dry-run takes charm-gated branches. For a plain graph/ls load
	// the set is empty, so every branch reads as un-charmed.
	m.MapSet("has_charm", fn("magus.has_charm", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		name := strArg(args, 0, "")
		for _, c := range tr.charms {
			if c == name {
				return vm.BoolValue(true), nil
			}
		}
		return vm.BoolValue(false), nil
	}))

	for _, level := range []string{"info", "warn", "error", "debug"} {
		m.MapSet(level, fn("magus."+level, func(_ context.Context, args []vm.Value) (vm.Value, error) {
			// Traced as a per-target op (attributed to tr.cur) so a dry-run shows
			// each target's logs in order; writing to the shared output buffer would
			// mix every probed target's logs together.
			tr.addOp("log", level, strArg(args, 0, ""))
			return vm.Null, nil
		}))
	}

	// magus.run(argv, opts?) recursively invokes `magus run <argv>`. The dry run
	// can't re-enter the runner, so it traces the invocation (the target and any
	// :charm suffix from argv[0]) as an op - the one imperative alternative to a
	// magus.needs() DAG edge.
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

	// magus.cmd/describe/insight/doctor return a captured-command result on the real
	// module; stub each as an empty success so `magus.describe(...).stdout` and the
	// like don't blow up in a dry run.
	for _, name := range []string{"cmd", "describe", "insight", "doctor"} {
		m.MapSet(name, fn("magus."+name, func(_ context.Context, _ []vm.Value) (vm.Value, error) {
			return emptyExecResult(), nil
		}))
	}

	// magus.modules()/magus.module(name) introspect the real host module registry,
	// which the sandbox doesn't wire (pulling host/std in would bloat the playground).
	// Stub them as empty-but-shaped so a reference and field access (e.g.
	// magus.module(x).methods) resolve in a dry run.
	m.MapSet("modules", fn("magus.modules", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return vm.ListValue(nil), nil
	}))
	m.MapSet("module", fn("magus.module", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		res := vm.NewMap()
		res.MapSet("name", vm.StrValue(""))
		res.MapSet("doc", vm.StrValue(""))
		res.MapSet("fields", vm.ListValue(nil))
		res.MapSet("methods", vm.ListValue(nil))
		return res, nil
	}))

	// Runtime-only members (a debugger, hints, fatal-abort, cache busting) have no
	// dry-run effect; stub them as no-ops so a reference resolves. They're here to
	// satisfy the surface parity guard, not because the dry run acts on them.
	for _, name := range []string{"hint", "fatal", "pry", "bustCache"} {
		m.MapSet(name, fn("magus."+name, retNull))
	}

	return m
}

// targetHandle returns the magus.target.<mode> callable producing a mode-tagged
// handle map ({mode, pattern}) for magus.needs to read.
func targetHandle(mode string) vm.Value {
	return fn("magus.target."+mode, func(_ context.Context, args []vm.Value) (vm.Value, error) {
		h := vm.NewMap()
		h.MapSet("mode", vm.StrValue(mode))
		h.MapSet("pattern", vm.StrValue(strArg(args, 0, "")))
		return h, nil
	})
}

// mapStr reads a string field from a map value, or returns the empty string value.
func mapStr(m vm.Value, key string) vm.Value {
	if v, ok := m.MapGet(key); ok && v.IsStr() {
		return v
	}
	return vm.StrValue("")
}

func retNull(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil }

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

// traceProject flattens the path and emitted options of a magus.project
// call into the graph model. It mirrors parseBuzzProjectOpts in the real binding.
func (r *Tracer) traceProject(path string, opts vm.Value) {
	p := Project{Path: path}
	if opts.IsMap() {
		if v, ok := opts.MapGet("depends_on"); ok {
			p.DependsOn = valToStrings(v)
		}
		if v, ok := opts.MapGet("outputs"); ok {
			p.Outputs = valToStrings(v)
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
			for _, name := range v.MapKeys() {
				pv, ok := v.MapGet(name)
				if !ok || !pv.IsMap() {
					continue
				}
				// Per-target policy mirrors the real binding (project_ns.go):
				// skipCache opts the target out of the cache; exclusive runs it
				// alone against the batch.
				if sv, ok := pv.MapGet("skipCache"); ok && sv.Bool() {
					p.NoCache = append(p.NoCache, name)
				}
				if ev, ok := pv.MapGet("exclusive"); ok && ev.Bool() {
					p.ExclusiveTargets = append(p.ExclusiveTargets, name)
				}
				if sv, ok := pv.MapGet("slots"); ok && sv.IsInt() {
					if n := sv.AsInt(); n > 0 {
						p.Slots = append(p.Slots, name+"="+strconv.FormatInt(n, 10))
					}
				}
			}
		}
	}
	r.projects = append(r.projects, p)
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
func strArg(args []vm.Value, i int, fallback string) string {
	if i < len(args) && args[i].IsStr() {
		return args[i].AsString()
	}
	return fallback
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
		if c = strings.TrimSpace(c); c != "" {
			charms = append(charms, c)
		}
	}
	return target, charms
}

// globToRegexp compiles a target glob into a regexp, mirroring the real binding's
// compileTargetPatterns: a pattern with no `*` matches any target ending in
// `-<pattern>` (^.*-<pattern>$); a pattern with `*` treats each `*` as `.*`,
// anchored end to end.
func globToRegexp(pattern string) *regexp.Regexp {
	if !strings.Contains(pattern, "*") {
		return regexp.MustCompile(`^.*-` + regexp.QuoteMeta(pattern) + `$`)
	}
	return regexp.MustCompile("^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, `.*`) + "$")
}

// matchTargets returns the discovered target keys matching re, sorted for a
// deterministic edge order.
func (r *Tracer) matchTargets(re *regexp.Regexp) []string {
	var out []string
	for _, name := range r.targetKeys {
		if re.MatchString(name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// normalizeTarget maps an export name, a depends_on argument, or a name typed at
// the console to its canonical kebab-case target key (regen_pgo -> regen-pgo,
// goBuild -> go-build, HTTPServer -> http-server). It delegates to the real magus
// normalizer so the sandbox resolves names exactly like `magus run` does - any
// casing or separator lands on the same target.
func normalizeTarget(name string) string {
	return types.DefaultTargetNameNormalizer.NormalizeTargetName(name)
}
