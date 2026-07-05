package bindings

import (
	"context"
	"log/slog"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
	"github.com/egladman/gopherbuzz/vm"
	buzzgen "github.com/egladman/magus/host/gen"
	ispell "github.com/egladman/magus/internal/spell"
)

// magusModules builds every magus host module (os/platform/fs/vcs/env/crypto/
// http/archive/charm/semver/yaml/…) via the magus-utils bindings-emitted host/gen
// trampolines, keyed by the bare import name each is exposed under. The closures
// capture sess so host callbacks (e.g. arg.index_func, os.with_env) can call back
// into the VM.
//
// The byte-level crypto (hmac, keyed base64) and http (download, upload_chunked,
// byteSize) companions are merged into their respective module maps so a script
// reaches a whole domain through one import — crypto.hmacSha256 and http.download
// sit alongside crypto.sha256Hex and http.get.
func magusModules(ctx context.Context, sess *buzz.Session) map[string]vm.Value {
	out := make(map[string]vm.Value, len(buzzgen.Modules))
	for name, reg := range buzzgen.Modules {
		out[name] = reg.Register(ctx, sess)
	}
	// Byte-level companions merged in so a script reaches a whole domain through
	// one import: crypto.hmacSha256 beside crypto.sha256Hex, http.download beside
	// http.get. (json also carries stringify_pretty, which Buzz's serialize lacks;
	// that ships in the generated trampoline already.)
	mergeModuleMap(out["crypto"], registerCryptoBytes())
	mergeModuleMap(out["http"], registerHTTPBytes())
	return out
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

// registerHostModules installs the host module surface a Buzz session sees: Buzz's
// own stdlib under bare names (so a magusfile or spell may `import "std"` /
// `import "serialize"` / `import "io"`), with the magus host modules layered on top
// of those same bare names — `import "os"` carries Buzz's os plus os.exec/which/…,
// and modules Buzz's stdlib lacks (http, vcs, archive, env, time, …) become new
// bare imports. The result is one superset surface, no separate `magus/extra`
// aggregate. Shared by the magusfile binding path (registerAllBuzz) and the spell
// handler op path (callBuzzSpellFunc), so both surfaces stay in lock-step.
// RegisterModuleSurface installs the shared Buzz module surface: Buzz's own
// stdlib, the magus testing extensions (assert/suite), and every magus host module
// (buzzgen.Modules) layered on top of the same bare names. It is the full surface
// a standalone script sees, shared by the magusfile engine (which then adds the
// magus.* namespace and the Target/Charm source types on top) and the `magus buzz`
// runner, so the two never drift.
func RegisterModuleSurface(ctx context.Context, sess *buzz.Session) {
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
}

func registerHostModules(ctx context.Context, sess *buzz.Session) {
	RegisterModuleSurface(ctx, sess)
	// Canonical value types (Target/Charm) plus the generated TargetQuery as a
	// flat-importable source module, so a spell's mgs_listTargets can be typed
	// {str: fun(Target, fun(any)) void/bool} instead of `any`, and a magusfile can name or
	// build a TargetQuery. Single source of truth lives in the spell package. The
	// built-in generator inlines only TargetModuleSource (Target/Charm) — built-ins
	// have no use for TargetQuery — so it is appended only here, on the runtime path.
	sess.SetSourceModule(ispell.TargetModulePath, strings.Join([]string{
		ispell.TargetModuleSource,
		// Command value types (PatchOp < Charm < Command ordering: each references
		// the prior). Inlined into built-ins too — see builtinModuleSources.
		ispell.PatchOpSource,
		ispell.CharmTypeSource,
		ispell.CommandSource,
		ispell.TargetQuerySource,
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

// MagusModuleKeys returns the member names of the magus.* module and its
// magus.target sub-module as the real Buzz bindings register them. It exists so the
// wasm playground (internal/playground), which keeps a SEPARATE recording
// implementation of this same surface, can diff against the source of truth in a
// guard test — the two host implementations must not silently drift.
func MagusModuleKeys() (top, target []string) {
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	registerAllBuzz(context.Background(), sess, map[string]vm.Callable{}, true)
	magus := sess.GetGlobal("magus")
	top = magus.MapKeys()
	if t, ok := magus.MapGet("target"); ok {
		target = t.MapKeys()
	}
	return top, target
}
