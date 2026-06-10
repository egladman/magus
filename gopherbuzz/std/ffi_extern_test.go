package std

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
)

// TestZdefExternVar compiles a tiny C library with data symbols and verifies
// every extern binding mode end-to-end: scalar load, double load, char* load,
// pointer load, and address-of for an opaque (array/struct-like) symbol. The
// pointer symbol points at the opaque one, so the script can prove the two
// modes agree without the test hard-coding any address.
func TestZdefExternVar(t *testing.T) {
	if buzz.GetFFIProvider() == nil {
		t.Skip("no FFI provider on this platform")
	}
	cc := ""
	for _, c := range []string{"cc", "clang", "gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "exvar.c")
	csrc := `
int answer = 42;
double ratio = 0.5;
const char *greeting = "hello";
int storage[4] = {1, 2, 3, 4};
int *firstcell = storage;
`
	if err := os.WriteFile(src, []byte(csrc), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	lib := filepath.Join(dir, "libexvar"+ext)
	if out, err := exec.Command(cc, "-shared", "-fPIC", "-o", lib, src).CombinedOutput(); err != nil {
		t.Fatalf("cc: %v\n%s", err, out)
	}

	ctx := context.Background()
	sess := buzz.NewSession(ctx)
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
	if err := sess.Exec(ctx, script); err != nil {
		t.Fatalf("exec: %v", err)
	}
	exp := sess.Exports()
	if v := exp["answer"]; !v.IsInt() || v.AsInt() != 42 {
		t.Errorf("answer = %s, want 42", v.String())
	}
	if v := exp["ratio"]; !v.IsFloat() || v.AsFloat() != 0.5 {
		t.Errorf("ratio = %s, want 0.5", v.String())
	}
	if v := exp["greeting"]; !v.IsStr() || v.AsString() != "hello" {
		t.Errorf("greeting = %s, want hello", v.String())
	}
	if v := exp["agree"]; !v.IsBool() || !v.AsBool() {
		t.Errorf("firstcell (pointer load) != storage (address-of): %s", v.String())
	}
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
	cc := ""
	for _, c := range []string{"cc", "clang", "gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "pt.c")
	csrc := `
typedef struct { double x; double y; } pt;
pt locate(double x, double y) { pt p = { x * 2.0, y + 1.0 }; return p; }
`
	if err := os.WriteFile(src, []byte(csrc), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	lib := filepath.Join(dir, "libpt"+ext)
	if out, err := exec.Command(cc, "-shared", "-fPIC", "-o", lib, src).CombinedOutput(); err != nil {
		t.Fatalf("cc: %v\n%s", err, out)
	}

	ctx := context.Background()
	sess := buzz.NewSession(ctx)
	defer sess.Close()
	Register(sess)

	script := `
final lib = zdef("` + lib + `", "CGPoint locate(double x, double y);");
final p = lib.locate(3.0, 4.0);
export final x = p["x"];
export final y = p["y"];
`
	if err := sess.Exec(ctx, script); err != nil {
		t.Fatalf("exec: %v", err)
	}
	exp := sess.Exports()
	if v := exp["x"]; !v.IsFloat() || v.AsFloat() != 6.0 {
		t.Errorf("x = %s, want 6", v.String())
	}
	if v := exp["y"]; !v.IsFloat() || v.AsFloat() != 5.0 {
		t.Errorf("y = %s, want 5", v.String())
	}
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
	cc := ""
	for _, c := range []string{"cc", "clang", "gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "rect.c")
	csrc := `
typedef struct { double x; double y; double w; double h; } rect;
rect bounds(double offset) { rect r = { offset, offset + 1.0, 1920.0, 1080.0 }; return r; }
`
	if err := os.WriteFile(src, []byte(csrc), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	lib := filepath.Join(dir, "librect"+ext)
	if out, err := exec.Command(cc, "-shared", "-fPIC", "-o", lib, src).CombinedOutput(); err != nil {
		t.Fatalf("cc: %v\n%s", err, out)
	}

	ctx := context.Background()
	sess := buzz.NewSession(ctx)
	defer sess.Close()
	Register(sess)

	script := `
final lib = zdef("` + lib + `", "CGRect bounds(double offset);");
final r = lib.bounds(100.0);
export final ok = r["x"] == 100.0 and r["y"] == 101.0 and r["w"] == 1920.0 and r["h"] == 1080.0;
`
	if err := sess.Exec(ctx, script); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if v := sess.Exports()["ok"]; !v.IsBool() || !v.AsBool() {
		t.Errorf("rect fields wrong")
	}
}

// TestZdefZigDialect runs the upstream-style Zig declarations end-to-end:
// same fixture semantics as the C-dialect tests, declared as Zig.
func TestZdefZigDialect(t *testing.T) {
	if buzz.GetFFIProvider() == nil {
		t.Skip("no FFI provider on this platform")
	}
	cc := ""
	for _, c := range []string{"cc", "clang", "gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "zigd.c")
	csrc := `
int answer = 41;
int add(int a, int b) { return a + b; }
double halve(double x) { return x / 2.0; }
const char *greet(void) { return "hey"; }
`
	if err := os.WriteFile(src, []byte(csrc), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	lib := filepath.Join(dir, "libzigd"+ext)
	if out, err := exec.Command(cc, "-shared", "-fPIC", "-o", lib, src).CombinedOutput(); err != nil {
		t.Fatalf("cc: %v\n%s", err, out)
	}

	ctx := context.Background()
	sess := buzz.NewSession(ctx)
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
	if err := sess.Exec(ctx, script); err != nil {
		t.Fatalf("exec: %v", err)
	}
	exp := sess.Exports()
	if v := exp["sum"]; !v.IsInt() || v.AsInt() != 42 {
		t.Errorf("sum = %s, want 42", v.String())
	}
	if v := exp["half"]; !v.IsFloat() || v.AsFloat() != 4.5 {
		t.Errorf("half = %s, want 4.5", v.String())
	}
	if v := exp["hello"]; !v.IsStr() || v.AsString() != "hey" {
		t.Errorf("hello = %s, want hey", v.String())
	}
	if v := exp["answer"]; !v.IsInt() || v.AsInt() != 41 {
		t.Errorf("answer = %s, want 41", v.String())
	}
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
	cc := ""
	for _, c := range []string{"cc", "clang", "gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "zs.c")
	csrc := `
typedef struct { int id; double score; } rec;
typedef struct { double x; double y; } pt;
void rec_init(rec *r, int id, double score) { r->id = id; r->score = score; }
double rec_score(const rec *r) { return r->score; }
pt rec_at(double x) { pt p = { x, x * 2.0 }; return p; }
`
	if err := os.WriteFile(src, []byte(csrc), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	lib := filepath.Join(dir, "libzs"+ext)
	if out, err := exec.Command(cc, "-shared", "-fPIC", "-o", lib, src).CombinedOutput(); err != nil {
		t.Fatalf("cc: %v\n%s", err, out)
	}

	ctx := context.Background()
	sess := buzz.NewSession(ctx)
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
	if err := sess.Exec(ctx, script); err != nil {
		t.Fatalf("exec: %v", err)
	}
	exp := sess.Exports()
	if v := exp["id"]; !v.IsInt() || v.AsInt() != 7 {
		t.Errorf("id = %s, want 7", v.String())
	}
	if v := exp["score"]; !v.IsFloat() || v.AsFloat() != 9.5 {
		t.Errorf("score = %s, want 9.5", v.String())
	}
	if v := exp["py"]; !v.IsFloat() || v.AsFloat() != 6.0 {
		t.Errorf("py = %s, want 6", v.String())
	}
}
