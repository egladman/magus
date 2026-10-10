package globalrestore

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
	analysischecker "golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"
)

// TestCmdMagusRestoresItsGlobals runs the analyzer with the root .golangci.yml
// settings over cmd/magus and its tests, so a test that assigns global or
// globalCfg without a restore fails here as well as in `magus run lint`.
func TestCmdMagusRestoresItsGlobals(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks cmd/magus and its dependencies")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cmd", "magus")); err != nil {
		t.Skipf("no enclosing magus checkout: %v", err)
	}
	t.Chdir(root)
	analyzer, err := New(Options{
		Module:  "github.com/egladman/magus",
		Package: "github.com/egladman/magus/cmd/magus",
		Vars:    []string{"global", "globalCfg"},
		Hint:    "t.Cleanup(snapshotGlobals())",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The root module does not type-check without the experiment its build sets.
	pkgs, err := packages.Load(&packages.Config{
		Mode:  packages.LoadAllSyntax,
		Dir:   root,
		Tests: true,
		Env:   append(os.Environ(), "GOEXPERIMENT=jsonv2"),
	}, "./cmd/magus")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("cmd/magus did not type-check")
	}
	graph, err := analysischecker.Analyze([]*analysis.Analyzer{analyzer}, pkgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, act := range graph.Roots {
		if act.Err != nil {
			t.Fatal(act.Err)
		}
		for _, d := range act.Diagnostics {
			t.Errorf("%s: %s", act.Package.Fset.Position(d.Pos), d.Message)
		}
	}
}
