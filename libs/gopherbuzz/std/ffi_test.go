package std

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findCC returns the path to a C compiler, skipping the test if none is found.
func findCC(t *testing.T) string {
	t.Helper()
	for _, c := range []string{"cc", "clang", "gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	t.Skip("no C compiler on PATH")
	return ""
}

// compileLib compiles csrc into a shared library named lib<base> in a fresh temp
// dir and returns its path.
func compileLib(t *testing.T, cc, base, csrc string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, base+".c")
	require.NoError(t, os.WriteFile(src, []byte(csrc), 0o644))
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	lib := filepath.Join(dir, "lib"+base+ext)
	out, err := exec.Command(cc, "-shared", "-fPIC", "-o", lib, src).CombinedOutput()
	require.NoErrorf(t, err, "cc: %s", out)
	return lib
}

// TestZdefExternVar compiles a tiny C library with data symbols and verifies
// every extern binding mode end-to-end: scalar load, double load, char* load,
// pointer load, and address-of for an opaque (array/struct-like) symbol. The
// pointer symbol points at the opaque one, so the script can prove the two
// modes agree without the test hard-coding any address.
func TestZdefExternVar(t *testing.T) {
	if buzz.GetFFIProvider() == nil {
		t.Skip("no FFI provider on this platform")
	}
	cc := findCC(t)
	lib := compileLib(t, cc, "exvar", `
int answer = 42;
double ratio = 0.5;
const char *greeting = "hello";
int storage[4] = {1, 2, 3, 4};
int *firstcell = storage;
`)

	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	Register(sess)

	script := `
final lib = zdef("` + lib + `",
    "extern int answer;"
    + "extern double ratio;"
    + "extern const char *greeting;"
    + "extern struct Opaque storage;"
    + "extern int *firstcell;");
export final answer = lib.answer;
export final ratio = lib.ratio;
export final greeting = lib.greeting;
export final agree = lib.firstcell == lib.storage;
`
	require.NoError(t, sess.Exec(ctx, script), "exec")
	exp := sess.Exports()
	answer := exp["answer"]
	assert.True(t, answer.IsInt(), "answer IsInt")
	assert.Equal(t, int64(42), answer.AsInt(), "answer")
	ratio := exp["ratio"]
	assert.True(t, ratio.IsFloat(), "ratio IsFloat")
	assert.Equal(t, 0.5, ratio.AsFloat(), "ratio")
	greeting := exp["greeting"]
	assert.True(t, greeting.IsStr(), "greeting IsStr")
	assert.Equal(t, "hello", greeting.AsString(), "greeting")
	agree := exp["agree"]
	assert.True(t, agree.IsBool(), "agree IsBool")
	assert.True(t, agree.AsBool(), "firstcell (pointer load) != storage (address-of)")
}

// TestZdefPoint2DReturn verifies the by-value two-double struct return
// (CGPoint/NSPoint shape) end-to-end against a compiled fixture. purego
// builds the register-pair return path on amd64/arm64 (darwin and linux),
// which covers this checkout's CI as well as the macOS frameworks that
// motivated it (CGEventGetLocation).
func TestZdefPoint2DReturn(t *testing.T) {
	if buzz.GetFFIProvider() == nil {
		t.Skip("no FFI provider on this platform")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("struct returns need amd64/arm64")
	}
	cc := findCC(t)
	lib := compileLib(t, cc, "pt", `
typedef struct { double x; double y; } pt;
pt locate(double x, double y) { pt p = { x * 2.0, y + 1.0 }; return p; }
`)

	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	Register(sess)

	script := `
final lib = zdef("` + lib + `", "CGPoint locate(double x, double y);");
final p = lib.locate(3.0, 4.0);
export final x = p["x"];
export final y = p["y"];
`
	require.NoError(t, sess.Exec(ctx, script), "exec")
	exp := sess.Exports()
	x := exp["x"]
	assert.True(t, x.IsFloat(), "x IsFloat")
	assert.Equal(t, 6.0, x.AsFloat(), "x")
	y := exp["y"]
	assert.True(t, y.IsFloat(), "y IsFloat")
	assert.Equal(t, 5.0, y.AsFloat(), "y")
}

// TestZdefRect4DReturn covers the 32-byte by-value struct return (CGRect
// shape): hidden sret pointer on amd64, four-register HFA on arm64.
func TestZdefRect4DReturn(t *testing.T) {
	if buzz.GetFFIProvider() == nil {
		t.Skip("no FFI provider on this platform")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("struct returns need amd64/arm64")
	}
	cc := findCC(t)
	lib := compileLib(t, cc, "rect", `
typedef struct { double x; double y; double w; double h; } rect;
rect bounds(double offset) { rect r = { offset, offset + 1.0, 1920.0, 1080.0 }; return r; }
`)

	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	Register(sess)

	script := `
final lib = zdef("` + lib + `", "CGRect bounds(double offset);");
final r = lib.bounds(100.0);
export final ok = r["x"] == 100.0 and r["y"] == 101.0 and r["w"] == 1920.0 and r["h"] == 1080.0;
`
	require.NoError(t, sess.Exec(ctx, script), "exec")
	ok := sess.Exports()["ok"]
	assert.True(t, ok.IsBool(), "ok IsBool")
	assert.True(t, ok.AsBool(), "rect fields wrong")
}

// TestZdefZigDialect runs the upstream-style Zig declarations end-to-end:
// same fixture semantics as the C-dialect tests, declared as Zig.
func TestZdefZigDialect(t *testing.T) {
	if buzz.GetFFIProvider() == nil {
		t.Skip("no FFI provider on this platform")
	}
	cc := findCC(t)
	lib := compileLib(t, cc, "zigd", `
int answer = 41;
int add(int a, int b) { return a + b; }
double halve(double x) { return x / 2.0; }
const char *greet(void) { return "hey"; }
`)

	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	Register(sess)

	script := `
final lib = zdef("` + lib + `",
    "fn add(a: c_int, b: c_int) c_int;"
    + "fn halve(x: f64) f64;"
    + "fn greet() [*:0]const u8;"
    + "var answer: c_int;");
export final sum = lib.add(20, 22);
export final half = lib.halve(9.0);
export final hello = lib.greet();
export final answer = lib.answer;
`
	require.NoError(t, sess.Exec(ctx, script), "exec")
	exp := sess.Exports()
	sum := exp["sum"]
	assert.True(t, sum.IsInt(), "sum IsInt")
	assert.Equal(t, int64(42), sum.AsInt(), "sum")
	half := exp["half"]
	assert.True(t, half.IsFloat(), "half IsFloat")
	assert.Equal(t, 4.5, half.AsFloat(), "half")
	hello := exp["hello"]
	assert.True(t, hello.IsStr(), "hello IsStr")
	assert.Equal(t, "hey", hello.AsString(), "hello")
	answer := exp["answer"]
	assert.True(t, answer.IsInt(), "answer IsInt")
	assert.Equal(t, int64(41), answer.AsInt(), "answer")
}

// TestZdefZigStructs exercises the Zig dialect's extern-struct declarations:
// the layout binds as {size, align, offsets}, struct pointers pass by
// reference, and a two-f64 struct returns by value through the CGPoint path.
func TestZdefZigStructs(t *testing.T) {
	if buzz.GetFFIProvider() == nil {
		t.Skip("no FFI provider on this platform")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("struct returns need amd64/arm64")
	}
	cc := findCC(t)
	lib := compileLib(t, cc, "zs", `
typedef struct { int id; double score; } rec;
typedef struct { double x; double y; } pt;
void rec_init(rec *r, int id, double score) { r->id = id; r->score = score; }
double rec_score(const rec *r) { return r->score; }
pt rec_at(double x) { pt p = { x, x * 2.0 }; return p; }
`)

	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	Register(sess)

	script := `
import "ffi";
final lib = zdef("` + lib + `", ` + "`" + `
    const Rec = extern struct { id: c_int, score: f64 };
    const Pt = extern struct { x: f64, y: f64 };
    fn rec_init(r: *Rec, id: c_int, score: f64) void;
    fn rec_score(r: *Rec) f64;
    fn rec_at(x: f64) Pt;
` + "`" + `);
final lay = lib.Rec;
final r = ffi.alloc(lay["size"]);
lib.rec_init(r, 7, 9.5);
export final id = ffi.read(r, lay["offsets"][0], "c_int");
export final score = lib.rec_score(r);
final p = lib.rec_at(3.0);
export final py = p["y"];
`
	require.NoError(t, sess.Exec(ctx, script), "exec")
	exp := sess.Exports()
	id := exp["id"]
	assert.True(t, id.IsInt(), "id IsInt")
	assert.Equal(t, int64(7), id.AsInt(), "id")
	score := exp["score"]
	assert.True(t, score.IsFloat(), "score IsFloat")
	assert.Equal(t, 9.5, score.AsFloat(), "score")
	py := exp["py"]
	assert.True(t, py.IsFloat(), "py IsFloat")
	assert.Equal(t, 6.0, py.AsFloat(), "py")
}
