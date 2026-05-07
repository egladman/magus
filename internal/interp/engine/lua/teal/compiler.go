package teal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/interp/engine"
	lua "github.com/egladman/magus/internal/interp/engine/lua"
	"github.com/egladman/magus/internal/interp/engine/lua/teal/spell"
	"github.com/egladman/magus/internal/interp/engine/lua/teal/std"
	ispell "github.com/egladman/magus/internal/spell"
)

// luaEngineNames mirrors interp/registry.go; duplicated to avoid an import cycle.
var luaEngineNames = []string{"luajit", "gopherlua"}

func defaultLuaEngine() engine.Engine {
	for _, name := range luaEngineNames {
		if e := engine.Lookup(name); e != nil {
			return e
		}
	}
	return nil
}

// TypeDecls returns concatenated .d.tl host-binding declarations; memoized via sync.OnceValues.
var TypeDecls = sync.OnceValues(func() (string, error) {
	var sb strings.Builder
	if err := fs.WalkDir(std.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".d.tl") {
			return err
		}
		data, readErr := std.FS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		sb.Write(data)
		sb.WriteByte('\n')
		return nil
	}); err != nil {
		return "", fmt.Errorf("teal: load type decls: %w", err)
	}
	// MagusSpell is derived from the built-in spell set rather than embedded as
	// a .d.tl, so the typed surface of require("magus.spell.<name>") stays in
	// lockstep with spells.json (see spell.SpellTypeDecl).
	sb.WriteString(spell.SpellTypeDecl())
	sb.WriteByte('\n')
	return sb.String(), nil
})

// spellModulesDir materializes one Teal declaration stub per built-in spell
// into a temp dir and returns it. Each stub `magus/spell/<name>.d.tl` declares a
// module whose type is MagusSpell, so `require("magus.spell.<name>")` type-checks
// to MagusSpell (a misspelled module is a compile error). The runtime side is
// already served by package.preload (see bindings/spell.go); this only teaches
// the Teal type-checker the module surface. Memoized for the process lifetime.
var spellModulesDir = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "magus-spellmods-")
	if err != nil {
		return "", fmt.Errorf("teal: spell module stubs: %w", err)
	}
	base := filepath.Join(dir, "magus", "spell")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("teal: spell module stubs: %w", err)
	}
	// A declaration file whose returned value is typed MagusSpell. MagusSpell is
	// a global record from the host preamble, in scope when the main file's
	// require resolves this stub (same compile env).
	const stub = "local s: MagusSpell\nreturn s\n"
	for _, spec := range ispell.Builtins() {
		// Keyed by spec.Name (the logical spell name, e.g. "go"), matching the
		// runtime package.preload key "magus.spell.<name>".
		if err := os.WriteFile(filepath.Join(base, spec.Name+".d.tl"), []byte(stub), 0o644); err != nil {
			return "", fmt.Errorf("teal: spell module stubs: %w", err)
		}
	}
	return dir, nil
})

// stdModulesDir materializes one Teal declaration stub per std module into a
// temp dir. Each stub `magus/extra/<name>.d.tl` returns a value typed as the
// module's host record (Os, Fs, …), so `require("magus.extra.<name>")` type-checks
// to that record (a misspelled module is a compile error). The record types are
// ambient globals from the host preamble (the std .d.tl files in TypeDecls), in
// scope when the main file's require resolves the stub. The runtime side is
// served by package.preload (bindings.registerStd); this only teaches the type-
// checker the module surface. archive has no host record (untyped in Teal), so
// it gets no stub — matching its prior status. Memoized for the process lifetime.
var stdModulesDir = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "magus-stdmods-")
	if err != nil {
		return "", fmt.Errorf("teal: std module stubs: %w", err)
	}
	base := filepath.Join(dir, "magus", "extra")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("teal: std module stubs: %w", err)
	}
	// module name → the ambient record type its require returns.
	records := map[string]string{
		"os": "Os", "platform": "Platform", "fs": "Fs", "vcs": "VCSModule",
		"crypto": "Crypto", "env": "Env", "json": "Json",
		"http": "Http", "time": "Time", "fmt": "Fmt", "charm": "CharmTool",
	}
	for name, rec := range records {
		stub := fmt.Sprintf("local s: %s\nreturn s\n", rec)
		if err := os.WriteFile(filepath.Join(base, name+".d.tl"), []byte(stub), 0o644); err != nil {
			return "", fmt.Errorf("teal: std module stubs: %w", err)
		}
	}
	return dir, nil
})

// configureModulePath points the Teal type-checker's module search (tl.search_module)
// at the built-in spell stubs plus workspace-local spell locations, so both
// `require("magus.spell.go")` (built-in, typed) and `require("hello")` for a
// local ./spells/hello.tl resolve at compile time. Path entries use Lua's `?.lua`
// form; tl.search_module swaps the suffix to .d.tl/.tl when searching. Falls back
// to the existing package.path for anything else.
//
// dir is the directory of the magusfile being compiled; local-spell lookups
// resolve relative to it first, so a magusfile type-checked from outside its own
// directory (e.g. workspace preload visiting a sub-project) still finds its
// ./spells. An empty dir (REPL) uses only the cwd-relative fallback.
func configureModulePath(r lua.Session, dir string) error {
	tl, ok := r.GetGlobal("tl").AsTable()
	if !ok {
		return fmt.Errorf("teal: tl global is not a table")
	}
	stubDir, err := spellModulesDir()
	if err != nil {
		return err
	}
	stdStubDir, err := stdModulesDir()
	if err != nil {
		return err
	}
	pkgPath := ""
	if pkg, ok := r.GetGlobal("package").AsTable(); ok {
		pkgPath, _ = pkg.RawGetString("path").AsString()
	}
	entries := []string{
		filepath.Join(stubDir, "?.lua"),    // built-in spell stubs (magus/spell/<name>.d.tl)
		filepath.Join(stdStubDir, "?.lua"), // std module stubs (magus/extra/<name>.d.tl)
	}
	if dir != "" {
		// magusfile-dir-relative — resolves local spells regardless of cwd. Each
		// flat <?>.lua entry is paired with a <?>/spell.lua entry for the directory
		// convention (spells/<name>/spell.tl).
		entries = append(entries,
			filepath.Join(dir, "?.lua"),
			filepath.Join(dir, "?", "spell.lua"),
			filepath.Join(dir, "spells", "?.lua"),
			filepath.Join(dir, "spells", "?", "spell.lua"),
			filepath.Join(dir, "magusfiles", "?.lua"),
		)
	}
	entries = append(entries,
		"./?.lua",              // workspace-local spell at cwd (the common run-from-here case)
		"./?/spell.lua",        // dir convention at cwd
		"./spells/?.lua",       // conventional spells/ dir
		"./spells/?/spell.lua", // dir convention under spells/
		"./magusfiles/?.lua",   // conventional magusfiles/ dir
	)
	if pkgPath != "" {
		entries = append(entries, pkgPath)
	}
	tl.RawSetString("path", engine.StringValue(strings.Join(entries, ";")))
	// The search hook reads this every lookup to resolve local spells relative to
	// the magusfile dir; set it on every call since one VM compiles many magusfiles.
	tl.RawSetString("__magus_base", engine.StringValue(dir))

	stub, err := localSpellStub()
	if err != nil {
		return err
	}
	return installLocalSpellSearchHook(r, stub)
}

// localSpellStub writes one WorkspaceSpell declaration stub and returns its path.
// A workspace-local spell file returns its mgs_ functions, but require()
// substitutes a spell handle at runtime; typing the require against this stub
// keeps compile and runtime in agreement (the same trick spellModulesDir uses for
// built-ins). Local spells use WorkspaceSpell rather than MagusSpell: their ops
// are not in the built-in union but follow the same CLI-command naming, so the
// __index fallback types any op access (foo["mytool"]()).
var localSpellStub = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "magus-localspell-")
	if err != nil {
		return "", fmt.Errorf("teal: local spell stub: %w", err)
	}
	p := filepath.Join(dir, "magusspell.d.tl")
	if err := os.WriteFile(p, []byte("local s: WorkspaceSpell\nreturn s\n"), 0o644); err != nil {
		return "", fmt.Errorf("teal: local spell stub: %w", err)
	}
	return p, nil
})

// installLocalSpellSearchHook wraps tl.search_module so a require that resolves
// to a workspace-local spell (the same <name>.tl / spells/ / magusfiles/ set the
// runtime searcher in registerLocalSpellSearcher matches) type-checks as
// MagusSpell via the shared stub, rather than inferring the spell file's raw
// mgs_ functions record. Guarded so repeated configuration does not re-wrap.
func installLocalSpellSearchHook(r lua.Session, stubPath string) error {
	// The hook is installed once per VM, but the magusfile dir (tl.__magus_base)
	// changes per compile, so it is read dynamically here rather than baked in.
	const tmpl = `
if not tl.__magus_local_spell_hook then
   tl.__magus_local_spell_hook = true
   local STUB = [[%s]]
   local orig = tl.search_module
   local function exists(p) local f = io.open(p, "r") if f then f:close() return true end return false end
   -- Each base is searched flat (<base>/<rel>.tl) and under the directory
   -- convention (<base>/<rel>/spell.tl).
   local function under(base, rel)
      return exists(base.."/"..rel..".tl") or exists(base.."/"..rel.."/spell.tl")
          or exists(base.."/spells/"..rel..".tl") or exists(base.."/spells/"..rel.."/spell.tl")
          or exists(base.."/magusfiles/"..rel..".tl")
   end
   local function is_local(rel)
      local base = tl.__magus_base
      if base and base ~= "" and under(base, rel) then return true end
      return exists(rel..".tl") or exists(rel.."/spell.tl")
          or exists("spells/"..rel..".tl") or exists("spells/"..rel.."/spell.tl")
          or exists("magusfiles/"..rel..".tl")
   end
   tl.search_module = function(name, search_all)
      local rel = name:gsub("%%.", "/")
      if is_local(rel) then
         return STUB, io.open(STUB, "r")
      end
      return orig(name, search_all)
   end
end
`
	fn, err := r.LoadString(fmt.Sprintf(tmpl, stubPath))
	if err != nil {
		return fmt.Errorf("teal: local spell search hook: %w", err)
	}
	if err := r.Call(engine.CallParams{Fn: fn, NRet: 0, Protect: true}); err != nil {
		return fmt.Errorf("teal: local spell search hook: %w", err)
	}
	return nil
}

// EnsureCompiler loads tl.lua only when the "tl" global is not yet set in r.
// Avoids re-executing the ~30 KB compiler on VMs that already have it.
func EnsureCompiler(r lua.Session) error {
	if !r.GetGlobal("tl").IsNil() {
		return nil
	}
	return LoadCompiler(r)
}

// LoadCompiler executes the embedded tl.lua and stores the result in the global "tl".
func LoadCompiler(r lua.Session) error {
	fn, err := r.LoadString(compiler)
	if err != nil {
		return fmt.Errorf("teal: load tl.lua: %w", err)
	}
	if err := r.Call(engine.CallParams{Fn: fn, NRet: 1, Protect: true}); err != nil {
		return fmt.Errorf("teal: exec tl.lua: %w", err)
	}
	tl := r.Get(-1)
	r.Pop(1)
	if tl.IsNil() {
		return fmt.Errorf("teal: tl.lua returned nil")
	}
	r.SetGlobal("tl", tl)

	tlt, ok := tl.AsTable()
	if !ok {
		return fmt.Errorf("teal: tl global is not a table")
	}
	loaderFn := tlt.RawGetString("loader")
	if !loaderFn.IsNil() {
		if err := r.Call(engine.CallParams{Fn: loaderFn, NRet: 0, Protect: true}); err != nil {
			return fmt.Errorf("teal: tl.loader(): %w", err)
		}
	}
	return nil
}

// InstallUtf8Shim adds Lua 5.3 APIs that tl.lua requires but are not provided
// by all backends: utf8.char, table.move, math.tointeger.
func InstallUtf8Shim(r lua.Session) {
	utf8 := r.NewTable()
	utf8.RawSetString("char", r.NewFunction(func(_ context.Context, r lua.Session) int {
		n := int(r.CheckNumber(1))
		var b []byte
		switch {
		case n < 0x80:
			b = []byte{byte(n)}
		case n < 0x800:
			b = []byte{byte(0xC0 | (n >> 6)), byte(0x80 | (n & 0x3F))}
		case n < 0x10000:
			b = []byte{byte(0xE0 | (n >> 12)), byte(0x80 | ((n >> 6) & 0x3F)), byte(0x80 | (n & 0x3F))}
		default:
			b = []byte{byte(0xF0 | (n >> 18)), byte(0x80 | ((n >> 12) & 0x3F)), byte(0x80 | ((n >> 6) & 0x3F)), byte(0x80 | (n & 0x3F))}
		}
		r.Push(engine.StringValue(string(b)))
		return 1
	}))
	r.SetGlobal("utf8", utf8)

	if tableLib, ok := r.GetGlobal("table").AsTable(); ok {
		tableLib.RawSetString("move", r.NewFunction(func(_ context.Context, r lua.Session) int {
			a1 := r.CheckTable(1)
			f := r.CheckInt(2)
			e := r.CheckInt(3)
			t := r.CheckInt(4)
			var a2 engine.Table
			if r.GetTop() >= 5 && !r.Get(5).IsNil() {
				a2 = r.CheckTable(5)
			} else {
				a2 = a1
			}
			for i := f; i <= e; i++ {
				a2.RawSetInt(t+(i-f), a1.RawGetInt(i))
			}
			r.Push(a2)
			return 1
		}))
	}

	if mathLib, ok := r.GetGlobal("math").AsTable(); ok {
		mathLib.RawSetString("tointeger", r.NewFunction(func(_ context.Context, r lua.Session) int {
			n := r.CheckNumber(1)
			i := float64(int64(n))
			if i == n {
				r.Push(engine.NumberValue(i))
			} else {
				r.Push(engine.NilValue)
			}
			return 1
		}))
		mathLib.RawSetString("maxinteger", engine.NumberValue(9007199254740992.0))
		mathLib.RawSetString("mininteger", engine.NumberValue(-9007199254740992.0))
	}

	installLoadShim(r)
}

// installLoadShim makes the global load accept a string chunk (Lua 5.2+). tl.lua
// targets Lua 5.3 and binds `local load = ... or load` at its top, then calls
// load(<string>, "@"..filename, "t") from its runtime package loader
// (tl_package_loader). gopher-lua's load only accepts a reader function, so
// without this shim that path fails with "bad argument #1 to load (function
// expected, got string)" whenever a require falls through to the Teal loader.
//
// A string chunk is routed to loadstring, which — unlike gopher-lua's load —
// accepts the chunkname as its second argument, so tracebacks raised inside a
// Teal-required module keep their "@filename" source label. The reader-function
// form forwards to the original load unchanged. Both delegate with MultRet so
// load's native arity (one value on success, two on error) is preserved rather
// than padded. (Lua 5.2's mode/env arguments are inapplicable on these 5.1
// backends and are ignored for the string form.)
//
// Installed before the compiler executes so tl.lua captures the shimmed load.
func installLoadShim(r lua.Session) {
	origLoad := r.GetGlobal("load")
	origLoadString := r.GetGlobal("loadstring")
	r.SetGlobal("load", r.NewFunction(func(_ context.Context, r lua.Session) int {
		// A string chunk goes to loadstring (it takes a chunkname); anything
		// else (a reader function) goes to the original load. Forward every
		// argument verbatim so chunkname and error messages are preserved.
		target := origLoad
		if _, isString := r.CheckAny(1).AsString(); isString {
			target = origLoadString
		}
		top := r.GetTop()
		args := make([]engine.Value, top)
		for i := 1; i <= top; i++ {
			args[i-1] = r.CheckAny(i)
		}
		base := r.GetTop()
		if err := r.Call(engine.CallParams{Fn: target, NRet: -1, Protect: true}, args...); err != nil {
			r.Push(engine.NilValue)
			r.Push(engine.StringValue(err.Error()))
			return 2
		}
		return r.GetTop() - base // forward however many values the delegate returned
	}))
}

// Compile calls tl.gen and returns compiled Lua bytes; type/syntax errors surface as Go errors.
func Compile(r lua.Session, path string, combined []byte) ([]byte, error) {
	tl, ok := r.GetGlobal("tl").AsTable()
	if !ok {
		return nil, fmt.Errorf("teal: tl.lua not loaded")
	}
	if err := configureModulePath(r, filepath.Dir(path)); err != nil {
		return nil, err
	}

	initEnv := tl.RawGetString("init_env")
	if err := r.Call(engine.CallParams{Fn: initEnv, NRet: 1, Protect: true},
		engine.BoolValue(false), engine.BoolValue(false), engine.StringValue(LuaTarget)); err != nil {
		return nil, fmt.Errorf("tl.init_env: %w", err)
	}
	env := r.Get(-1)
	r.Pop(1)
	if env.IsNil() {
		return nil, fmt.Errorf("tl.init_env returned nil")
	}

	genFn := tl.RawGetString("gen")
	if err := r.Call(engine.CallParams{Fn: genFn, NRet: 2, Protect: true},
		engine.StringValue(string(combined)), env); err != nil {
		return nil, fmt.Errorf("%s: teal compile: %w", path, err)
	}
	code := r.Get(-2)
	result := r.Get(-1)
	r.Pop(2)

	if code.IsNil() {
		msg := extractErrors(path, result, "syntax_errors")
		if msg == "" {
			msg = fmt.Sprintf("teal compile: %s: failed (no diagnostics extracted)", path)
		}
		return nil, errors.New(msg)
	}

	if errs := extractErrors(path, result, "type_errors"); errs != "" {
		return nil, errors.New(errs)
	}

	str, _ := code.AsString()
	return []byte(str), nil
}

// CompileSnippet compiles a Teal snippet for REPL use (lax=true; host preamble prepended).
func CompileSnippet(r lua.Session, source string) ([]byte, error) {
	preamble, err := TypeDecls()
	if err != nil {
		return nil, err
	}
	combined := ConcatPreamble(preamble, []byte(source))

	tl, ok := r.GetGlobal("tl").AsTable()
	if !ok {
		return nil, fmt.Errorf("teal: tl.lua not loaded")
	}
	if err := configureModulePath(r, ""); err != nil {
		return nil, err
	}

	initEnv := tl.RawGetString("init_env")
	if err := r.Call(engine.CallParams{Fn: initEnv, NRet: 1, Protect: true},
		engine.BoolValue(true), engine.BoolValue(false), engine.StringValue(LuaTarget)); err != nil {
		return nil, fmt.Errorf("tl.init_env: %w", err)
	}
	env := r.Get(-1)
	r.Pop(1)
	if env.IsNil() {
		return nil, fmt.Errorf("tl.init_env returned nil")
	}

	genFn := tl.RawGetString("gen")
	if err := r.Call(engine.CallParams{Fn: genFn, NRet: 2, Protect: true},
		engine.StringValue(string(combined)), env); err != nil {
		return nil, fmt.Errorf("<repl>: teal compile: %w", err)
	}
	code := r.Get(-2)
	result := r.Get(-1)
	r.Pop(2)

	if code.IsNil() {
		msg := extractErrors("<repl>", result, "syntax_errors")
		if msg == "" {
			msg = "teal compile: <repl>: failed (no diagnostics)"
		}
		return nil, errors.New(msg)
	}

	if errs := extractErrors("<repl>", result, "type_errors"); errs != "" {
		return nil, errors.New(errs)
	}

	str, _ := code.AsString()
	return []byte(str), nil
}

// ConcatPreamble builds preamble+src into a single []byte with one allocation.
func ConcatPreamble(preamble string, src []byte) []byte {
	out := make([]byte, len(preamble)+len(src))
	copy(out, preamble)
	copy(out[len(preamble):], src)
	return out
}

// extractErrors reads a named array field from a Teal result table and
// formats the errors as a multi-line string. Returns "" if the field is empty.
func extractErrors(filename string, result engine.Value, field string) string {
	tbl, ok := result.AsTable()
	if !ok {
		return ""
	}
	errList, ok := tbl.RawGetString(field).AsTable()
	if !ok {
		return ""
	}
	var msgs []string
	errList.ForEach(func(_, v engine.Value) {
		e, ok := v.AsTable()
		if !ok {
			return
		}
		yv, _ := e.RawGetString("y").AsNumber()
		xv, _ := e.RawGetString("x").AsNumber()
		y := int(yv)
		x := int(xv)
		msg := e.RawGetString("msg").String()
		msgs = append(msgs, fmt.Sprintf("%s:%d:%d: %s", filename, y, x, msg))
	})
	return strings.Join(msgs, "\n")
}

// CompileFile compiles the Teal source at srcPath to Lua using a fresh VM per call.
// Intended for code-generation tools, not runtime use.
func CompileFile(ctx context.Context, srcPath, preamble string) ([]byte, error) {
	eng := defaultLuaEngine()
	if eng == nil {
		return nil, fmt.Errorf("teal: no Lua engine registered; blank-import engine/lua/gopherlua or engine/lua/luajit")
	}
	sess, err := eng.NewSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("teal: new session: %w", err)
	}
	r, ok := sess.(lua.Session)
	if !ok {
		_ = sess.Close()
		return nil, fmt.Errorf("teal: engine session does not implement lua.Session")
	}
	defer r.Close()

	InstallUtf8Shim(r)
	if err := LoadCompiler(r); err != nil {
		return nil, err
	}
	src, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf("teal: read %s: %w", srcPath, err)
	}
	combined := ConcatPreamble(preamble, src)
	return Compile(r, srcPath, combined)
}
