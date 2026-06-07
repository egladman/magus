package buzz_test

import (
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
import "std";
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
import "std";
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
import "std";
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
import "std";
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
import "std";
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
