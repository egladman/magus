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
	env           *Env
	targets       map[string]Callable
	exportedNames map[string]bool
	// curVM is the VM currently executing a chunk in this session, or nil between
	// runs. The debugger (debug.go) reads it for stack introspection, and the run
	// helpers set/restore it (save-and-restore so a pry() eval doesn't clobber the
	// paused outer VM). stepHook/stepMask carry a pending step hook onto each VM.
	curVM    *vmpackage.VM
	stepHook func(StepEvent, DebugFrame)
	stepMask StepMask
	// includeDirs is the ordered list of directories searched when an import
	// statement names a module not yet bound in the session. NewSession populates
	// it from BUZZ_INCLUDE_PATH; the host may override via SetIncludeDirs.
	includeDirs []string
	// loadedPaths tracks absolute paths already loaded to prevent re-execution
	// and break import cycles.
	loadedPaths map[string]bool
	// syntheticModules maps an import path (e.g. "magus/extra") to a host-built
	// module value. An `import "<path>"` binds it under the path's basename
	// (or alias) without touching the filesystem; see loadFileImports. The host
	// registers these via SetSyntheticModule.
	syntheticModules map[string]Value
	// moduleResolver, if set, is consulted for an import path that is neither
	// already bound nor a synthetic module, before the includeDirs file search.
	// It lets the host resolve a path-style import to a prebuilt module value
	// on demand (e.g. a magus spell handle for `import "spells/hello"`). A false
	// return falls through to the file search. Set via SetModuleResolver.
	moduleResolver func(importPath string) (Value, bool)
	// importedTypes accumulates the exported object/enum declarations of flat
	// imported .bzz modules, so compileShared can hand them to the checker and
	// the importing file can name those types in annotations and literals. The
	// runtime values already merge via flat Exec; this carries the *types* the
	// checker would otherwise never see. Populated in loadFileImports.
	importedTypes []ast.Node
	// sourceModules maps an import path to embedded .bzz source. Unlike a
	// synthetic module (a host Value carrying functions), a source module is
	// real Buzz source, so its exported object/enum *types* are visible to the
	// importer's checker. It flat-merges like a file import. Set via
	// SetSourceModule; used for shipped Buzz library modules (e.g. canonical
	// magus types) that have no .bzz file on the include path.
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
}

// SetSyntheticModule registers v as the module imported by `import "<importPath>"`.
// The import binds v under the path's basename (e.g. "util" for "magus/extra"),
// or under an explicit alias. Host-provided modules resolve before any file
// search, so they need no .bzz file on disk.
func (s *Session) SetSyntheticModule(importPath string, v Value) {
	if s.syntheticModules == nil {
		s.syntheticModules = map[string]Value{}
	}
	s.syntheticModules[importPath] = v
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
func (s *Session) SetModuleResolver(fn func(importPath string) (Value, bool)) {
	s.moduleResolver = fn
}

func newSession(ctx context.Context) *Session {
	ctx2, cancel := context.WithCancel(ctx)
	env := vmpackage.NewEnv()
	vmpackage.RegisterStdlib(env)
	s := &Session{
		ctx:           ctx2,
		cancel:        cancel,
		env:           env,
		targets:       make(map[string]Callable),
		exportedNames: make(map[string]bool),
		loadedPaths:   make(map[string]bool),
	}
	// resume/resolve are session-bound so they can swap curVM to the fiber's VM
	// for the duration of Exec(), making Frames()/CallDepth()/step hooks reflect
	// the fiber's stack. They are registered under their keyword names (which the
	// user cannot shadow since resume/resolve are lexer keywords).
	env.Define("resume", vmpackage.DirectValue("resume", s.builtinResume))
	env.Define("resolve", vmpackage.DirectValue("resolve", s.builtinResolve))
	return s
}

// builtinResume is the session-aware implementation of `resume fiber`. It swaps
// curVM to the fiber's VM via enter() so debug introspection (Frames,
// CallDepth, step hooks) sees the fiber's stack for the duration of Exec().
//
// Returns the yielded value, or null if nothing was yielded or the fiber is
// over (upstream parity: resume never errors on a completed fiber).
func (s *Session) builtinResume(ctx context.Context, args []Value) (Value, error) {
	if len(args) < 1 {
		return Null, fmt.Errorf("buzz: resume requires a fiber argument")
	}
	fib, ok := vmpackage.AsFiber(args[0])
	if !ok {
		return Null, fmt.Errorf("buzz: resume requires a fiber, got %s", args[0].Kind())
	}
	switch fib.Status() {
	case vmpackage.FiberDone:
		// upstream: resume on a completed fiber returns null. If the fiber ended
		// in an error, re-surface it rather than swallowing it (Err is nil for a
		// clean completion, so this returns null, nil in that case).
		return Null, fib.Err()
	case vmpackage.FiberRunning:
		return Null, fmt.Errorf("buzz: cannot resume a running fiber (recursive resume)")
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
		return Null, err
	}
	fib.SetStatus(vmpackage.FiberDone)
	fib.SetReturn(result) // cache for resolve
	return Null, nil      // upstream: resume returns null on normal completion
}

// builtinResolve is the session-aware implementation of `resolve fiber`. It
// runs the fiber to completion, ignoring all yield points, and returns the
// fiber function's return value. Calling resolve on an already-done fiber
// returns the cached return value (upstream: resolve is idempotent after done).
func (s *Session) builtinResolve(ctx context.Context, args []Value) (Value, error) {
	if len(args) < 1 {
		return Null, fmt.Errorf("buzz: resolve requires a fiber argument")
	}
	fib, ok := vmpackage.AsFiber(args[0])
	if !ok {
		return Null, fmt.Errorf("buzz: resolve requires a fiber, got %s", args[0].Kind())
	}
	switch fib.Status() {
	case vmpackage.FiberDone:
		// idempotent: return the cached return value, re-surfacing a cached error
		// (Err is nil for a clean completion).
		return fib.Return(), fib.Err()
	case vmpackage.FiberRunning:
		return Null, fmt.Errorf("buzz: cannot resolve a running fiber (recursive resolve)")
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
			return Null, err
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
		return Null, err
	}
}

// NewSession creates a Buzz execution context. Inject globals with SetGlobal
// and register target callbacks via Targets. Close releases the context.
//
// BUZZ_INCLUDE_PATH (colon-separated on Unix, semicolon-separated on Windows)
// is read to populate the initial include directory list. The host may override
// it with SetIncludeDirs after construction.
func NewSession(ctx context.Context) *Session {
	s := newSession(ctx)
	if v := os.Getenv("BUZZ_INCLUDE_PATH"); v != "" {
		s.includeDirs = filepath.SplitList(v)
	}
	return s
}

// SetIncludeDirs replaces the directories searched for file-based imports.
// The host (e.g. magus/internal/interp) calls this to enforce workspace
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
func (s *Session) Targets() map[string]Callable { return s.targets }

// Exec parses, type-checks, compiles, and executes Buzz source code in the session's environment.
// Type errors are returned as hard errors (Buzz is statically typed).
func (s *Session) Exec(ctx context.Context, code string) error {
	chunk, err := s.compileShared(code)
	if err != nil {
		return err
	}
	vm := vmpackage.NewVM(ctx)
	defer s.enter(vm)()
	_, err = vm.Run(chunk, s.env)
	if err == nil {
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
	}
	return err
}

// execImport runs an imported module's source with import-private collection on,
// so the module's non-exported top-level names are recorded in importPrivate and
// hidden from the importer's checker (exports-only visibility). The flag is
// save-and-restored so a nested import (a module importing another) still collects.
func (s *Session) execImport(ctx context.Context, code string) error {
	prev := s.collectImportPrivate
	s.collectImportPrivate = true
	defer func() { s.collectImportPrivate = prev }()
	return s.Exec(ctx, code)
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
func (s *Session) Eval(ctx context.Context, code string) (Value, error) {
	chunk, err := s.compileShared(code)
	if err != nil {
		return Null, err
	}
	return s.EvalChunk(ctx, chunk)
}

// EvalChunk runs a previously compiled Chunk and returns its result value. The
// REPL driver compiles first (to tell a syntax error — fall back to the
// statement form — from a runtime error) then runs exactly once via this, so a
// snippet with side effects never executes twice.
func (s *Session) EvalChunk(ctx context.Context, chunk *Chunk) (Value, error) {
	vm := vmpackage.NewVM(ctx)
	defer s.enter(vm)()
	v, err := vm.Run(chunk, s.env)
	return v, err
}

// Globals returns a snapshot of the session's top-level bindings (name → value),
// including host-injected globals. The REPL filters host names for .globals.
func (s *Session) Globals() map[string]Value {
	names := s.env.Names()
	slots := s.env.Slots()
	out := make(map[string]Value, len(names))
	for name, slot := range names {
		out[name] = slots[slot]
	}
	return out
}

// Exports returns the subset of Globals() whose names were declared with
// export in a file executed via Exec or ExecChunk. The map is a fresh snapshot;
// mutations don't affect the session.
func (s *Session) Exports() map[string]Value {
	if len(s.exportedNames) == 0 {
		return nil
	}
	all := s.Globals()
	out := make(map[string]Value, len(s.exportedNames))
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
func (s *Session) compileShared(code string) (*Chunk, error) {
	prog, err := Parse(code)
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
	if errs := checkWithGlobals(prog, globals, s.importedTypes, s.importPrivateHint()); len(errs) != 0 {
		return nil, errs[0]
	}
	// DebugLines so magus.pry() / the REPL can report a paused frame's line and
	// drive line-level stepping; the cost is one parallel int32 slice per chunk.
	return CompileWith(prog, CompileOptions{
		SharedGlobals:   true,
		DebugLines:      true,
		PromoteTopLevel: s.promoteTopLevel,
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
// yet bound in the session, searches includeDirs for a matching .bzz file. The
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
		parts := strings.Split(imp.Path, "/")
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
		if v, ok := s.syntheticModules[imp.Path]; ok {
			s.env.Define(boundName, v)
			continue
		}

		// Host-provided source modules ship as embedded .bzz source so the
		// importer can use their exported object/enum types. They flat-merge
		// like a file import; the loadedPaths guard (keyed by import path)
		// prevents a second exec.
		if src, ok := s.sourceModules[imp.Path]; ok {
			key := "source:" + imp.Path
			if s.loadedPaths[key] {
				continue
			}
			s.loadedPaths[key] = true
			s.collectImportedTypes(src)
			if err := s.execImport(s.ctx, src); err != nil {
				return fmt.Errorf("buzz: import %q: %w", imp.Path, err)
			}
			continue
		}

		// A host module resolver (e.g. local magus spells: `import "spells/hello"`)
		// gets first refusal on path-style imports, ahead of the file search.
		if s.moduleResolver != nil {
			if v, ok := s.moduleResolver(imp.Path); ok {
				s.env.Define(boundName, v)
				continue
			}
		}

		path := s.findIncludeFile(basename)
		if path == "" {
			continue
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
			// collect its exported object/enum types so the importer can name
			// them (Exec only merges runtime values, not type declarations).
			s.collectImportedTypes(string(data))
			if err := s.execImport(s.ctx, string(data)); err != nil {
				return fmt.Errorf("buzz: import %q: %w", imp.Path, err)
			}
		}
	}
	return nil
}

// collectImportedTypes parses a flat-imported module's source and records its
// exported object/enum declarations on the session, so compileShared can hand
// them to the checker. Parse errors are ignored here: the subsequent Exec
// re-parses the same source and reports them authoritatively.
func (s *Session) collectImportedTypes(src string) {
	prog, err := Parse(src)
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
		}
	}
}

// loadImportAsAlias executes src in a sub-session that inherits the parent's
// host globals, then binds the sub-session's new globals as a map under alias.
func (s *Session) loadImportAsAlias(importPath, src, alias string) error {
	sub := newSession(s.ctx)
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
	m := NewMap()
	for name, slot := range sub.env.Names() {
		if _, wasHost := hostNames[name]; !wasHost {
			m.MapSet(name, sub.env.Slots()[slot])
		}
	}
	s.env.Define(alias, m)
	return nil
}

// findIncludeFile searches includeDirs for name.bzz (flat) or name/name.bzz
// (directory-per-module, matching the built-in spell layout).
func (s *Session) findIncludeFile(name string) string {
	for _, dir := range s.includeDirs {
		if p := filepath.Join(dir, name+".bzz"); bzzExists(p) {
			return p
		}
		if p := filepath.Join(dir, name, name+".bzz"); bzzExists(p) {
			return p
		}
	}
	return ""
}

func bzzExists(path string) bool { _, err := os.Stat(path); return err == nil }

// Compile parses, type-checks, and returns a runnable Chunk bound to this
// session's shared-globals scope. Pass the result to ExecChunk to run it,
// optionally multiple times without re-parsing.
func (s *Session) Compile(code string) (*Chunk, error) {
	return s.compileShared(code)
}

// ExecChunk runs a previously compiled Chunk in the session's environment.
func (s *Session) ExecChunk(ctx context.Context, chunk *Chunk) error {
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
func (s *Session) SetGlobal(name string, v Value) { s.env.Define(name, v) }

// GetGlobal returns the value bound to name, or Null if unbound. The signature
// matches the cross-engine engine.Session interface (which returns a bare
// Value); absence and an explicit null binding both yield Null.
func (s *Session) GetGlobal(name string) Value {
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
func (s *Session) CallValue(ctx context.Context, fn Value, args []Value) (Value, error) {
	vm := vmpackage.NewVM(ctx)
	defer s.enter(vm)()
	if err := vm.Call(fn, args); err != nil {
		return Null, err
	}
	result, err := vm.Exec()
	return result, err
}
