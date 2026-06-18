package playground

import (
	"context"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
)

// installHost wires a session for magusfile evaluation: the std library (with
// print captured into rec.out) plus stub `magus`, `magus/extra`, and
// `magus/spell/*` modules backed by rec. Every host effect is recorded, not
// performed.
func installHost(sess *buzz.Session, rec *Recorder) {
	buzzstd.RegisterWithOutput(sess, &rec.out)

	sess.SetGlobal("magus", buildMagus(sess, rec))
	sess.SetSyntheticModule("magus/extra", buildExtra(rec))
	for name, ops := range builtinSpellOps {
		sess.SetSyntheticModule("magus/spell/"+name, buildSpell(name, ops, rec))
	}
	// A workspace-local `import "spells/foo"` can't be resolved in the sandbox;
	// report it instead of failing the whole evaluation with a file-not-found.
	sess.SetModuleResolver(func(importPath string) (buzz.Value, bool) {
		m := buzz.NewMap()
		m.MapSet("name", buzz.StrValue(importPath))
		return m, true
	})
}

// fn is a small constructor alias matching the std package's helper.
func fn(name string, f func(context.Context, []buzz.Value) (buzz.Value, error)) buzz.Value {
	return buzz.DirectValue(name, f)
}

// buildMagus builds the recording `magus` module. It MUST cover the same member
// surface the real bindings register (internal/interp/bindings: MagusModuleKeys) —
// a magusfile referencing a member this host omits would fail to evaluate. The
// guard test TestMagusSurfaceMatchesBindings enforces that parity, so a new (or
// removed) real binding can't silently drift from this dry-run host. Members the
// dry run doesn't meaningfully act on are stubbed; only structure-declaring members
// (project.register, target.*, needs) are modeled into the graph.
func buildMagus(sess *buzz.Session, rec *Recorder) buzz.Value {
	m := buzz.NewMap()

	project := buzz.NewMap()
	project.MapSet("register", fn("magus.project.register", func(ctx context.Context, args []buzz.Value) (buzz.Value, error) {
		path, opts, err := captureRegister(ctx, sess, args)
		if err != nil {
			return buzz.Null, err
		}
		rec.recordProject(path, opts)
		return buzz.Null, nil
	}))
	m.MapSet("project", project)

	// magus.target.<mode>(...) return handles (mode-tagged maps) consumed by needs;
	// expand_globs returns an empty list (its standalone, non-dependency use).
	// Cross-project deps (import "project/...") aren't modeled in the single-file dry
	// run — there's no sibling project to enumerate in the sandbox.
	target := buzz.NewMap()
	target.MapSet("literal", targetHandle("literal"))
	target.MapSet("glob", targetHandle("glob"))
	target.MapSet("regex", targetHandle("regex"))
	target.MapSet("expand_globs", fn("magus.target.expand_globs", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		return buzz.ListValue(nil), nil
	}))
	m.MapSet("target", target)

	// magus.needs(handle, ...): record an exact same-project edge per literal handle;
	// glob/regex aren't expanded in the dry run (no project target set to match
	// against) — recorded as nothing.
	m.MapSet("needs", fn("magus.needs", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		for _, a := range args {
			if !a.IsMap() {
				continue
			}
			mode, _ := a.MapGet("mode")
			if !mode.IsStr() || mode.AsString() != "literal" {
				continue
			}
			if t := mapStr(a, "pattern").AsString(); t != "" {
				rec.addEdge(normalizeTarget(t))
			}
		}
		return buzz.Null, nil
	}))

	// magus.cache.<...>: a namespace in the real module (cache.remote, …); stub it as
	// a no-op namespace so cache.remote(github) at magusfile top level doesn't blow up.
	cache := buzz.NewMap()
	cache.MapSet("remote", fn("magus.cache.remote", retNull))
	m.MapSet("cache", cache)

	m.MapSet("has_charm", fn("magus.has_charm", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		return buzz.BoolValue(false), nil
	}))

	for _, level := range []string{"info", "warn", "error", "debug"} {
		m.MapSet(level, fn("magus."+level, func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
			// Recorded as a per-target op (attributed to rec.cur) so a dry-run
			// shows each target's logs in order; not written to the shared output
			// buffer, which would mix every probed target's logs together.
			rec.addOp("log", level, strArg(args, 0, ""))
			return buzz.Null, nil
		}))
	}

	// magus.cmd(...) returns a captured-command result on the real module; stub it as
	// an empty success so `magus.cmd(...).stdout` doesn't blow up in a dry run.
	m.MapSet("cmd", fn("magus.cmd", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		res := buzz.NewMap()
		res.MapSet("stdout", buzz.StrValue(""))
		res.MapSet("stderr", buzz.StrValue(""))
		res.MapSet("code", buzz.IntValue(0))
		res.MapSet("success", buzz.BoolValue(true))
		return res, nil
	}))

	// Runtime-only members (a debugger, hints, fatal-abort) have no dry-run effect;
	// stub them as no-ops so a reference resolves. They're here to satisfy the surface
	// parity guard, not because the dry run acts on them.
	for _, name := range []string{"hint", "fatal", "pry"} {
		m.MapSet(name, fn("magus."+name, retNull))
	}

	return m
}

// targetHandle returns the magus.target.<mode> callable producing a mode-tagged
// handle map ({mode, pattern}) for magus.needs to read.
func targetHandle(mode string) buzz.Value {
	return fn("magus.target."+mode, func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		h := buzz.NewMap()
		h.MapSet("mode", buzz.StrValue(mode))
		h.MapSet("pattern", buzz.StrValue(strArg(args, 0, "")))
		return h, nil
	})
}

// mapStr reads a string field from a map value, or returns the empty string value.
func mapStr(m buzz.Value, key string) buzz.Value {
	if v, ok := m.MapGet(key); ok && v.IsStr() {
		return v
	}
	return buzz.StrValue("")
}

func retNull(context.Context, []buzz.Value) (buzz.Value, error) { return buzz.Null, nil }

// captureRegister runs a magus.project.register configurator and returns the
// project path plus the options map it emits. It mirrors recordProjectOpts in the
// real binding: register(fn) configures this project (path defaults to "."),
// register(path, fn) an explicit one; the configurator emits its options via
// cb({...}). Returns a null opts value (no-op) for a malformed call.
func captureRegister(ctx context.Context, sess *buzz.Session, args []buzz.Value) (string, buzz.Value, error) {
	path := "."
	var configurator buzz.Value
	if len(args) >= 1 && args[0].IsStr() {
		path = args[0].AsString()
		if len(args) >= 2 {
			configurator = args[1]
		}
	} else if len(args) >= 1 {
		configurator = args[0]
	}
	if !configurator.IsFun() {
		return path, buzz.Null, nil
	}
	captured := buzz.Null
	cb := buzz.DirectValue("magus.cb", func(_ context.Context, a []buzz.Value) (buzz.Value, error) {
		if len(a) > 0 {
			captured = a[0]
		}
		return buzz.Null, nil
	})
	p := buzz.NewMap()
	p.MapSet("path", buzz.StrValue(path))
	if _, err := sess.CallValue(ctx, configurator, []buzz.Value{p, cb}); err != nil {
		return path, buzz.Null, err
	}
	return path, captured, nil
}

// recordProject flattens the path and emitted options of a magus.project.register
// call into the graph model. It mirrors parseBuzzProjectOpts in the real binding.
func (r *Recorder) recordProject(path string, opts buzz.Value) {
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
				if cv, ok := pv.MapGet("cachable"); ok && !cv.Bool() {
					p.NoCache = append(p.NoCache, name)
				}
				if iv, ok := pv.MapGet("isolated"); ok && iv.Bool() {
					p.Isolated = append(p.Isolated, name)
				}
			}
		}
	}
	r.projects = append(r.projects, p)
}

// buildSpell builds the object bound by `import "magus/spell/<name>"`: each op is
// a recording callable reachable as spell["<name>-<verb>"](), plus listTargets()
// and the handle metadata fields the real spell handle exposes.
func buildSpell(name string, ops []string, rec *Recorder) buzz.Value {
	h := buzz.NewMap()
	h.MapSet("name", buzz.StrValue(name))
	for _, op := range ops {
		h.MapSet(op, fn("spell."+op, func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
			rec.addOp("spell", op, spellArgsDetail(args))
			return buzz.Null, nil
		}))
	}
	opsCopy := append([]string(nil), ops...)
	h.MapSet("listTargets", fn("spell.listTargets", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		return strsToList(opsCopy), nil
	}))
	return h
}

// buildExtra builds the `magus/extra` aggregate: a namespace of recording host
// utilities. Only the members magusfiles commonly reach are stubbed; each
// records its call and returns a plausible canned value.
func buildExtra(rec *Recorder) buzz.Value {
	extra := buzz.NewMap()

	os := buzz.NewMap()
	os.MapSet("exec", fn("extra.os.exec", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		rec.addOp("exec", strArg(args, 0, "exec"), execDetail(args))
		res := buzz.NewMap()
		res.MapSet("stdout", buzz.StrValue(""))
		res.MapSet("stderr", buzz.StrValue(""))
		res.MapSet("code", buzz.IntValue(0))
		res.MapSet("success", buzz.BoolValue(true))
		return res, nil
	}))
	extra.MapSet("os", os)

	fs := buzz.NewMap()
	fs.MapSet("exists", fn("extra.fs.exists", retBool(false)))
	fs.MapSet("readFile", fn("extra.fs.readFile", retStr("")))
	fs.MapSet("glob", fn("extra.fs.glob", retEmptyList))
	fs.MapSet("list", fn("extra.fs.list", retEmptyList))
	for _, name := range []string{"writeFile", "removeAll", "makeDirectory"} {
		fs.MapSet(name, fn("extra.fs."+name, func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
			rec.addOp("exec", "fs."+name, "")
			return buzz.Null, nil
		}))
	}
	extra.MapSet("fs", fs)

	vcs := buzz.NewMap()
	vcs.MapSet("shortHash", fn("extra.vcs.shortHash", retStr("0000000")))
	vcs.MapSet("branch", fn("extra.vcs.branch", retStr("main")))
	extra.MapSet("vcs", vcs)

	env := buzz.NewMap()
	env.MapSet("get", fn("extra.env.get", retStr("")))
	extra.MapSet("env", env)

	return extra
}

func retBool(b bool) func(context.Context, []buzz.Value) (buzz.Value, error) {
	return func(context.Context, []buzz.Value) (buzz.Value, error) { return buzz.BoolValue(b), nil }
}
func retStr(s string) func(context.Context, []buzz.Value) (buzz.Value, error) {
	return func(context.Context, []buzz.Value) (buzz.Value, error) { return buzz.StrValue(s), nil }
}
func retEmptyList(context.Context, []buzz.Value) (buzz.Value, error) {
	return buzz.ListValue(nil), nil
}

func strsToList(ss []string) buzz.Value {
	items := make([]buzz.Value, len(ss))
	for i, s := range ss {
		items[i] = buzz.StrValue(s)
	}
	return buzz.ListValue(items)
}

// valToStrings reads a Buzz str or [str] into a Go slice.
func valToStrings(v buzz.Value) []string {
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
func strArg(args []buzz.Value, i int, fallback string) string {
	if i < len(args) && args[i].IsStr() {
		return args[i].AsString()
	}
	return fallback
}

func spellArgsDetail(args []buzz.Value) string {
	if len(args) == 0 || !args[0].IsMap() {
		return ""
	}
	if av, ok := args[0].MapGet("args"); ok {
		return strings.Join(valToStrings(av), " ")
	}
	return ""
}

func execDetail(args []buzz.Value) string {
	if len(args) >= 2 {
		return strings.Join(valToStrings(args[1]), " ")
	}
	return ""
}

// normalizeTarget maps an export name (or a depends_on argument) to its
// canonical kebab-case target key: regen_pgo -> regen-pgo, goBuild -> go-build.
func normalizeTarget(name string) string {
	var b strings.Builder
	for i, r := range name {
		switch {
		case r == '_' || r == ' ':
			b.WriteByte('-')
		case r >= 'A' && r <= 'Z':
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r - 'A' + 'a')
		default:
			b.WriteRune(r)
		}
	}
	return strings.ToLower(b.String())
}
