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

	"github.com/egladman/magus/internal/interp/engine"
	buzzengine "github.com/egladman/magus/internal/interp/engine/buzz"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

type sourceCtxKey struct{}
type normCtxKey struct{}

type projectPathCtxKey struct{}

// TargetContextGlobal is the session-global name under which the bindings layer
// stashes the shared magus.Context value (see bindings.registerAllBuzz). A target
// function receives it as its first argument; execBuzzSrc fetches it with GetGlobal and
// prepends it at dispatch. The double underscore keeps it out of the way of any
// magusfile identifier.
const TargetContextGlobal = "__magus_target_context"

// CtxFormTargetKeys returns the normalized keys of the exported functions in src whose
// FIRST parameter is annotated `magus\Context` (types.ContextParamAnnot) - the target
// contract. execBuzzSrc uses it to enforce that contract at load (an exported function
// missing the context is rejected with MGS1008) and to prepend the context at dispatch.
// It does NOT build the graph: the dependency graph is read statically by
// describe.Extract, which sees ctx-form declarations directly. Best-effort: a parse
// failure yields nil, matching the extractor's never-error contract.
func CtxFormTargetKeys(src string) map[string]bool {
	prog, err := buzz.ParseEmbedded(src)
	if err != nil || prog == nil {
		return nil
	}
	norm := types.DefaultTargetNameNormalizer
	out := map[string]bool{}
	for _, stmt := range prog.Stmts {
		fd, ok := stmt.(*ast.FunDecl)
		if !ok || !fd.IsExported {
			continue
		}
		if len(fd.ParamAnnots) > 0 && fd.ParamAnnots[0] == types.ContextParamAnnot {
			out[norm.NormalizeTargetName(fd.Name)] = true
		}
	}
	return out
}

// WithSource stores src in ctx so that bindings (e.g. ctx.needs) can
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
// magusfile is being parsed, so magus.project(fn) (the contextual form with no
// explicit path) can default to "this project".
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
// Buzz is the only engine today, so FindAll yields a single source. The loop is
// the seam a second engine would extend: each source is fully executed (including
// top-level declarations such as magus.project) before its target registry is
// consulted, and an unknown target falls through to the next source.
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

// Parse executes src in parse mode (name discovery only) and returns discovered targets.
func Parse(ctx context.Context, src *Source) ([]Target, error) {
	// Carry the source so bindings resolve paths relative to the magusfile's own
	// directory (local-spell require/import), not the process cwd, the same context
	// Run establishes. Without this, preloading a magusfile from outside its dir
	// fails to find its ./spells.
	ctx = WithSource(ctx, src)
	return parseBuzz(ctx, src)
}

// BuzzHostBindingsFn registers Go-backed host modules into a Buzz session.
// targets is the session's dispatchable target registry; exports maps each
// canonical target key to the exported function value itself, so ctx.needs
// can verify a passed function IS the exported target (nil when the session
// has no export discovery, e.g. the REPL). parseMode=true collects names only.
type BuzzHostBindingsFn func(ctx context.Context, sess *buzz.Session, targets map[string]vm.Callable, exports map[string]vm.Value, parseMode bool)

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
// It parses with ParseEmbedded, the same lenient mode magusfiles load under (Exec
// uses WithEmbedded). Strict buzz.Parse rejects top-level statements, which real
// magusfiles use freely (the repo's own has a top-level `if`), so parsing strict
// here would error and silently skip the check on exactly those files. A parse
// error still yields nil: Exec re-parses and reports the real syntax error with
// position. The substring gate skips the parse for the common magusfile that
// imports no spell (Exec is about to parse the source anyway).
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

// importBoundNames maps each import's bound namespace identifier to its path. A flat
// import (`as _`) binds no name; an alias binds itself; a plain import binds the path's
// last segment. Returns nil on a parse error (Exec re-parses and reports it).
func importBoundNames(src string) map[string]string {
	prog, err := buzz.ParseEmbedded(src)
	if err != nil {
		return nil
	}
	names := map[string]string{}
	for _, stmt := range prog.Stmts {
		imp, ok := stmt.(*ast.ImportStmt)
		if !ok || imp.Alias == "_" {
			continue
		}
		bound := imp.Alias
		if bound == "" {
			bound = imp.Path
			if i := strings.LastIndex(bound, "/"); i >= 0 {
				bound = bound[i+1:]
			}
		}
		names[bound] = imp.Path
	}
	return names
}

// importTargetCollisionErr reports a target whose name shadows a same-named import,
// which makes the module read null on member access.
func importTargetCollisionErr(name, importPath string) error {
	return fmt.Errorf("magusfile: target %q shadows the module import %q, so %s.<member> "+
		"reads null; rename the target or alias the import", name, importPath, name)
}

// runBuzz executes src on a fresh Buzz session and invokes target.
func runBuzz(ctx context.Context, src *Source, target string, extraArgs []string, workDir string) error {
	// Carry the target's directory on the context instead of os.Chdir-ing the whole
	// process. The host modules (std.*) resolve relative paths against this cwd, so
	// magusfile targets across projects (including a cross-project ctx.needs that
	// re-enters the interpreter) execute concurrently without corrupting a shared
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
		// Carries the MGS1006 code for lookup, but still Unwraps to ErrUnknownTarget so the fan-out
		// suppression (errors.Is(err, ErrUnknownTarget)) that skips projects lacking this target keeps working.
		return types.WrapDiagnostic(types.UnknownTarget, ErrUnknownTarget,
			"magusfile: unknown target %q (registered: %s)", target, strings.Join(names, ", "))
	}
	buzzArgs := make([]vm.Value, len(extraArgs))
	for i, s := range extraArgs {
		buzzArgs[i] = vm.StrValue(s)
	}
	ctx, exitCode := types.WithExitCapture(ctx)
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

// ctxlessTargetErr reports an exported magusfile function missing the magus.Context
// first parameter every target must declare. The signature is the contract magus reads
// statically to build the graph, so the ctx-less form is rejected at load.
func ctxlessTargetErr(name string) error {
	return types.DiagnosticErrorf(types.TargetMissingContext,
		"target %q must receive a %s as its first parameter: "+
			"change its signature to export fun %s(ctx: %s, args: [str])",
		name, types.ContextParamAnnot, name, types.ContextParamAnnot)
}

// execBuzzSrc creates a Buzz Session, registers bindings, and executes source files.
func execBuzzSrc(ctx context.Context, src *Source, parseMode bool) (*buzz.Session, map[string]vm.Callable, error) {
	// Carry the magusfile source on the context for the whole load, so the module
	// resolver registered below (which captures this ctx) can resolve top-level
	// `import "project/<path>"` cross-project handles during execution. Run mode
	// otherwise reaches here with a nil source (runBuzz sets it only for target
	// dispatch), and such an import would resolve to nothing. Parse mode already sets
	// it upstream; re-setting is idempotent.
	ctx = WithSource(ctx, src)
	// The buzz path uses the standalone interpreter's concrete API (Exec, Targets,
	// CallVal) directly; the engine.Session adapter is only for generic registry
	// consumers, so there's no need to round-trip through engine.Lookup. Confine
	// imports to the magusfiles layout (see magusSearchPaths); WithSearchPaths
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
	// does not enable this: there a later line must resolve earlier names.
	buzzSess.SetPromoteTopLevel(true)
	// Feed this session's compile phases, imports, and VM faults to the spine. A
	// no-op (session left unobserved) when telemetry is disabled on ctx.
	AttachSessionObservers(ctx, buzzSess, ModeMagusfile)

	targetMap := buzzSess.Targets()
	// exportVals is filled by the export-discovery loop below, after the files
	// execute; the bindings close over the map reference, so ctx.needs sees the
	// populated registry by the time any target body runs.
	exportVals := map[string]vm.Value{}
	if buzzHostBindingsFn != nil {
		buzzHostBindingsFn(ctx, buzzSess, targetMap, exportVals, parseMode)
	}

	// Import names across all the project's magusfiles, to catch a target that shadows one.
	importNames := map[string]string{}
	// ctx-form target keys across all the project's magusfiles: these receive a
	// magus.Context first argument at dispatch (injected below) instead of running
	// on the bare `args` signature the old form uses.
	ctxForm := map[string]bool{}
	for _, path := range src.Files {
		// rel names the offending file relative to its project dir, so a magusfiles/
		// directory (several files) is unambiguous; the project itself is already in
		// the surrounding "preload <project>" context. path is built from src.Dir, so
		// Rel is a pure-lexical relativize with no I/O and no symlink/escape pitfall.
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
		for name, importPath := range importBoundNames(code) {
			importNames[name] = importPath
		}
		for key := range CtxFormTargetKeys(code) {
			ctxForm[key] = true
		}
		// Validate spell handles before Exec: an unknown `magus/spell/<handle>`
		// resolves to nothing (gopherbuzz skips an unresolved import) and would
		// otherwise surface much later as a disconnected "undefined" error. Fail fast,
		// naming the file, with a did-you-mean instead.
		if buzzSpellImportCheckFn != nil {
			if err := buzzSpellImportCheckFn(spellImportNames(code)); err != nil {
				_ = buzzSess.Close()
				return nil, nil, fmt.Errorf("magusfile: %s: %w", rel, err)
			}
		}
		if err := TimeExec(ctx, ModeMagusfile, func() error { return buzzSess.Exec(ctx, code) }); err != nil {
			_ = buzzSess.Close()
			return nil, nil, fmt.Errorf("magusfile: exec %s: %w", rel, err)
		}
	}

	// Discover targets from exported functions (export fun name ...). The
	// normalizer is many-to-one (go_build, goBuild, GoBuild all give go-build), so two
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
	seen := make(map[string]string, len(names)) // canonical key -> source name
	// The shared magus.Context value ctx-form targets receive as their first
	// argument (stashed by the bindings layer). Null for a session without the
	// bindings (e.g. a bare test), in which case no ctx-form target is dispatchable.
	targetCtxVal := buzzSess.GetGlobal(TargetContextGlobal)
	for _, name := range names {
		val := exports[name]
		if !val.IsFun() {
			continue
		}
		// A target that shadows a same-named import makes the module read null; fail at load.
		if importPath, clash := importNames[name]; clash {
			_ = buzzSess.Close()
			return nil, nil, importTargetCollisionErr(name, importPath)
		}
		key := norm.NormalizeTargetName(name)
		if prev, dup := seen[key]; dup {
			_ = buzzSess.Close()
			return nil, nil, targetCollisionErr(prev, name, key)
		}
		// Every target must receive a magus.Context as its first parameter - the
		// signature IS the contract magus reads statically to build the graph. Reject the
		// old ctx-less form at load rather than dispatching it with the wrong arguments.
		if !ctxForm[key] {
			_ = buzzSess.Close()
			return nil, nil, ctxlessTargetErr(name)
		}
		seen[key] = name
		captured := val
		exportVals[key] = val
		targetMap[key] = func(ctx context.Context, args []vm.Value) (vm.Value, error) {
			return TimeCall(ctx, ModeMagusfile, func() (vm.Value, error) {
				// Prepend the magus.Context so the body's `ctx` parameter binds it; the
				// user args (the `[str]` second parameter) ride along after.
				return buzzSess.CallValue(ctx, captured, append([]vm.Value{targetCtxVal}, args...))
			})
		}
	}

	return buzzSess, targetMap, nil
}

// NewBuzzWorkerFunc returns the buzz.WorkerFunc that creates a pre-warmed Buzz
// session for src. Safe to call from multiple goroutines because execBuzzSrc reads
// sources by absolute path and does not acquire chdirMu.
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
	AttachSessionObservers(ctx, buzzSess, ModeRepl)
	if buzzHostBindingsFn != nil {
		// nil exports: the REPL has no export-discovery pass, so ctx.needs
		// falls back to name-only resolution there.
		buzzHostBindingsFn(ctx, buzzSess, buzzSess.Targets(), nil, false)
	}

	if autoloadDir != "" {
		if src, err := Find(autoloadDir); err == nil && src.Engine == "buzz" {
			for _, path := range src.Files {
				data, rerr := os.ReadFile(path)
				if rerr != nil {
					_ = buzzSess.Close()
					return nil, fmt.Errorf("repl: read %s: %w", path, rerr)
				}
				if eerr := TimeExec(ctx, ModeRepl, func() error { return buzzSess.Exec(ctx, string(data)) }); eerr != nil {
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
// against (see buzz.WithSearchPaths). `?` is the import name, filled in by the
// resolver. Each of gopherbuzz's upstream PROJECT-RELATIVE layouts (a sibling
// file, or a library directory) is searched, plus magus's own magusfiles/
// convention, in order relative to the process cwd, the project root, and the
// workspace root. The workspace root is read from ctx (types.WithWorkspace) and
// omitted when absent or identical to the project dir, so the common single-project
// case yields no duplicate entry.
//
// gopherbuzz's SYSTEM paths (/usr/share/buzz, /usr/local/share/buzz, $BUZZ_PATH)
// are deliberately NOT adopted, and BUZZ_INCLUDE_PATH is cleared at the call sites:
// a magusfile resolves imports only within the workspace, so a build stays hermetic
// and can't pull in arbitrary machine-installed buzz code. Note: an imported
// sibling is not auto-tracked for affected/drift; declare it in the project's
// `sources` so an edit marks the project dirty.
func magusSearchPaths(ctx context.Context, projectDir string) []string {
	// Roots searched, in order. "" is the process cwd (a bare, cwd-relative template).
	roots := []string{"", projectDir}
	if ws := types.WorkspaceFromContext(ctx); ws != nil {
		if root := ws.Root(); root != "" && root != projectDir {
			roots = append(roots, root)
		}
	}
	// Per root: the upstream project-relative layouts, then magus's magusfiles/ form.
	templates := []string{
		"?.buzz",
		filepath.Join("?", "main.buzz"),
		filepath.Join("?", "src", "main.buzz"),
		filepath.Join("?", "src", "?.buzz"),
		filepath.Join("magusfiles", "?.buzz"),
	}
	paths := make([]string, 0, len(roots)*len(templates))
	for _, r := range roots {
		for _, t := range templates {
			if r == "" {
				paths = append(paths, t)
			} else {
				paths = append(paths, filepath.Join(r, t))
			}
		}
	}
	return paths
}
