package buzz_test

import (
	"context"
	"testing"

	"github.com/egladman/gopherbuzz"
)

// magusfileSrc mirrors the shape of a real magusfile: a top-level config var read
// by an exported target (so it is captured and must stay an Env binding), plus a
// chunk-private scratch loop (promotable to a slot under PromoteTopLevel).
const magusfileSrc = `
var config = 42;
export fun getConfig() int { return config; }
var scratch = 0;
var i = 0;
while (i < 100) { scratch = scratch + i; i = i + 1; }
`

// TestPromoteSession_MagusfileShape exercises the M2 wiring: a session with
// SetPromoteTopLevel(true) (the magusfile execution path) must run a magusfile
// unchanged — the captured config stays live for its target, and the promoted
// scratch var simply drops out of the global namespace.
func TestPromoteSession_MagusfileShape(t *testing.T) {
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	s.SetPromoteTopLevel(true)
	if err := s.Exec(ctx, magusfileSrc); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	// The exported target still resolves the captured top-level config (live Env).
	exports := s.Exports()
	getConfig, ok := exports["getConfig"]
	if !ok {
		t.Fatal("exported target getConfig missing")
	}
	v, err := s.CallValue(ctx, getConfig, nil)
	if err != nil {
		t.Fatalf("CallValue(getConfig): %v", err)
	}
	if !v.IsInt() || v.AsInt() != 42 {
		t.Errorf("getConfig() = %v, want 42", v)
	}

	// config is captured by getConfig, so it stays an Env binding (visible).
	if _, ok := s.Globals()["config"]; !ok {
		t.Error("captured top-level 'config' should remain a visible Env global")
	}
	// scratch is chunk-private and promoted to a slot, so it is no longer a global.
	if _, ok := s.Globals()["scratch"]; ok {
		t.Error("chunk-private 'scratch' should be slot-promoted out of the global namespace")
	}
}

// TestPromoteSession_DefaultOffKeepsGlobals confirms the REPL/default path is
// unchanged: without SetPromoteTopLevel every top-level var stays an Env global,
// so a later Exec (a subsequent prompt line) can still see it.
func TestPromoteSession_DefaultOffKeepsGlobals(t *testing.T) {
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	if err := s.Exec(ctx, magusfileSrc); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if _, ok := s.Globals()["scratch"]; !ok {
		t.Error("without promotion, 'scratch' must remain a visible Env global for later chunks")
	}
	// A later chunk referencing the earlier scratch var compiles and runs.
	if err := s.Exec(ctx, `scratch = scratch + 1;`); err != nil {
		t.Errorf("later chunk referencing earlier top-level var failed: %v", err)
	}
}
