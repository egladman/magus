package interp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindMagusTl(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "magusfile.tl"), []byte("global function build(_args: {string}) end\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src, err := Find(dir)
	if err != nil {
		t.Fatal(err)
	}
	if src == nil {
		t.Fatal("expected source, got nil")
	}
	if src.Dir != dir {
		t.Errorf("Dir = %q, want %q", src.Dir, dir)
	}
	if len(src.Files) != 1 {
		t.Errorf("Files = %v, want 1 entry", src.Files)
	}
}

func TestFindNothing(t *testing.T) {
	t.Parallel()
	src, err := Find(t.TempDir())
	if !errors.Is(err, ErrNoMagusfile) {
		t.Errorf("Find error = %v, want ErrNoMagusfile", err)
	}
	if src != nil {
		t.Errorf("Find src = %+v, want nil", src)
	}
}

func TestParseTargetsPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `
global function build(args: {string}) end
global function test(args: {string}) end
global function go_vet(args: {string}) end
`
	path := filepath.Join(dir, "magusfile.tl")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	src := &Source{Dir: dir, Files: []string{path}}
	targets, err := Parse(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]Target{}
	for _, tgt := range targets {
		got[tgt.Key] = tgt
	}

	if _, ok := got["build"]; !ok {
		t.Error("missing target 'build'")
	}
	if _, ok := got["test"]; !ok {
		t.Error("missing target 'test'")
	}
	if _, ok := got["go-vet"]; !ok {
		t.Error("missing target 'go-vet'")
	}
}
