package bindings

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/egladman/magus/internal/hostmodules"
	"github.com/egladman/magus/internal/interp"
	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/spellruntime"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

func init() {
	interp.RegisterBuzzHostBindings(registerAllBuzz)
}

// registerAllBuzz installs the magus.* host API into a Buzz session.
//
// These bindings (and the magus-utils bindings-emitted ones in bindings/gen) are written
// directly against the concrete magus/gopherbuzz value system - NewMap, DirectValue,
// StrValue, and friends - rather than behind the generic engine.Value /
// engine.Session abstraction. That is deliberate, not a layering gap:
//
//   - Buzz is the only engine, so there is no second implementation to share with;
//     a buzz-local interface here would be a single-implementation wrapper over
//     hundreds of value-shaped call sites.
//   - magus/gopherbuzz is an intentionally public, standalone interpreter package;
//     binding against its real API exercises that public surface directly instead
//     of hiding it behind an internal seam.
//
// The generic engine.Session adapter (engine/buzz) still exists for the REPL/pry
// path; it is not used here.
//
// The namespace builders this calls live alongside, one file per concern:
// project_ns.go (magus.project), target.go (the magus.Context builder and its
// ctx.needs/glob dependency primitives, plus cross-project handles),
// spell_object.go (imported spell handles), modules.go (the host module surface),
// imports.go (project/spell import resolution), and pry.go (magus.pry).
func registerAllBuzz(ctx context.Context, sess *buzz.Session, targets map[string]vm.Callable, exports map[string]vm.Value, parseMode bool) {
	// One host-call observer for this registration, timing every magus.* native
	// callable into magus.buzz.host.call. nil (a pure pass-through) when telemetry
	// is disabled, so the VM's hot native-dispatch arm is untouched on one-shot runs.
	obs := interp.NewHostCallObserver(ctx)
	// Cross-project handle registry for this session: project imports register
	// each handle they bind, ctx.needs matches passed functions against it.
	ext := &externalHandles{}
	sess.SetGlobal("magus", buildMagusNS(ctx, sess, obs, parseMode, magusfileSurface))

	// A target declares its dependencies and cache footprint through the magus.Context
	// it receives as its first argument (ctx.needs/glob/inputs/outputs), NOT a floating
	// magus.* global: the signature is the contract magus reads statically to build the
	// graph, so the declaration surface lives only on the context. The value is stashed
	// under a session-global name execBuzzSrc fetches to prepend at dispatch; it closes
	// over the same targets/exports/ext so ctx.needs dispatches deps through the pool.
	sess.SetGlobal(interp.TargetContextGlobal, buildTargetContext(obs, targets, exports, ext))

	// The host utilities are reached under the same bare names as Buzz's own stdlib:
	// `import "os"`, `import "fs"`, `import "http"`, `import "vcs"`, ... A magusfile
	// selects methods off each module directly (proc.exec, fs.glob, vcs.status).
	// registerMagusModules layers the magus host methods onto Buzz's stdlib modules (a
	// superset surface) and is shared with spell-loading, so a magusfile and a handler
	// op spell see the same modules.
	registerMagusModules(ctx, sess)
	// Built-in spells follow the same import idiom as std modules: each spell is
	// reachable as `import "magus/spell/<name>"`, binding the spell handle under
	// its basename.
	builtins := spellruntime.Builtins()
	for name := range builtins {
		sess.SetNativeModule(spells.ModulePath(name), buzzSpellObject(name))
	}
	// Host-registered spells (the magusfile spell in internal/interp/magusfile.go,
	// and any spell a plugin registers at runtime) aren't compiled built-ins, so the
	// loop above doesn't reach them; expose each under the same import idiom. The
	// handle carries only the name; magus.project resolves the spec by name from the
	// host registry.
	for _, sp := range project.DefaultSpellRegistry().All() {
		if _, isBuiltin := builtins[sp.Name()]; isBuiltin {
			continue
		}
		sess.SetNativeModule(spells.ModulePath(sp.Name()), buzzSpellObject(sp.Name()))
	}
	// Workspace-local spells are imported by path: `import "spells/hello"` resolves
	// ./spells/hello.buzz on demand and binds its handle under the basename (hello),
	// registering by value when bound via magus.project.
	// Cross-project target imports: `import "project/<path>" as <alias>` binds a
	// module whose members are the other project's targets as callable handles,
	// so `ctx.needs(<alias>.<target>)` declares a target-level dependency across
	// the project boundary (a typo in the target name fails at load, not at run
	// time), and `<alias>.<target>()` dispatches it directly.
	sess.SetModuleResolver(func(importPath string) (vm.Value, bool) {
		if v, ok := resolveProjectImport(ctx, importPath, ext); ok {
			return v, true
		}
		return resolveLocalSpellImport(ctx, importPath)
	})
}

// magusSurface names the two places the magus.* namespace is installed. They share
// every member: a member reachable from a magusfile is reachable from a `magus buzz`
// script, and only the ones listed in buildMagusNS's withhold block differ.
type magusSurface int

const (
	// magusfileSurface is a magusfile being loaded or run: every member is live.
	magusfileSurface magusSurface = iota
	// scriptSurface is a standalone `magus buzz` script, snippet, test file, or REPL.
	// There is no workspace being loaded, so the members that DECLARE into one raise
	// MGS1022 instead.
	scriptSurface
)

// RegisterMagusNamespace installs the magus.* namespace into a standalone Buzz
// session, so `import "magus"` resolves in a `magus buzz` script the way it does in
// a magusfile and the members that need no magusfile (magus\describe, magus\cmd,
// magus\run, magus\insight, magus\doctor, the log levels, magus\module[s]) work
// there.
//
// It is a SEPARATE call from RegisterModuleSurface rather than part of it, because
// the magusfile engine installs its own richer namespace (registerAllBuzz) and must
// not have this one layered over it. The two are built by the same buildMagusNS, so
// they cannot drift.
func RegisterMagusNamespace(ctx context.Context, sess *buzz.Session) {
	sess.SetGlobal("magus", buildMagusNS(ctx, sess, interp.NewHostCallObserver(ctx), false, scriptSurface))
}

// buildMagusNS assembles the magus.* namespace object for one surface. The
// magusfile engine and `magus buzz` share it so the surfaces stay in lock-step, the
// same reason RegisterModuleSurface is shared for the host modules.
func buildMagusNS(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver, parseMode bool, surface magusSurface) vm.Value {
	magus := vm.NewMap()
	cacheNS := buildCacheNS(ctx, obs)
	ciNS := buildCINS(ctx, obs)
	magus.MapSet("project", buildProject(ctx, obs))
	magus.MapSet("cache", cacheNS)
	magus.MapSet("ci", ciNS)
	magus.MapSet("secret", buildSecretNS(ctx, obs))
	magus.MapSet("workspace", buildWorkspaceNS(ctx, obs))
	magus.MapSet("pry", directVal(obs, "magus.pry", buildBuzzPry(sess, parseMode)))

	// The host-declarable subset (magus.cmd/run/describe/insight/doctor,
	// magus.bust_cache) is generated from the std.Magus descriptor like every other
	// module, so the two can't drift and a declared method can't be silently left
	// unbound. Merged onto the hand-built magus map above, which carries only the
	// VM-infra members (project/cache/pry/log, plus the magus.Context) that can't
	// share a Go Impl across the boundary.
	mergeModuleMap(magus, bindinggen.RegisterMagus(ctx, sess))

	// magus.modules() / magus.module(name): typed, native introspection of the host
	// module registry - the same host.ModulesOutput core `magus describe module[s]`
	// formats, marshalled straight to Buzz objects instead of scraping a subprocess's
	// `-o json` stdout. modules() lists every module {name, doc, fields, methods};
	// module(name) returns one with fields + per-method Buzz signatures, and raises on
	// an unknown name. Hand-written (not declarative) because the core uses host,
	// which std can't import. hostmodules.Describe, not std.DescribeModules: std's
	// own registry no longer covers std/encoding's nine modules by itself - see
	// hostmodules's doc.
	magus.MapSet("modules", directVal(obs, "magus.modules", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return bindinggen.MapsVal(hostmodules.Describe("")), nil
	}))
	magus.MapSet("module", directVal(obs, "magus.module", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		name := ""
		if len(args) > 0 && args[0].IsStr() {
			name = args[0].AsString()
		}
		out := hostmodules.Describe(name)
		if len(out) == 0 {
			return vm.Null, fmt.Errorf("magus.module: unknown module %q", name)
		}
		return bindinggen.AnyMapVal(out[0].BuzzObject()), nil
	}))

	// magus.normalize(name): the canonical form of any magus entity name - a target, a
	// charm, or a spell op. Exposed because the rule is only knowable by running it:
	// build2 gains a '-' you did not type, and HTTPServer breaks before its last letter.
	// The table published in docs/concepts/targets.md is this function's output.
	//
	// It returns the canonical NAME, never a spell handle. A handle can only come from
	// a literal `import`, because internal/describe reads spell imports statically to
	// build the target graph - anything resolved dynamically would drop the target-uses-spell
	// edge and silently under-report the graph.
	magus.MapSet("canonicalName", directVal(obs, "magus.canonicalName", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsStr() {
			return vm.Null, fmt.Errorf("magus.canonicalName: expected a name string")
		}
		return bindinggen.StrVal(types.Normalize(args[0].AsString())), nil
	}))

	// magus.log.*: the one way to emit a message from a magusfile - there is no
	// separate std log module on this surface. Each level writes into the process
	// slog logger via emitMagusLog.
	//
	// Grouped, and grouped by BEHAVIOUR: everything here emits and returns. fatal and
	// raise stay on magus itself because they end the run, and a namespace that mixed
	// the two would let `magus\log.fatal(...)` read like one more level.
	logNS := vm.NewMap()
	logNS.MapSet("info", directVal(obs, "magus.log.info", buzzLogFn(slog.LevelInfo)))
	logNS.MapSet("debug", directVal(obs, "magus.log.debug", buzzLogFn(slog.LevelDebug)))
	logNS.MapSet("warn", directVal(obs, "magus.log.warn", buzzLogFn(slog.LevelWarn)))
	logNS.MapSet("error", directVal(obs, "magus.log.error", buzzLogFn(slog.LevelError)))
	// hint(msg): advisory nudge (see emitMagusHint) - non-fatal, deduped, honors the
	// hints toggle. Not a level, but it emits and returns, which is the line this
	// namespace is drawn on.
	logNS.MapSet("hint", directVal(obs, "magus.log.hint", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) > 0 && args[0].IsStr() {
			emitMagusHint(args[0].AsString())
		}
		return vm.Null, nil
	}))
	magus.MapSet("log", logNS)
	// magus.fatal(msg): log at error level, then abort with exit 1 via a typed
	// ExitError (the CLI/daemon map it to the exit status).
	magus.MapSet("fatal", directVal(obs, "magus.fatal", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		emitMagusLog(ctx, slog.LevelError, argStr(args, 0), nil)
		types.CaptureExit(ctx, 1)
		return vm.Null, types.ExitError{Code: 1}
	}))

	// The members that DECLARE into the workspace magus is loading: each records onto
	// the per-Open registry (or the CI provider selection) that only a magusfile
	// evaluation has. On the script surface the real member would find no registry and
	// return null - a silent no-op the caller reads as success - so it is replaced by
	// an MGS1022 guard that names the constraint. The rest of the namespace stays,
	// which is the point: withholding the whole `import "magus"` for these three read
	// as "the module does not exist".
	if surface == scriptSurface {
		magus.MapSet("project", magusfileOnly(obs, `magus\project`))
		cacheNS.MapSet("remote", magusfileOnly(obs, `magus\cache.remote`))
		ciNS.MapSet("provider", magusfileOnly(obs, `magus\ci.provider`))
	}
	return magus
}

// magusfileOnly returns a stand-in for a magus.* member that only a magusfile can
// call, raising MGS1022 with the member's name.
func magusfileOnly(obs buzz.DirectObserver, member string) vm.Value {
	return directVal(obs, member, func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return vm.Null, types.DiagnosticErrorf(types.MagusfileOnlyMember,
			"%s: only callable from a magusfile, not from a spell or a `magus buzz` script - it declares into the workspace magus is loading, and neither has one to declare into", member)
	})
}

// directVal builds a magus.* host callable timed under name via obs (from
// interp.NewHostCallObserver). When obs is nil buzz.WrapDirect returns fn
// unchanged, so an unobserved run pays nothing and the VM's hot native-dispatch
// arm is untouched.
func directVal(obs buzz.DirectObserver, name string, fn vm.Callable) vm.Value {
	return vm.DirectValue(name, buzz.WrapDirect(name, fn, obs))
}
