package buzz_test

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
)

func TestNewSession(t *testing.T) {
	s := buzz.NewSession(context.Background())
	if s == nil {
		t.Fatal("NewSession returned nil")
	}
	if s.Targets() == nil {
		t.Error("Targets() should return a non-nil map")
	}
}

func TestSession_ExecSimpleAssignment(t *testing.T) {
	s := buzz.NewSession(context.Background())
	if err := s.Exec(context.Background(), `var x: int = 42;`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	globals := s.Globals()
	v, ok := globals["x"]
	if !ok {
		t.Fatal("global 'x' not found after exec")
	}
	if !v.IsInt() {
		t.Fatalf("x.IsInt() = false, got Kind() = %q", v.Kind())
	}
	if v.AsInt() != 42 {
		t.Errorf("x.AsInt() = %d, want 42", v.AsInt())
	}
}

func TestSession_EvalExpression(t *testing.T) {
	s := buzz.NewSession(context.Background())
	// Use a function that returns a value to test Eval's return path.
	if err := s.Exec(context.Background(), `fun sum() > int { return 1 + 2; }`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	v, err := s.Eval(context.Background(), `return sum()`)
	if err != nil {
		t.Fatalf("Eval(return sum()): %v", err)
	}
	if !v.IsInt() || v.AsInt() != 3 {
		t.Errorf("Eval(return sum()) = %v, want 3", v)
	}
}

func TestSession_SyntheticModule(t *testing.T) {
	s := buzz.NewSession(context.Background())
	mod := buzz.NewMap()
	mod.MapSet("answer", buzz.IntValue(42))
	// Host registers the module under an import path; it resolves with no file
	// on disk and no include dirs configured.
	s.SetSyntheticModule("example/demo", mod)

	if err := s.Exec(context.Background(), `
import "example/demo";
var x = demo.answer;
`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	v, ok := s.Globals()["x"]
	if !ok {
		t.Fatal("global 'x' not bound; synthetic import did not resolve")
	}
	if !v.IsInt() || v.AsInt() != 42 {
		t.Errorf("x = %v, want 42", v)
	}
}

func TestSession_ModuleResolver(t *testing.T) {
	s := buzz.NewSession(context.Background())
	mod := buzz.NewMap()
	mod.MapSet("answer", buzz.IntValue(7))
	// The resolver gets first refusal on a path-style import that is neither
	// bound nor a synthetic module; it binds the returned module under the
	// path's basename.
	var gotPath string
	s.SetModuleResolver(func(importPath string) (buzz.Value, bool) {
		gotPath = importPath
		if importPath == "spells/widget" {
			return mod, true
		}
		return buzz.Null, false
	})

	if err := s.Exec(context.Background(), `
import "spells/widget";
var x = widget.answer;
`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if gotPath != "spells/widget" {
		t.Errorf("resolver called with %q, want \"spells/widget\"", gotPath)
	}
	v, ok := s.Globals()["x"]
	if !ok {
		t.Fatal("global 'x' not bound; resolver import did not resolve")
	}
	if !v.IsInt() || v.AsInt() != 7 {
		t.Errorf("x = %v, want 7", v)
	}
}

func TestSession_Compile_And_ExecChunk(t *testing.T) {
	s := buzz.NewSession(context.Background())
	chunk, err := s.Compile(`var y: str = "hello";`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if err := s.ExecChunk(context.Background(), chunk); err != nil {
		t.Fatalf("ExecChunk: %v", err)
	}
	v, ok := s.Globals()["y"]
	if !ok {
		t.Fatal("global 'y' not set after ExecChunk")
	}
	if v.AsString() != "hello" {
		t.Errorf("y = %q, want %q", v.AsString(), "hello")
	}
}

// TestConformance runs all .bzz files in testdata/.
// Each file may have header comments:
//
//	// @expect: <value>  — run and assert __r.String() == <value>
//	// @error: <substr>  — assert parse/type/compile/runtime error contains <substr>
//	// @skip: <reason>   — skip this test case
func TestConformance(t *testing.T) {
	files, err := filepath.Glob("testdata/*.bzz")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no conformance test files found in testdata/")
	}
	for _, path := range files {
		path := path
		name := strings.TrimSuffix(filepath.Base(path), ".bzz")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			meta := parseConformanceMeta(string(src))
			if meta.skip != "" {
				t.Skipf("skip: %s", meta.skip)
			}
			runConformanceCase(t, name, string(src), meta)
		})
	}
}

type conformanceMeta struct {
	expect string
	errStr string
	skip   string
}

func parseConformanceMeta(src string) conformanceMeta {
	var m conformanceMeta
	scanner := bufio.NewScanner(strings.NewReader(src))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "//") {
			break
		}
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "@expect:"); ok {
			m.expect = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(line, "@error:"); ok {
			m.errStr = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(line, "@skip:"); ok {
			m.skip = strings.TrimSpace(rest)
		}
	}
	return m
}

func runConformanceCase(t *testing.T, name, src string, meta conformanceMeta) {
	t.Helper()
	sess := buzz.NewSession(context.Background())
	defer func() { _ = sess.Close() }()
	buzzstd.Register(sess)
	err := sess.Exec(context.Background(), src)

	if meta.errStr != "" {
		if err == nil {
			t.Fatalf("%s: expected error containing %q, got none", name, meta.errStr)
		}
		if !strings.Contains(err.Error(), meta.errStr) {
			t.Fatalf("%s: error %q does not contain %q", name, err.Error(), meta.errStr)
		}
		return
	}

	if err != nil {
		t.Fatalf("%s: unexpected error: %v", name, err)
	}

	if meta.expect != "" {
		got := sess.GetGlobal("__r")
		if got.String() != meta.expect {
			t.Errorf("%s: __r = %q, want %q", name, got.String(), meta.expect)
		}
	}
}

// cLibSource is a tiny C library exercising every FFI capability: a scalar call,
// a float call, a pointer out-parameter, a by-reference struct, and a callback.
const cLibSource = `
#include <stdint.h>

int add(int a, int b) { return a + b; }
double scale(double x) { return x * 2.5; }

/* out-parameter: caller passes &out, we write through it */
void fill(int *out) { *out = 99; }

/* by-reference struct: { int32 id; double score } -> layout [0, 8], size 16 */
typedef struct { int32_t id; double score; } Rec;
void rec_init(Rec *r, int32_t id, double score) { r->id = id; r->score = score; }
int32_t rec_id(Rec *r) { return r->id; }

/* callback: apply a function pointer to x */
int apply(int (*f)(int), int x) { return f(x); }
`

// buildCLib compiles cLibSource into a shared object in a temp dir and returns its
// path, skipping the test if FFI is unsupported here or no C compiler is present.
func buildCLib(t *testing.T) string {
	t.Helper()
	if buzz.GetFFIProvider() == nil {
		t.Skipf("FFI unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	cc := ""
	for _, c := range []string{"cc", "clang", "gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler (cc/clang/gcc) on PATH")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "lib.c")
	if err := os.WriteFile(src, []byte(cLibSource), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := "so"
	if runtime.GOOS == "darwin" {
		ext = "dylib"
	}
	out := filepath.Join(dir, "libffitest."+ext)
	cmd := exec.Command(cc, "-shared", "-fPIC", "-o", out, src)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compiling C lib: %v\n%s", err, msg)
	}
	return out
}

// runBuzz executes src in a fresh std-enabled session and returns __r as a string.
func runBuzz(t *testing.T, src string) string {
	t.Helper()
	sess := buzz.NewSession(context.Background())
	defer func() { _ = sess.Close() }()
	buzzstd.Register(sess)
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatalf("Exec: %v\nsrc:\n%s", err, src)
	}
	r, ok := sess.Globals()["__r"]
	if !ok {
		t.Fatalf("__r not set by script:\n%s", src)
	}
	return r.String()
}

func TestFFIScalarCall(t *testing.T) {
	lib := buildCLib(t)
	got := runBuzz(t, `
final lib = zdef("`+lib+`", "int add(int a, int b);");
final __r = lib.add(40, 2);
`)
	if got != "42" {
		t.Errorf("add(40,2) = %q, want 42", got)
	}
}

func TestFFIFloatCall(t *testing.T) {
	lib := buildCLib(t)
	got := runBuzz(t, `
final lib = zdef("`+lib+`", "double scale(double x);");
final __r = lib.scale(4.0);
`)
	if got != "10" && got != "10.0" {
		t.Errorf("scale(4.0) = %q, want 10", got)
	}
}

func TestFFIPointerOutParam(t *testing.T) {
	lib := buildCLib(t)
	got := runBuzz(t, `
import "ffi";
final lib = zdef("`+lib+`", "void fill(int *out);");
final p = ffi.alloc(ffi.sizeOf("int"));
lib.fill(p);
final __r = ffi.read(p, 0, "int");
ffi.free(p);
`)
	if got != "99" {
		t.Errorf("fill out-param = %q, want 99", got)
	}
}

func TestFFIStructByReference(t *testing.T) {
	lib := buildCLib(t)
	// Build a Rec on the Buzz side, let C initialize it, read both fields back.
	got := runBuzz(t, `
import "ffi";
final lib = zdef("`+lib+`", "void rec_init(void *r, int id, double score); int rec_id(void *r);");
final lay = ffi.structLayout(["int", "double"]);
final r = ffi.alloc(lay["size"]);
lib.rec_init(r, 7, 9.5);
final id = ffi.read(r, lay["offsets"][0], "int");
final score = ffi.read(r, lay["offsets"][1], "double");
final viaC = lib.rec_id(r);
ffi.free(r);
final __r = "{id}/{score}/{viaC}";
`)
	if got != "7/9.5/7" {
		t.Errorf("struct-by-ref = %q, want 7/9.5/7", got)
	}
}

func TestFFICallback(t *testing.T) {
	lib := buildCLib(t)
	got := runBuzz(t, `
import "ffi";
final lib = zdef("`+lib+`", "int apply(void *f, int x);");
fun triple(n: int) > int { return n * 3; }
final cb = ffi.callback(triple, "int", ["int"]);
final __r = lib.apply(cb, 14);
`)
	if got != "42" {
		t.Errorf("apply(triple, 14) = %q, want 42", got)
	}
}

// TestFFILibmStillWorks guards the pre-existing libm path against regressions
// from the parser/type changes.
func TestFFILibmStillWorks(t *testing.T) {
	if buzz.GetFFIProvider() == nil {
		t.Skip("FFI unsupported here")
	}
	if runtime.GOOS != "linux" {
		t.Skip("libm name resolution validated on linux")
	}
	got := runBuzz(t, `
import "std";
final lib = zdef("libm", "double sqrt(double x);");
final __r = std.toInt(lib.sqrt(9.0));
`)
	if !strings.HasPrefix(got, "3") {
		t.Errorf("sqrt(9.0) = %q, want 3", got)
	}
}

// importModuleSrc is a flat-importable module: an exported function that reads a
// non-exported (captured) module var, plus a non-exported helper function. Under
// exports-only import visibility (M4) only `pub` crosses the import boundary; the
// module's own code still reads `secret` live at runtime.
const importModuleSrc = `
var secret = 42;
export fun pub() int { return secret; }
fun privHelper() int { return 7; }
`

func newImporter(t *testing.T) *buzz.Session {
	t.Helper()
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	s.SetPromoteTopLevel(true) // magusfile execution mode
	s.SetSourceModule("mymod", importModuleSrc)
	return s
}

// TestImportVisibility_ExportedCrosses verifies an exported function is callable
// through a flat import and still reads its module's non-exported state live —
// the runtime Env is untouched; only the importer's checker view is narrowed.
func TestImportVisibility_ExportedCrosses(t *testing.T) {
	s := newImporter(t)
	v, err := s.Eval(context.Background(), `import "mymod"; return pub();`)
	if err != nil {
		t.Fatalf("calling exported pub() across import failed: %v", err)
	}
	if !v.IsInt() || v.AsInt() != 42 {
		t.Errorf("pub() = %v, want 42 (exported fn must read its module's live secret)", v)
	}
}

// TestImportVisibility_NonExportedVarHidden verifies a module's non-exported var
// is invisible to the importer, and that the error names `export` as the fix.
func TestImportVisibility_NonExportedVarHidden(t *testing.T) {
	s := newImporter(t)
	_, err := s.Eval(context.Background(), `import "mymod"; return secret;`)
	if err == nil {
		t.Fatal("referencing a module's non-exported var should fail under exports-only imports")
	}
	if !strings.Contains(err.Error(), "export") {
		t.Errorf("error should point at the missing export, got: %v", err)
	}
}

// TestImportVisibility_NonExportedFuncHidden verifies a module's non-exported
// function is likewise not callable through the import.
func TestImportVisibility_NonExportedFuncHidden(t *testing.T) {
	s := newImporter(t)
	_, err := s.Eval(context.Background(), `import "mymod"; return privHelper();`)
	if err == nil {
		t.Fatal("calling a module's non-exported function should fail under exports-only imports")
	}
	if !strings.Contains(err.Error(), "export") {
		t.Errorf("error should point at the missing export, got: %v", err)
	}
}

// TestImportVisibility_ImporterMayShadow verifies the boundary hides only the
// imported binding: the importer can still declare its own same-named top-level
// var without colliding with the module's hidden one.
func TestImportVisibility_ImporterMayShadow(t *testing.T) {
	s := newImporter(t)
	v, err := s.Eval(context.Background(), `import "mymod"; var secret = 99; return secret;`)
	if err != nil {
		t.Fatalf("importer declaring its own 'secret' should be fine: %v", err)
	}
	if !v.IsInt() || v.AsInt() != 99 {
		t.Errorf("importer's own secret = %v, want 99", v)
	}
}

// magusfileSrc mirrors the shape of a real magusfile: a top-level config var read
// by an exported target (so it is captured and must stay an Env binding), plus a
// chunk-private scratch loop (promotable to a slot under PromoteTopLevel).
const magusfileSrc = `
var config = 42;
export fun getConfig() int { return config; }
var scratch = 0;
var i = 0;
while (i < 100) { scratch = scratch + i; i = i + 1; }
`

// TestPromoteSession_MagusfileShape exercises the M2 wiring: a session with
// SetPromoteTopLevel(true) (the magusfile execution path) must run a magusfile
// unchanged — the captured config stays live for its target, and the promoted
// scratch var simply drops out of the global namespace.
func TestPromoteSession_MagusfileShape(t *testing.T) {
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	s.SetPromoteTopLevel(true)
	if err := s.Exec(ctx, magusfileSrc); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	// The exported target still resolves the captured top-level config (live Env).
	exports := s.Exports()
	getConfig, ok := exports["getConfig"]
	if !ok {
		t.Fatal("exported target getConfig missing")
	}
	v, err := s.CallValue(ctx, getConfig, nil)
	if err != nil {
		t.Fatalf("CallValue(getConfig): %v", err)
	}
	if !v.IsInt() || v.AsInt() != 42 {
		t.Errorf("getConfig() = %v, want 42", v)
	}

	// config is captured by getConfig, so it stays an Env binding (visible).
	if _, ok := s.Globals()["config"]; !ok {
		t.Error("captured top-level 'config' should remain a visible Env global")
	}
	// scratch is chunk-private and promoted to a slot, so it is no longer a global.
	if _, ok := s.Globals()["scratch"]; ok {
		t.Error("chunk-private 'scratch' should be slot-promoted out of the global namespace")
	}
}

// TestPromoteSession_DefaultOffKeepsGlobals confirms the REPL/default path is
// unchanged: without SetPromoteTopLevel every top-level var stays an Env global,
// so a later Exec (a subsequent prompt line) can still see it.
func TestPromoteSession_DefaultOffKeepsGlobals(t *testing.T) {
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	if err := s.Exec(ctx, magusfileSrc); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if _, ok := s.Globals()["scratch"]; !ok {
		t.Error("without promotion, 'scratch' must remain a visible Env global for later chunks")
	}
	// A later chunk referencing the earlier scratch var compiles and runs.
	if err := s.Exec(ctx, `scratch = scratch + 1;`); err != nil {
		t.Errorf("later chunk referencing earlier top-level var failed: %v", err)
	}
}
