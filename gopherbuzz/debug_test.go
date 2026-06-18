package buzz

import (
	"context"
	"testing"

	vmpackage "github.com/egladman/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvalReturnsValue verifies Eval surfaces the value of a trailing return,
// which the REPL relies on to print bare expressions.
func TestEvalReturnsValue(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	defer s.Close()

	v, err := s.Eval(context.Background(), "return 21 + 21")
	require.NoError(t, err, "eval")
	require.True(t, v.IsInt(), "got %v, want int 42", v)
	assert.Equal(t, int64(42), v.AsInt(), "got %v, want int 42", v)
}

// TestEvalSharesGlobals verifies definitions from one Eval are visible to the
// next, so REPL state accumulates across lines.
func TestEvalSharesGlobals(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	defer s.Close()

	require.NoError(t, s.Exec(context.Background(), "final x = 10"), "exec")
	v, err := s.Eval(context.Background(), "return x * 2")
	require.NoError(t, err, "eval")
	assert.Equal(t, int64(20), v.AsInt())
}

// TestGlobals verifies Globals reports user-defined top-level bindings.
func TestGlobals(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	defer s.Close()

	require.NoError(t, s.Exec(context.Background(), "final foo = 1\nfinal bar = 2"), "exec")
	g := s.Globals()
	assert.Equal(t, int64(1), g["foo"].AsInt(), "globals foo")
	assert.Equal(t, int64(2), g["bar"].AsInt(), "globals bar")
}

// TestStepHookLines verifies the line hook fires once per source line in order,
// proving the compiled line table and the dispatch-loop gate line up.
func TestStepHookLines(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	defer s.Close()

	var lines []int
	s.SetStepHook(MaskLine, func(ev StepEvent, f DebugFrame) {
		if ev == vmpackage.StepLine {
			lines = append(lines, f.Line)
		}
	})

	src := "final a = 1\n" + // line 1
		"final b = 2\n" + // line 2
		"final c = a + b\n" // line 3
	require.NoError(t, s.Exec(context.Background(), src), "exec")
	s.ClearStepHook()

	assert.Equal(t, []int{1, 2, 3}, lines)
}

// TestFramesAndLocals verifies that, paused at a hook on a line inside a called
// function, Frames reports the nested stack and Locals reports the named slot.
func TestFramesAndLocals(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	defer s.Close()

	src := "fun inner(n: int) > int {\n" + // 1
		"  final doubled = n * 2\n" + // 2
		"  return doubled\n" + // 3
		"}\n" + // 4
		"final res = inner(21)\n" // 5

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
	require.NoError(t, s.Exec(context.Background(), src), "exec")
	s.ClearStepHook()

	require.True(t, captured, "hook never fired at inner:3")
	require.GreaterOrEqual(t, len(frames), 2, "frames = %v, want at least inner + main", frames)
	assert.Equal(t, "inner", frames[0].Name, "innermost frame")
	require.True(t, innerLocal.IsInt(), "local doubled = %v, want 42", innerLocal)
	assert.Equal(t, int64(42), innerLocal.AsInt(), "local doubled = %v, want 42", innerLocal)
}

// TestDebugLinesOffByDefault verifies the one-shot compile path carries no line
// table, keeping the hot path allocation-free.
func TestDebugLinesOffByDefault(t *testing.T) {
	prog, err := ParseEmbedded("final x = 1")
	require.NoError(t, err, "parse")
	chunk, err := CompileWith(prog, CompileOptions{})
	require.NoError(t, err, "compile")
	assert.Nil(t, chunk.Lines, "expected nil line table without DebugLines")
	assert.Equal(t, 0, chunk.LineAt(0), "lineAt should be 0 without debug info")
}
