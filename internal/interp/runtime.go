package interp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/interp/engine"
	buzzengine "github.com/egladman/magus/internal/interp/engine/buzz"
	lua "github.com/egladman/magus/internal/interp/engine/lua"
	teal "github.com/egladman/magus/internal/interp/engine/lua/teal"
	"github.com/egladman/magus/internal/run"
	"github.com/egladman/magus/types"
)

// chdirMu serializes all os.Chdir calls: the process working directory is
// global state and concurrent changes corrupt relative path resolution.
var chdirMu sync.Mutex

type sourceCtxKey struct{}
type normCtxKey struct{}

// WithSource stores src in ctx so that bindings (e.g. magus.dispatch) can
// retrieve the active magusfile source for pool lookup.
func WithSource(ctx context.Context, src *Source) context.Context {
	return context.WithValue(ctx, sourceCtxKey{}, src)
}

// SourceFromContext retrieves the Source stored by WithSource, or nil.
func SourceFromContext(ctx context.Context) *Source {
	v, _ := ctx.Value(sourceCtxKey{}).(*Source)
	return v
}

// WithTargetNameNormalizer stores n in ctx for use by target registration and lookup.
func WithTargetNameNormalizer(ctx context.Context, n types.TargetNameNormalizer) context.Context {
	return context.WithValue(ctx, normCtxKey{}, n)
}

// targetNameNormalizerFrom returns the normalizer stored in ctx, or DefaultTargetNameNormalizer.
func targetNameNormalizerFrom(ctx context.Context) types.TargetNameNormalizer {
	if n, ok := ctx.Value(normCtxKey{}).(types.TargetNameNormalizer); ok && n != nil {
		return n
	}
	return types.DefaultTargetNameNormalizer
}

// Source describes a located magusfile source.
type Source struct {
	Dir    string   // absolute directory containing the magusfile
	Files  []string // absolute source paths in load order
	Engine string   // "lua" or "buzz"; inferred from file extensions by Find
}

// Target is a single invocable target discovered in a magusfile.
type Target struct {
	Key  string // lowercase dispatch identifier
	Name string // mixed-case display name
}

// Run compiles each file in src, executes them on a fresh VM with host
// bindings registered, then invokes the named target.
func Run(ctx context.Context, src *Source, target string, extraArgs []string, workDir string) error {
	if src.Engine == "buzz" {
		return runBuzz(ctx, src, target, extraArgs, workDir)
	}

	if workDir != "" {
		chdirMu.Lock()
		cwd, err := os.Getwd()
		if err != nil {
			chdirMu.Unlock()
			return fmt.Errorf("magusfile: getwd: %w", err)
		}
		if err := os.Chdir(workDir); err != nil {
			chdirMu.Unlock()
			return fmt.Errorf("magusfile: chdir %s: %w", workDir, err)
		}
		defer func() {
			if err := os.Chdir(cwd); err != nil {
				slog.Warn("interp: chdir restore failed", slog.String("dir", cwd), slog.String("error", err.Error()))
			}
			chdirMu.Unlock()
		}()
	}

	// Capture an os.exit / magus.fatal code out-of-band: the Lua engines raise
	// host errors as Lua strings, so the typed ExitError doesn't survive the VM
	// boundary. Adding the recorder before NewLuaSession means it's on the runner's
	// context (SetContext) and reaches the host Impl.
	ctx, exitCode := types.WithExitRecorder(ctx)

	r, err := NewLuaSession(ctx)
	if err != nil {
		return fmt.Errorf("magusfile: new vm: %w", err)
	}
	defer func() { _ = r.Close() }()
	teal.InstallUtf8Shim(r)

	if hostBindingsFn == nil {
		return fmt.Errorf("magusfile: no host bindings registered; blank-import interp/bindings")
	}
	if err := hostBindingsFn(r, false); err != nil {
		return fmt.Errorf("magusfile: register host bindings: %w", err)
	}
	if err := teal.EnsureCompiler(r); err != nil {
		return fmt.Errorf("magusfile: load teal compiler: %w", err)
	}

	norm := targetNameNormalizerFrom(ctx)
	preGlobals := captureGlobalNames(r)

	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	ctx = WithSource(ctx, src)
	ctx = run.WithStepRepl(ctx, func(replCtx context.Context, name string, args []string, dir string) error {
		argsTable := r.NewTable()
		for i, a := range args {
			argsTable.RawSetInt(i+1, engine.StringValue(a))
		}
		locals := map[string]engine.Value{
			"name": engine.StringValue(name),
			"args": argsTable,
			"dir":  engine.StringValue(dir),
		}
		return Repl(replCtx, r, ReplOptions{
			WorkDir: workDir,
			Locals:  locals,
			Banner:  fmt.Sprintf("-- step repl: %s (dir: %s)", name, dir),
		})
	})

	for _, path := range src.Files {
		if err := compileAndExec(ctx, r, path); err != nil {
			return err
		}
	}

	if err := registerGlobalFunctionTargets(r, preGlobals, norm); err != nil {
		return err
	}

	registry, ok := r.GetGlobal("_magus_targets").AsTable()
	if !ok {
		return fmt.Errorf("magusfile: no targets registered")
	}
	key := norm.NormalizeTargetName(target)
	fn := registry.RawGetString(key)
	if fn.IsNil() {
		var names []string
		registry.ForEach(func(k, _ engine.Value) {
			names = append(names, k.String())
		})
		slices.Sort(names)
		return fmt.Errorf("magusfile: unknown target %q (registered: %s): %w",
			target, strings.Join(names, ", "), ErrUnknownTarget)
	}
	argsTable := r.NewTable()
	for i, a := range extraArgs {
		argsTable.RawSetInt(i+1, engine.StringValue(a))
	}
	err = r.Call(engine.CallParams{Fn: fn, NRet: 0, Protect: true}, argsTable)
	if code, ok := exitCode(); ok {
		return types.ExitError{Code: code}
	}
	if err != nil {
		return fmt.Errorf("magusfile: target %s: %w", target, err)
	}
	return nil
}

// RunDir runs target for the project in dir, trying each engine in order and falling
// back on ErrUnknownTarget. Returns ErrNoMagusfile or ErrUnknownTarget when not found.
//
// Each tried engine's source is fully executed (including top-level declarations
// such as magus.project.register) before its target registry is consulted, so when
// a target lives in a non-primary engine the earlier engine's source still runs.
// In the common single-engine directory this is a single execution; mixing engines
// in one directory is discouraged for this reason.
func RunDir(ctx context.Context, dir, target string, extraArgs []string) error {
	srcs, err := FindAll(dir)
	if err != nil {
		return err
	}

	if len(srcs) > 1 && slog.Default().Enabled(ctx, slog.LevelDebug) {
		warnCrossEngineShadow(ctx, srcs, target)
	}

	for _, src := range srcs {
		err = Run(ctx, src, target, extraArgs, dir)
		if errors.Is(err, ErrUnknownTarget) {
			continue
		}
		return err
	}
	return ErrUnknownTarget
}

// warnCrossEngineShadow logs a debug warning when target is declared in multiple engines.
func warnCrossEngineShadow(ctx context.Context, srcs []*Source, target string) {
	norm := targetNameNormalizerFrom(ctx)
	normalizedTarget := norm.NormalizeTargetName(target)
	var declarers []string
	for _, src := range srcs {
		targets, err := Parse(ctx, src)
		if err != nil {
			continue
		}
		for _, t := range targets {
			if t.Key == normalizedTarget {
				declarers = append(declarers, src.Engine)
				break
			}
		}
	}
	if len(declarers) > 1 {
		slog.DebugContext(ctx, "interp: cross-engine target shadowing",
			slog.String("target", target),
			slog.String("winner", declarers[0]),
			slog.String("shadowed", strings.Join(declarers[1:], ",")),
		)
	}
}

// Parse executes src in parse mode (stubs magus.target) and returns discovered targets.
func Parse(ctx context.Context, src *Source) ([]Target, error) {
	// Carry the source so bindings resolve paths relative to the magusfile's own
	// directory (local-spell require/import), not the process cwd — the same
	// context Run establishes. Without this, preloading a magusfile from outside
	// its dir fails to find its ./spells.
	ctx = WithSource(ctx, src)
	if src.Engine == "buzz" {
		return parseBuzz(ctx, src)
	}

	r, err := NewLuaSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("magusfile: parse: new vm: %w", err)
	}
	defer func() { _ = r.Close() }()
	teal.InstallUtf8Shim(r)

	if hostBindingsFn == nil {
		return nil, fmt.Errorf("magusfile: no host bindings registered; blank-import interp/bindings")
	}
	if err := hostBindingsFn(r, true); err != nil {
		return nil, fmt.Errorf("magusfile: parse: register bindings: %w", err)
	}
	norm := targetNameNormalizerFrom(ctx)
	preGlobals := captureGlobalNames(r)
	for _, path := range src.Files {
		if err := compileAndExec(ctx, r, path); err != nil {
			return nil, err
		}
	}
	if err := registerGlobalFunctionTargets(r, preGlobals, norm); err != nil {
		return nil, err
	}

	var names []string
	if registry, ok := r.GetGlobal("_magus_targets").AsTable(); ok {
		registry.ForEach(func(k, _ engine.Value) {
			if str, ok := k.AsString(); ok {
				names = append(names, str)
			}
		})
	}
	slices.Sort(names)

	targets := make([]Target, len(names))
	for i, name := range names {
		targets[i] = Target{
			Key:  name,
			Name: name,
		}
	}
	return targets, nil
}

// InstallReplPrelude sets up r for a REPL session (utf8 shim, bindings, Teal compiler).
func InstallReplPrelude(_ context.Context, r lua.Session) error {
	teal.InstallUtf8Shim(r)
	if hostBindingsFn == nil {
		return fmt.Errorf("interp: no host bindings registered; blank-import interp/bindings")
	}
	if err := hostBindingsFn(r, false); err != nil {
		return fmt.Errorf("interp: register host bindings: %w", err)
	}
	if err := teal.LoadCompiler(r); err != nil {
		return err
	}
	return nil
}

// CompileFile compiles path from Teal to Lua using the disk cache.
// Caller must have loaded the Teal compiler.
func CompileFile(ctx context.Context, r lua.Session, path string) ([]byte, error) {
	return compileTeal(ctx, r, path, false)
}

// compileAndExec compiles path and executes it on r (Teal compiler loaded lazily).
func compileAndExec(ctx context.Context, r lua.Session, path string) error {
	compiled, err := compileTeal(ctx, r, path, true)
	if err != nil {
		return err
	}
	if err := r.DoString(string(compiled)); err != nil {
		return fmt.Errorf("magusfile: exec %s: %w", path, err)
	}
	return nil
}

// compileTeal compiles path from Teal to Lua with disk caching.
// lazyCompiler=true calls teal.EnsureCompiler on miss; false assumes it is already loaded.
func compileTeal(ctx context.Context, r lua.Session, path string, lazyCompiler bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("magusfile: compile %s: %w", path, err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("magusfile: read %s: %w", path, err)
	}
	preamble, err := teal.TypeDecls()
	if err != nil {
		return nil, err
	}
	combined := teal.ConcatPreamble(preamble, source)
	key := cacheKey(combined)
	if compiled, hit := lookup(key); hit {
		return compiled, nil
	}
	if lazyCompiler {
		if err := teal.EnsureCompiler(r); err != nil {
			return nil, fmt.Errorf("magusfile: load teal compiler: %w", err)
		}
	}
	compiled, err := teal.Compile(r, path, combined)
	if err != nil {
		return nil, err
	}
	if err := store(key, compiled); err != nil {
		slog.Warn("interp: teal cache write failed", slog.String("error", err.Error()))
	}
	return compiled, nil
}

// ExecSource compiles and executes src on r under chdirMu, populating _magus_targets.
// This is the pool-worker VM startup sequence.
func ExecSource(ctx context.Context, r lua.Session, src *Source) error {
	if src.Dir != "" {
		chdirMu.Lock()
		cwd, err := os.Getwd()
		if err != nil {
			chdirMu.Unlock()
			return fmt.Errorf("pool-worker: getwd: %w", err)
		}
		if err := os.Chdir(src.Dir); err != nil {
			chdirMu.Unlock()
			return fmt.Errorf("pool-worker: chdir %s: %w", src.Dir, err)
		}
		defer func() {
			if err := os.Chdir(cwd); err != nil {
				slog.Warn("interp: chdir restore failed", slog.String("dir", cwd), slog.String("error", err.Error()))
			}
			chdirMu.Unlock()
		}()
	}
	norm := targetNameNormalizerFrom(ctx)
	preGlobals := captureGlobalNames(r)
	for _, path := range src.Files {
		if err := compileAndExec(ctx, r, path); err != nil {
			return err
		}
	}
	return registerGlobalFunctionTargets(r, preGlobals, norm)
}

// BuzzHostBindingsFn registers Go-backed host modules into a Buzz session.
// parseMode=true stubs magus.target to collect names only.
type BuzzHostBindingsFn func(ctx context.Context, sess *buzz.Session, targets map[string]buzz.Callable, parseMode bool)

var buzzHostBindingsFn BuzzHostBindingsFn

// RegisterBuzzHostBindings stores the Buzz host-binding function. Called from bindings init().
func RegisterBuzzHostBindings(fn BuzzHostBindingsFn) {
	if buzzHostBindingsFn != nil {
		panic("interp: Buzz host bindings already registered")
	}
	buzzHostBindingsFn = fn
}

// runBuzz executes src on a fresh Buzz session and invokes target.
func runBuzz(ctx context.Context, src *Source, target string, extraArgs []string, workDir string) error {
	if workDir != "" {
		chdirMu.Lock()
		cwd, err := os.Getwd()
		if err != nil {
			chdirMu.Unlock()
			return fmt.Errorf("magusfile: getwd: %w", err)
		}
		if err := os.Chdir(workDir); err != nil {
			chdirMu.Unlock()
			return fmt.Errorf("magusfile: chdir %s: %w", workDir, err)
		}
		defer func() {
			if err := os.Chdir(cwd); err != nil {
				slog.Warn("interp: chdir restore failed", slog.String("dir", cwd), slog.String("error", err.Error()))
			}
			chdirMu.Unlock()
		}()
	}

	buzzSess, targetMap, err := execBuzzSrc(ctx, src, false)
	if err != nil {
		return err
	}
	defer func() { _ = buzzSess.Close() }()

	norm := targetNameNormalizerFrom(ctx)
	key := norm.NormalizeTargetName(target)
	fn, ok := targetMap[key]
	if !ok {
		var names []string
		for k := range targetMap {
			names = append(names, k)
		}
		slices.Sort(names)
		return fmt.Errorf("magusfile: unknown target %q (registered: %s): %w",
			target, strings.Join(names, ", "), ErrUnknownTarget)
	}
	buzzArgs := make([]buzz.Value, len(extraArgs))
	for i, s := range extraArgs {
		buzzArgs[i] = buzz.StrValue(s)
	}
	ctx, exitCode := types.WithExitRecorder(ctx)
	ctx = WithSource(ctx, src)
	_, err = fn(ctx, buzzArgs)
	if code, ok := exitCode(); ok {
		return types.ExitError{Code: code}
	}
	if err != nil {
		return fmt.Errorf("magusfile: target %s: %w", target, err)
	}
	return nil
}

// parseBuzz executes src in parse mode to collect target names.
func parseBuzz(ctx context.Context, src *Source) ([]Target, error) {
	buzzSess, targetMap, err := execBuzzSrc(ctx, src, true)
	if err != nil {
		return nil, err
	}
	_ = buzzSess.Close()

	var names []string
	for name := range targetMap {
		names = append(names, name)
	}
	slices.Sort(names)

	out := make([]Target, len(names))
	for i, name := range names {
		out[i] = Target{Key: name, Name: name}
	}
	return out, nil
}

// captureGlobalNames returns a snapshot of all names currently in _G.
func captureGlobalNames(r lua.Session) map[string]struct{} {
	g := r.GetGlobal("_G")
	tbl, ok := g.AsTable()
	if !ok {
		return nil
	}
	names := make(map[string]struct{})
	tbl.ForEach(func(k, _ engine.Value) {
		if s, ok := k.AsString(); ok {
			names[s] = struct{}{}
		}
	})
	return names
}

// NormalizeTarget normalizes a target name using the TargetNameNormalizer stored
// in ctx (falls back to DefaultTargetNameNormalizer). Used by the pool to
// normalize submitted target names before looking them up in the targets map.
func NormalizeTarget(ctx context.Context, name string) string {
	return targetNameNormalizerFrom(ctx).NormalizeTargetName(name)
}

// targetCollisionErr reports two source target names that normalize to the same
// canonical key. Shared by the Teal and Buzz registration paths so the message
// stays identical across engines.
func targetCollisionErr(prev, cur, key string) error {
	return fmt.Errorf("magusfile: targets %q and %q both normalize to %q; "+
		"target names are matched case- and delimiter-insensitively, so rename one", prev, cur, key)
}

// registerGlobalFunctionTargets scans _G for function-valued globals added after
// the pre-snapshot and registers them in _magus_targets. Discovers targets declared
// as `global function name(args:{string}) ...` in Teal magusfiles.
func registerGlobalFunctionTargets(r lua.Session, pre map[string]struct{}, norm types.TargetNameNormalizer) error {
	if pre == nil {
		return nil
	}
	g := r.GetGlobal("_G")
	tbl, ok := g.AsTable()
	if !ok {
		return nil
	}
	registry, ok := r.GetGlobal("_magus_targets").AsTable()
	if !ok {
		registry = r.NewTable()
		r.SetGlobal("_magus_targets", registry)
	}
	// Collect candidate (source name, fn) pairs first, then process them in
	// sorted order: this makes collision detection deterministic regardless of
	// _G iteration order. All of a project's files are executed before this runs
	// (see Run), so one pass sees every declared target.
	type candidate struct {
		src string
		fn  engine.Value
	}
	var candidates []candidate
	tbl.ForEach(func(k, v engine.Value) {
		s, ok := k.AsString()
		if !ok {
			return
		}
		if _, wasPreexisting := pre[s]; wasPreexisting {
			return
		}
		fn, ok := v.AsFunction()
		if !ok {
			return
		}
		candidates = append(candidates, candidate{src: s, fn: fn})
	})
	slices.SortFunc(candidates, func(a, b candidate) int { return strings.Compare(a.src, b.src) })

	seen := make(map[string]string, len(candidates)) // canonical key → source name
	for _, c := range candidates {
		key := norm.NormalizeTargetName(c.src)
		if err := types.ValidateTargetName(key); err != nil {
			slog.Debug("interp: skipping invalid global function name as target",
				slog.String("name", c.src), slog.String("error", err.Error()))
			continue
		}
		// The normalizer is many-to-one (go_build, goBuild, GoBuild → go-build),
		// so two functions that normalize to the same canonical name are a hard
		// error rather than a silent last-write-wins clobber.
		if prev, dup := seen[key]; dup {
			return targetCollisionErr(prev, c.src, key)
		}
		seen[key] = c.src
		registry.RawSetString(key, c.fn)
	}
	return nil
}

// execBuzzSrc creates a Buzz Session, registers bindings, and executes source files.
func execBuzzSrc(ctx context.Context, src *Source, parseMode bool) (*buzz.Session, map[string]buzz.Callable, error) {
	// The buzz path uses the standalone interpreter's concrete API (Exec,
	// Targets, CallVal) directly; the engine.Session adapter is only for generic
	// registry consumers, so there's no need to round-trip through engine.Lookup.
	buzzSess := buzz.NewSession(ctx)
	// Override the env-var-derived include dirs with a workspace-sandboxed set
	// so scripts cannot escape the project root via BUZZ_INCLUDE_PATH.
	buzzSess.SetIncludeDirs(sandboxIncludeDirs(src.Dir))

	targetMap := buzzSess.Targets()
	if buzzHostBindingsFn != nil {
		buzzHostBindingsFn(ctx, buzzSess, targetMap, parseMode)
	}

	for _, path := range src.Files {
		data, err := os.ReadFile(path)
		if err != nil {
			_ = buzzSess.Close()
			return nil, nil, fmt.Errorf("magusfile: read %s: %w", path, err)
		}
		if err := buzzSess.Exec(ctx, string(data)); err != nil {
			_ = buzzSess.Close()
			return nil, nil, fmt.Errorf("magusfile: exec %s: %w", path, err)
		}
	}

	// Discover targets from exported functions (export fun name ...). The
	// normalizer is many-to-one (go_build, goBuild, GoBuild → go-build), so two
	// exports that normalize to the same canonical name are a hard error rather
	// than a silent last-write-wins clobber. Iterate in sorted order so the
	// reported pair is deterministic.
	norm := targetNameNormalizerFrom(ctx)
	exports := buzzSess.Exports()
	names := make([]string, 0, len(exports))
	for name := range exports {
		names = append(names, name)
	}
	slices.Sort(names)
	seen := make(map[string]string, len(names)) // canonical key → source name
	for _, name := range names {
		val := exports[name]
		if !val.IsFun() {
			continue
		}
		key := norm.NormalizeTargetName(name)
		if prev, dup := seen[key]; dup {
			_ = buzzSess.Close()
			return nil, nil, targetCollisionErr(prev, name, key)
		}
		seen[key] = name
		captured := val
		targetMap[key] = func(ctx context.Context, args []buzz.Value) (buzz.Value, error) {
			return buzzSess.CallValue(ctx, captured, args)
		}
	}

	return buzzSess, targetMap, nil
}

// NewBuzzWorkerFactory returns a WorkerFactory that creates a pre-warmed Buzz
// session for src. The factory is safe to call from multiple goroutines because
// execBuzzSrc reads sources by absolute path and does not acquire chdirMu.
func NewBuzzWorkerFactory(src *Source) buzz.WorkerFactory {
	return func(ctx context.Context) (*buzz.Session, map[string]buzz.Callable, error) {
		return execBuzzSrc(ctx, src, false)
	}
}

// NewBuzzReplSession creates a Buzz session with host bindings installed, ready
// for the shared REPL. When autoloadDir is non-empty and a magusfile.bzz is
// found in or above it, its files are executed first so their top-level
// definitions are available at the prompt (mirroring the Lua repl autoload).
// The returned engine.Session also satisfies the optional REPL/debug interfaces.
func NewBuzzReplSession(ctx context.Context, autoloadDir string) (engine.Session, error) {
	buzzSess := buzz.NewSession(ctx)
	buzzSess.SetIncludeDirs(sandboxIncludeDirs(autoloadDir))
	if buzzHostBindingsFn != nil {
		buzzHostBindingsFn(ctx, buzzSess, buzzSess.Targets(), false)
	}

	if autoloadDir != "" {
		if src, err := Find(autoloadDir); err == nil && src.Engine == "buzz" {
			for _, path := range src.Files {
				data, rerr := os.ReadFile(path)
				if rerr != nil {
					_ = buzzSess.Close()
					return nil, fmt.Errorf("repl: read %s: %w", path, rerr)
				}
				if eerr := buzzSess.Exec(ctx, string(data)); eerr != nil {
					_ = buzzSess.Close()
					return nil, fmt.Errorf("repl: autoload %s: %w", path, eerr)
				}
			}
		} else if err != nil && !errors.Is(err, ErrNoMagusfile) {
			slog.Warn("interp: buzz repl autoload find failed", slog.String("error", err.Error()))
		}
	}

	return buzzengine.Wrap(buzzSess), nil
}

// sandboxIncludeDirs reads BUZZ_INCLUDE_PATH and returns only the entries that
// fall within root, preventing scripts from importing files outside the
// workspace. Entries that cannot be resolved or escape via ".." are logged and
// dropped. Returns nil when root is empty or BUZZ_INCLUDE_PATH is unset.
func sandboxIncludeDirs(root string) []string {
	if root == "" {
		return nil
	}
	v := os.Getenv("BUZZ_INCLUDE_PATH")
	if v == "" {
		return nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, dir := range filepath.SplitList(v) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			slog.Warn("interp: BUZZ_INCLUDE_PATH entry could not be resolved, skipping",
				slog.String("dir", dir), slog.String("error", err.Error()))
			continue
		}
		rel, err := filepath.Rel(absRoot, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			slog.Warn("interp: BUZZ_INCLUDE_PATH entry outside workspace, skipping",
				slog.String("dir", dir), slog.String("workspace", absRoot))
			continue
		}
		out = append(out, abs)
	}
	return out
}
