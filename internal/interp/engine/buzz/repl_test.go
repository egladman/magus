package buzz

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/interp/engine"
)

func newReplSession(t *testing.T) *session {
	t.Helper()
	eng := engine.Lookup("buzz")
	if eng == nil {
		t.Skip("buzz engine not registered")
	}
	s, err := eng.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s.(*session)
}

func driver(t *testing.T, s *session) engine.ReplDriver {
	t.Helper()
	drivers := s.Drivers()
	if len(drivers) != 1 {
		t.Fatalf("Drivers() = %d, want 1", len(drivers))
	}
	if drivers[0].Language() != "buzz" {
		t.Fatalf("Language() = %q, want buzz", drivers[0].Language())
	}
	return drivers[0]
}

// TestDriverEvalExpression verifies a bare expression prints its value.
func TestDriverEvalExpression(t *testing.T) {
	d := driver(t, newReplSession(t))
	vals, err := d.EvalLine("6 * 7")
	if err != nil {
		t.Fatalf("EvalLine: %v", err)
	}
	if len(vals) != 1 {
		t.Fatalf("got %d values, want 1", len(vals))
	}
	if n, ok := vals[0].AsNumber(); !ok || n != 42 {
		t.Fatalf("got %v, want 42", vals[0])
	}
}

// TestDriverEvalStatement verifies a statement runs with no printable value and
// that a following expression sees its effect (shared globals).
func TestDriverEvalStatement(t *testing.T) {
	d := driver(t, newReplSession(t))
	vals, err := d.EvalLine("final n = 5")
	if err != nil {
		t.Fatalf("EvalLine decl: %v", err)
	}
	if len(vals) != 0 {
		t.Fatalf("decl produced %d values, want 0", len(vals))
	}
	vals, err = d.EvalLine("n + 1")
	if err != nil {
		t.Fatalf("EvalLine expr: %v", err)
	}
	if n, _ := vals[0].AsNumber(); n != 6 {
		t.Fatalf("got %v, want 6", vals[0])
	}
}

// TestDriverUserGlobalsFiltersHost verifies host bindings (magus) are omitted
// while user definitions are listed.
func TestDriverUserGlobalsFiltersHost(t *testing.T) {
	s := newReplSession(t)
	s.core.SetGlobal("magus", s.core.GetGlobal("magus")) // ensure a host-named global exists
	d := driver(t, s)
	if _, err := d.EvalLine("final mine = 99"); err != nil {
		t.Fatalf("EvalLine: %v", err)
	}
	g := d.UserGlobals()
	if _, ok := g["magus"]; ok {
		t.Fatal("UserGlobals leaked host binding 'magus'")
	}
	if g["mine"] == nil {
		t.Fatal("UserGlobals missing user global 'mine'")
	}
}

// TestEvalLineNoDoubleSideEffect verifies an expression with a side effect runs
// exactly once: the driver compiles the expression form before running, so it
// never falls through to re-running the statement form after execution.
func TestEvalLineNoDoubleSideEffect(t *testing.T) {
	s := newReplSession(t)
	d := driver(t, s)
	if _, err := d.EvalLine("var count = 0"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := d.EvalLine("fun bump() int { count = count + 1\nreturn count }"); err != nil {
		t.Fatalf("def: %v", err)
	}
	if _, err := d.EvalLine("bump()"); err != nil {
		t.Fatalf("call: %v", err)
	}
	vals, err := d.EvalLine("count")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n, _ := vals[0].AsNumber(); n != 1 {
		t.Fatalf("count = %v after one bump(), want 1 (double execution?)", vals[0])
	}
}

// TestLineDelta verifies brace counting drives multi-line continuation.
func TestLineDelta(t *testing.T) {
	d := driver(t, newReplSession(t))
	if got := d.LineDelta("fun f() int {"); got != 1 {
		t.Fatalf("open brace delta = %d, want 1", got)
	}
	if got := d.LineDelta("}"); got != -1 {
		t.Fatalf("close brace delta = %d, want -1", got)
	}
	if got := d.LineDelta(`final s = "a {b} c"`); got != 0 {
		t.Fatalf("string-literal braces delta = %d, want 0", got)
	}
}

// TestDebugReaderInterface verifies the adapter implements the optional REPL
// interfaces the shared Pry loop type-asserts.
func TestDebugReaderInterface(t *testing.T) {
	s := newReplSession(t)
	if _, ok := engine.Session(s).(engine.DebugReader); !ok {
		t.Fatal("session does not implement engine.DebugReader")
	}
	if _, ok := engine.Session(s).(engine.Stepper); !ok {
		t.Fatal("session does not implement engine.Stepper")
	}
	if _, ok := engine.Session(s).(engine.DriversProvider); !ok {
		t.Fatal("session does not implement engine.DriversProvider")
	}
}

// TestStepperFramesThroughEngine drives the engine-level Stepper + DebugReader
// end to end: pause at a line inside a called function and read the frame stack.
func TestStepperFramesThroughEngine(t *testing.T) {
	s := newReplSession(t)
	stepper := engine.Session(s).(engine.Stepper)
	dbg := engine.Session(s).(engine.DebugReader)

	src := "fun inner(n: int) int {\n" +
		"  return n + 1\n" +
		"}\n" +
		"final out = inner(7)\n"

	var depthAtInner int
	var nameAtInner string
	stepper.SetStepHook(engine.MaskLine, func(ev engine.StepEvent, f engine.Frame) {
		if f.Name == "inner" && nameAtInner == "" {
			depthAtInner = dbg.CallDepth()
			if fr := dbg.Frames(); len(fr) > 0 {
				nameAtInner = fr[0].Name
			}
		}
	})
	if err := s.DoString(src); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	stepper.ClearStepHook()

	if nameAtInner != "inner" {
		t.Fatalf("innermost frame at pause = %q, want inner", nameAtInner)
	}
	if depthAtInner < 2 {
		t.Fatalf("call depth inside inner = %d, want >= 2", depthAtInner)
	}
}
