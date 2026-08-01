package bindings

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	hostreg "github.com/egladman/magus/host/registry"
	"github.com/egladman/magus/internal/spellruntime"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
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

// hostTypeModuleSources maps a host module's bare import name to the Buzz mirror
// source of the value type(s) its methods return, so `import "<name>"` brings both
// the callable methods AND their result types into scope with no second import -
// the point of shipping each mirror with the module that returns it rather than a
// single monolithic bundle. See internal/spell/hosttypes.go for the generated
// mirrors and why each lives where it does.
//
// vcs's entry duplicates SemverVersionSource (also assembled into "semver" below):
// Tag.version is a SemverVersion, but a synthetic module's source companion is only
// ever COLLECTED (parsed for its declarations), never executed - so an
// `import "semver";` line embedded in vcs's own bundle would parse but contribute
// nothing (see Session.collectImportedModule, and DeclareModuleTypes's doc). A spell
// doing only `import "vcs";` still needs SemverVersion declared, so the fix is to
// duplicate the (single, generated) source string into both bundles at assembly
// time here, not to duplicate the generated file itself.
var hostTypeModuleSources = map[string]string{
	"os":       spellruntime.ExecResultSource,
	"fs":       spellruntime.FileInfoSource,
	"http":     spellruntime.HTTPResponseSource,
	"encoding": spellruntime.URLSource,
	"semver":   strings.Join([]string{spellruntime.SemverVersionSource, spellruntime.SemverNextSource}, "\n"),
	"vcs": strings.Join([]string{
		spellruntime.CommitAuthorSource, // precedes Commit: Commit.author is CommitAuthor
		spellruntime.CommitSource,
		spellruntime.SemverVersionSource, // co-located dup, precedes Tag: Tag.version is SemverVersion
		spellruntime.TagSource,
	}, "\n"),
}

// magusModules expresses magus's own modules as buzz.Modules: each wraps its
// host/gen register trampoline in a Bind that builds the module map (plus any
// byte-level companions) and layers it onto the stdlib module of the same name,
// or installs it fresh when Buzz has no such module. Ordered by name so the bind
// sequence is deterministic.
func magusModules() []buzz.Module {
	names := make([]string, 0, len(hostreg.Modules))
	for name := range hostreg.Modules {
		names = append(names, name)
	}
	sort.Strings(names)

	mods := make([]buzz.Module, 0, len(names))
	for _, name := range names {
		name := name
		reg := hostreg.Modules[name]
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
				// Layer this module's return-type mirrors on as a source companion
				// (see hostTypeModuleSources) - the same "native value + declaration
				// source under one import path" mechanism crypto/io already use for
				// their own signatures (see gopherbuzz/session.go's resolveImport).
				if src, ok := hostTypeModuleSources[name]; ok {
					s.SetSourceModule(name, src)
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
// (hostreg.Modules) layered on top of the same bare names. It is the full surface
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

// magusOwnedTypeSource is the bundle of mirrors for magus.* methods that are not
// bare-import host modules (magus.ls, magus.affected, magus.graph, magus.targets,
// magus.modules/module) - so they can't ride along with a "os"/"vcs"/... import the
// way hostTypeModuleSources's entries do. Declared in dependency order: ProjectEntry
// before Projects; the four TargetGraph leaves before TargetGraphNode before
// TargetGraphProject before TargetGraph; ModuleFieldEntry/ModuleMethodEntry before
// Module.
var magusOwnedTypeSource = strings.Join([]string{
	spellruntime.ProjectEntrySource,
	spellruntime.ProjectsSource,
	spellruntime.AffectedSource,
	spellruntime.GraphSource,
	spellruntime.CrossTargetRefSource,
	spellruntime.TargetSpellUseSource,
	spellruntime.InputRefSource,
	spellruntime.OutputRefSource,
	spellruntime.TargetGraphNodeSource,
	spellruntime.TargetGraphProjectSource,
	spellruntime.TargetGraphSource,
	spellruntime.ModuleFieldEntrySource,
	spellruntime.ModuleMethodEntrySource,
	spellruntime.ModuleSource,
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
//     import-triggered collection (SetSourceModule) never runs for it - see
//     DeclareModuleTypes's doc for why.
//
// It is layered on top of RegisterModuleSurface by the magusfile runtime and,
// deliberately, by `magus buzz` so a spell file and its `test "..." {}` blocks run
// under `magus buzz -t` with the same modules the engine loads them with.
func RegisterSpellSourceModules(sess *buzz.Session) {
	sess.SetSourceModule(spellruntime.SpellModulePath, spellruntime.SpellModuleSource)
	sess.SetSourceModule(spellruntime.CharmModulePath, spellruntime.CharmModuleSource)
	sess.DeclareModuleTypes("magus", magusOwnedTypeSource)
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
