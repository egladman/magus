package source

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"
)

// TestRootFromNestedModule resolves the enclosing module from inside a nested
// one, which is where golangci-lint runs a lib module.
func TestRootFromNestedModule(t *testing.T) {
	root := sourcetest.Module(t, "example.com/m", "cmd/app/main.go")
	inner := filepath.Join(root, "libs", "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "go.mod"), []byte("module example.com/m/libs/inner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(inner)

	got, err := Root("x", "example.com/m")
	if err != nil {
		t.Fatal(err)
	}
	if mustEval(t, got) != mustEval(t, root) {
		t.Fatalf("Root = %s, want %s", got, root)
	}
	if _, err := Root("x", "example.com/other"); err == nil || !strings.Contains(err.Error(), `module "example.com/other"`) {
		t.Fatalf("an undeclared module must fail naming it, got %v", err)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGlobsRequireMatches(t *testing.T) {
	root := sourcetest.Module(t, "example.com/m", "internal/guard/guard.go")
	if err := (Globs{"internal/guard/*.go"}).RequireMatches("lint", "files", root); err != nil {
		t.Fatal(err)
	}
	err := (Globs{"internal/guard/*.go", "internal/gaurd/*.go"}).RequireMatches("lint", "files", root)
	if err == nil || !strings.Contains(err.Error(), `lint: files pattern "internal/gaurd/*.go" matches no file`) {
		t.Fatalf("a dead pattern must fail naming the linter, setting and pattern, got %v", err)
	}
}

// TestGoFiles skips what the go tool never builds from: dot and underscore
// names, and testdata. A nested module is still part of the tree.
func TestGoFiles(t *testing.T) {
	root := sourcetest.Module(t, "example.com/m",
		"root.go", "cmd/app/main.go", "libs/inner/inner.go", "cmd/app/gen/gen.go",
		".cache/x/x.go", "_old/old.go", "cmd/app/_skip.go", "cmd/app/testdata/src/p/p.go")
	got, err := GoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cmd/app/gen/gen.go", "cmd/app/main.go", "libs/inner/inner.go", "root.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("GoFiles = %v, want %v", got, want)
	}
}

func TestRequireDirNames(t *testing.T) {
	files := []string{"cmd/app/gen/gen.go", "internal/docs/docs.go", "root.go"}
	if err := RequireDirNames("lint", "skip-dirs", "/r", []string{"gen", "docs", "cmd"}, files); err != nil {
		t.Fatal(err)
	}
	err := RequireDirNames("lint", "skip-dirs", "/r", []string{"gen", "node_modules"}, files)
	if err == nil || !strings.Contains(err.Error(), `lint: skip-dirs entry "node_modules" names no directory holding Go files`) {
		t.Fatalf("a skip entry matching nothing must fail naming it, got %v", err)
	}
	// A file name is not a directory, so root.go does not make "root.go" live.
	if err := RequireDirNames("lint", "skip-dirs", "/r", []string{"root.go"}, files); err == nil {
		t.Fatal("a file name must not count as a directory")
	}
}

func TestRequireDirs(t *testing.T) {
	files := []string{"internal/queue/provider/host.go", "cmd/app/main.go"}
	if err := RequireDirs("lint", "dirs", "/r", []string{"internal/queue", "cmd/app"}, files); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"internal/que", "internal/job", "cmd/app/main.go"} {
		err := RequireDirs("lint", "dirs", "/r", []string{dir}, files)
		if err == nil || !strings.Contains(err.Error(), `lint: dirs entry "`+dir+`" holds no Go files`) {
			t.Errorf("%s: got %v", dir, err)
		}
	}
}

func TestHint(t *testing.T) {
	if got := Hint("base", ""); got != "base" {
		t.Errorf("no hint: got %q", got)
	}
	if got := Hint("base", "see docs/x.md"); got != "base; see docs/x.md" {
		t.Errorf("hint: got %q", got)
	}
}

func TestRequirePackage(t *testing.T) {
	root := sourcetest.Module(t, "example.com/m", "cmd/app/main.go", "root.go")
	for _, pkg := range []string{"example.com/m", "example.com/m/cmd/app"} {
		if err := RequirePackage("lint", "package", root, "example.com/m", pkg); err != nil {
			t.Errorf("%s: %v", pkg, err)
		}
	}
	for pkg, want := range map[string]string{
		"example.com/m/cmd/gone": `lint: package "example.com/m/cmd/gone" has no Go files`,
		"example.com/elsewhere":  `lint: package "example.com/elsewhere" is outside module example.com/m`,
	} {
		if err := RequirePackage("lint", "package", root, "example.com/m", pkg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", pkg, err, want)
		}
	}
}

// TestInModuleSkipsWithoutModule keeps analysistest runs, which have no module,
// free of the check.
func TestInModuleSkipsWithoutModule(t *testing.T) {
	called := false
	if err := InModule("lint", "", func(string) error { called = true; return nil }); err != nil || called {
		t.Fatalf("an empty module must skip the check: err=%v called=%v", err, called)
	}
}
