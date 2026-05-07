package bindings

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/engine"
	"github.com/egladman/magus/internal/interp/engine/lua"
	"github.com/egladman/magus/internal/interp/engine/lua/teal"
	ispell "github.com/egladman/magus/internal/spell"
	luagen "github.com/egladman/magus/internal/std/gen/lua"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

func init() {
	// Lazy-register so fast subcommands (help, version) skip the registration loop entirely.
	project.DefaultSpellRegistry().SetEnsureHook(ensureSpellsRegistered)
}

var ensureSpellsRegistered = sync.OnceFunc(func() {
	for _, spec := range ispell.Builtins() {
		opts := []types.SpellOption{
			types.WithSources(spec.Needs...),
			types.WithClaims(spec.Claims...),
			types.WithSpellOutputs(spec.Provides...),
			types.WithTargets(spec.TargetNames()...),
			types.WithInvoker(newSpellInvoker(spec.Targets)),
			types.WithCommandRenderer(newCommandRenderer(spec.Targets)),
			types.WithTargetSources(spec.TargetNeeds),
			types.WithTargetCharms(charmNamesByTarget(spec.Targets)),
			types.WithTargetDocs(docsByTarget(spec.Targets)),
		}
		if spec.Opaque {
			opts = append(opts, types.WithForeignProcess())
		}
		if len(spec.VersionCmd) > 0 {
			opts = append(opts, types.WithVersionProbe(newVersionProbe(spec.VersionCmd)))
		}
		project.DefaultSpellRegistry().RegisterSpell(types.NewSpell(spec.Name, opts...))
	}
})

// charmNamesByTarget extracts the sorted charm names each target declares, for
// discovery surfaces like `magus describe`.
func charmNamesByTarget(targets map[string]ispell.Target) map[string][]string {
	out := make(map[string][]string, len(targets))
	for name, t := range targets {
		if len(t.Charms) == 0 {
			continue
		}
		names := make([]string, 0, len(t.Charms))
		for c := range t.Charms {
			names = append(names, c)
		}
		slices.Sort(names)
		out[name] = names
	}
	return out
}

// docsByTarget extracts each target handler's doc comment, for discovery surfaces
// like `magus describe`. Targets with no comment are omitted.
func docsByTarget(targets map[string]ispell.Target) map[string]string {
	out := make(map[string]string, len(targets))
	for name, t := range targets {
		if t.Doc != "" {
			out[name] = t.Doc
		}
	}
	return out
}

// newVersionProbe runs argv in the project dir and returns trimmed stdout.
func newVersionProbe(argv []string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, dir string) (string, error) {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("version probe %v in %s: %w", argv, dir, err)
		}
		return strings.TrimSpace(string(out)), nil
	}
}

// newCommandRenderer returns the fork-command preview used by `magus describe`:
// for a fork target it reports cmd plus the argv as reshaped by the active charms
// (the same resolveCharmArgs the runtime uses), executing nothing. A function-op
// target (Func set) or a no-op (empty Cmd) returns ok=false — there is no static
// command to show.
func newCommandRenderer(targets map[string]ispell.Target) func(string, []string) (string, []string, bool) {
	return func(target string, charms []string) (string, []string, bool) {
		tgt, ok := targets[target]
		if !ok || tgt.Func != "" || tgt.Cmd == "" {
			return "", nil, false
		}
		args, err := resolveCharmArgs(types.WithCharms(context.Background(), charms), tgt.Args, tgt.Charms)
		if err != nil {
			return "", nil, false
		}
		return tgt.Cmd, args, true
	}
}

// newSpellInvoker returns an invoker closure for a built-in spell. Every built-in
// operation is fork (data: cmd/args/charms), so dispatch forks the tool without
// any script VM; an undeclared or function-op target is a graceful no-op (built-ins
// have no script body to run a function-op against).
func newSpellInvoker(targets map[string]ispell.Target) func(context.Context, types.InvokeRequest) (any, error) {
	return func(ctx context.Context, req types.InvokeRequest) (any, error) {
		tgt, ok := targets[req.Target]
		if !ok || tgt.Func != "" {
			return nil, nil // function-op (no built-in body to dispatch); graceful no-op
		}
		_, err := runForkTarget(ctx, tgt, forkOpts{cwd: req.Dir, args: project.ExtraArgs(ctx)})
		return nil, err
	}
}

// newLuaSpellInvoker returns an invoker for a workspace-local Teal spell that
// carries in-VM function-ops. A fork target forks its command as usual; a Func
// target is dispatched in a fresh VM via callLuaSpellFunc. Unknown targets are a
// graceful no-op, matching the built-in and Buzz invokers. The Lua twin of
// newBuzzSpellInvoker.
func newLuaSpellInvoker(m ispell.Spec, luaCode string) func(context.Context, types.InvokeRequest) (any, error) {
	return func(ctx context.Context, req types.InvokeRequest) (any, error) {
		tgt, ok := m.Targets[req.Target]
		if !ok {
			return nil, nil
		}
		if tgt.Func != "" {
			return callLuaSpellFunc(ctx, luaCode, req.Target, req)
		}
		_, err := runForkTarget(ctx, tgt, forkOpts{cwd: req.Dir, args: project.ExtraArgs(ctx)})
		return nil, err
	}
}

// callLuaSpellFunc executes the compiled spell module in a fresh host-registered
// Lua VM and calls the named op's handler with the invocation's Target and an
// input callback, returning its result marshalled to a Go value. The handler is
// function(target, cb): it calls cb(io) with a table and reads the op's inputs the
// host writes into it (the cache passes {project, hash, dest/src} via req.Params) —
// the same typed contract a Buzz function-op uses. A fresh VM per call means the
// module body re-runs each invocation, so a function-op spell's module body must be
// idempotent. The Lua twin of callBuzzSpellFunc.
func callLuaSpellFunc(ctx context.Context, luaCode, opKey string, req types.InvokeRequest) (any, error) {
	r, err := interp.NewLuaSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("spell function-op %q: new vm: %w", opKey, err)
	}
	defer func() { _ = r.Close() }()
	teal.InstallUtf8Shim(r)
	if err := registerAll(r, false); err != nil {
		return nil, fmt.Errorf("spell function-op %q: host bindings: %w", opKey, err)
	}
	fn, err := r.LoadString(luaCode)
	if err != nil {
		return nil, fmt.Errorf("spell function-op %q: load: %w", opKey, err)
	}
	if err := r.Call(engine.CallParams{Fn: fn, NRet: 1, Protect: true}); err != nil {
		return nil, fmt.Errorf("spell function-op %q: exec: %w", opKey, err)
	}
	mod, ok := r.Get(-1).AsTable()
	r.Pop(1)
	if !ok {
		return nil, fmt.Errorf("spell function-op %q: module did not return a table", opKey)
	}
	ops, err := callLuaTable(r, mod.RawGetString("mgs_listTargets"))
	if err != nil {
		return nil, fmt.Errorf("spell function-op %q: mgs_listTargets: %w", opKey, err)
	}
	handler := ops.RawGetString(opKey)
	if _, ok := handler.AsFunction(); !ok {
		return nil, fmt.Errorf("spell function-op %q: op is not a function", opKey)
	}
	// cb delivers the op's inputs by copying req.Params into the table the handler
	// hands it (the handler sees the writes after cb returns).
	cb := r.NewFunction(func(_ context.Context, r lua.Session) int {
		if io, ok := r.Get(1).AsTable(); ok {
			for k, v := range req.Params {
				io.RawSetString(k, luagen.AnyToValue(r, v))
			}
		}
		return 0
	})
	if err := r.Call(engine.CallParams{Fn: handler, NRet: 1, Protect: true}, luaTargetValue(r, req), cb); err != nil {
		return nil, fmt.Errorf("spell function-op %q: %w", opKey, err)
	}
	rv := r.Get(-1)
	r.Pop(1)
	return luagen.ValueToAny(rv), nil
}

// callLuaTable calls fn (expected to return a single table) and returns it.
func callLuaTable(r lua.Session, fn engine.Value) (engine.Table, error) {
	if fn.IsNil() {
		return nil, fmt.Errorf("not a function")
	}
	if err := r.Call(engine.CallParams{Fn: fn, NRet: 1, Protect: true}); err != nil {
		return nil, err
	}
	t, ok := r.Get(-1).AsTable()
	r.Pop(1)
	if !ok {
		return nil, fmt.Errorf("did not return a table")
	}
	return t, nil
}

// luaTargetValue builds the Target table a spell handler receives as its first
// argument (the Lua twin of targetValue).
func luaTargetValue(r lua.Session, req types.InvokeRequest) engine.Value {
	t := r.NewTable()
	t.RawSetString("name", engine.StringValue(req.Target))
	t.RawSetString("projectPath", engine.StringValue(req.Dir))
	t.RawSetString("charms", r.NewTable())
	t.RawSetString("files", r.NewTable())
	return t
}

// spellOptsFromLua reads a per-target method's {cwd=, args={...}} options table
// from stack slot 1 (the method's sole argument).
func spellOptsFromLua(r lua.Session) (opts forkOpts) {
	tbl, ok := r.Get(1).AsTable()
	if !ok {
		return opts
	}
	if cv, ok := tbl.RawGetString("cwd").AsString(); ok {
		opts.cwd = cv
	}
	if argsTbl, ok := tbl.RawGetString("args").AsTable(); ok {
		opts.hasArgs = true
		for i := 1; i <= argsTbl.Len(); i++ {
			if s, ok := argsTbl.RawGetInt(i).AsString(); ok {
				opts.args = append(opts.args, s)
			}
		}
	}
	if envTbl, ok := tbl.RawGetString("env").AsTable(); ok {
		opts.env = map[string]string{}
		envTbl.ForEach(func(k, v engine.Value) {
			opts.env[k.String()] = v.String()
		})
	}
	if sv, ok := tbl.RawGetString("stdin").AsString(); ok {
		opts.stdin = sv
	}
	return opts
}

// registerLocalSpellSearcher installs a package.loaders searcher so that
// require("hello") in a magusfile resolves a workspace-local Teal spell at
// ./hello.tl, ./spells/hello.tl, or ./magusfiles/hello.tl — the runtime twin of
// the compile-time search path the Teal type-checker uses (see
// teal.configureModulePath). It is sugar for magus.spell.load(<resolved path>):
// the spell is compiled and registered, and require returns its { name } handle
// for spells = { ... } binding. Built-in spells (magus.spell.<name>) continue to
// resolve first via package.preload.
func registerLocalSpellSearcher(r lua.Session) {
	pkg, ok := r.GetGlobal("package").AsTable()
	if !ok {
		return
	}
	// gopher-lua's C-module loader errors hard ("package.cpath must be a string")
	// when cpath is unset, aborting require before later searchers run. We ship no
	// C modules, so blank it; require then falls through to our searcher.
	pkg.RawSetString("cpath", engine.StringValue(""))

	loaders, ok := pkg.RawGetString("loaders").AsTable()
	if !ok {
		return
	}
	searcher := r.NewFunction(func(ctx context.Context, r lua.Session) int {
		name := r.CheckString(1)
		rel := strings.ReplaceAll(name, ".", string(os.PathSeparator))
		// Resolve relative to the magusfile's own directory first, so a require in a
		// magusfile executed from outside its dir (workspace preload visiting a
		// sub-project) still finds its ./spells; fall back to cwd for run-from-here.
		// Each base dir is searched in two layouts: flat (<base>/<rel>.tl) and the
		// directory convention (<base>/<rel>/spell.tl — preferred, keeps a spell's
		// source together and easy to discover). rel may already carry "spells/"
		// (require("spells.hello")) or not (require("hello")), so both a bare and a
		// spells/-prefixed base are tried.
		var cands []string
		bases := []string{}
		if src := interp.SourceFromContext(ctx); src != nil && src.Dir != "" {
			bases = append(bases, src.Dir, filepath.Join(src.Dir, "spells"), filepath.Join(src.Dir, "magusfiles"))
		}
		bases = append(bases, "", "spells", "magusfiles")
		for _, base := range bases {
			cands = append(cands, filepath.Join(base, rel+".tl"), filepath.Join(base, rel, "spell.tl"))
		}
		var path string
		for _, cand := range cands {
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				path = cand
				break
			}
		}
		if path == "" {
			return 0 // not a local spell; require tries the next searcher
		}
		r.Push(r.NewFunction(func(ctx context.Context, r lua.Session) int {
			m, ok := loadLocalSpell(ctx, path)
			if !ok {
				// Raise rather than return nil: a nil-returning loader makes
				// require() cache package.loaded[name] = true, so a later
				// require of the same local spell silently returns the boolean
				// true instead of re-resolving. Raising propagates the failure
				// and leaves the module uncached, so the next require retries.
				r.RaiseError("magus.spell.load: %s", path)
				return 0
			}
			r.Push(newSpellHandle(r, m))
			return 1
		}))
		return 1
	})
	// Insert our searcher at the FRONT of package.loaders so a workspace-local
	// spell (./spells/<name>.tl) always resolves through it, before any default
	// searcher whose miss could poison require's module cache for the bare name.
	for i := loaders.Len(); i >= 1; i-- {
		loaders.RawSetInt(i+1, loaders.RawGetInt(i))
	}
	loaders.RawSetInt(1, searcher)
}

// registerSpells populates package.preload and magus.spell in the VM.
func registerSpells(r lua.Session) error {
	pkg, ok := r.GetGlobal("package").AsTable()
	if !ok {
		return nil
	}
	preload, ok := pkg.RawGetString("preload").AsTable()
	if !ok {
		return nil
	}

	spellTables := make(map[string]engine.Table)
	for _, spec := range ispell.Builtins() {
		// Built-in spells are pure data; build the handle table from spec —
		// name plus a Go-backed callable per fork op. The table is exposed via
		// require("magus.spell.<name>"), which type-checks to MagusSpell.
		tbl := r.NewTable()
		tbl.RawSetString("name", engine.StringValue(spec.Name))
		for name, tgt := range spec.Targets {
			if tgt.Func != "" {
				continue
			}
			bindForkTargetMethod(r, tbl, name, tgt)
		}
		bindListTargets(r, tbl, spec.Targets)
		spellTables[spec.Name] = tbl
		captured := tbl
		preload.RawSetString("magus.spell."+spec.Name, r.NewFunction(func(_ context.Context, r lua.Session) int {
			r.Push(captured)
			return 1
		}))
	}

	if magus, ok := r.GetGlobal("magus").AsTable(); ok {
		spellNS := r.NewTable()
		// get resolves a spell by name at runtime — use it for spells the typed
		// require form can't carry (host-registered spells like "magusfile", plugin
		// names computed at runtime). Returns the rich built-in table when available,
		// else a {name = ...} handle for any other registered spell, else nil. For
		// built-ins, prefer require("magus.spell.<name>") (compile-time checked).
		spellNS.RawSetString("get", r.NewFunction(func(_ context.Context, r lua.Session) int {
			name := r.CheckString(1)
			if tbl, found := spellTables[name]; found {
				r.Push(tbl)
				return 1
			}
			if _, ok := project.DefaultSpellRegistry().Lookup(name); ok {
				handle := r.NewTable()
				handle.RawSetString("name", engine.StringValue(name))
				r.Push(handle)
				return 1
			}
			r.Push(engine.NilValue)
			return 1
		}))
		spellNS.RawSetString("define", r.NewFunction(magusSpellNew))
		spellNS.RawSetString("load", r.NewFunction(magusSpellLoad))

		magus.RawSetString("spell", spellNS)
	}
	return nil
}

// magusSpellNew implements magus.spell.define(definition): a pure constructor.
// It decodes the definition to validate it and resolve needs()/provides(), then
// returns the definition as a MagusSpell handle. It registers nothing —
// registration happens when the handle is bound via magus.project.register.
// The resolved needs/provides are marshalled back onto the handle as data so the
// spell can be decoded by value at bind time, in whatever VM holds it.
func magusSpellNew(_ context.Context, r lua.Session) int {
	spec, ok := r.Get(1).AsTable()
	if !ok {
		slog.Error("magus.spell.define: definition must be a table")
		return 0
	}
	m, err := ispell.Decode(luaSpellObj{t: spec, rt: r})
	if err != nil {
		r.RaiseError("magus.spell.define: %v", err)
		return 0
	}
	spec.RawSetString("needs", stringSliceToTable(r, m.Needs))
	spec.RawSetString("provides", stringSliceToTable(r, m.Provides))
	r.Push(spec)
	return 1
}

// forkTargetNames returns the spell's fork (forkable) target names, sorted.
func forkTargetNames(targets map[string]ispell.Target) []string {
	names := make([]string, 0, len(targets))
	for name, tgt := range targets {
		if tgt.Func == "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// bindListTargets adds listTargets() to a spell handle: it returns the runnable
// target names, for introspection alongside the static per-target methods
// (hello.build()). Targets are invoked through those methods, not by name.
func bindListTargets(r lua.Session, tbl engine.Table, targets map[string]ispell.Target) {
	names := forkTargetNames(targets)
	tbl.RawSetString("listTargets", r.NewFunction(func(_ context.Context, r lua.Session) int {
		r.Push(stringSliceToTable(r, names))
		return 1
	}))
}

// bindForkTargetMethod attaches tgt as a callable method named target on tbl:
// calling spell.<target>(opts?) forks the target's command in opts.cwd
// (defaulting to ".") with opts.args appended. Shared by the built-in registry
// tables and the handles magus.spell.load returns.
func bindForkTargetMethod(r lua.Session, tbl engine.Table, target string, tgt ispell.Target) {
	tbl.RawSetString(target, r.NewFunction(func(ctx context.Context, r lua.Session) int {
		opts := spellOptsFromLua(r)
		res, err := runForkTarget(ctx, tgt, opts)
		if err != nil {
			r.RaiseError("spell %s: %s", target, err.Error())
			return 0
		}
		if tgt.Capture {
			pushExecRecord(r, res.Record())
			return 1
		}
		return 0
	}))
}

// pushExecRecord pushes the shared {stdout, stderr, code, ok} exec record onto
// the stack, marshalled the same way os.exec's record is (see run.ExecResult.Record
// and gen/lua.anyToValue): string/bool direct, int as a Lua number.
func pushExecRecord(r lua.Session, rec map[string]any) {
	t := r.NewTable()
	for k, v := range rec {
		switch x := v.(type) {
		case string:
			t.RawSetString(k, engine.StringValue(x))
		case bool:
			t.RawSetString(k, engine.BoolValue(x))
		case int:
			t.RawSetString(k, engine.NumberValue(float64(x)))
		}
	}
	r.Push(t)
}

// newSpellHandle builds the MagusSpell handle a magusfile receives for a
// registered workspace-local spell: its name plus a callable per fork target
// (spell.<target>(opts?)). Local spells are fork-only, so every target becomes
// such a method — making spell.targetName access work the same as for built-ins.
func newSpellHandle(r lua.Session, m ispell.Spec) engine.Table {
	h := r.NewTable()
	h.RawSetString("name", engine.StringValue(m.Name))
	// Marshal the resolved spell as data so magus.project.register can decode and
	// register the handle by value — needed because load evaluates the spell in a
	// throwaway VM whose functions are gone by bind time.
	h.RawSetString("needs", stringSliceToTable(r, m.Needs))
	h.RawSetString("provides", stringSliceToTable(r, m.Provides))
	h.RawSetString("claims", stringSliceToTable(r, m.Claims))
	h.RawSetString("version_cmd", stringSliceToTable(r, m.VersionCmd))
	h.RawSetString("opaque", engine.BoolValue(m.Opaque))
	h.RawSetString("ops", targetsToTable(r, m.Targets))
	for name, tgt := range m.Targets {
		if tgt.Func != "" {
			continue
		}
		bindForkTargetMethod(r, h, name, tgt)
	}
	bindListTargets(r, h, m.Targets)
	return h
}

// stringSliceToTable builds a Lua array table from ss (a Table is also a Value).
func stringSliceToTable(r lua.Session, ss []string) engine.Table {
	t := r.NewTable()
	for i, s := range ss {
		t.RawSetInt(i+1, engine.StringValue(s))
	}
	return t
}

// targetsToTable marshals resolved targets back to the nested ops table shape
// ispell.Decode reads (a fork target unless it declares fn).
func targetsToTable(r lua.Session, targets map[string]ispell.Target) engine.Table {
	ops := r.NewTable()
	for name, t := range targets {
		op := r.NewTable()
		if t.Func != "" {
			op.RawSetString("fn", engine.StringValue(t.Func))
		}
		if t.Cmd != "" {
			op.RawSetString("cmd", engine.StringValue(t.Cmd))
		}
		if len(t.Args) > 0 {
			op.RawSetString("args", stringSliceToTable(r, t.Args))
		}
		if len(t.Charms) > 0 {
			charms := r.NewTable()
			for cn, c := range t.Charms {
				ce := r.NewTable()
				ce.RawSetString("ops", patchOpsToTable(r, c.Ops))
				charms.RawSetString(cn, ce)
			}
			op.RawSetString("charms", charms)
		}
		ops.RawSetString(name, op)
	}
	return ops
}

// patchOpsToTable marshals a charm's RFC 6902 ops back to the array-of-records
// table shape ispell.Decode reads.
func patchOpsToTable(r lua.Session, ops []ispell.PatchOp) engine.Table {
	arr := r.NewTable()
	for i, po := range ops {
		t := r.NewTable()
		t.RawSetString("op", engine.StringValue(po.Op))
		t.RawSetString("path", engine.StringValue(po.Path))
		if po.Value != "" {
			t.RawSetString("value", engine.StringValue(po.Value))
		}
		if po.From != "" {
			t.RawSetString("from", engine.StringValue(po.From))
		}
		arr.RawSetInt(i+1, t)
	}
	return arr
}

// magusSpellLoad implements magus.spell.load(path): compile and register a local
// spell, returning a MagusSpell handle (name plus a method per fork target) so
// the spell can be bound via magus.project.register({spells = {...}}) and its
// targets invoked as spell.<target>(opts?).
func magusSpellLoad(ctx context.Context, r lua.Session) int {
	m, ok := loadLocalSpell(ctx, r.CheckString(1))
	if !ok {
		r.Push(engine.NilValue)
		return 1
	}
	r.Push(newSpellHandle(r, m))
	return 1
}

// loadLocalSpell compiles a workspace-local Teal spell at path and registers
// it, returning its spec and ok=false on any failure. Engine-agnostic (it
// evaluates the spell in a throwaway Lua runtime), so the Lua, Buzz and JS
// spell.load bindings all call it. Errors are logged, not raised: discovery
// paths cannot route a Lua error back to a pcall.
func loadLocalSpell(ctx context.Context, path string) (ispell.Spec, bool) {
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			slog.Error("magus.spell.load: getwd", "err", err)
			return ispell.Spec{}, false
		}
		path = filepath.Join(cwd, path)
	}
	if strings.HasSuffix(path, ".bzz") {
		return loadLocalBuzzSpell(ctx, path)
	}
	preamble, err := interp.TypeDecls()
	if err != nil {
		slog.Error("magus.spell.load: host type declarations", "err", err)
		return ispell.Spec{}, false
	}
	luaCode, err := interp.CompileTealFile(ctx, path, preamble)
	if err != nil {
		slog.Error("magus.spell.load: compile", "path", path, "err", err)
		return ispell.Spec{}, false
	}

	// Evaluate in a throwaway runtime so a faulty spell can't disturb the magusfile VM.
	fr, err := interp.NewLuaSession(ctx)
	if err != nil {
		slog.Error("magus.spell.load: new vm", "path", path, "err", err)
		return ispell.Spec{}, false
	}
	defer func() { _ = fr.Close() }()
	// Install the full host environment so a function-op spell whose module body
	// does `local http = require("magus.extra.http")` resolves it; a fork spell's
	// body requires nothing, so this is harmless for it.
	teal.InstallUtf8Shim(fr)
	if err := registerAll(fr, false); err != nil {
		slog.Error("magus.spell.load: host bindings", "path", path, "err", err)
		return ispell.Spec{}, false
	}

	fn, err := fr.LoadString(string(luaCode))
	if err != nil {
		slog.Error("magus.spell.load: load", "path", path, "err", err)
		return ispell.Spec{}, false
	}
	if err := fr.Call(engine.CallParams{Fn: fn, NRet: 1, Protect: true}); err != nil {
		slog.Error("magus.spell.load: exec", "path", path, "err", err)
		return ispell.Spec{}, false
	}
	spec, ok := fr.Get(-1).AsTable()
	fr.Pop(1)
	if !ok {
		slog.Error("magus.spell.load: spell source must return a table", "path", path)
		return ispell.Spec{}, false
	}

	// A spell module exposes its mgs_-prefixed functions; resolve them
	// to the definition data the shared decoder reads.
	def, err := resolveLua(fr, spec)
	if err != nil {
		slog.Error("magus.spell.load: spec", "path", path, "err", err)
		return ispell.Spec{}, false
	}
	m, err := ispell.Decode(luaSpellObj{t: def, rt: fr})
	if err != nil {
		slog.Error("magus.spell.load: decode", "path", path, "err", err)
		return ispell.Spec{}, false
	}
	// A function-op spell must register eagerly, capturing its compiled source, so
	// the in-VM invoker can re-dispatch it (the throwaway VM's functions are gone by
	// bind time). This mirrors the Buzz loadBuzzSpell path. A fork-only spell keeps
	// the deferred by-value registration: its handle (newSpellHandle) carries the
	// resolved spec and magus.project.register decodes it at bind time.
	if hasFunctionOp(m) {
		registerLocalLuaFnSpell(m, string(luaCode))
	}
	return m, true
}

// hasFunctionOp reports whether any of m's targets is an in-VM function-op (Func
// set) rather than a fork command.
func hasFunctionOp(m ispell.Spec) bool {
	for _, t := range m.Targets {
		if t.Func != "" {
			return true
		}
	}
	return false
}

// loadSpellFile loads a spell file as a function-op-capable SpellDriver and
// registers it — the in-package entry point the remote cache backend uses to
// resolve a backend selected by a file path. Both engines are supported: a .bzz
// spell loads through the Buzz path, a .tl spell through the Teal path (which
// registers a function-op spell eagerly, capturing its source for in-VM dispatch).
func loadSpellFile(ctx context.Context, path string) (types.SpellDriver, error) {
	switch {
	case strings.HasSuffix(path, ".bzz"):
		_, sp, err := loadBuzzSpell(ctx, path)
		if err != nil {
			return nil, err // explicit nil: don't return a typed-nil *types.Spell as a non-nil interface
		}
		return sp, nil
	case strings.HasSuffix(path, ".tl"):
		m, ok := loadLocalSpell(ctx, path)
		if !ok {
			return nil, fmt.Errorf("spell file %q: load failed", path)
		}
		sp, ok := project.DefaultSpellRegistry().Lookup(m.Name)
		if !ok {
			// A fork-only Teal spell is not eagerly registered; a function-op backend
			// (the only kind wired here) always is. Guide the author rather than
			// returning a confusing nil driver.
			return nil, fmt.Errorf("spell file %q: a function-op spell (with in-VM ops) is required for a cache backend", path)
		}
		return sp, nil
	default:
		return nil, fmt.Errorf("spell file %q: must be a .bzz or .tl spell", path)
	}
}

// loadLocalBuzzSpell compiles a workspace-local Buzz spell at path, returning its
// spec and ok=false on any failure. Extract routes through the same
// ispell.Decode the Teal path uses, so a .bzz workspace spell, a Teal spell,
// and a built-in are all read and validated identically. Errors are logged, not
// raised, since discovery paths cannot route an error back to the caller.
// Registration is deferred to magus.project.register; the handle the caller
// builds carries the resolved spec so it registers by value there.
func loadLocalBuzzSpell(ctx context.Context, path string) (ispell.Spec, bool) {
	// loadBuzzSpell registers the spell with the function-op invoker (capturing its
	// source), so an imported Buzz spell carries function-ops whether it is later
	// bound to a project or wired as the remote cache backend. project bind finds
	// it already registered and binds it by name.
	m, _, err := loadBuzzSpell(ctx, path)
	if err != nil {
		slog.Error("magus.spell.load: buzz", "path", path, "err", err)
		return ispell.Spec{}, false
	}
	return m, true
}

// resolveLua calls a spell module's mgs_ functions once and
// assembles the definition table the shared decoder reads (keyed by the decoder's
// field names). mgs_getName is required; the rest are optional. Mirrors the Buzz
// resolver so a Teal spell and a Buzz spell decode through the same path.
func resolveLua(r lua.Session, spec engine.Table) (engine.Table, error) {
	nameFn := spec.RawGetString("mgs_getName")
	if nameFn.IsNil() {
		return nil, fmt.Errorf("a spell module must define mgs_getName")
	}
	def := r.NewTable()
	nv, err := callLuaValue(r, nameFn)
	if err != nil {
		return nil, fmt.Errorf("mgs_getName: %w", err)
	}
	def.RawSetString("name", nv)

	// OptionalContract is the canonical list so this loop and the Buzz resolver
	// stay in lockstep.
	for _, f := range ispell.OptionalContract {
		fn := spec.RawGetString(f.Name)
		if fn.IsNil() {
			continue
		}
		var args []string
		if f.TakesDir {
			args = []string{""}
		}
		rv, err := callLuaValue(r, fn, args...)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		// ops gets one post-processing step (the twin of the Buzz resolver's
		// resolveOps): a function-valued op becomes a {fn} function-op record so the
		// shared decoder reads it as an in-VM handler. A record op passes through.
		if f.Field == "ops" {
			if ops, ok := rv.AsTable(); ok {
				rv = resolveLuaOps(r, ops)
			}
		}
		def.RawSetString(f.Field, rv)
	}
	return def, nil
}

// resolveLuaOps reduces a function-valued mgs_listTargets — the Lua/Teal twin of
// the Buzz resolver's resolveOps — into the records the shared decoder reads. A
// function-valued op becomes a {fn = <op-key>} record dispatched in-VM at invoke
// time (by re-indexing mgs_listTargets on the captured source; Lua function values
// carry no exported name like Buzz's FunName, so the op key is the handle).
// Record-shaped ops pass through untouched. Unlike Buzz, Teal has no fork-handler
// form — its fork op IS the record — and no doc-comment capture, so no "handler"
// flag is set (Teal function-ops are exempt from the doctor doc check). The doc
// exemption is a deliberate scope decision, not a hard limit: comments survive in
// Teal's AST but are dropped at codegen, so reliable capture would mean extending
// the vendored Teal compiler. See docs/engines.md (the engine-agnostic spell
// contract) for the rationale.
func resolveLuaOps(r lua.Session, ops engine.Table) engine.Table {
	out := r.NewTable()
	ops.ForEach(func(k, v engine.Value) {
		key, ok := k.AsString()
		if !ok {
			return
		}
		if _, isFn := v.AsFunction(); isFn {
			rec := r.NewTable()
			rec.RawSetString("fn", engine.StringValue(key))
			out.RawSetString(key, rec)
			return
		}
		out.RawSetString(key, v)
	})
	return out
}

// callLuaValue calls fn with string args and returns its single result value.
func callLuaValue(r lua.Session, fn engine.Value, args ...string) (engine.Value, error) {
	vals := make([]engine.Value, len(args))
	for i, a := range args {
		vals[i] = engine.StringValue(a)
	}
	if err := r.Call(engine.CallParams{Fn: fn, NRet: 1, Protect: true}, vals...); err != nil {
		return nil, err
	}
	res := r.Get(-1)
	r.Pop(1)
	return res, nil
}

// localSpellBaseOptions builds the SpellOptions common to every workspace-local
// spell registration (cache metadata, command renderer, charm/doc discovery),
// minus the invoker — each registration path supplies its own.
func localSpellBaseOptions(m ispell.Spec) []types.SpellOption {
	opts := []types.SpellOption{
		types.WithSources(m.Needs...),
		types.WithClaims(m.Claims...),
		types.WithSpellOutputs(m.Provides...),
		types.WithTargets(m.TargetNames()...),
		types.WithCommandRenderer(newCommandRenderer(m.Targets)),
		types.WithTargetCharms(charmNamesByTarget(m.Targets)),
		types.WithTargetDocs(docsByTarget(m.Targets)),
		// Empty for Teal locals (no comment-capture); populated for a Buzz handle.
		types.WithDocRequiredTargets(m.DocTargets...),
	}
	if m.Opaque {
		opts = append(opts, types.WithForeignProcess())
	}
	if len(m.VersionCmd) > 0 {
		opts = append(opts, types.WithVersionProbe(newVersionProbe(m.VersionCmd)))
	}
	return opts
}

// registerLocalSpell registers a decoded fork-only workspace-local spell into the
// default registry. Engine-agnostic — the shared ispell.Decode produces m for
// both the Teal and Buzz spell.load by-value paths — so this is the single
// deferred registration point (called at magus.project.register bind time). A
// function-op spell instead registers eagerly at load via registerLocalLuaFnSpell.
func registerLocalSpell(m ispell.Spec) {
	opts := append(localSpellBaseOptions(m), types.WithInvoker(newSpellInvoker(m.Targets)))
	project.DefaultSpellRegistry().RegisterIfAbsent(types.NewSpell(m.Name, opts...))
}

// registerLocalLuaFnSpell registers a workspace-local Teal spell that has in-VM
// function-ops, capturing its compiled source so newLuaSpellInvoker can re-dispatch
// each op at invoke time. The Lua twin of loadBuzzSpell's eager registration.
func registerLocalLuaFnSpell(m ispell.Spec, luaCode string) {
	opts := append(localSpellBaseOptions(m), types.WithInvoker(newLuaSpellInvoker(m, luaCode)))
	project.DefaultSpellRegistry().RegisterIfAbsent(types.NewSpell(m.Name, opts...))
}

// luaSpellObj adapts a Lua spell-definition table to ispell.Obj so the shared
// decoder reads it. needs()/provides() are invoked through rt (the live
// magusfile VM for inline define, or the throwaway VM for spell.load).
type luaSpellObj struct {
	t  engine.Table
	rt lua.Session
}

func (o luaSpellObj) Str(key string) (string, bool) { return o.t.RawGetString(key).AsString() }
func (o luaSpellObj) Bool(key string) bool          { return o.t.RawGetString(key).AsBool() }
func (o luaSpellObj) Strs(key string) []string      { return tableToStringSlice(o.t.RawGetString(key)) }

func (o luaSpellObj) Obj(key string) (ispell.Obj, bool) {
	t, ok := o.t.RawGetString(key).AsTable()
	if !ok {
		return nil, false
	}
	return luaSpellObj{t: t, rt: o.rt}, true
}

func (o luaSpellObj) Objs(key string) []ispell.Obj {
	t, ok := o.t.RawGetString(key).AsTable()
	if !ok {
		return nil
	}
	var out []ispell.Obj
	for i := 1; i <= t.Len(); i++ {
		if sub, ok := t.RawGetInt(i).AsTable(); ok {
			out = append(out, luaSpellObj{t: sub, rt: o.rt})
		}
	}
	return out
}

func (o luaSpellObj) Keys() []string {
	var keys []string
	o.t.ForEach(func(k, _ engine.Value) {
		if s, ok := k.AsString(); ok {
			keys = append(keys, s)
		}
	})
	return keys
}

func (o luaSpellObj) CallStrs(key string, args ...string) ([]string, error) {
	fn := o.t.RawGetString(key)
	if fn.IsNil() {
		return nil, nil
	}
	// A bound handle carries the resolved list (not a function); read it directly.
	if _, ok := fn.AsTable(); ok {
		return tableToStringSlice(fn), nil
	}
	vals := make([]engine.Value, len(args))
	for i, a := range args {
		vals[i] = engine.StringValue(a)
	}
	if err := o.rt.Call(engine.CallParams{Fn: fn, NRet: 1, Protect: true}, vals...); err != nil {
		return nil, err
	}
	res := o.rt.Get(-1)
	o.rt.Pop(1)
	return tableToStringSlice(res), nil
}
