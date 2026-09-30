package bindings

import (
	"cmp"
	"context"
	"io"
	"log/slog"
	"os"
	"slices"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/spell"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
)

// Module labels, continuing gopherbuzz's origin classification (see
// gopherbuzz/module.go, upstream/gopherbuzz) into magus's own third origin.
// labelMagus marks a module that originates in magus; labelWASM additionally
// marks one safe in the browser playground (pure compute, no
// filesystem/process/network/OS randomness).
const (
	labelMagus = "magus"
	labelWASM  = "wasm"
)

// magusModules expresses magus's own modules as buzz.Modules: each wraps its
// generated register trampoline in a Bind that builds the module map (plus any
// byte-level companions) and layers it onto the stdlib module of the same name,
// or installs it fresh when Buzz has no such module. Ordered by name so the bind
// sequence is deterministic.
func magusModules(modules ffi.Set) []buzz.Module {
	mods := hostModuleBinds(modules)
	// Buzz-implemented modules bind through the SAME list, so a session sees one
	// surface and nothing downstream can tell which language implemented what.
	// They need no trampoline and no native value: SetModuleDecls on a path with no
	// native module is executed by resolveImport, which is how magus/spell and
	// upstream's own assert/suite/testing already ship.
	for _, sm := range std.AllSource() {
		mods = append(mods, buzz.Module{
			Name: sm.ImportPath(),
			// No WASM label: a Buzz module may import any host module, and whether
			// its imports are browser-safe is not knowable from here. Marking one
			// WASM-capable is a decision for whoever can vouch for its imports.
			Labels: []string{labelMagus},
			Bind: func(s *buzz.Session, _ buzz.ModuleEnv) error {
				s.SetModuleDecls(sm.ImportPath(), sm.Source)
				return nil
			},
		})
	}
	return mods
}

// hostModuleBinds is the native half of magusModules: one buzz.Module per registry
// entry, ordered by name. The MCP client surface uses it alone, because the
// Buzz-implemented spell modules import the wider host and do not belong there.
func hostModuleBinds(modules ffi.Set) []buzz.Module {
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	slices.Sort(names)

	mods := make([]buzz.Module, 0, len(names))
	for _, name := range names {
		reg := modules[name]
		labels := []string{labelMagus}
		if reg.Capabilities.Has(ffi.WASM) {
			labels = append(labels, labelWASM)
		}
		// The registry key is the identifier the module BINDS as; reg.Path is what
		// an import line spells when the two differ (`json` bound from
		// `encoding/json`). Buzz resolves a native module by full path and defines
		// it under the basename, so registering at the path is what makes
		// `import "encoding/json"` resolve while call sites keep writing `json\parse`.
		importPath := name
		if reg.Path != "" {
			importPath = reg.Path
		}
		mods = append(mods, buzz.Module{
			Name:   importPath,
			Labels: labels,
			Bind: func(s *buzz.Session, env buzz.ModuleEnv) error {
				mod := reg.Register(env.Ctx, s)
				// http keeps two byte-level companions that no descriptor can yet
				// declare: byteSize and upload_chunked. crypto no longer needs any:
				// its HMAC and base64 methods became declared std.Module methods once
				// TypeByteSlice existed, and this hook shrank by one domain as a result.
				if name == "http" {
					mergeModuleMap(mod, registerHTTPBytes())
				}
				// Layer this module's DECLARATIONS on as a source companion: the same
				// "native value + declaration source under one import path" mechanism
				// crypto/io already use for their own signatures (see
				// gopherbuzz/session.go's resolveImport). They carry the return-type
				// mirrors and an extern per method, so every call through this module
				// types against a real signature instead of Unknown.
				// Read the declarations by NAME (moduledecls writes one flat file per
				// module, gen/decls/json.buzz) but register them under the IMPORT
				// PATH, because resolveImport looks them up by the path the import
				// line spelled.
				if src, ok := spell.ModuleDecls(name); ok {
					s.SetModuleDecls(importPath, src)
				}
				// Buzz's stdlib may already own this bare name (os, fs, crypto):
				// overlay the magus methods onto it so callers see the union (magus
				// wins on the few shared keys, e.g. os.exit/fs.exists, its forms
				// being sandbox- and context-aware). Otherwise install fresh.
				if base, ok := s.NativeModule(importPath); ok {
					mergeModuleMap(base, mod)
				} else {
					s.SetNativeModule(importPath, mod)
				}
				return nil
			},
		})
	}
	return mods
}

// pureStdlib names the Buzz stdlib modules both MCP Buzz tools provide: pure
// compute, with no filesystem, process, reflection, FFI or test harness.
var pureStdlib = []string{"std", "math", "crypto", "serialize", "buffer"}

// PureStdlib splits the Buzz stdlib for the MCP Buzz tools. provide is what both
// install. withheld is every other stdlib module name, which each tool refuses
// with its own reason rather than letting it read as a missing file import.
func PureStdlib() (provide []buzz.Module, withheld []string) {
	for _, module := range buzzstd.Modules {
		if slices.Contains(pureStdlib, module.Name) {
			provide = append(provide, module)
		} else {
			withheld = append(withheld, module.Name)
		}
	}
	return provide, withheld
}

// clientWithheld are the magus\ members the MCP client does not offer: cmd runs an
// arbitrary magus command line, and pry reads stdin and writes stdout, which in the
// worker are the request and response channels.
var clientWithheld = []string{"cmd", "pry"}

// clientHostModules is the host-module set the MCP client tool provides: every
// module marked WASM (pure compute) except env, which reads the process environment.
func clientHostModules() ffi.Set {
	out := make(ffi.Set, len(bindinggen.Modules))
	for name, reg := range bindinggen.Modules {
		if name == "env" || !reg.Capabilities.Has(ffi.WASM) {
			continue
		}
		out[name] = reg
	}
	return out
}

// ClientDeniedImportPaths are the import paths the MCP client tool refuses, with
// a reason, rather than leaving them to read as a missing file import: every host
// and stdlib module outside magus\, PureStdlib and clientHostModules.
func ClientDeniedImportPaths() []string {
	allowed := map[string]bool{"magus": true}
	provide, withheld := PureStdlib()
	for _, module := range provide {
		allowed[module.Name] = true
	}
	for name, reg := range clientHostModules() {
		allowed[cmp.Or(reg.Path, name)] = true
	}
	var denied []string
	for name, reg := range bindinggen.Modules {
		if path := cmp.Or(reg.Path, name); !allowed[path] {
			denied = append(denied, path)
		}
	}
	for _, name := range withheld {
		if !allowed[name] {
			denied = append(denied, name)
		}
	}
	slices.Sort(denied)
	return slices.Compact(denied)
}

// InstallClient binds the MCP client surface into sess: magus\ without the
// clientWithheld members, PureStdlib, and clientHostModules. Nothing that touches
// the filesystem, process, network or environment is installed. out receives
// std.print.
func InstallClient(ctx context.Context, sess *buzz.Session, out io.Writer) error {
	env := buzz.ModuleEnv{Ctx: ctx, Out: out}
	stdlib, _ := PureStdlib()
	if err := sess.Provide(env, stdlib...); err != nil {
		return err
	}
	if err := sess.Provide(env, hostModuleBinds(clientHostModules())...); err != nil {
		return err
	}
	sess.SetNativeModule("magus", assembleMagus(ctx, sess, interp.NewHostCallObserver(ctx), false, scriptSurface, clientWithheld...))
	spell.DeclareMagusTypes(sess, func(member string) bool { return !slices.Contains(clientWithheld, member) })
	return nil
}

// mergeModuleMap copies all keys from src into dst. On a key both define, src
// wins — the order callers rely on when layering one module over another.
func mergeModuleMap(dst, src vm.Value) {
	for _, k := range src.MapKeys() {
		if v, ok := src.MapGet(k); ok {
			dst.MapSet(k, v)
		}
	}
}

// moduleSurfaceConfig is the resolved options for one RegisterModuleSurface call.
type moduleSurfaceConfig struct {
	modules ffi.Set
	// scriptOut is where std.print goes. Defaults to STDERR: under `magus run` a
	// magusfile's print is human output like every other thing magus says, and
	// stdout carries the structured answer (-o json|yaml|jsonl|template) alone. A
	// magusfile with a print in it would otherwise emit a document no parser
	// accepts. `magus buzz` overrides it: there the script IS the program, so its
	// output is the command's output.
	scriptOut io.Writer
}

// ModuleSurfaceOption configures one registration of the host module surface.
type ModuleSurfaceOption func(*moduleSurfaceConfig)

// WithModules replaces the default host-module set for one session. It is the
// test seam for a fake fs/http/vcs module; callers use registry.Modules.With to
// replace only the capability they need without mutating global state.
func WithModules(modules ffi.Set) ModuleSurfaceOption {
	return func(c *moduleSurfaceConfig) { c.modules = modules }
}

// WithScriptOutput sends std.print to w instead of the default stderr. `magus buzz`
// passes stdout: it runs a script as a program, so the script's output is the
// command's output rather than commentary alongside one.
func WithScriptOutput(w io.Writer) ModuleSurfaceOption {
	return func(c *moduleSurfaceConfig) { c.scriptOut = w }
}

// RegisterModuleSurface installs the shared Buzz module surface: Buzz's own
// stdlib, the magus testing extensions (assert/suite), and every magus module
// (hostreg.Modules) layered on top of the same bare names. It is the full surface
// a standalone script sees, shared by the magusfile engine (which then adds the
// magus.* namespace and the Target/Charm source types on top) and the `magus buzz`
// runner, so the two never drift.
func RegisterModuleSurface(ctx context.Context, sess *buzz.Session, opts ...ModuleSurfaceOption) {
	cfg := moduleSurfaceConfig{modules: bindinggen.Modules, scriptOut: os.Stderr}
	for _, opt := range opts {
		opt(&cfg)
	}
	// Buzz's stdlib provides the base modules; the magus modules then layer onto
	// the same bare names (their Bind reads back and merges) or install fresh. One
	// registration path: gopherbuzz's stdlib and magus's own modules are both
	// buzz.Modules applied through Session.Provide.
	//
	// A print goes to the captured stdout on ctx when there is one: a target's print is
	// its output, withheld, streamed and stored under its ref like a subprocess's.
	env := buzz.ModuleEnv{Ctx: ctx, OutFunc: func(ctx context.Context) io.Writer {
		if stdout, _, ok := run.CapturedOutput(ctx); ok {
			return stdout
		}
		return cfg.scriptOut
	}}
	_ = sess.Provide(env, buzzstd.Modules...)
	guardUpstreamStdlib(sess)
	_ = sess.Provide(env, magusModules(cfg.modules)...)
}

// registerMagusModules installs the magus module surface a Buzz session sees: Buzz's
// own stdlib under bare names (so a magusfile or spell may `import "std"` /
// `import "serialize"` / `import "io"`), with the magus modules layered on top
// of those same bare names — `import "os"` carries Buzz's os plus proc.exec/which/…,
// and modules Buzz's stdlib lacks (http, vcs, archive, env, time, …) become new
// bare imports. The result is one superset surface, no separate `magus/extra`
// aggregate. Shared by the magusfile binding path (registerAllBuzz) and the spell
// handler op path (callBuzzSpellFunc), so both surfaces stay in lock-step.
func registerMagusModules(ctx context.Context, sess *buzz.Session) {
	RegisterModuleSurface(ctx, sess)
	RegisterSpellSourceModules(sess)
}

// RegisterSpellSourceModules installs every source-only Buzz module a spell (or
// magusfile) imports for its value types:
//
//   - magus/spell (spell.SpellModulePath): the canonical Target/Command/Service/
//     Charm/PatchOp types a spell op WRITES. Kept separate from the base host-module
//     surface because a plain script needs none of these until it imports a spell
//     module.
//   - magus/charm: the pure-Buzz patch constructors.
//   - magus/lint: the Finding a Buzz lint rule returns.
//   - magus: spell.MagusDeclSource, the declarations for the magus namespace itself. The
//     namespace VALUE is a native module registered elsewhere (registerAllBuzz,
//     RegisterMagusNamespace); only its types are declared here.
//
// It is layered on top of RegisterModuleSurface by the magusfile runtime and,
// deliberately, by `magus buzz` so a spell file and its `test "..." {}` blocks run
// under `magus buzz -t` with the same modules the engine loads them with.
func RegisterSpellSourceModules(sess *buzz.Session) {
	RegisterSpellDecls(sess)
	DeclareMagusTypes(sess)
}

// RegisterSpellDecls stores the spell, charm, and lint declaration source.
// Storing it does not parse it: a declaration module is parsed when something
// imports it. DeclareMagusTypes is the parse, and it is separate because a
// closed script (a guard hook) never imports magus and should not pay for the
// mirrors on every tool call.
func RegisterSpellDecls(sess *buzz.Session) {
	sess.SetModuleDecls(spell.SpellModulePath, spell.SpellModuleSource)
	sess.SetModuleDecls(spell.CharmModulePath, spell.CharmModuleSource)
	sess.SetModuleDecls(spell.LintModulePath, spell.LintModuleSource)
}

// buzzLogFn builds the Buzz trampoline for magus.<level>(msg, fields?). It routes
// through the shared emitMagusLog so every host log path formats identically.
func buzzLogFn(level slog.Level) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		emitMagusLog(ctx, level, argStr(args, 0), argStrMap(args, 1))
		return vm.Null, nil
	}
}

// MagusModuleKeys returns the member names of the magus.* module as the real
// Buzz bindings register them. It exists so the wasm playground
// (internal/playground), which keeps a SEPARATE recording implementation of
// this same surface, can diff against the source of truth in a guard test —
// the two host implementations must not silently drift.
func MagusModuleKeys() []string {
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	registerAllBuzz(context.Background(), sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
	return slices.DeleteFunc(magusNativeModule(sess).MapKeys(), spell.IsMagusEnum)
}

// magusNativeModule is the registered magus module value, and a missing one is a
// panic rather than a zero value: both readers below exist to catch drift, and a zero
// map would report an empty surface as agreement.
func magusNativeModule(sess *buzz.Session) vm.Value {
	mod, ok := sess.NativeModule("magus")
	if !ok {
		panic("bindings: the magus module is not registered; registerAllBuzz must run first")
	}
	return mod
}

// MagusNamespaceKeys returns the member names bound inside one of magus's nested
// namespaces (`magus\\cache`, `magus\\secret`), or nil when no such member exists or
// it is not a map.
//
// Same role as MagusModuleKeys one level down: std declares these as a Namespace
// rendering to an object with static extern methods, and nothing but a test connects
// that declaration to the map buildMagus actually assembles.
func MagusNamespaceKeys(name string) []string {
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	registerAllBuzz(context.Background(), sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
	nested, ok := magusNativeModule(sess).MapGet(name)
	if !ok || !nested.IsMap() {
		return nil
	}
	return nested.MapKeys()
}
