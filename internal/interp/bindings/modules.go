package bindings

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"

	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/spellruntime"
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
func magusModules(modules bindinggen.Set) []buzz.Module {
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	slices.Sort(names)

	mods := make([]buzz.Module, 0, len(names))
	for _, name := range names {
		reg := modules[name]
		labels := []string{labelMagus}
		if reg.Capabilities.Has(bindinggen.WASM) {
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
				// declare: byteSize and upload_chunked. crypto no longer needs any -
				// its HMAC and base64 methods became declared std.Module methods once
				// TypeByteSlice existed, and this hook shrank by one domain as a result.
				if name == "http" {
					mergeModuleMap(mod, registerHTTPBytes())
				}
				// Layer this module's DECLARATIONS on as a source companion - the same
				// "native value + declaration source under one import path" mechanism
				// crypto/io already use for their own signatures (see
				// gopherbuzz/session.go's resolveImport). They carry the return-type
				// mirrors and an extern per method, so every call through this module
				// types against a real signature instead of Unknown.
				// Read the declarations by NAME - moduledecls writes one flat file per
				// module, gen/decls/json.buzz - but register them under the IMPORT
				// PATH, because resolveImport looks them up by the path the import
				// line spelled.
				if src, ok := spellruntime.ModuleDecls(name); ok {
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
	modules bindinggen.Set
	// scriptOut is where std.print goes. Defaults to STDERR: under `magus run` a
	// magusfile's print is human output like every other thing magus says, and
	// stdout carries the structured answer (-o json|yaml|jsonl|template) alone. A
	// magusfile with a print in it would otherwise emit a document no parser
	// accepts. `magus buzz` overrides it - there the script IS the program, so its
	// output is the command's output.
	scriptOut io.Writer
}

// ModuleSurfaceOption configures one registration of the host module surface.
type ModuleSurfaceOption func(*moduleSurfaceConfig)

// WithModules replaces the default host-module set for one session. It is the
// test seam for a fake fs/http/vcs module; callers use registry.Modules.With to
// replace only the capability they need without mutating global state.
func WithModules(modules bindinggen.Set) ModuleSurfaceOption {
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
	buzzstd.RegisterWithOutput(sess, cfg.scriptOut)
	_ = sess.Provide(buzz.ModuleEnv{Ctx: ctx}, magusModules(cfg.modules)...)
}

// registerMagusModules installs the magus module surface a Buzz session sees: Buzz's
// own stdlib under bare names (so a magusfile or spell may `import "std"` /
// `import "serialize"` / `import "io"`), with the magus modules layered on top
// of those same bare names — `import "os"` carries Buzz's os plus os.exec/which/…,
// and modules Buzz's stdlib lacks (http, vcs, archive, env, time, …) become new
// bare imports. The result is one superset surface, no separate `magus/extra`
// aggregate. Shared by the magusfile binding path (registerAllBuzz) and the spell
// handler op path (callBuzzSpellFunc), so both surfaces stay in lock-step.
func registerMagusModules(ctx context.Context, sess *buzz.Session) {
	RegisterModuleSurface(ctx, sess)
	RegisterSpellSourceModules(sess)
}

// magusUndeclaredTypeSource carries the only magus.* mirrors the generated
// declarations cannot reach, because the generator derives a module's mirrors from
// its std.Module method RETURNS and these have none:
//
//   - Module / ModuleFieldEntry / ModuleMethodEntry: magus.modules and magus.module are
//     hand-bound in buzz.go rather than declared as std.Module methods, so they are not
//     in std.All() at all. Folding them into the descriptor would delete this entry.
//   - TargetRun / Run: annotate-only shapes. No host method returns them; they exist so a
//     caller can type a run payload it decoded itself. TargetRun precedes Run because
//     Run.targets is a list of it.
//
// Everything else that used to live here is now generated, leaf-first, from the
// returns themselves - a mirror can no longer go missing when a return is added.
var magusUndeclaredTypeSource = strings.Join([]string{
	spellruntime.ModuleFieldEntrySource,
	spellruntime.ModuleMethodEntrySource,
	spellruntime.ModuleSource,
	spellruntime.TargetRunSource,
	spellruntime.RunSource,
}, "\n")

// RegisterSpellSourceModules installs every source-only Buzz module a spell (or
// magusfile) imports for its value types:
//
//   - magus/spell (spellruntime.SpellModulePath): the canonical Target/Command/Service/
//     Charm/PatchOp types a spell op WRITES. Kept separate from the base host-module
//     surface because a plain script needs none of these until it imports a spell
//     module.
//   - magus/charm: the pure-Buzz patch constructors.
//   - the magus namespace's own return types (magusOwnedTypeSource above), declared
//     directly rather than behind an importable path: "magus" is bound as a session
//     global (see registerAllBuzz), not a lazily-imported module, so the normal
//     import-triggered collection (SetModuleDecls) never runs for it - see
//     DeclareModuleTypes's doc for why.
//
// It is layered on top of RegisterModuleSurface by the magusfile runtime and,
// deliberately, by `magus buzz` so a spell file and its `test "..." {}` blocks run
// under `magus buzz -t` with the same modules the engine loads them with.
func RegisterSpellSourceModules(sess *buzz.Session) {
	sess.SetModuleDecls(spellruntime.SpellModulePath, spellruntime.SpellModuleSource)
	sess.SetModuleDecls(spellruntime.CharmModulePath, spellruntime.CharmModuleSource)
	// "magus" is bound as a session GLOBAL, not a lazily-imported module, so the
	// import-triggered SetModuleDecls collection never runs for it (see
	// DeclareModuleTypes). It gets the same generated source every other module gets -
	// object mirrors plus an extern per method - which is what types magus\\affectedImpact
	// and friends at a call site instead of leaving them Unknown.
	decls, ok := spellruntime.ModuleDecls("magus")
	if !ok {
		panic("bindings: generated magus declarations are missing; run `magus run generate`")
	}
	sess.DeclareModuleTypes("magus", decls+"\n"+magusUndeclaredTypeSource)
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
	return sess.GetGlobal("magus").MapKeys()
}
