package bindings

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
	"github.com/egladman/gopherbuzz/vm"
	buzzgen "github.com/egladman/magus/host/gen"
	ispell "github.com/egladman/magus/internal/spell"
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
// host/gen register trampoline in a Bind that builds the module map (plus any
// byte-level companions) and layers it onto the stdlib module of the same name,
// or installs it fresh when Buzz has no such module. Ordered by name so the bind
// sequence is deterministic.
func magusModules() []buzz.Module {
	names := make([]string, 0, len(buzzgen.Modules))
	for name := range buzzgen.Modules {
		names = append(names, name)
	}
	sort.Strings(names)

	mods := make([]buzz.Module, 0, len(names))
	for _, name := range names {
		name := name
		reg := buzzgen.Modules[name]
		labels := []string{labelMagus}
		if reg.WASMCompatible {
			labels = append(labels, labelWASM)
		}
		mods = append(mods, buzz.Module{
			Name:   name,
			Labels: labels,
			Bind: func(s *buzz.Session, env buzz.ModuleEnv) error {
				mod := reg.Register(env.Ctx, s)
				// Byte-level companions so a script reaches a whole domain through
				// one import: crypto.hmacSha256 beside crypto.sha256Hex,
				// http.download beside http.get.
				switch name {
				case "crypto":
					mergeModuleMap(mod, registerCryptoBytes())
				case "http":
					mergeModuleMap(mod, registerHTTPBytes())
				}
				// Buzz's stdlib may already own this bare name (os, fs, crypto):
				// overlay the magus methods onto it so callers see the union (magus
				// wins on the few shared keys, e.g. os.exit/fs.exists, its forms
				// being sandbox- and context-aware). Otherwise install fresh.
				if base, ok := s.SyntheticModule(name); ok {
					mergeModuleMap(base, mod)
				} else {
					s.SetSyntheticModule(name, mod)
				}
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

// registerMagusModules installs the magus module surface a Buzz session sees: Buzz's
// own stdlib under bare names (so a magusfile or spell may `import "std"` /
// `import "serialize"` / `import "io"`), with the magus modules layered on top
// of those same bare names — `import "os"` carries Buzz's os plus os.exec/which/…,
// and modules Buzz's stdlib lacks (http, vcs, archive, env, time, …) become new
// bare imports. The result is one superset surface, no separate `magus/extra`
// aggregate. Shared by the magusfile binding path (registerAllBuzz) and the spell
// handler op path (callBuzzSpellFunc), so both surfaces stay in lock-step.
// RegisterModuleSurface installs the shared Buzz module surface: Buzz's own
// stdlib, the magus testing extensions (assert/suite), and every magus module
// (buzzgen.Modules) layered on top of the same bare names. It is the full surface
// a standalone script sees, shared by the magusfile engine (which then adds the
// magus.* namespace and the Target/Charm source types on top) and the `magus buzz`
// runner, so the two never drift.
func RegisterModuleSurface(ctx context.Context, sess *buzz.Session) {
	// Buzz's stdlib provides the base modules; the magus modules then layer onto
	// the same bare names (their Bind reads back and merges) or install fresh. One
	// registration path: gopherbuzz's stdlib and magus's own modules are both
	// buzz.Modules applied through Session.Provide.
	buzzstd.Register(sess)
	_ = sess.Provide(buzz.ModuleEnv{Ctx: ctx}, magusModules()...)
}

func registerMagusModules(ctx context.Context, sess *buzz.Session) {
	RegisterModuleSurface(ctx, sess)
	RegisterSpellSourceModules(sess)
}

// RegisterSpellSourceModules installs the `magus/target` and `magus/charm` source
// modules a spell (or magusfile) imports: the canonical Target/Charm/Command value
// types plus the pure-Buzz charm constructors. It is layered on top of
// RegisterModuleSurface by the magusfile runtime and, deliberately, by `magus buzz`
// so a spell file and its `test "..." {}` blocks run under `magus buzz -t` with the
// same modules the engine loads them with. Kept separate from the base surface
// because a plain script needs neither type until it imports a spell module.
func RegisterSpellSourceModules(sess *buzz.Session) {
	// Canonical value types (Target/Charm) as a flat-importable source module, so
	// a spell's mgs_listTargets can be typed {str: fun(Target, fun(any)) void/bool}
	// instead of `any`. Single source of truth lives in the spell package.
	sess.SetSourceModule(ispell.TargetModulePath, strings.Join([]string{
		ispell.TargetModuleSource,
		// Command value types (PatchOp < Charm < Command ordering: each references
		// the prior). Inlined into built-ins too — see builtinModuleSources.
		ispell.PatchOpSource,
		ispell.CharmTypeSource,
		ispell.CommandSource,
		// Service must follow Command (its command/readiness/stop fields are typed
		// Command), so a workspace-local spell can author a service op. The dry host
		// registers the same bundle; keep the two in step.
		ispell.ServiceSource,
		ispell.ExecResultSource,
		// Boundary mirrors of the host-method record shapes, so a magusfile can
		// annotate a vcs.commit / fs.stat / http.* / semver.parse / parse_url result
		// for compile-checked field access. CommitAuthor precedes Commit (Commit's
		// author field is typed CommitAuthor).
		ispell.CommitAuthorSource,
		ispell.CommitSource,
		ispell.FileInfoSource,
		ispell.HTTPResponseSource,
		ispell.SemverVersionSource,
		ispell.URLSource,
	}, "\n"))
	// magus/charm: the pure-Buzz patch constructors, registered as its own source
	// module so a handler op spell or a magusfile can `import "magus/charm"` and
	// build charms with charm.after/set/… (the built-in generator inlines it for
	// self-contained command spells; see SelfContainedBuiltinSource).
	sess.SetSourceModule(ispell.CharmModulePath, ispell.CharmModuleSource)
}

// buzzLogFn builds the Buzz trampoline for magus.<level>(msg, fields?). It routes
// through the shared emitMagusLog so every host log path formats identically.
func buzzLogFn(level slog.Level) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		emitMagusLog(ctx, level, argStr(args, 0), argStrMap(args, 1))
		return vm.Null, nil
	}
}

// buzzIONoop backs magus.inputs / magus.outputs at runtime. The declarations are
// read statically at load (describe.Extract), which is authoritative because a cache
// hit skips the body; the runtime call is a pure no-op so a body reaching it on a
// miss just proceeds.
func buzzIONoop(_ context.Context, _ []vm.Value) (vm.Value, error) { return vm.Null, nil }

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
