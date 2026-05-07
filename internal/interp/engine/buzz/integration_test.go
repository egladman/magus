package buzz_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings"
)

func TestIntegration_ParseTargets(t *testing.T) {
	src := &interp.Source{
		Dir:    t.TempDir(),
		Engine: "buzz",
	}

	// Write a minimal magusfile.bzz to a temp file.
	path := filepath.Join(src.Dir, "magusfile.bzz")
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
	path := filepath.Join(dir, "magusfile.bzz")
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
	path := filepath.Join(dir, "magusfile.bzz")
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
	path := filepath.Join(dir, "magusfile.bzz")
	content := `
import "magus";
magus.project.register(".", {
    "outputs": ["bin/*"],
});
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
