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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSession(t *testing.T) {
	s := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	require.NotNil(t, s, "NewSession returned nil")
	assert.NotNil(t, s.Targets(), "Targets() should return a non-nil map")
}

func TestSession_ExecSimpleAssignment(t *testing.T) {
	s := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	require.NoError(t, s.Exec(context.Background(), `var x: int = 42;`), "Exec")
	globals := s.Globals()
	v, ok := globals["x"]
	require.True(t, ok, "global 'x' not found after exec")
	require.True(t, v.IsInt(), "x.IsInt() = false, got Kind() = %q", v.Kind())
	assert.Equal(t, int64(42), v.AsInt(), "x.AsInt()")
}

func TestSession_EvalExpression(t *testing.T) {
	s := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	// Use a function that returns a value to test Eval's return path.
	require.NoError(t, s.Exec(context.Background(), `fun sum() > int { return 1 + 2; }`), "Exec")
	v, err := s.Eval(context.Background(), `return sum()`)
	require.NoError(t, err, "Eval(return sum())")
	require.True(t, v.IsInt(), "Eval(return sum()) = %v, want 3", v)
	assert.Equal(t, int64(3), v.AsInt(), "Eval(return sum()) = %v, want 3", v)
}

func TestSession_SyntheticModule(t *testing.T) {
	s := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	mod := buzz.NewMap()
	mod.MapSet("answer", buzz.IntValue(42))
	// Host registers the module under an import path; it resolves with no file
	// on disk and no include dirs configured.
	s.SetSyntheticModule("example/demo", mod)

	require.NoError(t, s.Exec(context.Background(), `
import "example/demo";
var x = demo.answer;
`), "Exec")
	v, ok := s.Globals()["x"]
	require.True(t, ok, "global 'x' not bound; synthetic import did not resolve")
	require.True(t, v.IsInt(), "x = %v, want 42", v)
	assert.Equal(t, int64(42), v.AsInt(), "x = %v, want 42", v)
}

func TestSession_ModuleResolver(t *testing.T) {
	s := buzz.NewSession(context.Background(), buzz.WithEmbedded())
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

	require.NoError(t, s.Exec(context.Background(), `
import "spells/widget";
var x = widget.answer;
`), "Exec")
	assert.Equal(t, "spells/widget", gotPath, "resolver called with %q", gotPath)
	v, ok := s.Globals()["x"]
	require.True(t, ok, "global 'x' not bound; resolver import did not resolve")
	require.True(t, v.IsInt(), "x = %v, want 7", v)
	assert.Equal(t, int64(7), v.AsInt(), "x = %v, want 7", v)
}

func TestSession_Compile_And_ExecChunk(t *testing.T) {
	s := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	chunk, err := s.Compile(`var y: str = "hello";`)
	require.NoError(t, err, "Compile")
	require.NoError(t, s.ExecChunk(context.Background(), chunk), "ExecChunk")
	v, ok := s.Globals()["y"]
	require.True(t, ok, "global 'y' not set after ExecChunk")
	assert.Equal(t, "hello", v.AsString(), "y")
}

// TestConformance runs all .buzz files in testdata/.
// Each file may have header comments:
//
//	// @expect: <value>  — run and assert __r.String() == <value>
//	// @error: <substr>  — assert parse/type/compile/runtime error contains <substr>
//	// @skip: <reason>   — skip this test case
func TestConformance(t *testing.T) {
	files, err := filepath.Glob("testdata/*.buzz")
	require.NoError(t, err)
	require.NotEmpty(t, files, "no conformance test files found in testdata/")
	for _, path := range files {
		path := path
		name := strings.TrimSuffix(filepath.Base(path), ".buzz")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			require.NoErrorf(t, err, "read %s", path)
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
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	buzzstd.Register(sess)
	err := sess.Exec(context.Background(), src)

	if meta.errStr != "" {
		require.Errorf(t, err, "%s: expected error containing %q, got none", name, meta.errStr)
		require.Containsf(t, err.Error(), meta.errStr, "%s: error %q does not contain %q", name, err.Error(), meta.errStr)
		return
	}

	require.NoErrorf(t, err, "%s: unexpected error", name)

	if meta.expect != "" {
		got := sess.GetGlobal("__r")
		assert.Equalf(t, meta.expect, got.String(), "%s: __r", name)
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
	require.NoError(t, os.WriteFile(src, []byte(cLibSource), 0o644))
	ext := "so"
	if runtime.GOOS == "darwin" {
		ext = "dylib"
	}
	out := filepath.Join(dir, "libffitest."+ext)
	cmd := exec.Command(cc, "-shared", "-fPIC", "-o", out, src)
	msg, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "compiling C lib:\n%s", msg)
	return out
}

// runBuzz executes src in a fresh std-enabled session and returns __r as a string.
func runBuzz(t *testing.T, src string) string {
	t.Helper()
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	buzzstd.Register(sess)
	require.NoErrorf(t, sess.Exec(context.Background(), src), "Exec\nsrc:\n%s", src)
	r, ok := sess.Globals()["__r"]
	require.Truef(t, ok, "__r not set by script:\n%s", src)
	return r.String()
}

func TestFFIScalarCall(t *testing.T) {
	lib := buildCLib(t)
	got := runBuzz(t, `
final lib = zdef("`+lib+`", "int add(int a, int b);");
final __r = lib.add(40, 2);
`)
	assert.Equal(t, "42", got, "add(40,2)")
}

func TestFFIFloatCall(t *testing.T) {
	lib := buildCLib(t)
	got := runBuzz(t, `
final lib = zdef("`+lib+`", "double scale(double x);");
final __r = lib.scale(4.0);
`)
	assert.Contains(t, []string{"10", "10.0"}, got, "scale(4.0) = %q, want 10", got)
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
	assert.Equal(t, "99", got, "fill out-param")
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
	assert.Equal(t, "7/9.5/7", got, "struct-by-ref")
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
	assert.Equal(t, "42", got, "apply(triple, 14)")
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
	assert.Truef(t, strings.HasPrefix(got, "3"), "sqrt(9.0) = %q, want 3", got)
}

// importModuleSrc is a flat-importable module: an exported function that reads a
// non-exported (captured) module var, plus a non-exported helper function. Under
// exports-only import visibility (M4) only `pub` crosses the import boundary; the
// module's own code still reads `secret` live at runtime.
const importModuleSrc = `
var secret = 42;
export fun pub() > int { return secret; }
fun privHelper() > int { return 7; }
`

func newImporter(t *testing.T) *buzz.Session {
	t.Helper()
	ctx := context.Background()
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
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
	require.NoError(t, err, "calling exported pub() across import failed")
	require.True(t, v.IsInt(), "pub() = %v, want 42 (exported fn must read its module's live secret)", v)
	assert.Equal(t, int64(42), v.AsInt(), "pub() = %v, want 42 (exported fn must read its module's live secret)", v)
}

// TestImportVisibility_NonExportedVarHidden verifies a module's non-exported var
// is invisible to the importer, and that the error names `export` as the fix.
func TestImportVisibility_NonExportedVarHidden(t *testing.T) {
	s := newImporter(t)
	_, err := s.Eval(context.Background(), `import "mymod"; return secret;`)
	require.Error(t, err, "referencing a module's non-exported var should fail under exports-only imports")
	assert.Contains(t, err.Error(), "export", "error should point at the missing export")
}

// TestImportVisibility_NonExportedFuncHidden verifies a module's non-exported
// function is likewise not callable through the import.
func TestImportVisibility_NonExportedFuncHidden(t *testing.T) {
	s := newImporter(t)
	_, err := s.Eval(context.Background(), `import "mymod"; return privHelper();`)
	require.Error(t, err, "calling a module's non-exported function should fail under exports-only imports")
	assert.Contains(t, err.Error(), "export", "error should point at the missing export")
}

// TestImportVisibility_ImporterMayShadow verifies the boundary hides only the
// imported binding: the importer can still declare its own same-named top-level
// var without colliding with the module's hidden one.
func TestImportVisibility_ImporterMayShadow(t *testing.T) {
	s := newImporter(t)
	v, err := s.Eval(context.Background(), `import "mymod"; var secret = 99; return secret;`)
	require.NoError(t, err, "importer declaring its own 'secret' should be fine")
	require.True(t, v.IsInt(), "importer's own secret = %v, want 99", v)
	assert.Equal(t, int64(99), v.AsInt(), "importer's own secret = %v, want 99", v)
}

// magusfileSrc mirrors the shape of a real magusfile: a top-level config var read
// by an exported target (so it is captured and must stay an Env binding), plus a
// chunk-private scratch loop (promotable to a slot under PromoteTopLevel).
const magusfileSrc = `
var config = 42;
export fun getConfig() > int { return config; }
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
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
	s.SetPromoteTopLevel(true)
	require.NoError(t, s.Exec(ctx, magusfileSrc), "Exec")

	// The exported target still resolves the captured top-level config (live Env).
	exports := s.Exports()
	getConfig, ok := exports["getConfig"]
	require.True(t, ok, "exported target getConfig missing")
	v, err := s.CallValue(ctx, getConfig, nil)
	require.NoError(t, err, "CallValue(getConfig)")
	require.True(t, v.IsInt(), "getConfig() = %v, want 42", v)
	assert.Equal(t, int64(42), v.AsInt(), "getConfig() = %v, want 42", v)

	// config is captured by getConfig, so it stays an Env binding (visible).
	_, ok = s.Globals()["config"]
	assert.True(t, ok, "captured top-level 'config' should remain a visible Env global")
	// scratch is chunk-private and promoted to a slot, so it is no longer a global.
	_, ok = s.Globals()["scratch"]
	assert.False(t, ok, "chunk-private 'scratch' should be slot-promoted out of the global namespace")
}

// TestPromoteSession_DefaultOffKeepsGlobals confirms the REPL/default path is
// unchanged: without SetPromoteTopLevel every top-level var stays an Env global,
// so a later Exec (a subsequent prompt line) can still see it.
func TestPromoteSession_DefaultOffKeepsGlobals(t *testing.T) {
	ctx := context.Background()
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
	require.NoError(t, s.Exec(ctx, magusfileSrc), "Exec")
	_, ok := s.Globals()["scratch"]
	assert.True(t, ok, "without promotion, 'scratch' must remain a visible Env global for later chunks")
	// A later chunk referencing the earlier scratch var compiles and runs.
	assert.NoError(t, s.Exec(ctx, `scratch = scratch + 1;`), "later chunk referencing earlier top-level var failed")
}
