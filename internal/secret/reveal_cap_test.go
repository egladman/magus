package secret

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// revealCeiling is how many Reveal calls production code makes. Each is a place a
// credential stops protecting itself. Lower it when a call goes away; raising it is a
// review question: could the Secret travel further before it is revealed?
const revealCeiling = 35

// TestRevealCallsStayCapped counts Reveal calls in the root module's non-test files.
func TestRevealCallsStayCapped(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the module root is not two levels up: %v", err)
	}
	var sites []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules" || isModule(path)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 0 {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Reveal" {
				sites = append(sites, fset.Position(call.Pos()).String())
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) > revealCeiling {
		t.Fatalf("%d production Reveal calls, over the ceiling of %d; keep the Secret until the line that hands it out:\n%s",
			len(sites), revealCeiling, strings.Join(sites, "\n"))
	}
	if len(sites) < revealCeiling {
		t.Fatalf("%d production Reveal calls, under the ceiling of %d: lower revealCeiling to %d", len(sites), revealCeiling, len(sites))
	}
}

// isModule reports whether dir holds its own go.mod, which makes it another module.
func isModule(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil
}
