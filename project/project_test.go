package project

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

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
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSingleGoProject discovers one project at the workspace root.
func TestSingleGoProject(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "magusfile.tl", "")

	ws, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	projects := ws.All()
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	want := types.Project{
		Path: ".",
		Dir:  ws.Root, // symlink-resolved by Inspect
	}
	if got := *projects[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("project = %+v, want %+v", got, want)
	}
}

// TestNestedProjects discovers projects in subdirectories.
func TestNestedProjects(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "magusfile.tl", "")
	touch(t, root, "api/magusfile.tl", "")
	touch(t, root, "web/studio/magusfile.tl", "")

	ws, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(ws.Projects))
	for path := range ws.Projects {
		got = append(got, path)
	}
	slices.Sort(got)
	want := []string{".", "api", "web/studio"}
	if !slices.Equal(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.All()) != 1 {
		t.Fatalf("got %d projects, want 1 (skip dirs must not produce projects)", len(ws.All()))
	}
	if ws.All()[0].Path != "." {
		t.Fatalf("got %q, want %q", ws.All()[0].Path, ".")
	}
}

// TestScopeFromCwd returns the innermost project for a path inside a
// project subtree.
func TestScopeFromCwd(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "magusfile.tl", "")
	touch(t, root, "api/magusfile.tl", "")
	if err := os.MkdirAll(filepath.Join(root, "api", "internal", "store"), 0o755); err != nil {
		t.Fatal(err)
	}

	ws, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := Where(ws, filepath.Join(root, "api", "internal", "store"))
	if !ok {
		t.Fatal("ScopeFromCwd: not found")
	}
	if p.Path != "api" {
		t.Fatalf("got %q, want %q", p.Path, "api")
	}
}

// TestScopeFromCwdAtRoot returns false when cwd is the workspace root
// — the root "." project should not be returned as a cwd-scope match.
func TestScopeFromCwdAtRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "magusfile.tl", "")

	ws, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Where(ws, root); ok {
		t.Fatal("workspace root must not be returned as a cwd-scope project")
	}
}

// TestProjectPathSlashes verifies that paths always use forward
// slashes regardless of host OS.
func TestProjectPathSlashes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	touch(t, root, "web/studio/magusfile.tl", "")

	ws, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	p := ws.Get("web/studio")
	if p == nil {
		t.Fatal("web/studio not discovered")
	}
	if p.Path != "web/studio" {
		t.Fatalf("Path = %q, want %q", p.Path, "web/studio")
	}
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
	if err != nil {
		t.Fatal(err)
	}
	matches := ws.UnderPath("extensions/")
	got := make([]string, 0, len(matches))
	for _, p := range matches {
		got = append(got, p.Path)
	}
	slices.Sort(got)
	want := []string{"extensions/a", "extensions/b"}
	if !slices.Equal(got, want) {
		t.Fatalf("UnderPath = %v, want %v", got, want)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".", "tools/modern"}
	got := make([]string, 0, len(ws.Projects))
	for path := range ws.Projects {
		got = append(got, path)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("Projects = %v, want %v", got, want)
	}
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
	if err := os.MkdirAll(filepath.Join(root, "tools/empty/magusfiles"), 0o755); err != nil {
		t.Fatal(err)
	}

	ws, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".", "tools/modern"}
	got := make([]string, 0, len(ws.Projects))
	for path := range ws.Projects {
		got = append(got, path)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("Projects = %v, want %v", got, want)
	}
}

// TestResolvedSpells verifies ResolvedSpells() compiles (BUG 2: Target param removed).
func TestResolvedSpells(t *testing.T) {
	t.Parallel()
	p := &types.Project{Path: "x"}
	if got := p.ResolvedSpells; got != nil {
		t.Errorf("ResolvedSpells() = %v, want nil before bind", got)
	}
}

// TestRegisterSpellNilPanics verifies Register(nil) panics.
func TestRegisterSpellNilPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("Register(nil) did not panic")
		}
	}()
	DefaultSpellRegistry().RegisterSpell(nil)
}
