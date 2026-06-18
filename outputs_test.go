package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// openTempWorkspace creates a minimal workspace in a temp dir with a single
// project registered at projPath with the given output globs.
func openTempWorkspace(t *testing.T, projPath string, outputs []string) (*Magus, string) {
	t.Helper()
	root := t.TempDir()

	// An empty magusfile.buzz at the root marks it as the workspace root.
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	// An empty magusfile.buzz in the project directory makes magus discover it.
	projDir := filepath.Join(root, filepath.FromSlash(projPath))
	require.NoError(t, os.MkdirAll(projDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projDir, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	if len(outputs) > 0 {
		reg.RegisterProject(projPath, WithOutputs(outputs...))
	}

	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	return m, root
}

// TestCleanOutputsRemovesMatchedFiles verifies that CleanOutputs deletes
// files matching declared Outputs globs and returns their paths.
func TestCleanOutputsRemovesMatchedFiles(t *testing.T) {
	m, root := openTempWorkspace(t, "api", []string{"bin/api", "gen/**"})

	binDir := filepath.Join(root, "api", "bin")
	genDir := filepath.Join(root, "api", "gen")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	require.NoError(t, os.MkdirAll(genDir, 0o755))

	// Create files that should be cleaned.
	binFile := filepath.Join(binDir, "api")
	genFile := filepath.Join(genDir, "types.pb.go")
	for _, f := range []string{binFile, genFile} {
		require.NoError(t, os.WriteFile(f, []byte("placeholder"), 0o644))
	}

	projects := m.All()
	removed, err := m.CleanOutputs(context.Background(), projects, false)
	require.NoError(t, err, "CleanOutputs")
	require.NotEmpty(t, removed, "CleanOutputs: no files removed")
	for _, path := range removed {
		_, err := os.Stat(path)
		assert.True(t, os.IsNotExist(err), "file still exists after clean: %s", path)
	}
}

// TestCleanOutputsDryRunDoesNotDelete verifies that --dry-run lists matched
// files without deleting them.
func TestCleanOutputsDryRunDoesNotDelete(t *testing.T) {
	m, root := openTempWorkspace(t, "api", []string{"bin/api"})

	binDir := filepath.Join(root, "api", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	target := filepath.Join(binDir, "api")
	require.NoError(t, os.WriteFile(target, []byte("binary"), 0o755))

	projects := m.All()
	removed, err := m.CleanOutputs(context.Background(), projects, true /* dryRun */)
	require.NoError(t, err, "CleanOutputs (dry-run)")
	require.NotEmpty(t, removed, "dry-run: expected at least one matched path to be returned")
	// File must still exist.
	_, err = os.Stat(target)
	assert.NoError(t, err, "dry-run: file was deleted")
}

// TestCleanOutputsNoMatchIsNoop verifies that CleanOutputs is a no-op when
// no output files exist on disk.
func TestCleanOutputsNoMatchIsNoop(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", []string{"bin/api"})
	projects := m.All()
	removed, err := m.CleanOutputs(context.Background(), projects, false)
	require.NoError(t, err, "CleanOutputs (no files)")
	assert.Empty(t, removed, "expected no removals")
}

// TestFindOutputOwnerMatchesDeclaration verifies that FindOutputOwner returns
// the owning project for a path that matches one of its Output globs.
func TestFindOutputOwnerMatchesDeclaration(t *testing.T) {
	// m.Root() is symlink-resolved by project.Discover; build query paths on it
	// (not the raw t.TempDir) so they share that canonical base on macOS.
	m, _ := openTempWorkspace(t, "api", []string{"bin/**", "gen/**"})

	check := func(relPath, want string) {
		abs := filepath.Join(m.Root(), filepath.FromSlash(relPath))
		p := m.FindOutputOwner(abs)
		got := ""
		if p != nil {
			got = p.Path
		}
		assert.Equalf(t, want, got, "FindOutputOwner(%q)", relPath)
	}

	check("api/bin/api", "api")
	check("api/gen/types.pb.go", "api")
	check("api/src/main.go", "") // not an output
	check("other/bin/app", "")   // different project
}

// TestProjectsResolvesTargets verifies that Projects returns the correct
// project records for a given set of targets.
func TestProjectsResolvesTargets(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)

	targets := []types.Target{{Path: "api", Name: "build"}}
	projects := m.ResolveProjects(targets)
	require.Len(t, projects, 1, "Projects")
	assert.Equal(t, "api", projects[0].Path)
}
