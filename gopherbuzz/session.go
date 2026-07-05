package buzz

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/gopherbuzz/ast"
	vmpackage "github.com/egladman/gopherbuzz/vm"
)

// Session is a single Buzz execution context.
// Not safe for concurrent use; ensure one goroutine owns it at a time.
type Session struct {
	ctx           context.Context
	cancel        context.CancelFunc
	env           *vmpackage.Env
	targets       map[string]vmpackage.Callable
	tests         []TestEntry
	exportedNames map[string]bool
	// embedded relaxes the script-conformance rules upstream Buzz enforces (no
	// top-level control flow, labeled args). Default false (strict, like upstream);
	// the embedding hosts — REPL, magus eval, magusfile loading, the eval test
	// harness — set it via WithEmbedded because top-level statements are their whole
	// purpose. It is the explicit, named deviation from upstream.
	embedded bool
	// curVM is the VM currently executing a chunk in this session, or nil between
	// runs. The debugger (debug.go) reads it for stack introspection, and the run
	// helpers set/restore it (save-and-restore so a pry() eval doesn't clobber the
	// paused outer VM). stepHook/stepMask carry a pending step hook onto each VM.
	curVM    *vmpackage.VM
	stepHook func(vmpackage.StepEvent, vmpackage.DebugFrame)
	stepMask vmpackage.StepMask
	// searchPaths is the ordered list of path templates searched when an import
	// statement names a module not yet bound in the session. Each template holds
	// `?` (replaced with the import path) and may reference environment variables
	// (e.g. $BUZZ_PATH). NewSession seeds it from DefaultSearchPaths; the host may
	// override it at construction via WithSearchPaths. See findIncludeFile.
	searchPaths []string
	// includeDirs is an additional ordered list of plain directories searched
	// after searchPaths, for the `-L` CLI flag and BUZZ_INCLUDE_PATH. NewSession
	// populates it from BUZZ_INCLUDE_PATH; the host may override via SetIncludeDirs.
	includeDirs []string
	// loadedPaths tracks absolute paths already loaded to prevent re-execution
	// and break import cycles.
	loadedPaths map[string]bool
	// syntheticModules maps an import path (e.g. "magus/extra") to a host-built
	// module value. An `import "<path>"` binds it under the path's basename
	// (or alias) without touching the filesystem; see loadFileImports. The host
	// registers these via SetSyntheticModule.
	syntheticModules map[string]vmpackage.Value
	// moduleResolver, if set, is consulted for an import path that is neither
	// already bound nor a synthetic module, before the includeDirs file search.
	// It lets the host resolve a path-style import to a prebuilt module value
	// on demand (e.g. a magus spell handle for `import "spells/hello"`). A false
	// return falls through to the file search. Set via SetModuleResolver.
	moduleResolver func(importPath string) (vmpackage.Value, bool)
	// importedTypes accumulates the exported object/enum declarations of flat
	// imported .buzz modules, so compileShared can hand them to the checker and
	// the importing file can name those types in annotations and literals. The
	// runtime values already merge via flat Exec; this carries the *types* the
	// checker would otherwise never see. Populated in loadFileImports.
	importedTypes []ast.Node
	// importedModuleFuncs maps each flat-imported module's bound name (basename
	// or alias) to its exported function declarations. checkWithGlobals uses
	// this to build a typed namespace object for each import instead of `any`,
	// so qualified access like `state\wm()` resolves to its declared return type
	// and field access on module-returned values is type-checked correctly.
	importedModuleFuncs map[string][]*ast.FunDecl
	// sourceModules maps an import path to embedded .buzz source. Unlike a
	// synthetic module (a host Value carrying functions), a source module is
	// real Buzz source, so its exported object/enum *types* are visible to the
	// importer's checker. It flat-merges like a file import. Set via
	// SetSourceModule; used for shipped Buzz library modules (e.g. canonical
	// magus types) that have no .buzz file on the include path.
	sourceModules map[string]string
	// promoteTopLevel opts this session's compiles into top-level slot promotion
	// (CompileOptions.PromoteTopLevel). The magusfile execution path enables it for
	// faster top-level hot code; the REPL leaves it off, since a later prompt line
	// must still resolve earlier top-level names by name. Under it a non-exported,
	// non-captured top-level var becomes chunk-private, so cross-file/import code
	// referencing it must go through `export`. See SetPromoteTopLevel.
	promoteTopLevel bool
	// importPrivate is the set of non-exported top-level names introduced by flat
	// `import`ed modules (each module chunk's Chunk.Private). They remain live in
	// the runtime Env — the module's own functions read them — but compileShared
	// hides them from a later compile's checker, so a flat importer sees only a
	// module's `export`ed names (exports-only visibility). collectImportPrivate is
	// set while executing an import so Exec knows to accumulate them; same-project
	// files (executed directly, not via import) are unaffected.
	importPrivate        map[string]bool
	collectImportPrivate bool
	// declaredNamespaces maps a flat-imported module's full `namespace a\b\c`
	// path to the import path that first claimed it. Upstream Buzz exposes a
	// no-alias import's exports under this declared path and rejects two imports
	// sharing one namespace; gopherbuzz mirrors both (see bindNamespacePath),
	// on top of its own basename/splat access conveniences.
	declaredNamespaces map[string]string
}

// SetSyntheticModule registers v as the module imported by `import "<importPath>"`.
// The import binds v under the path's basename (e.g. "util" for "magus/extra"),
// or under an explicit alias. Host-provided modules resolve before any file
// search, so they need no .buzz file on disk.
func (s *Session) SetSyntheticModule(importPath string, v vmpackage.Value) {
	if s.syntheticModules == nil {
		s.syntheticModules = map[string]vmpackage.Value{}
	}
	s.syntheticModules[importPath] = v
}

// SyntheticModule returns the value registered for importPath via
// SetSyntheticModule, or ok=false if none is registered. It lets a host that
// layers its own methods onto a stdlib module under a shared name (e.g. magus
// merging host methods onto Buzz's bare "os"/"fs"/"crypto") read the registered
// module back so it can be extended in place rather than replaced.
func (s *Session) SyntheticModule(importPath string) (vmpackage.Value, bool) {
	v, ok := s.syntheticModules[importPath]
	return v, ok
}

// SetSourceModule registers src as the embedded Buzz source imported by
// `import "<importPath>"`. It resolves before the includeDirs file search and
// flat-merges (its exported object/enum types become visible to the importer's
// checker, which a synthetic value module cannot provide). Use it for shipped
// Buzz library modules that have no file on the include path.
func (s *Session) SetSourceModule(importPath, src string) {
	if s.sourceModules == nil {
		s.sourceModules = map[string]string{}
	}
	s.sourceModules[importPath] = src
}

// SetModuleResolver installs fn as the on-demand resolver for path-style imports
// (see the moduleResolver field). fn is called with the import path and binds its
// returned value under the path's basename (or alias) when it reports ok; a false
// return leaves the import for the includeDirs file search.
func (s *Session) SetModuleResolver(fn func(importPath string) (vmpackage.Value, bool)) {
	s.moduleResolver = fn
}

// newSession is the raw embedding primitive: it defaults to embedded parsing
// because its internal users — the session pool, child/sub sessions, the REPL,
// and the test harness — are all embedding contexts where top-level statements
// are expected. The public NewSession resets to strict (upstream parity) and
// re-enables leniency only via WithEmbedded.
func newSession(ctx context.Context) *Session {
	ctx2, cancel := context.WithCancel(ctx)
	env := vmpackage.NewEnv()
	vmpackage.RegisterStdlib(env)
	s := &Session{
		ctx:                ctx2,
		cancel:             cancel,
		env:                env,
		embedded:           true,
		targets:            make(map[string]vmpackage.Callable),
		exportedNames:      make(map[string]bool),
		loadedPaths:        make(map[string]bool),
		declaredNamespaces: make(map[string]string),
	}
	// resume/resolve are session-bound so they can swap curVM to the fiber's VM
	// for the duration of Exec(), making Frames()/CallDepth()/step hooks reflect
	// the fiber's stack. They are registered under their keyword names (which the
	// user cannot shadow since resume/resolve are lexer keywords).
	env.Define("resume", vmpackage.DirectValue("resume", s.builtinResume))
	env.Define("resolve", vmpackage.DirectValue("resolve", s.builtinResolve))
	// The test-block registrar (see compiler.compileTestDecl). Bound under a name
	// no user identifier can spell, so `test "x" {…}` blocks register their bodies
	// without any reserved-name collision.
	env.Define(testRegistrarName, vmpackage.DirectValue(testRegistrarName, s.registerTest))
	return s
}

// testRegistrarName is the session-env name the compiler lowers a test block to a
// call of. The leading '$' makes it unspellable as a Buzz identifier.
const testRegistrarName = "$buzz_test"

// TestEntry is one registered `test "Name" { … }` block: its name and the
// zero-argument closure that runs its body.
type TestEntry struct {
	Name string
	Fn   vmpackage.Value
}

// registerTest is the session-bound implementation of the test-block registrar.
// The compiler calls it once per test block with (name, bodyClosure).
func (s *Session) registerTest(_ context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
	if len(args) != 2 || !args[0].IsStr() {
		return vmpackage.Null, fmt.Errorf("buzz: internal: malformed test registration")
	}
	s.tests = append(s.tests, TestEntry{Name: args[0].AsString(), Fn: args[1]})
	return vmpackage.Null, nil
}

// Tests returns the test blocks registered while executing this session's code,
// in source order. A normal run never executes their bodies; a test runner calls
// each Fn (e.g. via CallValue) and treats a returned error as a failure.
func (s *Session) Tests() []TestEntry { return s.tests }

// builtinResume is the session-aware implementation of `resume fiber`. It swaps
// curVM to the fiber's VM via enter() so debug introspection (Frames,
// CallDepth, step hooks) sees the fiber's stack for the duration of Exec().
//
// Returns the yielded value, or null if nothing was yielded or the fiber is
// over (upstream parity: resume never errors on a completed fiber).
func (s *Session) builtinResume(ctx context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
	if len(args) < 1 {
		return vmpackage.Null, fmt.Errorf("buzz: resume requires a fiber argument")
	}
	fib, ok := vmpackage.AsFiber(args[0])
	if !ok {
		return vmpackage.Null, fmt.Errorf("buzz: resume requires a fiber, got %s", args[0].Kind())
	}
	switch fib.Status() {
	case vmpackage.FiberDone:
		// upstream: resume on a completed fiber returns null. If the fiber ended
		// in an error, re-surface it rather than swallowing it (Err is nil for a
		// clean completion, so this returns null, nil in that case).
		return vmpackage.Null, fib.Err()
	case vmpackage.FiberRunning:
		return vmpackage.Null, fmt.Errorf("buzz: cannot resume a running fiber (recursive resume)")
	}
	fibVM := fib.VM()
	fibVM.SetCtx(ctx)
	fib.SetStatus(vmpackage.FiberRunning)
	defer s.enter(fibVM)()
	result, err := fibVM.Exec()
	if err != nil {
		var ys *vmpackage.YieldSignal
		if errors.As(err, &ys) {
			fib.SetStatus(vmpackage.FiberSuspended)
			return vmpackage.YieldValue(ys), nil
		}
		fib.SetStatus(vmpackage.FiberDone)
		fib.SetErr(err) // cache so a later resume/resolve re-surfaces it
		return vmpackage.Null, err
	}
	fib.SetStatus(vmpackage.FiberDone)
	fib.SetReturn(result)      // cache for resolve
	return vmpackage.Null, nil // upstream: resume returns null on normal completion
}

// builtinResolve is the session-aware implementation of `resolve fiber`. It
// runs the fiber to completion, ignoring all yield points, and returns the
// fiber function's return value. Calling resolve on an already-done fiber
// returns the cached return value (upstream: resolve is idempotent after done).
func (s *Session) builtinResolve(ctx context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
	if len(args) < 1 {
		return vmpackage.Null, fmt.Errorf("buzz: resolve requires a fiber argument")
	}
	fib, ok := vmpackage.AsFiber(args[0])
	if !ok {
		return vmpackage.Null, fmt.Errorf("buzz: resolve requires a fiber, got %s", args[0].Kind())
	}
	switch fib.Status() {
	case vmpackage.FiberDone:
		// idempotent: return the cached return value, re-surfacing a cached error
		// (Err is nil for a clean completion).
		return fib.Return(), fib.Err()
	case vmpackage.FiberRunning:
		return vmpackage.Null, fmt.Errorf("buzz: cannot resolve a running fiber (recursive resolve)")
	}
	fibVM := fib.VM()
	fibVM.SetCtx(ctx)
	fib.SetStatus(vmpackage.FiberRunning)
	defer s.enter(fibVM)()
	for {
		// resolve dismisses every yield, so poll cancellation here too: Exec's own
		// check only fires every 256 back-edges, which a yield-dense fiber may not
		// reach between suspends, leaving cancellation latency otherwise unbounded.
		if err := ctx.Err(); err != nil {
			fib.SetStatus(vmpackage.FiberDone)
			fib.SetErr(err)
			return vmpackage.Null, err
		}
		result, err := fibVM.Exec()
		if err == nil {
			fib.SetStatus(vmpackage.FiberDone)
			fib.SetReturn(result)
			return result, nil
		}
		var ys *vmpackage.YieldSignal
		if errors.As(err, &ys) {
			// dismiss this yield and continue running toward completion
			continue
		}
		fib.SetStatus(vmpackage.FiberDone)
		fib.SetErr(err)
		return vmpackage.Null, err
	}
}

// DefaultSearchPaths is the ordered list of path templates an unconfigured
// Session searches to resolve `import "<name>"` to a file. In each template `?`
// is replaced with the import path and environment variables are expanded (a
// template referencing an unset variable is skipped, so an unset $BUZZ_PATH
// drops its entries rather than searching the filesystem root).
//
// It mirrors the upstream Buzz search order (https://buzz-lang.dev); module files
// use the `.buzz` extension. Override per-session with WithSearchPaths.
var DefaultSearchPaths = []string{
	"./?.buzz",
	"./?/main.buzz",
	"./?/src/main.buzz",
	"./?/src/?.buzz",
	"/usr/share/buzz/?.buzz",
	"/usr/share/buzz/?/main.buzz",
	"/usr/share/buzz/?/src/main.buzz",
	"/usr/share/buzz/?/src/?.buzz",
	"/usr/local/share/buzz/?.buzz",
	"/usr/local/share/buzz/?/main.buzz",
	"/usr/local/share/buzz/?/src/main.buzz",
	"/usr/local/share/buzz/?/src/?.buzz",
	"$BUZZ_PATH/?.buzz",
	"$BUZZ_PATH/?/main.buzz",
	"$BUZZ_PATH/?/src/main.buzz",
	"$BUZZ_PATH/?/src/?.buzz",
}

// Option configures a Session at construction. See NewSession.
type Option func(*Session)

// WithSearchPaths replaces the session's import search path templates (see
// DefaultSearchPaths for the syntax). Passing no paths is a no-op, leaving the
// session on DefaultSearchPaths. A host that wants to confine imports to its own
// layout passes its own templates here (e.g. magus restricts resolution to
// `magusfiles/?.buzz` under the project and workspace roots).
func WithSearchPaths(paths ...string) Option {
	return func(s *Session) {
		if len(paths) > 0 {
			s.searchPaths = paths
		}
	}
}

// WithEmbedded relaxes the upstream-Buzz script-conformance rules (top-level
// statements and labeled args) for this session. Embedding hosts (REPL, magus
// eval, magusfile loading) must set it; without it a session parses strictly,
// matching upstream.
func WithEmbedded() Option {
	return func(s *Session) { s.embedded = true }
}

// NewSession creates a Buzz execution context. Inject globals with SetGlobal
// and register target callbacks via Targets. Close releases the context.
//
// Imports resolve against DefaultSearchPaths unless WithSearchPaths overrides it.
// BUZZ_INCLUDE_PATH (colon-separated on Unix, semicolon-separated on Windows) is
// read to populate the additional include directory list, searched after the
// templates; the host may override it with SetIncludeDirs.
func NewSession(ctx context.Context, opts ...Option) *Session {
	s := newSession(ctx)
	// Public API default is strict (upstream Buzz parity); embedding hosts opt back
	// into leniency with WithEmbedded. (The raw newSession defaults embedded for its
	// internal embedding users.)
	s.embedded = false
	s.searchPaths = DefaultSearchPaths
	if v := os.Getenv("BUZZ_INCLUDE_PATH"); v != "" {
		s.includeDirs = filepath.SplitList(v)
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// NewChild creates an isolated session that inherits this session's import
// resolution (search paths, include dirs, synthetic modules, module resolver)
// but starts with a fresh top-level scope and its own loaded-path set. io.runFile
// uses it so a run file cannot see or mutate the caller's globals — parity with
// upstream buzz, whose runFile executes the file in its own scope, not the caller's.
func (s *Session) NewChild() *Session {
	c := newSession(s.ctx)
	c.embedded = s.embedded // inherit the parent session's parse mode
	c.searchPaths = s.searchPaths
	c.includeDirs = s.includeDirs
	c.syntheticModules = s.syntheticModules
	c.moduleResolver = s.moduleResolver
	return c
}

// SetIncludeDirs replaces the directories searched for file-based imports.
// The host (e.g. internal/interp) calls this to enforce workspace
// sandboxing before running any user code.
func (s *Session) SetIncludeDirs(dirs []string) { s.includeDirs = dirs }

// SetPromoteTopLevel enables top-level slot promotion for every chunk this
// session compiles (see Session.promoteTopLevel and CompileOptions.PromoteTopLevel).
// The magusfile execution path turns it on for faster top-level code; the REPL
// must leave it off so a later prompt line can resolve earlier top-level names.
func (s *Session) SetPromoteTopLevel(on bool) { s.promoteTopLevel = on }

// IncludeDirs returns the current include directory list.
func (s *Session) IncludeDirs() []string { return s.includeDirs }

// Targets returns the target map populated by magus.target.new().
func (s *Session) Targets() map[string]vmpackage.Callable { return s.targets }

// Exec parses, type-checks, compiles, and executes Buzz source code in the session's environment.
// Type errors are returned as hard errors (Buzz is statically typed).
func (s *Session) Exec(ctx context.Context, code string) error {
	_, err := s.exec(ctx, code)
	return err
}

// exec is Exec's workhorse, additionally returning the names this chunk
// exported (chunk.Exports). Importers use that exact per-chunk list to build a
// namespace object — a set-diff against the session-wide exportedNames can't
// tell that a module re-exported a name another module already exported, which
// would silently drop the later module's export from its namespace object.
func (s *Session) exec(ctx context.Context, code string) ([]string, error) {
	chunk, err := s.compileShared(code)
	if err != nil {
		return nil, err
	}
	vm := vmpackage.NewVM(ctx)
	defer s.enter(vm)()
	if _, err = vm.Run(chunk, s.env); err != nil {
		return nil, err
	}
	for _, name := range chunk.Exports {
		s.exportedNames[name] = true
	}
	if s.collectImportPrivate {
		if s.importPrivate == nil {
			s.importPrivate = map[string]bool{}
		}
		for _, name := range chunk.Private {
			s.importPrivate[name] = true
		}
	}
	return chunk.Exports, nil
}

// execImport runs an imported module's source with import-private collection on,
// so the module's non-exported top-level names are recorded in importPrivate and
// hidden from the importer's checker (exports-only visibility). The flag is
// save-and-restored so a nested import (a module importing another) still
// collects. It returns the chunk's exported names for the namespace-object bind.
func (s *Session) execImport(ctx context.Context, code string) ([]string, error) {
	prev := s.collectImportPrivate
	s.collectImportPrivate = true
	defer func() { s.collectImportPrivate = prev }()
	return s.exec(ctx, code)
}

// enter makes vm the session's current VM (for debugger introspection) and
// applies any pending step hook, returning a restore func to defer. Save-and-
// restore lets a pry() eval run a nested VM without losing the paused outer one.
func (s *Session) enter(vm *vmpackage.VM) func() {
	prev := s.curVM
	s.curVM = vm
	if s.stepHook != nil {
		vm.SetStepHook(s.stepMask, s.stepHook)
	}
	return func() { s.curVM = prev }
}

// Eval compiles and runs code against the session's shared scope and returns the
// program's result value (the value of a trailing `return <expr>`, else Null).
// The REPL uses it to print bare expressions.
func (s *Session) Eval(ctx context.Context, code string) (vmpackage.Value, error) {
	chunk, err := s.compileShared(code)
	if err != nil {
		return vmpackage.Null, err
	}
	return s.EvalChunk(ctx, chunk)
}

// EvalChunk runs a previously compiled Chunk and returns its result value. The
// REPL driver compiles first (to tell a syntax error — fall back to the
// statement form — from a runtime error) then runs exactly once via this, so a
// snippet with side effects never executes twice.
func (s *Session) EvalChunk(ctx context.Context, chunk *vmpackage.Chunk) (vmpackage.Value, error) {
	vm := vmpackage.NewVM(ctx)
	defer s.enter(vm)()
	v, err := vm.Run(chunk, s.env)
	return v, err
}

// Globals returns a snapshot of the session's top-level bindings (name → value),
// including host-injected globals. The REPL filters host names for .globals.
func (s *Session) Globals() map[string]vmpackage.Value {
	names := s.env.Names()
	slots := s.env.Slots()
	out := make(map[string]vmpackage.Value, len(names))
	for name, slot := range names {
		out[name] = slots[slot]
	}
	return out
}

// Exports returns the subset of Globals() whose names were declared with
// export in a file executed via Exec or ExecChunk. The map is a fresh snapshot;
// mutations don't affect the session.
func (s *Session) Exports() map[string]vmpackage.Value {
	if len(s.exportedNames) == 0 {
		return nil
	}
	all := s.Globals()
	out := make(map[string]vmpackage.Value, len(s.exportedNames))
	for name := range s.exportedNames {
		if v, ok := all[name]; ok {
			out[name] = v
		}
	}
	return out
}

// DoString executes code using the session's own context. Embedders needing
// per-call cancellation use Exec directly. Required by the cross-engine
// engine.Session interface (every backend implements DoString), so it stays
// even though it is a thin wrapper over Exec.
func (s *Session) DoString(code string) error { return s.Exec(s.ctx, code) }

// compileShared parses, type-checks, and compiles code for execution against
// the session's shared Env. The session runs many chunks against one Env
// (injected globals, prior definitions, target callbacks), so top-level
// declarations are Env bindings (SharedGlobals), not per-Run slots. Predefined
// globals are passed to the checker so they aren't flagged as undefined.
func (s *Session) compileShared(code string) (*vmpackage.Chunk, error) {
	prog, err := parseModed(code, !s.embedded)
	if err != nil {
		return nil, err
	}
	// Resolve file-based imports before type-checking so the globals they
	// introduce are visible to the checker.
	if err := s.loadFileImports(prog); err != nil {
		return nil, err
	}
	names := s.env.Names()
	globals := make([]string, 0, len(names))
	for name := range names {
		// Exports-only import visibility: a name made private by some flat import
		// is hidden from this compile's checker unless another module exported it.
		// It stays in the runtime Env (the owning module's functions still read it).
		if s.importPrivate[name] && !s.exportedNames[name] {
			continue
		}
		globals = append(globals, name)
	}
	if errs := checkWithGlobals(prog, globals, s.importedTypes, s.importedModuleFuncs, s.importPrivateHint()); len(errs) != 0 {
		return nil, errs[0]
	}
	// DebugLines so magus.pry() / the REPL can report a paused frame's line and
	// drive line-level stepping; the cost is one parallel int32 slice per chunk.
	return CompileWith(prog, CompileOptions{
		SharedGlobals:   true,
		DebugLines:      true,
		PromoteTopLevel: s.promoteTopLevel,
		ImportedTypes:   s.importedTypes,
	})
}

// importPrivateHint returns the names hidden by exports-only import visibility
// (importPrivate minus anything since exported), so the checker can suggest
// `export`ing one instead of reporting a bare "undefined". nil when none apply.
func (s *Session) importPrivateHint() map[string]bool {
	if len(s.importPrivate) == 0 {
		return nil
	}
	hint := make(map[string]bool, len(s.importPrivate))
	for name := range s.importPrivate {
		if !s.exportedNames[name] {
			hint[name] = true
		}
	}
	return hint
}

// loadFileImports scans prog for import statements and, for any module name not
// yet bound in the session, searches includeDirs for a matching .buzz file. The
// file is executed (via Exec, which recurses through compileShared) before the
// caller proceeds to type-check, so imported globals are visible to the checker.
// loadedPaths prevents re-execution and breaks import cycles.
//
// Alias semantics:
//   - no alias or alias "_": flat exec — file's globals merge into parent env.
//   - alias "x": exec in a sub-session (inheriting host globals), collect new
//     bindings, create a map value, bind it under "x" in the parent env.
func (s *Session) loadFileImports(prog *ast.Program) error {
	// Note: don't early-return on empty includeDirs — host-provided synthetic
	// modules (e.g. "magus/extra") resolve without any include path.
	for _, stmt := range prog.Stmts {
		imp, ok := stmt.(*ast.ImportStmt)
		if !ok {
			continue
		}
		// `buzz:<name>` is upstream Buzz's package-manager scheme for a built-in
		// stdlib module (`import "buzz:os"`), disambiguating it from a package or
		// file import. gopherbuzz registers the stdlib under bare names, so strip
		// the scheme and resolve/bind as if the bare name was imported; the
		// original spelling is kept for diagnostics.
		resolvePath := strings.TrimPrefix(imp.Path, "buzz:")
		parts := strings.Split(resolvePath, "/")
		basename := parts[len(parts)-1]

		// Determine the bound name used to detect "already loaded":
		// for aliased imports use the alias, else use the basename.
		boundName := basename
		if imp.Alias != "" && imp.Alias != "_" {
			boundName = imp.Alias
		}
		if _, bound := s.env.Get(boundName); bound {
			continue
		}

		// Host-provided synthetic modules (e.g. "magus/extra") resolve before
		// any filesystem search and bind directly under the import's name.
		if v, ok := s.syntheticModules[resolvePath]; ok {
			s.env.Define(boundName, v)
			continue
		}

		// Host-provided source modules ship as embedded .buzz source so the
		// importer can use their exported object/enum types. They flat-merge
		// like a file import; the loadedPaths guard (keyed by import path)
		// prevents a second exec.
		if src, ok := s.sourceModules[resolvePath]; ok {
			key := "source:" + resolvePath
			if s.loadedPaths[key] {
				continue
			}
			s.loadedPaths[key] = true
			s.collectImportedModule(boundName, src)
			exports, err := s.execImport(s.ctx, src)
			if err != nil {
				return fmt.Errorf("buzz: import %q: %w", imp.Path, err)
			}
			s.bindNamespaceObject(boundName, exports)
			if ns := s.declaredNamespace(src); ns != nil {
				if err := s.bindNamespacePath(ns, exports, imp.Path); err != nil {
					return err
				}
			}
			continue
		}

		// A host module resolver (e.g. local magus spells: `import "spells/hello"`)
		// gets first refusal on path-style imports, ahead of the file search.
		if s.moduleResolver != nil {
			if v, ok := s.moduleResolver(resolvePath); ok {
				s.env.Define(boundName, v)
				continue
			}
		}

		path := s.findIncludeFile(resolvePath)
		if path == "" {
			// Nothing resolved this import: not an already-bound name, a synthetic
			// or source module, the host module resolver, or a .buzz file on the
			// search path. Binding nothing would let the unresolved name surface
			// later as a disconnected "undefined" error, or silently no-op if it is
			// never referenced, so fail here at the import that is actually wrong.
			return fmt.Errorf("buzz: import %q: module not found", imp.Path)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("buzz: import %q: resolve path: %w", imp.Path, err)
		}
		if s.loadedPaths[abs] {
			continue
		}
		s.loadedPaths[abs] = true

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("buzz: import %q: %w", imp.Path, err)
		}

		if imp.Alias != "" && imp.Alias != "_" {
			// Aliased import: exec in an isolated sub-session so the file's
			// globals don't leak into the parent env. Collect the new globals
			// and expose them as a map bound under the alias.
			if err := s.loadImportAsAlias(imp.Path, string(data), imp.Alias); err != nil {
				return err
			}
		} else {
			// Flat import: merge file's globals directly into this env, and
			// collect its exported types and functions so the importer can name
			// them (Exec only merges runtime values, not type declarations).
			s.collectImportedModule(boundName, string(data))
			exports, err := s.execImport(s.ctx, string(data))
			if err != nil {
				return fmt.Errorf("buzz: import %q: %w", imp.Path, err)
			}
			// Also bind a namespace object under the basename so upstream-Buzz
			// qualified access (`regex\reCompile`) resolves the same export the
			// splat above bound unqualified (`reCompile`). gopherbuzz accepts both.
			s.bindNamespaceObject(boundName, exports)
			// And, when the file declares `namespace a\b\c`, bind its exports
			// under that full path too, matching upstream Buzz exactly (and
			// erroring on a duplicate namespace instead of failing downstream).
			if ns := s.declaredNamespace(string(data)); ns != nil {
				if err := s.bindNamespacePath(ns, exports, imp.Path); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// collectImportedModule parses a flat-imported module's source and records its
// exported declarations on the session. Object/enum types go into importedTypes
// (so the checker can resolve them in annotations and literals); exported
// functions go into importedModuleFuncs[boundName] (so the checker can build a
// typed namespace object for the import instead of using `any`). Parse errors
// are ignored: the subsequent Exec re-parses the same source authoritatively.
func (s *Session) collectImportedModule(boundName, src string) {
	prog, err := parseModed(src, !s.embedded)
	if err != nil {
		return
	}
	for _, stmt := range prog.Stmts {
		switch d := stmt.(type) {
		case *ast.ObjectDecl:
			if d.IsExported {
				s.importedTypes = append(s.importedTypes, d)
			}
		case *ast.EnumDecl:
			if d.IsExported {
				s.importedTypes = append(s.importedTypes, d)
			}
		case *ast.FunDecl:
			if d.IsExported {
				if s.importedModuleFuncs == nil {
					s.importedModuleFuncs = map[string][]*ast.FunDecl{}
				}
				s.importedModuleFuncs[boundName] = append(s.importedModuleFuncs[boundName], d)
			}
		}
	}
}

// bindNamespaceObject binds, under name, a map of the names a flat import just
// exported (the chunk's own Exports). It lets upstream-style qualified access
// `name\export` resolve the same value the unqualified splat also bound. Skipped
// if name is already bound (e.g. the module exports something matching its own
// basename). Using the chunk's exact export list — rather than diffing the
// session-wide export set — keeps it correct when two modules export the same
// identifier: each module's namespace object captures its own export.
func (s *Session) bindNamespaceObject(name string, exports []string) {
	if _, exists := s.env.Get(name); exists {
		return
	}
	m := vmpackage.NewMap()
	for _, n := range exports {
		if v, ok := s.env.Get(n); ok {
			m.MapSet(n, v)
		}
	}
	s.env.Define(name, m)
}

// declaredNamespace returns the segments of a module's leading `namespace a\b\c`
// declaration (nil if it declares none). Upstream Buzz exposes a no-alias
// import's exports under this full path; gopherbuzz mirrors that in
// bindNamespacePath, in addition to its own basename/splat conveniences.
func (s *Session) declaredNamespace(src string) []string {
	prog, err := parseModed(src, !s.embedded)
	if err != nil {
		return nil
	}
	for _, stmt := range prog.Stmts {
		if ns, ok := stmt.(*ast.NamespaceStmt); ok {
			return strings.Split(ns.Name, `\`)
		}
		// The namespace decl, if present, leads the file (after nothing that
		// binds a name); stop at the first non-namespace statement so we don't
		// scan the whole body.
		break
	}
	return nil
}

// bindNamespacePath makes a flat import's exports reachable under its declared
// `namespace a\b\c` path, matching upstream Buzz (`a\b\c\export`). Segments nest
// as maps, merging into any prefix a sibling namespace already created (so a\b
// and a\c coexist under one `a`). importPath is used only for the duplicate
// diagnostic. A single-segment namespace that collides with an existing binding
// (e.g. the module's basename equals its namespace) is left as-is: the basename
// object already resolves it. Returns an error on a duplicate namespace or a
// segment that collides with a non-map binding.
func (s *Session) bindNamespacePath(segments []string, exports []string, importPath string) error {
	if len(segments) == 0 {
		return nil
	}
	full := strings.Join(segments, `\`)
	if prev, dup := s.declaredNamespaces[full]; dup {
		return fmt.Errorf("buzz: import %q: the namespace %q already exists (also declared by import %q)", importPath, full, prev)
	}

	leaf := vmpackage.NewMap()
	for _, n := range exports {
		if v, ok := s.env.Get(n); ok {
			leaf.MapSet(n, v)
		}
	}

	if len(segments) == 1 {
		if _, exists := s.env.Get(segments[0]); !exists {
			s.env.Define(segments[0], leaf)
		}
		s.declaredNamespaces[full] = importPath
		return nil
	}

	// Multi-segment: walk/create nested maps, merging into existing prefixes.
	head := segments[0]
	var cur vmpackage.Value
	if existing, ok := s.env.Get(head); ok {
		if !existing.IsMap() {
			return fmt.Errorf("buzz: import %q: namespace %q conflicts with existing binding %q", importPath, full, head)
		}
		cur = existing
	} else {
		cur = vmpackage.NewMap()
		s.env.Define(head, cur)
	}
	for _, seg := range segments[1 : len(segments)-1] {
		next, ok := cur.MapGet(seg)
		if !ok {
			next = vmpackage.NewMap()
			cur.MapSet(seg, next)
		} else if !next.IsMap() {
			return fmt.Errorf("buzz: import %q: namespace %q conflicts with existing binding %q", importPath, full, seg)
		}
		cur = next
	}
	cur.MapSet(segments[len(segments)-1], leaf)
	s.declaredNamespaces[full] = importPath
	return nil
}

// loadImportAsAlias executes src in a sub-session that inherits the parent's
// host globals, then binds the sub-session's new globals as a map under alias.
func (s *Session) loadImportAsAlias(importPath, src, alias string) error {
	sub := newSession(s.ctx)
	sub.embedded = s.embedded // inherit the parent session's parse mode
	sub.searchPaths = s.searchPaths
	sub.SetIncludeDirs(s.includeDirs)
	sub.loadedPaths = s.loadedPaths
	sub.syntheticModules = s.syntheticModules
	sub.sourceModules = s.sourceModules
	sub.moduleResolver = s.moduleResolver

	// Copy parent's current globals into the sub-session so the imported file
	// can reference host APIs (magus, print, etc.).
	hostNames := s.env.Names()
	hostSlots := s.env.Slots()
	for name, slot := range hostNames {
		sub.env.Define(name, hostSlots[slot])
	}

	// Execute the imported file.
	if err := sub.Exec(s.ctx, src); err != nil {
		return fmt.Errorf("buzz: import %q: %w", importPath, err)
	}

	// Collect new globals: anything in sub-session not in the host snapshot.
	m := vmpackage.NewMap()
	for name, slot := range sub.env.Names() {
		if _, wasHost := hostNames[name]; !wasHost {
			m.MapSet(name, sub.env.Slots()[slot])
		}
	}
	s.env.Define(alias, m)
	return nil
}

// findIncludeFile resolves an import path to a file. It first tries each
// searchPaths template (the whole import path is substituted for `?`, matching
// upstream Buzz where the import string — not just its basename — is the library
// name), then falls back to the plain includeDirs (-L / BUZZ_INCLUDE_PATH),
// searching importPath.buzz (flat). This matches upstream resolution exactly.
func (s *Session) findIncludeFile(importPath string) string {
	for _, tmpl := range s.searchPaths {
		p := expandSearchPath(tmpl, importPath)
		if p == "" {
			continue
		}
		if bzzExists(p) {
			return p
		}
	}
	for _, dir := range s.includeDirs {
		if p := filepath.Join(dir, importPath+".buzz"); bzzExists(p) {
			return p
		}
	}
	return ""
}

// expandSearchPath fills a search-path template: it expands environment
// variables and substitutes `?` with importPath. It returns "" when the template
// references an unset environment variable, so an unset $BUZZ_PATH skips its
// entries instead of resolving to a bare "/?.buzz". Env expansion runs first so a
// `?` is never introduced by an env value (import paths contain no `$`).
func expandSearchPath(tmpl, importPath string) string {
	missing := false
	expanded := os.Expand(tmpl, func(key string) string {
		v := os.Getenv(key)
		if v == "" {
			missing = true
		}
		return v
	})
	if missing {
		return ""
	}
	return strings.ReplaceAll(expanded, "?", importPath)
}

func bzzExists(path string) bool { _, err := os.Stat(path); return err == nil }

// Compile parses, type-checks, and returns a runnable Chunk bound to this
// session's shared-globals scope. Pass the result to ExecChunk to run it,
// optionally multiple times without re-parsing.
func (s *Session) Compile(code string) (*vmpackage.Chunk, error) {
	return s.compileShared(code)
}

// ExecChunk runs a previously compiled Chunk in the session's environment.
func (s *Session) ExecChunk(ctx context.Context, chunk *vmpackage.Chunk) error {
	vm := vmpackage.NewVM(ctx)
	defer s.enter(vm)()
	_, err := vm.Run(chunk, s.env)
	if err == nil {
		for _, name := range chunk.Exports {
			s.exportedNames[name] = true
		}
	}
	return err
}

// SetGlobal binds name to v in the session's global Env.
func (s *Session) SetGlobal(name string, v vmpackage.Value) { s.env.Define(name, v) }

// GetGlobal returns the value bound to name, or Null if unbound. The signature
// matches the cross-engine engine.Session interface (which returns a bare
// Value); absence and an explicit null binding both yield Null.
func (s *Session) GetGlobal(name string) vmpackage.Value {
	v, _ := s.env.Get(name)
	return v
}

// Close releases the session's resources.
func (s *Session) Close() error {
	s.cancel()
	return nil
}

// CallValue invokes a Buzz function (or direct callable) Value with the given arguments.
// Host code (e.g. magus target dispatch) uses this to call back into Buzz.
func (s *Session) CallValue(ctx context.Context, fn vmpackage.Value, args []vmpackage.Value) (vmpackage.Value, error) {
	vm := vmpackage.NewVM(ctx)
	defer s.enter(vm)()
	if err := vm.Call(fn, args); err != nil {
		return vmpackage.Null, err
	}
	result, err := vm.Exec()
	return result, err
}
