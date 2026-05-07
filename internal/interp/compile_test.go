package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunAndParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.tl")
	content := `
global function build(args: {string}) end
global function test(args: {string}) end
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &Source{Dir: dir, Files: []string{path}}

	if err := Run(context.Background(), src, "build", nil, dir); err != nil {
		t.Fatalf("Run build: %v", err)
	}

	targets, err := Parse(context.Background(), src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("Parse: expected 2 targets, got %d", len(targets))
	}
}
