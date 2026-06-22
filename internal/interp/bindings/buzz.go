package bindings

import (
	"context"
	"fmt"
	"log/slog"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/host"
	buzzgen "github.com/egladman/magus/host/gen"
	"github.com/egladman/magus/internal/interp"
	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

func init() {
	interp.RegisterBuzzHostBindings(registerAllBuzz)
}

// registerAllBuzz installs the magus.* host API into a Buzz session.
//
// These bindings (and the magus-utils bindings-emitted ones in host/gen) are written
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
//
// The namespace builders this calls live alongside, one file per concern:
// project_ns.go (magus.project), target_ns.go (magus.target/needs/cache),
// spell_object.go (imported spell handles), modules.go (the host module surface),
// imports.go (project/spell import resolution), and pry.go (magus.pry).
func registerAllBuzz(ctx context.Context, sess *buzz.Session, targets map[string]vm.Callable, parseMode bool) {
	magus := vm.NewMap()
	magus.MapSet("project", buildProject(ctx))
	magus.MapSet("target", buildTargetNS(targets))
	magus.MapSet("cache", buildCacheNS(ctx))
	// magus.needs(...): the one dependency primitive. Each argument is a TargetQuery
	// from magus.target.literal/glob/regex; the matched targets are awaited via
	// the Buzz VM pool (cross-project queries dispatch through CrossDispatch).
	magus.MapSet("needs", vm.DirectValue("magus.needs", buildBuzzNeeds(targets)))
	magus.MapSet("pry", vm.DirectValue("magus.pry", buildBuzzPry(sess, parseMode)))

	// The host-declarable subset (magus.cmd, magus.bust_cache) is generated from
	// the std.Magus descriptor like every other module, so the two can't drift and
	// a declared method can't be silently left unbound. Merged onto the hand-built
	// magus map above (which carries only the VM-infra members — needs/target/
	// project/cache/pry/log — that can't share a Go Impl across the boundary).
	mergeModuleMap(magus, buzzgen.RegisterMagus(ctx, sess))

	// magus.modules() / magus.module(name): typed, native introspection of the host
	// module registry — the same host.ModulesOutput core `magus describe
	// module[s]` formats, marshalled straight to Buzz records instead of scraping a
	// subprocess's `-o json` stdout. modules() lists every module {name, doc, fields,
	// methods}; module(name) returns one with fields + per-method Buzz signatures, and
	// raises on an unknown name. Hand-written (not declarative) because the core uses
	// host, which std can't import.
	magus.MapSet("modules", vm.DirectValue("magus.modules", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return host.RecordsVal(host.ModulesOutput("").Modules), nil
	}))
	magus.MapSet("module", vm.DirectValue("magus.module", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		name := ""
		if len(args) > 0 && args[0].IsStr() {
			name = args[0].AsString()
		}
		out := host.ModulesOutput(name)
		if len(out.Modules) == 0 {
			return vm.Null, fmt.Errorf("magus.module: unknown module %q", name)
		}
		return host.AnyMapVal(out.Modules[0].Record()), nil
	}))

	// Logging on the magus namespace itself (magus.info/debug/warn/error): the one
	// way to log from a magusfile — there is no separate std log module. Each level
	// writes into the process slog logger via emitMagusLog.
	magus.MapSet("info", vm.DirectValue("magus.info", buzzLogFn(slog.LevelInfo)))
	magus.MapSet("debug", vm.DirectValue("magus.debug", buzzLogFn(slog.LevelDebug)))
	magus.MapSet("warn", vm.DirectValue("magus.warn", buzzLogFn(slog.LevelWarn)))
	magus.MapSet("error", vm.DirectValue("magus.error", buzzLogFn(slog.LevelError)))

	// magus.hint(msg): advisory nudge (see emitMagusHint) — non-fatal, deduped,
	// honors the hints toggle.
	magus.MapSet("hint", vm.DirectValue("magus.hint", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) > 0 && args[0].IsStr() {
			emitMagusHint(args[0].AsString())
		}
		return vm.Null, nil
	}))
	// magus.fatal(msg): log at error level, then abort with exit 1 via a typed
	// ExitError (the CLI/daemon map it to the exit status).
	magus.MapSet("fatal", vm.DirectValue("magus.fatal", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		emitMagusLog(ctx, slog.LevelError, argStr(args, 0), nil)
		types.RecordExit(ctx, 1)
		return vm.Null, types.ExitError{Code: 1}
	}))
	sess.SetGlobal("magus", magus)

	// The host utilities are reached under the same bare names as Buzz's own
	// stdlib: `import "os"`, `import "fs"`, `import "http"`, `import "vcs"`, … A
	// magusfile selects methods off each module directly (os.exec, fs.glob,
	// vcs.shortHash). registerHostModules layers the magus host methods onto
	// Buzz's stdlib modules — a superset surface — and is shared with
	// spell-loading, so a magusfile and a handler op spell see the same modules.
	registerHostModules(ctx, sess)
	// Built-in spells follow the same import idiom as std modules: each spell is
	// reachable as `import "magus/spell/<name>"`, binding the spell handle under
	// its basename.
	builtins := ispell.Builtins()
	for name := range builtins {
		sess.SetSyntheticModule("magus/spell/"+name, buzzSpellObject(name))
	}
	// Host-registered spells (the magusfile spell in internal/interp/magusfile.go,
	// and any spell a plugin registers at runtime) aren't compiled built-ins, so the
	// loop above doesn't reach them; expose each under the same import idiom so a
	// project can bind it via `import "magus/spell/<name>"`. The handle carries only
	// the name; magus.project resolves the spec by name from the host
	// registry.
	for _, sp := range project.DefaultSpellRegistry().All() {
		if _, isBuiltin := builtins[sp.Name()]; isBuiltin {
			continue
		}
		sess.SetSyntheticModule("magus/spell/"+sp.Name(), buzzSpellObject(sp.Name()))
	}
	// Workspace-local spells are imported by path: `import "spells/hello"` resolves
	// ./spells/hello.buzz on demand and binds its handle under the basename
	// (hello), and the handle registers by value when bound via
	// magus.project.
	// Cross-project target imports: `import "project/<path>" as <alias>` binds a
	// module whose members are the other project's targets as cross-project handles,
	// so `magus.needs(<alias>.<target>)` declares a target-level dependency across the
	// project boundary (a typo in the target name fails at load, not at run time).
	sess.SetModuleResolver(func(importPath string) (vm.Value, bool) {
		if v, ok := resolveProjectImport(ctx, importPath); ok {
			return v, true
		}
		return resolveLocalSpellImport(ctx, importPath)
	})
}
