package buzz

import (
	"context"
	"testing"

	vmpackage "github.com/egladman/gopherbuzz/vm"
)

// runProg compiles src with the given options and runs it against a fresh env
// that has the VM intrinsics available (spawn, zdef) via the Env fallback in
// slot mode, returning the program's top-level return value.
func runProg(t *testing.T, src string, opts CompileOptions) Value {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunk, err := CompileWith(prog, opts)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	env := vmpackage.NewEnv()
	vmpackage.RegisterStdlib(env)
	v, err := vmpackage.NewVM(context.Background()).Run(chunk, env)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return v
}

// TestSlotTopLevelLoop is the workload behind BenchmarkLoopSum: a tight
// top-level arithmetic loop whose variables become stack slots in slot mode.
func TestSlotTopLevelLoop(t *testing.T) {
	src := `var sum = 0;
var i = 0;
while (i < 1000) { sum = sum + i; i = i + 1; }
return sum;`
	wantInt(t, runProg(t, src, CompileOptions{}), 499500)
}

// TestSlotEnvEquivalence asserts the two compile modes agree on results for
// programs that don't depend on cross-Run global sharing.
func TestSlotEnvEquivalence(t *testing.T) {
	srcs := []string{
		`var a = 3; var b = 4; return a * a + b * b;`,
		`var s = 0; for (var i = 0; i < 5; i = i + 1) { s = s + i; } return s;`,
		`var total = 0; foreach (x in 0..10) { total = total + x; } return total;`,
		`var n = 0; { var n = 41; } return n + 1;`, // inner block shadows; outer unchanged
	}
	for _, src := range srcs {
		slot := runProg(t, src, CompileOptions{})
		env := runProg(t, src, CompileOptions{SharedGlobals: true})
		if !slot.RawEqual(env) {
			t.Errorf("mode mismatch for %q: slot=%s env=%s", src, slot.String(), env.String())
		}
	}
}

// TestSlotTopLevelClosureCapture documents that a closure over a top-level
// variable in slot mode captures it as an upvalue — by value at closure
// creation, matching Buzz's existing nested-function closure semantics.
func TestSlotTopLevelClosureCapture(t *testing.T) {
	// Read-only capture: the closure sees the value present at creation.
	wantInt(t, runProg(t, `var x = 10;
fun getx() int { return x; }
return getx();`, CompileOptions{}), 10)

	// Snapshot semantics: mutating x after the closure is built does not change
	// what the closure returns (by-value upvalue). In SharedGlobals mode the
	// same source would observe the live global instead — that divergence is
	// the intended difference between the two models.
	wantInt(t, runProg(t, `var x = 1;
fun getx() int { return x; }
x = 2;
return getx();`, CompileOptions{}), 1)
}
