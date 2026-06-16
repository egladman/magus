package cache

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestLastEntryFor_NoEntries(t *testing.T) {
	cdir := t.TempDir()
	c, err := Open(cdir)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.LastEntry("nonexistent/project")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
}

func TestLastEntryFor_ReturnsLatest(t *testing.T) {
	root := t.TempDir()
	cdir := t.TempDir()

	// Set up a minimal project with one source and one declared output.
	src := filepath.Join(root, "myservice", "main.go")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outRel := filepath.Join("myservice", "out.bin")
	spec := Spec{
		ProjectPath:   "myservice",
		Sources:       []string{"myservice/*.go"},
		Outputs:       []string{outRel},
		WorkspaceRoot: root,
		Target:        "build",
	}
	fn := func(_ context.Context) error {
		abs := filepath.Join(root, outRel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		return os.WriteFile(abs, []byte("binary"), 0o755)
	}

	c, err := Open(cdir, WithMutable(true), WithLogger(discardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), spec, fn); err != nil {
		t.Fatal(err)
	}

	m, logPath, err := c.LastEntry("myservice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ProjectPath != "myservice" {
		t.Errorf("manifest.ProjectPath = %q, want %q", m.ProjectPath, "myservice")
	}
	if logPath == "" {
		t.Error("expected non-empty log path")
	}
	if m.Target != "build" {
		t.Errorf("manifest.Target = %q, want %q", m.Target, "build")
	}
}

func TestLastEntryForTarget_FiltersTarget(t *testing.T) {
	root := t.TempDir()
	cdir := t.TempDir()

	writeOutput := func(name string) func(context.Context) error {
		return func(_ context.Context) error {
			abs := filepath.Join(root, "svc", name)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return err
			}
			return os.WriteFile(abs, []byte("x"), 0o644)
		}
	}

	src := filepath.Join(root, "svc", "main.go")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Open(cdir, WithMutable(true), WithLogger(discardLogger))
	if err != nil {
		t.Fatal(err)
	}

	buildSpec := Spec{
		ProjectPath:   "svc",
		Sources:       []string{"svc/*.go"},
		Outputs:       []string{"svc/build.out"},
		WorkspaceRoot: root,
		Target:        "build",
	}
	testSpec := Spec{
		ProjectPath:   "svc",
		Sources:       []string{"svc/*.go"},
		Outputs:       []string{"svc/test.out"},
		WorkspaceRoot: root,
		Target:        "test",
	}

	if _, err := c.Run(context.Background(), buildSpec, writeOutput("build.out")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), testSpec, writeOutput("test.out")); err != nil {
		t.Fatal(err)
	}

	// Filtering by "test" should return only the test entry.
	m, _, err := c.LastEntryForTarget("svc", "test")
	if err != nil {
		t.Fatalf("LastEntryForTarget(test): unexpected error: %v", err)
	}
	if m.Target != "test" {
		t.Errorf("LastEntryForTarget(test): Target = %q, want %q", m.Target, "test")
	}

	// Filtering by "build" should return only the build entry.
	m, _, err = c.LastEntryForTarget("svc", "build")
	if err != nil {
		t.Fatalf("LastEntryForTarget(build): unexpected error: %v", err)
	}
	if m.Target != "build" {
		t.Errorf("LastEntryForTarget(build): Target = %q, want %q", m.Target, "build")
	}

	// Filtering by an unknown target returns ErrNotExist.
	_, _, err = c.LastEntryForTarget("svc", "format")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LastEntryForTarget(format): expected fs.ErrNotExist, got %v", err)
	}
}
