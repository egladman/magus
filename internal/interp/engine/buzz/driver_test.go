package buzz

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/interp/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newReplSession(t *testing.T) *session {
	t.Helper()
	eng := engine.Lookup("buzz")
	if eng == nil {
		t.Skip("buzz engine not registered")
	}
	s, err := eng.NewSession(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s.(*session)
}

func driver(t *testing.T, s *session) engine.ReplDriver {
	t.Helper()
	drivers := s.Drivers()
	require.Len(t, drivers, 1)
	require.Equal(t, "buzz", drivers[0].Language())
	return drivers[0]
}

// TestDriverEvalExpression verifies a bare expression prints its value.
func TestDriverEvalExpression(t *testing.T) {
	d := driver(t, newReplSession(t))
	vals, err := d.EvalLine("6 * 7")
	require.NoError(t, err)
	require.Len(t, vals, 1)
	n, ok := vals[0].AsNumber()
	assert.True(t, ok)
	assert.Equal(t, float64(42), n)
}

// TestDriverEvalStatement verifies a statement runs with no printable value and
// that a following expression sees its effect (shared globals).
func TestDriverEvalStatement(t *testing.T) {
	d := driver(t, newReplSession(t))
	vals, err := d.EvalLine("final n = 5")
	require.NoError(t, err)
	assert.Empty(t, vals, "decl should produce no values")

	vals, err = d.EvalLine("n + 1")
	require.NoError(t, err)
	require.NotEmpty(t, vals)
	n, _ := vals[0].AsNumber()
	assert.Equal(t, float64(6), n)
}

// TestDriverUserGlobalsFiltersHost verifies host bindings (magus) are omitted
// while user definitions are listed.
func TestDriverUserGlobalsFiltersHost(t *testing.T) {
	s := newReplSession(t)
	s.core.SetGlobal("magus", s.core.GetGlobal("magus")) // ensure a host-named global exists
	d := driver(t, s)
	_, err := d.EvalLine("final mine = 99")
	require.NoError(t, err)
	g := d.UserGlobals()
	assert.NotContains(t, g, "magus", "UserGlobals leaked host binding 'magus'")
	assert.NotNil(t, g["mine"], "UserGlobals missing user global 'mine'")
}

// TestEvalLineNoDoubleSideEffect verifies an expression with a side effect runs
// exactly once: the driver compiles the expression form before running, so it
// never falls through to re-running the statement form after execution.
func TestEvalLineNoDoubleSideEffect(t *testing.T) {
	s := newReplSession(t)
	d := driver(t, s)
	_, err := d.EvalLine("var count = 0")
	require.NoError(t, err, "init")
	_, err = d.EvalLine("fun bump() > int { count = count + 1\nreturn count }")
	require.NoError(t, err, "def")
	_, err = d.EvalLine("bump()")
	require.NoError(t, err, "call")
	vals, err := d.EvalLine("count")
	require.NoError(t, err, "read")
	require.NotEmpty(t, vals)
	n, _ := vals[0].AsNumber()
	assert.Equal(t, float64(1), n, "count after one bump(), want 1 (double execution?)")
}

// TestLineDelta verifies brace counting drives multi-line continuation.
func TestLineDelta(t *testing.T) {
	d := driver(t, newReplSession(t))
	assert.Equal(t, 1, d.LineDelta("fun f() int {"), "open brace delta")
	assert.Equal(t, -1, d.LineDelta("}"), "close brace delta")
	assert.Equal(t, 0, d.LineDelta(`final s = "a {b} c"`), "string-literal braces delta")
}

// TestDebugReaderInterface verifies the adapter implements the optional REPL
// interfaces the shared Pry loop type-asserts.
func TestDebugReaderInterface(t *testing.T) {
	s := newReplSession(t)
	_, ok := engine.Session(s).(engine.DebugReader)
	assert.True(t, ok, "session does not implement engine.DebugReader")
	_, ok = engine.Session(s).(engine.Stepper)
	assert.True(t, ok, "session does not implement engine.Stepper")
	_, ok = engine.Session(s).(engine.DriversProvider)
	assert.True(t, ok, "session does not implement engine.DriversProvider")
}

// TestStepperFramesThroughEngine drives the engine-level Stepper + DebugReader
// end to end: pause at a line inside a called function and read the frame stack.
func TestStepperFramesThroughEngine(t *testing.T) {
	s := newReplSession(t)
	stepper := engine.Session(s).(engine.Stepper)
	dbg := engine.Session(s).(engine.DebugReader)

	src := "fun inner(n: int) > int {\n" +
		"  return n + 1\n" +
		"}\n" +
		"final res = inner(7)\n"

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
	require.NoError(t, s.DoString(src))
	stepper.ClearStepHook()

	assert.Equal(t, "inner", nameAtInner, "innermost frame at pause")
	assert.GreaterOrEqual(t, depthAtInner, 2, "call depth inside inner")
}
