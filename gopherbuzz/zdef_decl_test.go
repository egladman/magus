package buzz_test

import (
	"testing"

	"github.com/egladman/gopherbuzz"
)

// TestZdefDeclaresFreeFunctions verifies the upstream-Buzz zdef semantics: a
// top-level `zdef("lib", "<decls>")` statement declares the C symbols it names
// as module globals, so they type-check and compile when called by bare name
// (rather than through gopherbuzz's zdef-handle). The names are also exported.
func TestZdefDeclaresFreeFunctions(t *testing.T) {
	src := `
import "ffi";
zdef("libm", "double sqrt(double x); double pow(double base, double exp);");
final a = sqrt(4.0);
final b = pow(2.0, exp: 3.0);
`
	prog, err := buzz.Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Compiles without "undefined: sqrt/pow" — the checker pre-declared them and
	// the compiler lowered the zdef into global bindings. A labeled FFI arg
	// (exp: 3.0) must also be accepted (labels are ignored, written order kept).
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	if err != nil {
		t.Fatalf("CompileWith: %v", err)
	}
	want := map[string]bool{"sqrt": false, "pow": false}
	for _, e := range chunk.Exports {
		if _, ok := want[e]; ok {
			want[e] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("zdef symbol %q was not exported (Exports = %v)", name, chunk.Exports)
		}
	}
}

// TestZdefInsideFunctionStaysHandle confirms the lowering is top-level only: a
// zdef call inside a function body remains an ordinary expression returning the
// handle (gopherbuzz's lib.Func() form), so existing handle-style code is
// unaffected.
func TestZdefInsideFunctionStaysHandle(t *testing.T) {
	src := `
import "ffi";
fun openLib() > any {
    return zdef("libm", "double sqrt(double x);");
}
`
	prog, err := buzz.Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	if err != nil {
		t.Fatalf("CompileWith: %v", err)
	}
	for _, e := range chunk.Exports {
		if e == "sqrt" {
			t.Errorf("zdef inside a function must not declare globals; got export %q", e)
		}
	}
}
