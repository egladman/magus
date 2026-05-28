package buzz

import (
	"context"
	"testing"
)

// TestEvalReturnsValue verifies Eval surfaces the value of a trailing return,
// which the REPL relies on to print bare expressions.
func TestEvalReturnsValue(t *testing.T) {
	s := NewSession(context.Background())
	defer s.Close()

	v, err := s.Eval(context.Background(), "return 21 + 21")
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !v.IsInt() || v.AsInt() != 42 {
		t.Fatalf("got %v, want int 42", v)
	}
}

// TestEvalSharesGlobals verifies definitions from one Eval are visible to the
// next, so REPL state accumulates across lines.
func TestEvalSharesGlobals(t *testing.T) {
	s := NewSession(context.Background())
	defer s.Close()

	if err := s.Exec(context.Background(), "final x = 10"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	v, err := s.Eval(context.Background(), "return x * 2")
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if v.AsInt() != 20 {
		t.Fatalf("got %d, want 20", v.AsInt())
	}
}

// TestGlobals verifies Globals reports user-defined top-level bindings.
func TestGlobals(t *testing.T) {
	s := NewSession(context.Background())
	defer s.Close()

	if err := s.Exec(context.Background(), "final foo = 1\nfinal bar = 2"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	g := s.Globals()
	if g["foo"].AsInt() != 1 || g["bar"].AsInt() != 2 {
		t.Fatalf("globals = %v, want foo=1 bar=2", g)
	}
}

// TestStepHookLines verifies the line hook fires once per source line in order,
// proving the compiled line table and the dispatch-loop gate line up.
func TestStepHookLines(t *testing.T) {
	s := NewSession(context.Background())
	defer s.Close()

	var lines []int
	s.SetStepHook(MaskLine, func(ev StepEvent, f DebugFrame) {
		if ev == StepLine {
			lines = append(lines, f.Line)
		}
	})

	src := "final a = 1\n" + // line 1
		"final b = 2\n" + // line 2
		"final c = a + b\n" // line 3
	if err := s.Exec(context.Background(), src); err != nil {
		t.Fatalf("exec: %v", err)
	}
	s.ClearStepHook()

	want := []int{1, 2, 3}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("lines = %v, want %v", lines, want)
		}
	}
}

// TestFramesAndLocals verifies that, paused at a hook on a line inside a called
// function, Frames reports the nested stack and Locals reports the named slot.
func TestFramesAndLocals(t *testing.T) {
	s := NewSession(context.Background())
	defer s.Close()

	src := "fun inner(n: int) int {\n" + // 1
		"  final doubled = n * 2\n" + // 2
		"  return doubled\n" + // 3
		"}\n" + // 4
		"final out = inner(21)\n" // 5

	var frames []DebugFrame
	var innerLocal Value
	var captured bool
	s.SetStepHook(MaskLine, func(ev StepEvent, f DebugFrame) {
		// Stop at line 3 (return), by which point `doubled` is assigned.
		if captured || f.Line != 3 || f.Name != "inner" {
			return
		}
		captured = true
		frames = s.Frames()
		innerLocal = s.Locals(0)["doubled"]
	})
	if err := s.Exec(context.Background(), src); err != nil {
		t.Fatalf("exec: %v", err)
	}
	s.ClearStepHook()

	if !captured {
		t.Fatal("hook never fired at inner:3")
	}
	if len(frames) < 2 {
		t.Fatalf("frames = %v, want at least inner + main", frames)
	}
	if frames[0].Name != "inner" {
		t.Fatalf("innermost frame = %q, want inner", frames[0].Name)
	}
	if !innerLocal.IsInt() || innerLocal.AsInt() != 42 {
		t.Fatalf("local doubled = %v, want 42", innerLocal)
	}
}

// TestDebugLinesOffByDefault verifies the one-shot compile path carries no line
// table, keeping the hot path allocation-free.
func TestDebugLinesOffByDefault(t *testing.T) {
	prog, err := Parse("final x = 1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if chunk.Lines != nil {
		t.Fatalf("expected nil line table without DebugLines, got %v", chunk.Lines)
	}
	if chunk.LineAt(0) != 0 {
		t.Fatalf("lineAt should be 0 without debug info, got %d", chunk.LineAt(0))
	}
}
