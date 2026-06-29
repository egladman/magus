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

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/ast"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/internal/interp/engine"
	buzzengine "github.com/egladman/magus/internal/interp/engine/buzz"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

type sourceCtxKey struct{}
type normCtxKey struct{}

type projectPathCtxKey struct{}

// WithSource stores src in ctx so that bindings (e.g. magus.needs) can
// retrieve the active magusfile source for pool lookup.
func WithSource(ctx context.Context, src *Source) context.Context {
	return context.WithValue(ctx, sourceCtxKey{}, src)
}

// SourceFromContext retrieves the Source stored by WithSource, or nil.
func SourceFromContext(ctx context.Context) *Source {
	v, _ := ctx.Value(sourceCtxKey{}).(*Source)
	return v
}

// WithProjectPath stores the workspace-relative path of the project whose
// magusfile is being parsed, so magus.project(fn) — the contextual
// form with no explicit path — can default to "this project".
func WithProjectPath(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, projectPathCtxKey{}, path)
}

// ProjectPathFromContext returns the project path stored by WithProjectPath, and
// whether one was set.
func ProjectPathFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(projectPathCtxKey{}).(string)
	return v, ok
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
	Engine string   // engine name ("buzz"); inferred from file extensions by Find
}

// Target is a single invocable target discovered in a magusfile.
type Target struct {
	Key  string // lowercase dispatch identifier
	Name string // mixed-case display name
}

// Run compiles each file in src, executes them on a fresh Buzz session with host
// bindings registered, then invokes the named target.
func Run(ctx context.Context, src *Source, target string, extraArgs []string, workDir string) error {
	return runBuzz(ctx, src, target, extraArgs, workDir)
}

// RunDir runs target for the project in dir. Returns ErrNoMagusfile or
// ErrUnknownTarget when not found.
//
// Buzz is the only engine today, so FindAll yields a single source and this
// resolves to one execution. The loop is the seam a second engine would extend:
// each source is fully executed (including top-level declarations such as
// magus.project) before its target registry is consulted, and an unknown target
// falls through to the next source.
func RunDir(ctx context.Context, dir, target string, extraArgs []string) error {
	srcs, err := FindAll(dir)
	if err != nil {
		return err
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

// Parse executes src in parse mode (stubs magus.target) and returns discovered targets.
func Parse(ctx context.Context, src *Source) ([]Target, error) {
	// Carry the source so bindings resolve paths relative to the magusfile's own
	// directory (local-spell require/import), not the process cwd — the same
	// context Run establishes. Without this, preloading a magusfile from outside
	// its dir fails to find its ./spells.
	ctx = WithSource(ctx, src)
	return parseBuzz(ctx, src)
}

// BuzzHostBindingsFn registers Go-backed host modules into a Buzz session.
// parseMode=true stubs magus.target to collect names only.
type BuzzHostBindingsFn func(ctx context.Context, sess *buzz.Session, targets map[string]vm.Callable, parseMode bool)

var buzzHostBindingsFn BuzzHostBindingsFn

// RegisterBuzzHostBindings stores the Buzz host-binding function. Called from bindings init().
func RegisterBuzzHostBindings(fn BuzzHostBindingsFn) {
	if buzzHostBindingsFn != nil {
		panic("interp: Buzz host bindings already registered")
	}
	buzzHostBindingsFn = fn
}

// buzzSpellImportCheckFn validates the handles a magusfile imports via
// `import "magus/spell/<handle>"`. The bindings package owns the spell registry,
// so it supplies the check; nil until it registers one. See
// RegisterBuzzSpellImportCheck and spellImportNames.
var buzzSpellImportCheckFn func(handles []string) error

// RegisterBuzzSpellImportCheck stores the validator for `magus/spell/*` imports.
// Called from bindings init(), the same seam as RegisterBuzzHostBindings.
func RegisterBuzzSpellImportCheck(fn func(handles []string) error) {
	if buzzSpellImportCheckFn != nil {
		panic("interp: Buzz spell-import check already registered")
	}
	buzzSpellImportCheckFn = fn
}

// spellImportNames returns the <handle> of every `import "magus/spell/<handle>"`
// statement in src. It reads the parsed AST, not the raw text, so a commented-out
// or string-literal import never counts.
//
// It parses with ParseEmbedded — the same lenient mode magusfiles load under
// (Exec uses WithEmbedded). Strict buzz.Parse rejects top-level statements, which
// real magusfiles use freely (the repo's own has a top-level `if`), so parsing
// strict here would error and silently skip the check on exactly those files.
// A parse error still yields nil: Exec re-parses and reports the real syntax error
// with position. The substring gate skips the parse entirely for the common
// magusfile that imports no spell — Exec is about to parse the source anyway.
func spellImportNames(src string) []string {
	if !strings.Contains(src, "magus/spell/") {
		return nil
	}
	prog, err := buzz.ParseEmbedded(src)
	if err != nil {
		return nil
	}
	var handles []string
	for _, stmt := range prog.Stmts {
		imp, ok := stmt.(*ast.ImportStmt)
		if !ok {
			continue
		}
		if handle, ok := strings.CutPrefix(imp.Path, "magus/spell/"); ok {
			handles = append(handles, handle)
		}
	}
	return handles
}

// runBuzz executes src on a fresh Buzz session and invokes target.
func runBuzz(ctx context.Context, src *Source, target string, extraArgs []string, workDir string) error {
	// Carry the target's directory on the context instead of os.Chdir-ing the whole
	// process. The host modules (std.*) resolve relative paths against this cwd, so
	// magusfile targets across projects — including a cross-project magus.needs that
	// re-enters the interpreter — execute concurrently without corrupting a shared
	// process working directory.
	if workDir != "" {
		ctx = std.WithCwd(ctx, workDir)
	}
	slog.DebugContext(ctx, "interp: run magusfile target", "target", target, "dir", workDir)

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
	buzzArgs := make([]vm.Value, len(extraArgs))
	for i, s := range extraArgs {
		buzzArgs[i] = vm.StrValue(s)
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

// targetCollisionErr reports two source target names that normalize to the same
// canonical key. Used by the Buzz registration path so the message
// stays identical across engines.
func targetCollisionErr(prev, cur, key string) error {
	return fmt.Errorf("magusfile: targets %q and %q both normalize to %q; "+
		"target names are matched case- and delimiter-insensitively, so rename one", prev, cur, key)
}

// execBuzzSrc creates a Buzz Session, registers bindings, and executes source files.
func execBuzzSrc(ctx context.Context, src *Source, parseMode bool) (*buzz.Session, map[string]vm.Callable, error) {
	// Carry the magusfile source on the context for the whole load, so the module
	// resolver registered below (which captures this ctx) can resolve top-level
	// `import "project/<path>"` cross-project handles during execution. Run mode
	// otherwise reaches here with a nil source — runBuzz sets it only for target
	// dispatch — and such an import would resolve to nothing. Parse mode already sets
	// it upstream; re-setting is idempotent.
	ctx = WithSource(ctx, src)
	// The buzz path uses the standalone interpreter's concrete API (Exec,
	// Targets, CallVal) directly; the engine.Session adapter is only for generic
	// registry consumers, so there's no need to round-trip through engine.Lookup.
	// Confine imports to the magusfiles layout (see magusSearchPaths); WithSearchPaths
	// replaces gopherbuzz's upstream default so a magusfile resolves siblings the same
	// way regardless of the process cwd, and cannot escape via BUZZ_INCLUDE_PATH.
	buzzSess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithSearchPaths(magusSearchPaths(ctx, src.Dir)...))
	// NewSession seeds includeDirs from BUZZ_INCLUDE_PATH; clear them so resolution
	// stays limited to the magusfiles search paths above.
	buzzSess.SetIncludeDirs(nil)
	// Magusfiles run as whole files, not incrementally, so a non-exported,
	// non-captured top-level var is chunk-private and can use a fast stack slot
	// instead of an Env binding. The cross-file/cross-target surface is `export`ed
	// functions, which stay Env-bound. The REPL (NewBuzzReplSession) deliberately
	// does not enable this — there a later line must resolve earlier names.
	buzzSess.SetPromoteTopLevel(true)

	targetMap := buzzSess.Targets()
	if buzzHostBindingsFn != nil {
		buzzHostBindingsFn(ctx, buzzSess, targetMap, parseMode)
	}

	for _, path := range src.Files {
		// rel names the offending file relative to its project dir, so a magusfiles/
		// directory (several files) is unambiguous; the project itself is already in
		// the surrounding "preload <project>" context. path is built from src.Dir, so
		// Rel is a pure-lexical relativize with no I/O and no symlink/escape pitfalls.
		rel, relErr := filepath.Rel(src.Dir, path)
		if relErr != nil {
			rel = path
		}
		data, err := os.ReadFile(path)
		if err != nil {
			_ = buzzSess.Close()
			return nil, nil, fmt.Errorf("magusfile: read %s: %w", rel, err)
		}
		code := string(data)
		// Validate spell handles before Exec: an unknown `magus/spell/<handle>`
		// resolves to nothing (gopherbuzz skips an unresolved import) and would
		// otherwise surface much later as a disconnected "undefined" error. Fail
		// fast, naming the file, with a did-you-mean instead.
		if buzzSpellImportCheckFn != nil {
			if err := buzzSpellImportCheckFn(spellImportNames(code)); err != nil {
				_ = buzzSess.Close()
				return nil, nil, fmt.Errorf("magusfile: %s: %w", rel, err)
			}
		}
		if err := buzzSess.Exec(ctx, code); err != nil {
			_ = buzzSess.Close()
			return nil, nil, fmt.Errorf("magusfile: exec %s: %w", rel, err)
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
		targetMap[key] = func(ctx context.Context, args []vm.Value) (vm.Value, error) {
			return buzzSess.CallValue(ctx, captured, args)
		}
	}

	return buzzSess, targetMap, nil
}

// NewBuzzWorkerFunc returns the buzz.WorkerFunc that creates a pre-warmed Buzz
// session for src. The returned WorkerFunc is safe to call from multiple goroutines because
// execBuzzSrc reads sources by absolute path and does not acquire chdirMu.
func NewBuzzWorkerFunc(src *Source) buzz.WorkerFunc {
	return func(ctx context.Context) (*buzz.Session, map[string]vm.Callable, error) {
		return execBuzzSrc(ctx, src, false)
	}
}

// NewBuzzReplSession creates a Buzz session with host bindings installed, ready
// for the shared REPL. When autoloadDir is non-empty and a magusfile.buzz is
// found in or above it, its files are executed first so their top-level
// definitions are available at the prompt.
// The returned engine.Session also satisfies the optional REPL/debug interfaces.
func NewBuzzReplSession(ctx context.Context, autoloadDir string) (engine.Session, error) {
	buzzSess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithSearchPaths(magusSearchPaths(ctx, autoloadDir)...))
	buzzSess.SetIncludeDirs(nil)
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

// magusSearchPaths returns the import search path templates a magusfile resolves
// against (see buzz.WithSearchPaths). magus deliberately does not adopt gopherbuzz's
// upstream default; an `import "<name>"` resolves only to a magusfiles/ sibling,
// looked up — in order — relative to the process cwd, the project root, and the
// workspace root. `?` is the import name (filled in by the resolver). The
// workspace root is read from ctx (types.WithWorkspace) and is omitted when absent
// or identical to the project dir, so the common single-project case yields no
// duplicate entry.
func magusSearchPaths(ctx context.Context, projectDir string) []string {
	paths := []string{
		filepath.Join("magusfiles", "?.buzz"),
		filepath.Join(projectDir, "magusfiles", "?.buzz"),
	}
	if ws := types.WorkspaceFromContext(ctx); ws != nil {
		if root := ws.Root(); root != "" && root != projectDir {
			paths = append(paths, filepath.Join(root, "magusfiles", "?.buzz"))
		}
	}
	return paths
}
