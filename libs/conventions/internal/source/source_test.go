package source

import (
	"os"
	"path/filepath"
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

func TestGlobsCheck(t *testing.T) {
	root := sourcetest.Module(t, "example.com/m", "internal/guard/guard.go")
	if err := (Globs{"internal/guard/*.go"}).Check("lint", "files", root); err != nil {
		t.Fatal(err)
	}
	err := (Globs{"internal/guard/*.go", "internal/gaurd/*.go"}).Check("lint", "files", root)
	if err == nil || !strings.Contains(err.Error(), `lint: files pattern "internal/gaurd/*.go" matches no file`) {
		t.Fatalf("a dead pattern must fail naming the linter, setting and pattern, got %v", err)
	}
}

func TestCheckPackage(t *testing.T) {
	root := sourcetest.Module(t, "example.com/m", "cmd/app/main.go", "root.go")
	for _, pkg := range []string{"example.com/m", "example.com/m/cmd/app"} {
		if err := CheckPackage("lint", "package", root, "example.com/m", pkg); err != nil {
			t.Errorf("%s: %v", pkg, err)
		}
	}
	for pkg, want := range map[string]string{
		"example.com/m/cmd/gone": `lint: package "example.com/m/cmd/gone" has no Go files`,
		"example.com/elsewhere":  `lint: package "example.com/elsewhere" is outside module example.com/m`,
	} {
		if err := CheckPackage("lint", "package", root, "example.com/m", pkg); err == nil || !strings.Contains(err.Error(), want) {
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
