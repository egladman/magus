package buzz_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings" // init() wires the magus host bindings (magus.project.register, etc.)
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

func TestIntegration_ParseTargets(t *testing.T) {
	src := &interp.Source{
		Dir:    t.TempDir(),
		Engine: "buzz",
	}

	// Write a minimal magusfile.buzz to a temp file.
	path := filepath.Join(src.Dir, "magusfile.buzz")
	content := `
import "magus";

export fun build(_args: [str]) > void {}
export fun test(_args: [str]) > void {}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	src.Files = []string{path}

	targets, err := interp.Parse(context.Background(), src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	got := make(map[string]bool)
	for _, tgt := range targets {
		got[tgt.Key] = true
	}
	for _, want := range []string{"build", "test"} {
		if !got[want] {
			t.Errorf("target %q not found; got %v", want, targets)
		}
	}
}

func TestIntegration_RunTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	ran := false
	// We can't capture the side effect of an empty function body,
	// so we test that Run succeeds without error.
	content := `
import "magus";
export fun greet(_args: [str]) > void {}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	src := &interp.Source{Dir: dir, Files: []string{path}, Engine: "buzz"}
	err := interp.Run(context.Background(), src, "greet", nil, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = ran
}

func TestIntegration_UnknownTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	content := `import "magus";
export fun build() > void {}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	src := &interp.Source{Dir: dir, Files: []string{path}, Engine: "buzz"}
	err := interp.Run(context.Background(), src, "notexist", nil, "")
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
}

func TestIntegration_ProjectRegister(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	content := `
import "magus";
magus.project.register(".", fun(p, cb) > bool { cb({
    "outputs": ["bin/*"],
}); return true; });
export fun build(_args: [str]) > void {}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	src := &interp.Source{Dir: dir, Files: []string{path}, Engine: "buzz"}
	if err := interp.Run(context.Background(), src, "build", nil, ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
