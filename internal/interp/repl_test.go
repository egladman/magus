package interp_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/interp/engine"
	lua "github.com/egladman/magus/internal/interp/engine/lua"
)

// TestPry_LocalsListsLuaLocals verifies that .locals inside the pry REPL
// surfaces real Lua locals from the frame that called magus.pry().
//
// We exercise interp.Pry() directly with a scripted stdin rather than going
// through the magus.pry binding, so the test is independent of the binding's
// stepping plumbing.
func TestPry_LocalsListsLuaLocals(t *testing.T) {
	ctx := context.Background()
	r, err := interp.NewLuaSession(ctx)
	if err != nil {
		t.Fatalf("NewLuaSession: %v", err)
	}
	defer r.Close()
	if err := interp.InstallReplPrelude(ctx, r); err != nil {
		t.Fatalf("InstallReplPrelude: %v", err)
	}

	// Stash a function on the runner that calls Pry() with two locals.
	// We invoke Pry via a Go function so the locals frame is the Lua chunk
	// we craft below.
	called := false
	pryFn := func(_ context.Context, rr lua.Session) int {
		called = true
		input := strings.NewReader(".locals\n.exit\n")
		var out strings.Builder
		_, perr := interp.Pry(ctx, rr, interp.PryContext{}, interp.ReplOptions{
			Stdin:  input,
			Stdout: &out,
		})
		if perr != nil {
			t.Errorf("Pry: %v", perr)
		}
		t.Logf("pry output:\n%s", out.String())
		if !strings.Contains(out.String(), "alpha") {
			t.Errorf("expected local `alpha` in .locals output: %s", out.String())
		}
		if !strings.Contains(out.String(), "beta") {
			t.Errorf("expected local `beta` in .locals output: %s", out.String())
		}
		return 0
	}
	r.SetGlobal("__test_pry", r.NewFunction(pryFn))

	script := `
local alpha = 42
local beta = "hi"
__test_pry()
`
	if err := r.DoString(script); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	if !called {
		t.Fatal("__test_pry was never called")
	}
}

// TestPry_WhereamiPrintsSource ensures .whereami reads the source file and
// includes the marked line.
func TestPry_WhereamiPrintsSource(t *testing.T) {
	ctx := context.Background()
	r, err := interp.NewLuaSession(ctx)
	if err != nil {
		t.Fatalf("NewLuaSession: %v", err)
	}
	defer r.Close()
	if err := interp.InstallReplPrelude(ctx, r); err != nil {
		t.Fatalf("InstallReplPrelude: %v", err)
	}

	// Write a temp source file we can point .whereami at.
	tmp := t.TempDir() + "/sample.lua"
	srcBody := "-- line 1\n-- line 2\n-- line 3 PRY HERE\n-- line 4\n-- line 5\n"
	if err := writeTempFile(tmp, srcBody); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	input := strings.NewReader(".whereami\n.exit\n")
	var out strings.Builder

	_, err = interp.Pry(ctx, r, interp.PryContext{
		File: tmp,
		Line: 3,
		Func: "main",
	}, interp.ReplOptions{
		Stdin:  input,
		Stdout: &out,
	})
	if err != nil {
		t.Fatalf("Pry: %v", err)
	}
	if !strings.Contains(out.String(), "PRY HERE") {
		t.Errorf("expected source line in .whereami output: %s", out.String())
	}
}

// TestPry_BacktraceShowsFrames ensures .where prints a frame list when one
// is available, even if only via the captured PryContext.
func TestPry_BacktraceShowsFrames(t *testing.T) {
	ctx := context.Background()
	r, err := interp.NewLuaSession(ctx)
	if err != nil {
		t.Fatalf("NewLuaSession: %v", err)
	}
	defer r.Close()
	if err := interp.InstallReplPrelude(ctx, r); err != nil {
		t.Fatalf("InstallReplPrelude: %v", err)
	}

	input := strings.NewReader(".where\n.exit\n")
	var out strings.Builder

	_, err = interp.Pry(ctx, r, interp.PryContext{
		File: "magusfile.tl",
		Line: 7,
		Frames: []engine.Frame{
			{Source: "@magusfile.tl", ShortSrc: "magusfile.tl", CurrentLine: 7, Name: "main", What: "Lua"},
		},
	}, interp.ReplOptions{
		Stdin:  input,
		Stdout: &out,
	})
	if err != nil {
		t.Fatalf("Pry: %v", err)
	}
	if !strings.Contains(out.String(), "magusfile.tl") {
		t.Errorf("expected source in backtrace: %s", out.String())
	}
}

// TestPry_HelpListsCommands ensures the help text mentions the new pry-only
// commands so users discover them.
func TestPry_HelpListsCommands(t *testing.T) {
	ctx := context.Background()
	r, err := interp.NewLuaSession(ctx)
	if err != nil {
		t.Fatalf("NewLuaSession: %v", err)
	}
	defer r.Close()
	if err := interp.InstallReplPrelude(ctx, r); err != nil {
		t.Fatalf("InstallReplPrelude: %v", err)
	}

	input := strings.NewReader(".help\n.exit\n")
	var out strings.Builder

	_, err = interp.Pry(ctx, r, interp.PryContext{}, interp.ReplOptions{
		Stdin:  input,
		Stdout: &out,
	})
	if err != nil {
		t.Fatalf("Pry: %v", err)
	}
	for _, cmd := range []string{".whereami", ".where", ".locals", ".globals", ".pp", ".step", ".next", ".finish", ".history"} {
		if !strings.Contains(out.String(), cmd) {
			t.Errorf("expected %q in help output: %s", cmd, out.String())
		}
	}
}

func writeTempFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
