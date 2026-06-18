package project

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func init() {
	// Register a minimal magusfile-spell stand-in so hasMarker can find
	// magusfile.tl and magusfiles/*.tl during project discovery in these tests.
	// The real "magusfile" spell lives in internal/interp and can't be imported
	// here (cycle).
	DefaultSpellRegistry().RegisterSpell(types.NewSpell(
		"magusfile",
		types.WithSources("magusfile.tl"),
		types.WithDeclarationFiles("magusfile.tl"),
		types.WithDeclarationDirGlobs("magusfiles/*.tl"),
	))
}

// touch writes content (often empty) to <root>/<rel>, creating the
// parent directory tree as needed.
func touch(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
}

// TestSingleGoProject discovers one project at the workspace root.
func TestSingleGoProject(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "magusfile.tl", "")

	ws, err := Discover(context.Background(), root)
	require.NoError(t, err)
	projects := ws.All()
	require.Len(t, projects, 1)
	want := types.Project{
		Path: ".",
		Dir:  ws.Root, // symlink-resolved by Inspect
	}
	assert.Equal(t, want, *projects[0])
}

// TestNestedProjects discovers projects in subdirectories.
func TestNestedProjects(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "magusfile.tl", "")
	touch(t, root, "api/magusfile.tl", "")
	touch(t, root, "web/studio/magusfile.tl", "")

	ws, err := Discover(context.Background(), root)
	require.NoError(t, err)
	got := make([]string, 0, len(ws.Projects))
	for path := range ws.Projects {
		got = append(got, path)
	}
	slices.Sort(got)
	assert.Equal(t, []string{".", "api", "web/studio"}, got)
}

// TestSkipDirs verifies that known skip directories are not descended.
func TestSkipDirs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "magusfile.tl", "")
	for _, skipped := range []string{
		"vendor/sub/magusfile.tl",
		"node_modules/pkg/magusfile.tl",
		"target/release/magusfile.tl",
		".git/hooks/magusfile.tl",
		".magus/manifests/magusfile.tl",
	} {
		touch(t, root, skipped, "")
	}

	ws, err := Discover(context.Background(), root)
	require.NoError(t, err)
	require.Len(t, ws.All(), 1, "skip dirs must not produce projects")
	assert.Equal(t, ".", ws.All()[0].Path)
}

// TestScopeFromCwd returns the innermost project for a path inside a
// project subtree.
func TestScopeFromCwd(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "magusfile.tl", "")
	touch(t, root, "api/magusfile.tl", "")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "api", "internal", "store"), 0o755))

	ws, err := Discover(context.Background(), root)
	require.NoError(t, err)
	p, ok := Where(ws, filepath.Join(root, "api", "internal", "store"))
	require.True(t, ok, "ScopeFromCwd: not found")
	assert.Equal(t, "api", p.Path)
}

// TestScopeFromCwdAtRoot returns false when cwd is the workspace root
// — the root "." project should not be returned as a cwd-scope match.
func TestScopeFromCwdAtRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "magusfile.tl", "")

	ws, err := Discover(context.Background(), root)
	require.NoError(t, err)
	_, ok := Where(ws, root)
	assert.False(t, ok, "workspace root must not be returned as a cwd-scope project")
}

// TestProjectPathSlashes verifies that paths always use forward
// slashes regardless of host OS.
func TestProjectPathSlashes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "web/studio/magusfile.tl", "")

	ws, err := Discover(context.Background(), root)
	require.NoError(t, err)
	p := ws.Get("web/studio")
	require.NotNil(t, p, "web/studio not discovered")
	assert.Equal(t, "web/studio", p.Path)
}

// TestUnderPath narrows projects by path prefix.
func TestUnderPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "extensions/a/magusfile.tl", "")
	touch(t, root, "extensions/b/magusfile.tl", "")
	touch(t, root, "api/magusfile.tl", "")
	touch(t, root, "magusfile.tl", "")

	ws, err := Discover(context.Background(), root)
	require.NoError(t, err)
	matches := ws.UnderPath("extensions/")
	got := make([]string, 0, len(matches))
	for _, p := range matches {
		got = append(got, p.Path)
	}
	slices.Sort(got)
	assert.Equal(t, []string{"extensions/a", "extensions/b"}, got)
}

// TestMagusfileTlIsTheOnlyFileMarker verifies that magusfile.tl is the only
// recognised project marker; legacy .go forms are not.
func TestMagusfileTlIsTheOnlyFileMarker(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "magusfile.tl", "")
	touch(t, root, "tools/modern/magusfile.tl", "")
	// legacy forms must NOT be recognised
	touch(t, root, "tools/go-form/magusfile.go", "")
	touch(t, root, "tools/mage/magefile.go", "")

	ws, err := Discover(context.Background(), root)
	require.NoError(t, err)
	got := make([]string, 0, len(ws.Projects))
	for path := range ws.Projects {
		got = append(got, path)
	}
	slices.Sort(got)
	assert.Equal(t, []string{".", "tools/modern"}, got)
}

// TestMagusFilesDirMarker verifies that a magusfiles/ subdirectory with
// at least one .tl file marks its parent as a project.
func TestMagusFilesDirMarker(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "magusfile.tl", "")
	touch(t, root, "tools/modern/magusfiles/build.tl", "")
	// legacy magefiles/ must NOT be recognised
	touch(t, root, "tools/legacy/magefiles/build.go", "")
	// An empty magusfiles/ directory must NOT mark a parent.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tools/empty/magusfiles"), 0o755))

	ws, err := Discover(context.Background(), root)
	require.NoError(t, err)
	got := make([]string, 0, len(ws.Projects))
	for path := range ws.Projects {
		got = append(got, path)
	}
	slices.Sort(got)
	assert.Equal(t, []string{".", "tools/modern"}, got)
}

// TestResolvedSpells verifies ResolvedSpells() compiles (BUG 2: Target param removed).
func TestResolvedSpells(t *testing.T) {
	t.Parallel()
	p := &types.Project{Path: "x"}
	assert.Nil(t, p.ResolvedSpells, "ResolvedSpells() should be nil before bind")
}

// TestRegisterSpellNilPanics verifies Register(nil) panics.
func TestRegisterSpellNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		DefaultSpellRegistry().RegisterSpell(nil)
	}, "Register(nil) did not panic")
}
