package buzz_test

import (
	"context"
	"testing"

	"github.com/egladman/gopherbuzz"
)

func TestNewSession(t *testing.T) {
	s := buzz.NewSession(context.Background())
	if s == nil {
		t.Fatal("NewSession returned nil")
	}
	if s.Targets() == nil {
		t.Error("Targets() should return a non-nil map")
	}
}

func TestSession_ExecSimpleAssignment(t *testing.T) {
	s := buzz.NewSession(context.Background())
	if err := s.Exec(context.Background(), `var x: int = 42;`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	globals := s.Globals()
	v, ok := globals["x"]
	if !ok {
		t.Fatal("global 'x' not found after exec")
	}
	if !v.IsInt() {
		t.Fatalf("x.IsInt() = false, got Kind() = %q", v.Kind())
	}
	if v.AsInt() != 42 {
		t.Errorf("x.AsInt() = %d, want 42", v.AsInt())
	}
}

func TestSession_EvalExpression(t *testing.T) {
	s := buzz.NewSession(context.Background())
	// Use a function that returns a value to test Eval's return path.
	if err := s.Exec(context.Background(), `fun sum() > int { return 1 + 2; }`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	v, err := s.Eval(context.Background(), `return sum()`)
	if err != nil {
		t.Fatalf("Eval(return sum()): %v", err)
	}
	if !v.IsInt() || v.AsInt() != 3 {
		t.Errorf("Eval(return sum()) = %v, want 3", v)
	}
}

func TestSession_SyntheticModule(t *testing.T) {
	s := buzz.NewSession(context.Background())
	mod := buzz.NewMap()
	mod.MapSet("answer", buzz.IntValue(42))
	// Host registers the module under an import path; it resolves with no file
	// on disk and no include dirs configured.
	s.SetSyntheticModule("example/demo", mod)

	if err := s.Exec(context.Background(), `
import "example/demo";
var x = demo.answer;
`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	v, ok := s.Globals()["x"]
	if !ok {
		t.Fatal("global 'x' not bound; synthetic import did not resolve")
	}
	if !v.IsInt() || v.AsInt() != 42 {
		t.Errorf("x = %v, want 42", v)
	}
}

func TestSession_ModuleResolver(t *testing.T) {
	s := buzz.NewSession(context.Background())
	mod := buzz.NewMap()
	mod.MapSet("answer", buzz.IntValue(7))
	// The resolver gets first refusal on a path-style import that is neither
	// bound nor a synthetic module; it binds the returned module under the
	// path's basename.
	var gotPath string
	s.SetModuleResolver(func(importPath string) (buzz.Value, bool) {
		gotPath = importPath
		if importPath == "spells/widget" {
			return mod, true
		}
		return buzz.Null, false
	})

	if err := s.Exec(context.Background(), `
import "spells/widget";
var x = widget.answer;
`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if gotPath != "spells/widget" {
		t.Errorf("resolver called with %q, want \"spells/widget\"", gotPath)
	}
	v, ok := s.Globals()["x"]
	if !ok {
		t.Fatal("global 'x' not bound; resolver import did not resolve")
	}
	if !v.IsInt() || v.AsInt() != 7 {
		t.Errorf("x = %v, want 7", v)
	}
}

func TestSession_Compile_And_ExecChunk(t *testing.T) {
	s := buzz.NewSession(context.Background())
	chunk, err := s.Compile(`var y: str = "hello";`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if err := s.ExecChunk(context.Background(), chunk); err != nil {
		t.Fatalf("ExecChunk: %v", err)
	}
	v, ok := s.Globals()["y"]
	if !ok {
		t.Fatal("global 'y' not set after ExecChunk")
	}
	if v.AsString() != "hello" {
		t.Errorf("y = %q, want %q", v.AsString(), "hello")
	}
}
