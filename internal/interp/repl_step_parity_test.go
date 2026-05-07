//go:build !cgo

// Parity tests for the step-hook implementation across gopher-lua and Buzz.
// These run in pure-Go (CGO_ENABLED=0) builds where gopher-lua is the active
// Lua backend. A companion file (repl_step_parity_cgo_test.go) covers LuaJIT
// under //go:build cgo.
package interp_test

import (
	"context"
	"testing"

	_ "github.com/egladman/magus/internal/interp/engine/lua/gopherlua"

	"github.com/egladman/magus/internal/interp/engine"
)

// stepEvent records one engine.StepEvent + line number from a step hook callback.
type stepEvent struct {
	ev   engine.StepEvent
	line int
}

func newStepSession(t *testing.T, engineID string) engine.Session {
	t.Helper()
	eng := engine.Lookup(engineID)
	if eng == nil {
		t.Skipf("%s engine not registered", engineID)
	}
	sess, err := eng.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession(%s): %v", engineID, err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// collectEvents installs a MaskLine|MaskCall|MaskReturn hook on sess, runs
// code via DoString, and returns the recorded events.
func collectEvents(t *testing.T, sess engine.Session, mask engine.StepMask, code string) []stepEvent {
	t.Helper()
	stepper, ok := sess.(engine.Stepper)
	if !ok {
		t.Fatal("session does not implement engine.Stepper")
	}
	var got []stepEvent
	stepper.SetStepHook(mask, func(ev engine.StepEvent, frame engine.Frame) {
		got = append(got, stepEvent{ev: ev, line: frame.CurrentLine})
	})
	if err := sess.DoString(code); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	stepper.ClearStepHook()
	return got
}

// TestStepParity_GopherLua_LineSequence verifies that MaskLine events fire at
// the correct line numbers for sequential statements.
func TestStepParity_GopherLua_LineSequence(t *testing.T) {
	sess := newStepSession(t, "gopherlua")

	events := collectEvents(t, sess, engine.MaskLine, `
local x = 1
local y = 2
local z = x + y
`)
	// Expect one line event per statement. Blank first line → statements at 2,3,4.
	var lines []int
	for _, e := range events {
		if e.ev == engine.StepLine {
			lines = append(lines, e.line)
		}
	}
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 line events, got %v", lines)
	}
	// Lines must be strictly increasing.
	for i := 1; i < len(lines); i++ {
		if lines[i] <= lines[i-1] {
			t.Errorf("line sequence not ascending: %v", lines)
			break
		}
	}
}

// TestStepParity_GopherLua_ReturnEvent verifies that a return event fires for
// an explicit return statement inside a Lua function.
func TestStepParity_GopherLua_ReturnEvent(t *testing.T) {
	sess := newStepSession(t, "gopherlua")

	events := collectEvents(t, sess, engine.MaskLine|engine.MaskReturn, `
local function add(a, b)
  return a + b
end
local r = add(1, 2)
`)
	hasReturn := false
	for _, e := range events {
		if e.ev == engine.StepReturn {
			hasReturn = true
		}
	}
	if !hasReturn {
		t.Errorf("expected at least one return event, got %v", events)
	}
}

// TestStepParity_GopherLua_CallDepth verifies that CallDepth increases inside
// a function call, enabling step-over logic to work.
func TestStepParity_GopherLua_CallDepth(t *testing.T) {
	sess := newStepSession(t, "gopherlua")
	stepper, ok := sess.(engine.Stepper)
	if !ok {
		t.Fatal("gopherlua session does not implement engine.Stepper")
	}
	dbg, ok := sess.(engine.DebugReader)
	if !ok {
		t.Fatal("gopherlua session does not implement engine.DebugReader")
	}

	var outerDepth, innerDepth int
	first := true
	stepper.SetStepHook(engine.MaskLine, func(_ engine.StepEvent, _ engine.Frame) {
		if first {
			outerDepth = dbg.CallDepth()
			first = false
		} else {
			innerDepth = dbg.CallDepth()
		}
	})

	// Two line events: one at top level, one inside the function.
	_ = sess.DoString(`
local function inner()
  local v = 1
end
inner()
`)
	stepper.ClearStepHook()

	// The inner function's frame should be deeper than the outer call site.
	if innerDepth <= outerDepth {
		t.Errorf("expected innerDepth (%d) > outerDepth (%d)", innerDepth, outerDepth)
	}
}

func localKeys(m map[string]engine.Value) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
