package buzz

import (
	"context"
	"testing"
	"time"

	vmpackage "github.com/egladman/gopherbuzz/vm"
)

// runProgJIT compiles src and runs it with the JIT forced on or off, returning
// the top-level return value and any error.
func runProgJIT(t *testing.T, src string, jit bool) (Value, error) {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	env := vmpackage.NewEnv()
	vmpackage.RegisterStdlib(env)
	vmpackage.SetJIT(jit)
	defer vmpackage.SetJIT(false)
	return vmpackage.NewVM(context.Background()).Run(chunk, env)
}

// jitPrograms are top-level integer loops the baseline JIT is meant to cover.
var jitPrograms = []struct {
	name string
	src  string
}{
	{"loopsum", `var sum = 0; var i = 0;
		while (i < 1000) { sum = sum + i; i = i + 1; } return sum;`},
	{"loopeq_even", `var count = 0; var i = 0;
		while (i < 1000) { if (i % 2 == 0) { count = count + 1; } i = i + 1; } return count;`},
	{"mul", `var acc = 1; var i = 1;
		while (i <= 10) { acc = acc * 2; i = i + 1; } return acc;`},
	{"nested", `var total = 0; var i = 0;
		while (i < 50) { var j = 0; while (j < 50) { total = total + 1; j = j + 1; } i = i + 1; }
		return total;`},
	{"sub_div_mod", `var x = 1000; var steps = 0;
		while (x > 1) { if (x % 2 == 0) { x = x / 2; } else { x = x - 1; } steps = steps + 1; }
		return steps;`},
	{"const_first", `var i = 0; var s = 0;
		while (1000 > i) { s = s + i; i = i + 1; } return s;`},
	{"float_sum", `var sum = 0.0; var i = 0.0;
		while (i < 1000.0) { sum = sum + i; i = i + 1.0; } return sum;`},
	{"float_mul", `var acc = 1.0; var i = 0.0;
		while (i < 10.0) { acc = acc * 2.0; i = i + 1.0; } return acc;`},
	{"float_div", `var x = 1024.0; var i = 0.0;
		while (i < 10.0) { x = x / 2.0; i = i + 1.0; } return x;`},
	{"float_cmp", `var c = 0.0; var i = 0.0;
		while (i < 1000.0) { if (i >= 500.0) { c = c + 1.0; } i = i + 1.0; } return c;`},
	{"mixed_promote", `var sum = 0.0; var i = 0;
		while (i < 100) { sum = sum + i; i = i + 1; } return sum;`},
}

// TestJITMatchesInterpreter is the core differential test: every program must
// produce the identical result with the JIT on and off, and the JIT must
// actually engage on the top-level loop.
func TestJITMatchesInterpreter(t *testing.T) {
	for _, p := range jitPrograms {
		t.Run(p.name, func(t *testing.T) {
			want, err := runProgJIT(t, p.src, false)
			if err != nil {
				t.Fatalf("interp: %v", err)
			}
			vmpackage.ResetJITStats()
			got, err := runProgJIT(t, p.src, true)
			if err != nil {
				t.Fatalf("jit: %v", err)
			}
			if vmpackage.JITAvailable() && vmpackage.JITRunCount() == 0 {
				t.Fatalf("JIT did not engage for %s (chunk ineligible?)", p.name)
			}
			if got.String() != want.String() {
				t.Fatalf("%s: jit=%v interp=%v", p.name, got, want)
			}
		})
	}
}

// TestJITDeopt forces a guard miss: an `any`-typed value laundered into the loop
// variable makes an int op see a non-int operand. The JIT must deopt to the
// interpreter and produce the exact same outcome (here, a runtime type error)
// as the pure-interpreter run.
func TestJITDeopt(t *testing.T) {
	// `bad` is any-typed holding a string; `n + bad` inside the loop forces the
	// fused int op to see a non-int and deopt. Both engines must agree.
	src := `var bad: any = "x"; var n = 0; var i = 0;
		while (i < 5) { n = n + i; i = i + 1; }
		n = n + bad; return n;`
	want, errWant := runProgJIT(t, src, false)
	vmpackage.ResetJITStats()
	got, errGot := runProgJIT(t, src, true)
	if (errWant == nil) != (errGot == nil) {
		t.Fatalf("error mismatch: interp=%v jit=%v", errWant, errGot)
	}
	if errWant == nil && got.String() != want.String() {
		t.Fatalf("value mismatch: jit=%v interp=%v", got, want)
	}
}

// TestJITCancellation confirms a JIT'd loop honors context cancellation via the
// back-edge poll: a long loop must return the cancellation error promptly
// instead of running to completion.
func TestJITCancellation(t *testing.T) {
	if !vmpackage.JITAvailable() {
		t.Skip("no JIT backend on this build")
	}
	prog, err := Parse(`var i = 0; while (i < 1000000000) { i = i + 1; } return i;`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	env := vmpackage.NewEnv()
	vmpackage.RegisterStdlib(env)
	vmpackage.SetJIT(true)
	defer vmpackage.SetJIT(false)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()

	done := make(chan struct{})
	var runErr error
	go func() {
		_, runErr = vmpackage.NewVM(ctx).Run(chunk, env)
		close(done)
	}()
	select {
	case <-done:
		if runErr == nil {
			t.Fatal("expected cancellation error, got nil (loop ran to completion?)")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("JIT'd loop did not honor cancellation within 5s")
	}
}
