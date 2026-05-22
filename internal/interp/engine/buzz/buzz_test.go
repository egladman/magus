package buzz_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/interp/engine"
	_ "github.com/egladman/magus/internal/interp/engine/buzz"
)

func TestBuzzEngine_Registered(t *testing.T) {
	e := engine.Lookup("buzz")
	if e == nil {
		t.Fatal("buzz engine not registered after package import")
	}
	if e.ID() != "buzz" {
		t.Errorf("ID() = %q, want %q", e.ID(), "buzz")
	}
}

func TestBuzzEngine_NewSession(t *testing.T) {
	e := engine.Lookup("buzz")
	if e == nil {
		t.Skip("buzz engine not registered")
	}
	s, err := e.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	if err := s.DoString(`var x: int = 1;`); err != nil {
		t.Errorf("DoString simple assignment: %v", err)
	}
}

func TestBuzzEngine_GetSetGlobal(t *testing.T) {
	e := engine.Lookup("buzz")
	if e == nil {
		t.Skip("buzz engine not registered")
	}
	s, err := e.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	s.SetGlobal("msg", engine.StringValue("hi"))
	v := s.GetGlobal("msg")
	if v == nil || v.IsNil() {
		t.Fatal("GetGlobal returned nil after SetGlobal")
	}
	got, ok := v.AsString()
	if !ok || got != "hi" {
		t.Errorf("GetGlobal = %q, %v; want %q, true", got, ok, "hi")
	}
}
