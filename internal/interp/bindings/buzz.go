package bindings

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	buzzeng "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/ast"
	buzzstd "github.com/egladman/gopherbuzz/std"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/engine"
	buzzengine "github.com/egladman/magus/internal/interp/engine/buzz"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/run"
	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/internal/std"
	extracrypto "github.com/egladman/magus/internal/std/extra/crypto"
	extrahttp "github.com/egladman/magus/internal/std/extra/http"
	"github.com/egladman/magus/internal/std/gen/buzz"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

func init() {
	interp.RegisterBuzzHostBindings(registerAllBuzz)
}

// registerAllBuzz installs the magus.* host API into a Buzz session.
//
// These bindings (and the magus-bindings-gen-emitted ones in gen/buzz) are written
// directly against the concrete magus/gopherbuzz value system — NewMap, DirectValue,
// StrValue, and friends — rather than behind the generic engine.Value /
// engine.Session abstraction. That is deliberate, not a layering gap:
//
//   - Buzz is the only engine, so there is no second implementation to share
//     with and nothing for an interface to abstract over. A buzz-local interface
//     here would be a single-implementation wrapper over hundreds of
//     value-shaped call sites.
//   - magus/gopherbuzz is an intentionally public, standalone interpreter package;
//     binding against its real API exercises that public surface directly
//     instead of hiding it behind an internal seam.
//
// The generic engine.Session adapter (engine/buzz) still exists for the
// REPL/pry path; it is not used here.
func registerAllBuzz(ctx context.Context, sess *buzzeng.Session, targets map[string]buzzeng.Callable, parseMode bool) {
	magus := buzzeng.NewMap()
	magus.MapSet("project", buildProjectNS(ctx, sess))
	magus.MapSet("target", buildTargetNS(targets))
	magus.MapSet("spell", buildSpellNS(ctx))
	magus.MapSet("cache", buildCacheNS(ctx))
	// magus.needs(...): the one dependency primitive. Each argument is a Target handle
	// from magus.target.literal/glob/regex; the matched targets are awaited via
	// the Buzz VM pool (cross-project handles dispatch through CrossDispatch).
	magus.MapSet("needs", buzzeng.DirectValue("magus.needs", buildBuzzNeeds(targets)))
	// magus.has_charm(name): true when execution charm `name` is active — it lets a
	// function target branch on any charm carried in context, e.g. build:container
	// or the built-in rw (has_charm("rw")).
	magus.MapSet("has_charm", buzzeng.DirectValue("magus.has_charm", func(ctx context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		if len(args) == 0 || !args[0].IsStr() {
			return buzzeng.Null, nil
		}
		return buzzeng.BoolValue(types.HasCharm(ctx, args[0].AsString())), nil
	}))
	magus.MapSet("pry", buzzeng.DirectValue("magus.pry", buildBuzzPry(sess, parseMode)))

	// Logging on the magus namespace itself (magus.info/debug/warn/error): the one
	// way to log from a magusfile — there is no separate std log module. Each level
	// writes into the process slog logger via emitMagusLog.
	magus.MapSet("info", buzzeng.DirectValue("magus.info", buzzLogFn(slog.LevelInfo)))
	magus.MapSet("debug", buzzeng.DirectValue("magus.debug", buzzLogFn(slog.LevelDebug)))
	magus.MapSet("warn", buzzeng.DirectValue("magus.warn", buzzLogFn(slog.LevelWarn)))
	magus.MapSet("error", buzzeng.DirectValue("magus.error", buzzLogFn(slog.LevelError)))

	// magus.cmd runs the magus binary with args; it raises when the invocation
	// exits non-zero (parity with os.exec).
	magus.MapSet("cmd", buzzeng.DirectValue("magus.cmd", func(ctx context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		rec, err := std.MagusCmd(ctx, argStrSlice(args, 0))
		if err != nil {
			return buzzeng.Null, err
		}
		return execRecordToBuzz(rec), nil
	}))

	// magus.hint(msg): advisory nudge (see emitMagusHint) — non-fatal, deduped,
	// honors the hints toggle.
	magus.MapSet("hint", buzzeng.DirectValue("magus.hint", func(_ context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		if len(args) > 0 && args[0].IsStr() {
			emitMagusHint(args[0].AsString())
		}
		return buzzeng.Null, nil
	}))
	// magus.fatal(msg): log at error level, then abort with exit 1 via a typed
	// ExitError (the CLI/daemon map it to the exit status).
	magus.MapSet("fatal", buzzeng.DirectValue("magus.fatal", func(ctx context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		emitMagusLog(ctx, slog.LevelError, argStr(args, 0), nil)
		types.RecordExit(ctx, 1)
		return buzzeng.Null, types.ExitError{Code: 1}
	}))
	sess.SetGlobal("magus", magus)

	// The host utilities are reached under the same bare names as Buzz's own
	// stdlib: `import "os"`, `import "fs"`, `import "http"`, `import "vcs"`, … A
	// magusfile selects methods off each module directly (os.exec, fs.glob,
	// vcs.shortHash). registerHostModules layers the magus host methods onto
	// Buzz's stdlib modules — a superset surface — and is shared with
	// spell-loading, so a magusfile and a function-op spell see the same modules.
	registerHostModules(ctx, sess)
	// Built-in spells follow the same import idiom as std modules: each spell is
	// reachable as `import "magus/spell/<name>"`, binding the spell handle under
	// its basename. This mirrors require("magus.spell.<name>") in Teal.
	for _, spec := range ispell.Builtins() {
		sess.SetSyntheticModule("magus/spell/"+spec.Name, buzzSpellObject(spec.Name))
	}
	// Workspace-local spells are imported by path: `import "spells/hello"` resolves
	// ./spells/hello.buzz on demand and binds its handle under the basename
	// (hello). This is the import sugar for magus.spell.load on that path, and the
	// handle registers by value when bound via magus.project.register.
	// Cross-project target imports: `import "project/<path>" as <alias>` binds a
	// module whose members are the other project's targets as cross-project handles,
	// so `magus.needs(<alias>.<target>)` declares a target-level dependency across the
	// project boundary (a typo in the target name fails at load, not at run time).
	sess.SetModuleResolver(func(importPath string) (buzzeng.Value, bool) {
		if v, ok := resolveProjectImport(ctx, importPath); ok {
			return v, true
		}
		return resolveLocalSpellImport(ctx, importPath)
	})
}

// resolveProjectImport resolves `import "project/<path>"` to a module of the named
// project's targets, each a cross-project handle ({mode literal, pattern <target>,
// project <path>}) that magus.needs dispatches across the boundary. The path is
// dot-relative to the importing magusfile's directory (matching how the graph and
// runtime resolve cross deps). Target names are read statically by scanning the
// dependency's `export fun` declarations — no VM load, so no import-time recursion.
// Returns ok=false for any non-"project/" import.
func resolveProjectImport(ctx context.Context, importPath string) (buzzeng.Value, bool) {
	const prefix = "project/"
	if !strings.HasPrefix(importPath, prefix) {
		return buzzeng.Null, false
	}
	raw := strings.TrimPrefix(importPath, prefix)
	src := interp.SourceFromContext(ctx)
	if src == nil || raw == "" {
		return buzzeng.Null, false
	}
	depDir := filepath.Clean(filepath.Join(src.Dir, filepath.FromSlash(raw)))
	srcs, err := interp.FindAll(depDir)
	if err != nil {
		return buzzeng.Null, false
	}
	norm := types.DefaultTargetNameNormalizer.NormalizeTargetName
	m := buzzeng.NewMap()
	for _, s := range srcs {
		if s.Engine != "buzz" {
			continue
		}
		for _, f := range s.Files {
			b, rerr := os.ReadFile(f)
			if rerr != nil {
				continue
			}
			prog, perr := buzzeng.Parse(string(b))
			if perr != nil || prog == nil {
				continue
			}
			// Member key is the raw `export fun` identifier (so <alias>.build_playground
			// resolves); the handle's target is the kebab-normalized run name.
			for _, stmt := range prog.Stmts {
				fn, ok := stmt.(*ast.FunDecl)
				if !ok || !fn.IsExported {
					continue
				}
				h := buzzeng.NewMap()
				h.MapSet(buzzTargetHandleKey, buzzeng.BoolValue(true))
				h.MapSet("mode", buzzeng.StrValue("literal"))
				h.MapSet("pattern", buzzeng.StrValue(norm(fn.Name)))
				h.MapSet("project", buzzeng.StrValue(raw))
				m.MapSet(fn.Name, h)
			}
		}
	}
	return m, true
}

// resolveLocalSpellImport resolves a path-style import (e.g. "spells/hello") to a
// workspace-local spell at <importPath>.buzz, relative to the process cwd (the
// magusfile's directory at run time, matching magus.spell.load). It returns the
// spell handle and ok=true when a file exists and parses as a spell; otherwise
// ok=false, leaving the import to the normal file search.
func resolveLocalSpellImport(ctx context.Context, importPath string) (buzzeng.Value, bool) {
	// Resolve relative to the magusfile's own directory first, so a magusfile
	// imported from outside its dir (e.g. workspace preload visiting a sub-project)
	// still finds its ./spells; fall back to cwd for the run-from-here case.
	dirs := []string{}
	if src := interp.SourceFromContext(ctx); src != nil && src.Dir != "" {
		dirs = append(dirs, src.Dir)
	}
	dirs = append(dirs, "")
	for _, dir := range dirs {
		// Two layouts are accepted: a flat spells/<name>.buzz, and the directory
		// convention spells/<name>/spell.buzz (preferred — keeps a spell's source
		// and any future companion files together, easy to discover).
		for _, rel := range []string{importPath + ".buzz", filepath.Join(importPath, "spell.buzz")} {
			path := rel
			if dir != "" {
				path = filepath.Join(dir, rel)
			}
			if fi, err := os.Stat(path); err != nil || fi.IsDir() {
				continue
			}
			// loadLocalSpell absolutizes a relative path and registers the Buzz spell
			// with function-op support, so the returned handle's name resolves to a
			// function-op-capable spell whether it is bound to a project or wired as
			// the remote cache backend.
			if m, ok := loadLocalSpell(ctx, path); ok {
				return spellHandleFromMeta(m), true
			}
		}
	}
	return buzzeng.Null, false
}

// magusModules builds every magus host module (os/platform/fs/vcs/env/crypto/
// http/archive/charm/semver/yaml/…) via the magus-bindings-gen-emitted buzzgen
// trampolines, keyed by the bare import name each is exposed under. The closures
// capture sess so host callbacks (e.g. arg.index_func, os.with_env) can call back
// into the VM.
//
// The byte-level crypto (hmac, keyed base64) and http (download, upload_chunked,
// byteSize) companions are merged into their respective module maps so a script
// reaches a whole domain through one import — crypto.hmacSha256 and http.download
// sit alongside crypto.sha256Hex and http.get.
func magusModules(ctx context.Context, sess *buzzeng.Session) map[string]buzzeng.Value {
	cryptoNS := buzzgen.RegisterCrypto(ctx, sess)
	mergeModuleMap(cryptoNS, extracrypto.Register(ctx, sess))

	httpNS := buzzgen.RegisterHttp(ctx, sess)
	mergeModuleMap(httpNS, extrahttp.Register(ctx, sess))

	return map[string]buzzeng.Value{
		"os":       buzzgen.RegisterOs(ctx, sess),
		"platform": buzzgen.RegisterPlatform(ctx, sess),
		"fs":       buzzgen.RegisterFs(ctx, sess),
		"vcs":      buzzgen.RegisterVcs(ctx, sess),
		"archive":  buzzgen.RegisterArchive(ctx, sess),
		"crypto":   cryptoNS,
		"env":      buzzgen.RegisterEnv(ctx, sess),
		// json's parse/stringify duplicate Buzz's serialize.jsonDecode/jsonEncode,
		// but stringify_pretty (indented output) has no serialize equivalent.
		"json":     buzzgen.RegisterJson(ctx, sess),
		"http":     httpNS,
		"time":     buzzgen.RegisterTime(ctx, sess),
		"fmt":      buzzgen.RegisterFmt(ctx, sess),
		"markdown": buzzgen.RegisterMarkdown(ctx, sess),
		"charm":    buzzgen.RegisterCharm(ctx, sess),
		"encoding": buzzgen.RegisterEncoding(ctx, sess),
		"path":     buzzgen.RegisterPath(ctx, sess),
		"strings":  buzzgen.RegisterStrings(ctx, sess),
		"semver":   buzzgen.RegisterSemver(ctx, sess),
		"yaml":     buzzgen.RegisterYaml(ctx, sess),
	}
}

// mergeModuleMap copies all keys from src into dst. On a key both define, src
// wins — the order callers rely on when layering one module over another.
func mergeModuleMap(dst, src buzzeng.Value) {
	for _, k := range src.MapKeys() {
		if v, ok := src.MapGet(k); ok {
			dst.MapSet(k, v)
		}
	}
}

// registerHostModules installs the host module surface a Buzz session sees: Buzz's
// own stdlib under bare names (so a magusfile or spell may `import "std"` /
// `import "serialize"` / `import "io"`), with the magus host modules layered on top
// of those same bare names — `import "os"` carries Buzz's os plus os.exec/which/…,
// and modules Buzz's stdlib lacks (http, vcs, archive, env, time, …) become new
// bare imports. The result is one superset surface, no separate `magus/extra`
// aggregate. Shared by the magusfile binding path (registerAllBuzz) and the spell
// function-op path (callBuzzSpellFunc), so both surfaces stay in lock-step.
func registerHostModules(ctx context.Context, sess *buzzeng.Session) {
	buzzstd.Register(sess)
	for name, mod := range magusModules(ctx, sess) {
		if base, ok := sess.SyntheticModule(name); ok {
			// Buzz's stdlib already owns this bare name (os, fs, crypto): overlay
			// the magus methods onto it so callers see the union. magus wins on the
			// few shared keys (os.exit/os.sleep, fs.exists) — its forms are sandbox-
			// and context-aware where the bare stdlib is not.
			mergeModuleMap(base, mod)
		} else {
			sess.SetSyntheticModule(name, mod)
		}
	}
	// Canonical value types (Target/Charm/Strategy) as a flat-importable source
	// module, so a spell's mgs_listTargets can be typed {str: fun(Target, fun(any)) bool}
	// instead of `any`. Single source of truth lives in the spell package; the
	// built-in generator inlines the same source into each compiled built-in.
	sess.SetSourceModule(ispell.TargetModulePath, ispell.TargetModuleSource)
}

// buzzLogFn builds the Buzz trampoline for magus.<level>(msg, fields?). It routes
// through the shared emitMagusLog so every host log path formats identically.
func buzzLogFn(level slog.Level) func(context.Context, []buzzeng.Value) (buzzeng.Value, error) {
	return func(ctx context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		emitMagusLog(ctx, level, argStr(args, 0), argStrMap(args, 1))
		return buzzeng.Null, nil
	}
}

// MagusModuleKeys returns the member names of the magus.* module and its
// magus.target sub-module as the real Buzz bindings register them. It exists so the
// wasm playground (internal/playground), which keeps a SEPARATE recording
// implementation of this same surface, can diff against the source of truth in a
// guard test — the two host implementations must not silently drift.
func MagusModuleKeys() (top, target []string) {
	sess := buzzeng.NewSession(context.Background())
	registerAllBuzz(context.Background(), sess, map[string]buzzeng.Callable{}, true)
	magus := sess.GetGlobal("magus")
	top = magus.MapKeys()
	if t, ok := magus.MapGet("target"); ok {
		target = t.MapKeys()
	}
	return top, target
}

func buildProjectNS(ctx context.Context, sess *buzzeng.Session) buzzeng.Value {
	ns := buzzeng.NewMap()
	// magus.project.register takes a configurator function, mirroring the spell-op
	// shape `fun(p, cb) > bool`: the host hands it the project `p` and a sink `cb`,
	// and the function emits its options map via cb({...}) exactly once. Two forms:
	//
	//   register(fn)        — configures THIS project; its path comes from context
	//                         (the magusfile's own project), so it can't be wrong.
	//   register(path, fn)  — configures the project at an explicit workspace path
	//                         (the rare central/monorepo form, e.g. one magusfile
	//                         declaring several projects).
	ns.MapSet("register", buzzeng.DirectValue("magus.project.register", func(callCtx context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		if len(args) == 0 {
			return buzzeng.Null, nil
		}
		var path string
		var fn buzzeng.Value
		if args[0].IsStr() {
			path = args[0].AsString()
			if len(args) >= 2 {
				fn = args[1]
			}
		} else {
			fn = args[0]
			path, _ = interp.ProjectPathFromContext(ctx)
		}
		if !fn.IsFun() {
			return buzzeng.Null, fmt.Errorf(
				"magus.project.register expects a configurator function `fun(p, cb) { cb({...}); }`%s",
				registerMapHint(args[0]))
		}

		optsVal, err := recordProjectOpts(callCtx, sess, fn, path)
		if err != nil {
			return buzzeng.Null, err
		}
		opts, err := parseBuzzProjectOpts(ctx, optsVal)
		if err != nil {
			return buzzeng.Null, err
		}
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.RegisterProject(path, opts...)
		}
		return buzzeng.Null, nil
	}))
	return ns
}

// recordProjectOpts calls a register configurator once with the project handle p
// and a recording sink cb, returning the single options map it emits via cb({...}).
// Mirrors recordForkSpec: the function must call cb exactly once with a map. p
// carries the project's workspace path and name so a configurator may branch on
// them; the common case ignores it.
func recordProjectOpts(ctx context.Context, sess *buzzeng.Session, fn buzzeng.Value, path string) (buzzeng.Value, error) {
	captured := buzzeng.Null
	calls := 0
	cb := buzzeng.DirectValue("magus.cb", func(_ context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		calls++
		if calls > 1 {
			return buzzeng.Null, fmt.Errorf("magus.project.register: the configurator must call cb({...}) exactly once")
		}
		if len(args) > 0 {
			captured = args[0]
		}
		return buzzeng.Null, nil
	})
	p := buzzeng.NewMap()
	p.MapSet("path", buzzeng.StrValue(path))
	p.MapSet("name", buzzeng.StrValue(projectBaseName(path)))
	if _, err := sess.CallValue(ctx, fn, []buzzeng.Value{p, cb}); err != nil {
		return buzzeng.Null, err
	}
	if !captured.IsMap() {
		return buzzeng.Null, fmt.Errorf("magus.project.register: the configurator must call cb({...}) with an options map")
	}
	return captured, nil
}

// projectBaseName returns the last path segment of a workspace-relative project
// path ("magus/website" -> "website"), or the path itself when it has no separator.
func projectBaseName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// registerMapHint nudges a caller still passing the old declarative map toward the
// configurator form; empty for any other bad argument.
func registerMapHint(arg buzzeng.Value) string {
	if arg.IsMap() {
		return "; pass it inside the configurator instead: register(fun(p, cb) { cb({...}); })"
	}
	return ""
}

func parseBuzzProjectOpts(ctx context.Context, v buzzeng.Value) ([]workspace.ProjectOption, error) {
	if !v.IsMap() {
		return nil, nil
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
					return nil, fmt.Errorf("magus.project.register: spell %q: %w", name, err)
				}
				registerLocalSpell(m)
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
	// targets maps a target name to a per-target policy table: cachable=false opts
	// the target out of the cache; isolated=true serializes it against the batch.
	if tv, ok := v.MapGet("targets"); ok && tv.IsMap() {
		for _, name := range tv.MapKeys() {
			pv, ok := tv.MapGet(name)
			if !ok || !pv.IsMap() {
				continue
			}
			if cv, ok := pv.MapGet("cachable"); ok && !cv.Bool() {
				opts = append(opts, workspace.WithTarget(name, workspace.NoCache()))
			}
			if sv, ok := pv.MapGet("isolated"); ok && sv.Bool() {
				opts = append(opts, workspace.WithTarget(name, workspace.Isolated()))
			}
		}
	}
	return opts, nil
}

// spellHandleFromMeta builds the MagusSpell handle magus.spell.load returns. It
// marshals the resolved spec back as native data so magus.project.register can
// decode and register the spell by value at bind time — needed because load
// evaluates the spell in a throwaway session whose functions are gone by then.
func spellHandleFromMeta(m ispell.Spec) buzzeng.Value {
	h := buzzeng.NewMap()
	h.MapSet("name", buzzeng.StrValue(m.Name))
	h.MapSet("needs", strSliceToBuzzList(m.Needs))
	h.MapSet("provides", strSliceToBuzzList(m.Provides))
	h.MapSet("claims", strSliceToBuzzList(m.Claims))
	h.MapSet("version_cmd", strSliceToBuzzList(m.VersionCmd))
	h.MapSet("opaque", buzzeng.BoolValue(m.Opaque))
	h.MapSet("ops", targetsToBuzzMap(m.Targets))
	bindBuzzTargetDispatch(h, m.Targets)
	return h
}

// bindBuzzTargetDispatch wires a Buzz spell handle's runnable surface:
//
//   - spell.<target>(opts?) — a callable per fork target. This is the way to
//     invoke an op: docker.build({cwd: "..", args: ["-t", tag, "."]}), go.generate().
//   - listTargets() — returns the runnable target names, for introspection.
//
// A method's optional {cwd=, args=[...], env={...}} table appends opts.args to
// the target's base argv and overlays opts.env on the subprocess — so
// flag-carrying and cross-compile invocations need no os.exec. With no opts.args
// the `magus run <t> -- <extra>` args ride along via project.ExtraArgs.
func bindBuzzTargetDispatch(h buzzeng.Value, targets map[string]ispell.Target) {
	h.MapSet("listTargets", buzzeng.DirectValue("spell.listTargets", func(_ context.Context, _ []buzzeng.Value) (buzzeng.Value, error) {
		return strSliceToBuzzList(forkTargetNames(targets)), nil
	}))
	for name, tgt := range targets {
		if tgt.Func == "" {
			bindBuzzForkTargetMethod(h, name, tgt)
		}
	}
}

// bindBuzzForkTargetMethod attaches tgt as a callable method named target on h,
// so spell.<target>(opts?) forks the target.
func bindBuzzForkTargetMethod(h buzzeng.Value, target string, tgt ispell.Target) {
	h.MapSet(target, buzzeng.DirectValue("spell."+target, func(ctx context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		opts := spellOptsFromBuzz(args, 0)
		res, err := runBuzzForkTarget(ctx, tgt, opts)
		if err != nil {
			return buzzeng.Null, err
		}
		if tgt.Capture {
			return execRecordToBuzz(res.Record()), nil
		}
		return buzzeng.Null, nil
	}))
}

// execRecordToBuzz converts the shared {stdout, stderr, code, ok} exec record to
// a Buzz map, marshalled the same way os.exec's record is (see gen/buzz.bzAnyVal):
// string/bool direct, int as a Buzz int.
func execRecordToBuzz(rec map[string]any) buzzeng.Value {
	m := buzzeng.NewMap()
	for k, v := range rec {
		switch x := v.(type) {
		case string:
			m.MapSet(k, buzzeng.StrValue(x))
		case bool:
			m.MapSet(k, buzzeng.BoolValue(x))
		case int:
			m.MapSet(k, buzzeng.IntValue(int64(x)))
		}
	}
	return m
}

// runBuzzForkTarget forks tgt (opts.cwd defaults to the process cwd in
// runForkTarget). With explicit opts.args it uses them; otherwise it forwards the
// `magus run <t> -- <extra>` args, so a bare go.test() still threads through
// `magus run test -- -run X`.
func runBuzzForkTarget(ctx context.Context, tgt ispell.Target, opts forkOpts) (run.ExecResult, error) {
	if !opts.hasArgs {
		opts.args = project.ExtraArgs(ctx)
	}
	return runForkTarget(ctx, tgt, opts)
}

// spellOptsFromBuzz reads an optional {cwd=, args=[...], env={...}} options
// table at args[idx], the Buzz analogue of spellOptsFromLua. opts.hasArgs
// reports whether an "args" key was present, so callers know to fall back to
// project.ExtraArgs when it was not.
func spellOptsFromBuzz(args []buzzeng.Value, idx int) (opts forkOpts) {
	if idx >= len(args) || !args[idx].IsMap() {
		return opts
	}
	o := args[idx]
	if cv, ok := o.MapGet("cwd"); ok && cv.IsStr() {
		opts.cwd = cv.AsString()
	}
	if av, ok := o.MapGet("args"); ok {
		opts.args = buzzValToStringSlice(av)
		opts.hasArgs = true
	}
	if ev, ok := o.MapGet("env"); ok && ev.IsMap() {
		opts.env = map[string]string{}
		for _, k := range ev.MapKeys() {
			if v, ok := ev.MapGet(k); ok {
				opts.env[k] = v.AsString()
			}
		}
	}
	if sv, ok := o.MapGet("stdin"); ok && sv.IsStr() {
		opts.stdin = sv.AsString()
	}
	return opts
}

// targetsToBuzzMap marshals resolved targets back to the nested ops map shape
// ispell.Decode reads (a fork target unless it declares fn).
func targetsToBuzzMap(targets map[string]ispell.Target) buzzeng.Value {
	ops := buzzeng.NewMap()
	for name, t := range targets {
		op := buzzeng.NewMap()
		if t.Cmd != "" {
			op.MapSet("cmd", buzzeng.StrValue(t.Cmd))
		}
		if len(t.Args) > 0 {
			op.MapSet("args", strSliceToBuzzList(t.Args))
		}
		if len(t.Charms) > 0 {
			charms := buzzeng.NewMap()
			for cn, c := range t.Charms {
				ce := buzzeng.NewMap()
				ce.MapSet("ops", patchOpsToBuzzList(c.Ops))
				charms.MapSet(cn, ce)
			}
			op.MapSet("charms", charms)
		}
		ops.MapSet(name, op)
	}
	return ops
}

// patchOpsToBuzzList marshals a charm's RFC 6902 ops back to the array-of-records
// list shape ispell.Decode reads.
func patchOpsToBuzzList(ops []ispell.PatchOp) buzzeng.Value {
	items := make([]buzzeng.Value, len(ops))
	for i, po := range ops {
		m := buzzeng.NewMap()
		m.MapSet("op", buzzeng.StrValue(po.Op))
		m.MapSet("path", buzzeng.StrValue(po.Path))
		if po.Value != "" {
			m.MapSet("value", buzzeng.StrValue(po.Value))
		}
		if po.From != "" {
			m.MapSet("from", buzzeng.StrValue(po.From))
		}
		items[i] = m
	}
	return buzzeng.ListValue(items)
}

func buildTargetNS(targets map[string]buzzeng.Callable) buzzeng.Value {
	ns := buzzeng.NewMap()

	ns.MapSet("expand_globs", buzzeng.DirectValue("magus.target.expand_globs", func(_ context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		if len(args) == 0 {
			return buzzeng.ListValue(nil), nil
		}
		matched := matchBuzzTargets(targets, buzzValToStringSlice(args[0]))
		return strSliceToBuzzList(matched), nil
	}))

	// named/glob/regex return typed Target handles (a tagged map carrying the
	// matching mode and the literal pattern) consumed by magus.needs. The pattern
	// must be a string literal so the static extractor (internal/describe) can
	// recover the edge from source without evaluating the magusfile.
	ns.MapSet("literal", buzzeng.DirectValue("magus.target.literal", buildBuzzTargetHandle("literal")))
	ns.MapSet("glob", buzzeng.DirectValue("magus.target.glob", buildBuzzTargetHandle("glob")))
	ns.MapSet("regex", buzzeng.DirectValue("magus.target.regex", buildBuzzTargetHandle("regex")))

	return ns
}

func buildSpellNS(ctx context.Context) buzzeng.Value {
	ns := buzzeng.NewMap()

	// get resolves a spell by name at runtime — use it for spells the import form
	// can't carry (host-registered spells like "magusfile", plugin names computed
	// at runtime). For built-ins, prefer `import "magus/spell/<name>"`.
	ns.MapSet("get", buzzeng.DirectValue("magus.spell.get", func(_ context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		if len(args) == 0 || !args[0].IsStr() {
			return buzzeng.Null, nil
		}
		return buzzSpellObject(args[0].AsString()), nil
	}))

	ns.MapSet("load", buzzeng.DirectValue("magus.spell.load", func(_ context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		m, ok := loadLocalSpell(ctx, argStr(args, 0))
		if !ok {
			return buzzeng.Null, nil
		}
		return spellHandleFromMeta(m), nil
	}))

	return ns
}

// buildCacheNS assembles magus.cache for a magusfile. Today it exposes remote(),
// which wires an imported spell as the cross-shard remote cache backend:
//
//	import "spells/github/actions" as github
//	magus.cache.remote(github)
//
// The import already registered the spell (with function-op support, for a Buzz
// spell); remote() just records its name on the per-Open workspace registry, and
// magus.Open resolves it by name once the magusfile has been evaluated. The spell
// must expose get_artifact/put_artifact function-ops (and optionally enabled()).
func buildCacheNS(ctx context.Context) buzzeng.Value {
	ns := buzzeng.NewMap()
	ns.MapSet("remote", buzzeng.DirectValue("magus.cache.remote", func(_ context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		if len(args) == 0 || !args[0].IsMap() {
			return buzzeng.Null, fmt.Errorf("magus.cache.remote: expected an imported spell handle")
		}
		nv, ok := args[0].MapGet("name")
		if !ok || !nv.IsStr() || nv.AsString() == "" {
			return buzzeng.Null, fmt.Errorf("magus.cache.remote: argument is not a spell handle (no name)")
		}
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.SetRemoteBackend(nv.AsString())
		}
		return buzzeng.Null, nil
	}))
	return ns
}

// buzzSpellObject returns a spell handle map with the spell's full spec:
// name, needs, claims, provides, plus listTargets() and a callable per target.
func buzzSpellObject(name string) buzzeng.Value {
	m := buzzeng.NewMap()
	m.MapSet("name", buzzeng.StrValue(name))

	spec, ok := ispell.Builtins()[name]
	if !ok {
		return m
	}

	m.MapSet("needs", strSliceToBuzzList(spec.Needs))
	m.MapSet("claims", strSliceToBuzzList(spec.Claims))
	m.MapSet("provides", strSliceToBuzzList(spec.Provides))

	// listTargets() + a callable per fork target (go.test(), docker.build()).
	bindBuzzTargetDispatch(m, spec.Targets)

	return m
}

func strSliceToBuzzList(ss []string) buzzeng.Value {
	items := make([]buzzeng.Value, len(ss))
	for i, s := range ss {
		items[i] = buzzeng.StrValue(s)
	}
	return buzzeng.ListValue(items)
}

func buzzValToStringSlice(v buzzeng.Value) []string {
	switch {
	case v.IsStr():
		return []string{v.AsString()}
	case v.IsList():
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

// buzzTargetHandleKey tags the map returned by magus.target.literal/glob/regex so
// a handle is distinguishable from an ordinary value.
const buzzTargetHandleKey = "__magus_target"

// handleMode is the kind of target reference a magus.target.* handle carries.
type handleMode int

const (
	handleLiteral  handleMode = iota // an exact target name
	handleGlob                       // a glob over sibling target names
	handleRegex                      // a regexp over sibling target names
	handleExternal                   // a specific target in another project
)

// targetHandle is the decoded form of a magus.target.* handle. The Buzz handle map
// is read into this once, by decodeTargetHandle, so consumers (needs, resolution,
// cross-project dispatch) work with typed fields instead of re-parsing string keys.
type targetHandle struct {
	mode    handleMode
	value   string // the target name (literal/external) or the glob/regex pattern
	project string // cross-project (external) only: the other project's path, as written
}

// buildBuzzTargetHandle returns the magus.target.<mode> constructor for the pattern
// forms (glob/regex). The argument is a required string literal (the literal-first-
// arg discipline: the static extractor recovers the edge from source without running
// the VM).
func buildBuzzTargetHandle(mode string) func(context.Context, []buzzeng.Value) (buzzeng.Value, error) {
	return func(_ context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		if len(args) == 0 || !args[0].IsStr() {
			return buzzeng.Null, fmt.Errorf("magus.target.%s: argument must be a string literal", mode)
		}
		h := buzzeng.NewMap()
		h.MapSet(buzzTargetHandleKey, buzzeng.BoolValue(true))
		h.MapSet("mode", buzzeng.StrValue(mode))
		h.MapSet("pattern", buzzeng.StrValue(args[0].AsString()))
		return h, nil
	}
}

// decodeTargetHandle reads a magus.target.* handle map into a typed targetHandle,
// once. It is the single place that knows the handle's wire keys; ok is false for
// any value that isn't a tagged handle with a known mode. A literal handle carrying
// a project is a cross-project (external) reference.
func decodeTargetHandle(v buzzeng.Value) (targetHandle, bool) {
	if !v.IsMap() {
		return targetHandle{}, false
	}
	if tag, ok := v.MapGet(buzzTargetHandleKey); !ok || !tag.Bool() {
		return targetHandle{}, false
	}
	modeStr := ""
	if m, ok := v.MapGet("mode"); ok && m.IsStr() {
		modeStr = m.AsString()
	}
	var h targetHandle
	switch modeStr {
	case "literal":
		h.mode = handleLiteral
	case "glob":
		h.mode = handleGlob
	case "regex":
		h.mode = handleRegex
	default:
		return targetHandle{}, false
	}
	if p, ok := v.MapGet("pattern"); ok && p.IsStr() {
		h.value = p.AsString()
	}
	if h.mode == handleLiteral {
		if pr, ok := v.MapGet("project"); ok && pr.IsStr() && pr.AsString() != "" {
			h.mode = handleExternal
			h.project = pr.AsString()
		}
	}
	return h, true
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
func dispatchBuzzExternal(ctx context.Context, h targetHandle) error {
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
	depPath, err := file.Resolve(h.project, filepath.ToSlash(callerRel))
	if err != nil {
		return err
	}
	dep := ws.Get(depPath)
	if dep == nil {
		return fmt.Errorf("magus: cross-project dependency: unknown project %q", depPath)
	}
	target := strings.ToLower(h.value)
	lim := cache.LimiterFromContext(ctx)
	return proc.RunChildSync(ctx, lim, func() error {
		return cd.Dispatch(cache.WithoutSlotHeld(ctx), dep.Dir, target)
	})
}

// resolveTargetHandle expands a decoded same-project handle to matching target
// names: literal is an exact (lowercased) name, glob matches via matchBuzzTargets,
// regex matches registered names against the compiled pattern. External handles
// are dispatched separately and are not valid here.
func resolveTargetHandle(targets map[string]buzzeng.Callable, h targetHandle) ([]string, error) {
	switch h.mode {
	case handleLiteral:
		return []string{strings.ToLower(h.value)}, nil
	case handleGlob:
		return matchBuzzTargets(targets, []string{h.value}), nil
	case handleRegex:
		re, err := regexp.Compile(h.value)
		if err != nil {
			return nil, fmt.Errorf("target.regex %q: %w", h.value, err)
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
		return nil, fmt.Errorf("target handle: not a same-project handle")
	}
}

// buildBuzzNeeds returns magus.needs(...), the one dependency primitive. Every
// argument must be a Target handle from magus.target.literal/glob/regex —
// bare strings and lists are not accepted, so a dependency is always a typed,
// statically-recoverable edge. Same-project handles resolve to target names awaited
// through the VM pool / TargetMemo path (dispatchBuzzDeps); an external handle
// dispatches cross-project via CrossDispatch.
func buildBuzzNeeds(targets map[string]buzzeng.Callable) func(context.Context, []buzzeng.Value) (buzzeng.Value, error) {
	return func(callCtx context.Context, args []buzzeng.Value) (buzzeng.Value, error) {
		var names []string
		for _, arg := range args {
			h, ok := decodeTargetHandle(arg)
			if !ok {
				return buzzeng.Null, fmt.Errorf("magus.needs: each argument must be a magus.target.* handle (literal/glob/regex)")
			}
			if h.mode == handleExternal {
				if err := dispatchBuzzExternal(callCtx, h); err != nil {
					return buzzeng.Null, fmt.Errorf("magus.needs: %w", err)
				}
				continue
			}
			resolved, err := resolveTargetHandle(targets, h)
			if err != nil {
				return buzzeng.Null, fmt.Errorf("magus.needs: %w", err)
			}
			names = append(names, resolved...)
		}
		if err := dispatchBuzzDeps(callCtx, targets, names); err != nil {
			return buzzeng.Null, fmt.Errorf("magus.needs: %w", err)
		}
		return buzzeng.Null, nil
	}
}

// dispatchBuzzDeps awaits the named same-project targets: via the Buzz VM pool
// when one is in ctx (parallel, TargetMemo-deduped), else inline sequential. It
// returns unprefixed errors so each caller attaches its own verb name.
func dispatchBuzzDeps(callCtx context.Context, targets map[string]buzzeng.Callable, names []string) error {
	if len(names) == 0 {
		return nil
	}
	names = dedupStrings(names)
	if src := interp.SourceFromContext(callCtx); src != nil {
		if reg := buzzeng.PoolRegistryFromContext(callCtx); reg != nil {
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
func buzzDispatchViaPool(ctx context.Context, p *buzzeng.Pool, names []string) error {
	lim := cache.LimiterFromContext(ctx)
	ancestors := buzzeng.AncestorsFromContext(ctx)
	return proc.RunChildSync(ctx, lim, func() error {
		childCtx := cache.WithoutSlotHeld(ctx)
		return p.Dispatch(childCtx, names, ancestors)
	})
}

// matchBuzzTargets matches registered Buzz target names against glob/suffix patterns.
// Patterns without "*" match as suffix shorthand: "build" → ".*-build".
// Patterns with "*" are translated to regexps ("*" → ".*", anchored).
func matchBuzzTargets(targets map[string]buzzeng.Callable, patterns []string) []string {
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

// buildBuzzPry returns the magus.pry() direct callable for a Buzz session. It suspends
// execution at the call site and opens the shared Pry REPL on the running
// session, with stack introspection and (via the VM step hook) .step/.next/
// .finish. In parse mode (target enumeration) it is a no-op so loading a
// magusfile.buzz never blocks on a breakpoint.
func buildBuzzPry(sess *buzzeng.Session, parseMode bool) buzzeng.Callable {
	if parseMode {
		return func(_ context.Context, _ []buzzeng.Value) (buzzeng.Value, error) {
			return buzzeng.Null, nil
		}
	}
	return func(ctx context.Context, _ []buzzeng.Value) (buzzeng.Value, error) {
		esess := buzzengine.Wrap(sess)
		opts := interp.ReplOptions{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}

		resume, err := interp.Pry(ctx, esess, buzzPryContext(esess), opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "magus.pry: %v\n", err)
			return buzzeng.Null, nil
		}
		if resume == interp.ResumeContinue {
			return buzzeng.Null, nil
		}
		buzzInstallStepHook(ctx, esess, resume, opts)
		return buzzeng.Null, nil
	}
}

// buzzPryContext builds the REPL's PryContext from the session's current call
// stack (innermost frame first).
func buzzPryContext(esess engine.Session) interp.PryContext {
	pctx := interp.PryContext{}
	dbg, ok := esess.(engine.DebugReader)
	if !ok {
		return pctx
	}
	frames := dbg.Frames()
	pctx.Frames = frames
	if len(frames) > 0 {
		pctx.File = frames[0].Source
		pctx.Line = frames[0].CurrentLine
		pctx.Func = frames[0].Name
	}
	return pctx
}

// buzzInstallStepHook arms a one-shot line hook that re-enters the Pry REPL when
// execution reaches the next line selected by mode (step into / over / finish),
// then resumes. The hook keys purely off
// line events and call depth: step stops at any depth, next at the start depth
// or shallower, finish strictly shallower (the current frame has returned).
func buzzInstallStepHook(ctx context.Context, esess engine.Session, resume interp.PryResume, opts interp.ReplOptions) {
	stepper, ok := esess.(engine.Stepper)
	if !ok {
		fmt.Fprintln(os.Stdout, "(stepping not supported on this engine — resuming)")
		return
	}
	dbg, _ := esess.(engine.DebugReader)
	depthNow := func() int {
		if dbg != nil {
			return dbg.CallDepth()
		}
		return 0
	}

	startDepth := depthNow()
	mode := resume
	var hook func(engine.StepEvent, engine.Frame)
	hook = func(ev engine.StepEvent, _ engine.Frame) {
		if ev != engine.StepLine {
			return
		}
		cur := depthNow()
		switch mode {
		case interp.ResumeStep: // step into: stop at the next line, any depth
		case interp.ResumeNext: // step over: skip lines in deeper (called) frames
			if cur > startDepth {
				return
			}
		case interp.ResumeFinish: // run until the current frame returns
			if cur >= startDepth {
				return
			}
		}
		// Suspend stepping while the REPL is open so nested evals don't re-fire.
		stepper.ClearStepHook()
		next, err := interp.Pry(ctx, esess, buzzPryContext(esess), opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "magus.pry: %v\n", err)
			return
		}
		if next == interp.ResumeContinue {
			return // leave hook cleared: run to completion
		}
		mode = next
		startDepth = depthNow()
		stepper.SetStepHook(engine.MaskLine, hook)
	}
	stepper.SetStepHook(engine.MaskLine, hook)
}
