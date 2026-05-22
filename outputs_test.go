package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
)

// openTempWorkspace creates a minimal workspace in a temp dir with a single
// project registered at projPath with the given output globs.
func openTempWorkspace(t *testing.T, projPath string, outputs []string) (*Magus, string) {
	t.Helper()
	root := t.TempDir()

	// An empty magusfile.bzz at the root marks it as the workspace root.
	if err := os.WriteFile(filepath.Join(root, "magusfile.bzz"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	// An empty magusfile.bzz in the project directory makes magus discover it.
	projDir := filepath.Join(root, filepath.FromSlash(projPath))
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "magusfile.bzz"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewWorkspaceRegistry()
	if len(outputs) > 0 {
		reg.RegisterProject(projPath, WithOutputs(outputs...))
	}

	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, root
}

// TestCleanOutputsRemovesMatchedFiles verifies that CleanOutputs deletes
// files matching declared Outputs globs and returns their paths.
func TestCleanOutputsRemovesMatchedFiles(t *testing.T) {
	m, root := openTempWorkspace(t, "api", []string{"bin/api", "gen/**"})

	binDir := filepath.Join(root, "api", "bin")
	genDir := filepath.Join(root, "api", "gen")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create files that should be cleaned.
	binFile := filepath.Join(binDir, "api")
	genFile := filepath.Join(genDir, "types.pb.go")
	for _, f := range []string{binFile, genFile} {
		if err := os.WriteFile(f, []byte("placeholder"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	projects := m.All()
	removed, err := m.CleanOutputs(context.Background(), projects, false)
	if err != nil {
		t.Fatalf("CleanOutputs: %v", err)
	}
	if len(removed) == 0 {
		t.Fatal("CleanOutputs: no files removed")
	}
	for _, path := range removed {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("file still exists after clean: %s", path)
		}
	}
}

// TestCleanOutputsDryRunDoesNotDelete verifies that --dry-run lists matched
// files without deleting them.
func TestCleanOutputsDryRunDoesNotDelete(t *testing.T) {
	m, root := openTempWorkspace(t, "api", []string{"bin/api"})

	binDir := filepath.Join(root, "api", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(binDir, "api")
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	projects := m.All()
	removed, err := m.CleanOutputs(context.Background(), projects, true /* dryRun */)
	if err != nil {
		t.Fatalf("CleanOutputs (dry-run): %v", err)
	}
	if len(removed) == 0 {
		t.Fatal("dry-run: expected at least one matched path to be returned")
	}
	// File must still exist.
	if _, err := os.Stat(target); err != nil {
		t.Errorf("dry-run: file was deleted: %v", err)
	}
}

// TestCleanOutputsNoMatchIsNoop verifies that CleanOutputs is a no-op when
// no output files exist on disk.
func TestCleanOutputsNoMatchIsNoop(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", []string{"bin/api"})
	projects := m.All()
	removed, err := m.CleanOutputs(context.Background(), projects, false)
	if err != nil {
		t.Fatalf("CleanOutputs (no files): %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("expected no removals, got %d: %v", len(removed), removed)
	}
}

// TestFindOutputOwnerMatchesDeclaration verifies that FindOutputOwner returns
// the owning project for a path that matches one of its Output globs.
func TestFindOutputOwnerMatchesDeclaration(t *testing.T) {
	m, root := openTempWorkspace(t, "api", []string{"bin/**", "gen/**"})

	cases := []struct {
		relPath string
		want    string // project path, or "" for not found
	}{
		{"api/bin/api", "api"},
		{"api/gen/types.pb.go", "api"},
		{"api/src/main.go", ""}, // not an output
		{"other/bin/app", ""},   // different project
	}

	for _, tc := range cases {
		abs := filepath.Join(root, filepath.FromSlash(tc.relPath))
		p := m.FindOutputOwner(abs)
		got := ""
		if p != nil {
			got = p.Path
		}
		if got != tc.want {
			t.Errorf("FindOutputOwner(%q): got %q, want %q", tc.relPath, got, tc.want)
		}
	}
}

// TestProjectsResolvesTargets verifies that Projects returns the correct
// project records for a given set of targets.
func TestProjectsResolvesTargets(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)

	targets := []types.Target{{Path: "api", Name: "build"}}
	projects := m.ResolveProjects(targets)
	if len(projects) != 1 {
		t.Fatalf("Projects: got %d projects, want 1", len(projects))
	}
	if projects[0].Path != "api" {
		t.Errorf("Projects: got path %q, want %q", projects[0].Path, "api")
	}
}
