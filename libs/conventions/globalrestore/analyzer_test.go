package globalrestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
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

const stateSource = `package state

type Config struct {
	Quiet bool
	Level int
}

var (
	Mode  string
	Cfg   Config
	Table = map[string]int{}
	Other int
)

func snapshot() func() {
	savedMode, savedCfg := Mode, Cfg
	return func() { Mode, Cfg = savedMode, savedCfg }
}

func peek() string { return Mode }
`

const stateTests = `package state

import "testing"

func TestLeaksAssignment(t *testing.T) {
	Mode = "quiet" // want "TestLeaksAssignment assigns Mode, process-global state.*; use snapshot$"
	Mode = "loud"
}

func TestLeaksField(t *testing.T) {
	Cfg.Quiet = true // want "TestLeaksField assigns Cfg"
}

func TestLeaksElement(t *testing.T) {
	Table["k"] = 1 // want "TestLeaksElement assigns Table"
}

func TestLeaksIncrement(t *testing.T) {
	Cfg.Level++ // want "TestLeaksIncrement assigns Cfg"
}

func setQuiet(t *testing.T) {
	t.Helper()
	Cfg.Quiet = true // want "setQuiet assigns Cfg"
}

func setLoud(t *testing.T) {
	t.Helper()
	t.Cleanup(snapshot())
	Cfg.Quiet = false
}

func TestHelpers(t *testing.T) {
	setLoud(t)
	setQuiet(t)
}

func TestSnapshotHelperRestores(t *testing.T) {
	t.Cleanup(snapshot())
	Mode = "quiet"
	Cfg.Quiet = true
}

func TestInlineCleanupRestores(t *testing.T) {
	prev := Cfg
	t.Cleanup(func() { Cfg = prev })
	Cfg.Quiet = true
}

func TestFieldCleanupRestores(t *testing.T) {
	prev := Cfg.Quiet
	t.Cleanup(func() { Cfg.Quiet = prev })
	Cfg.Quiet = true
}

func TestDeferRestores(t *testing.T) {
	prev := Mode
	defer func() { Mode = prev }()
	Mode = "quiet"
}

func TestDeferredSnapshotRestores(t *testing.T) {
	defer snapshot()()
	Mode = "quiet"
}

func TestUnrelatedCleanupDoesNotRestore(t *testing.T) {
	t.Cleanup(func() { _ = peek() })
	Mode = "quiet" // want "TestUnrelatedCleanupDoesNotRestore assigns Mode"
}

func TestUnconfiguredIgnored(t *testing.T) {
	Other = 1
	local := Config{}
	local.Quiet = true
	_ = local
}

func TestShadowIgnored(t *testing.T) {
	Mode := "local"
	Mode = "other"
	_ = Mode
}

func TestOuterCleanupCoversSubtests(t *testing.T) {
	t.Cleanup(snapshot())
	t.Run("a", func(t *testing.T) { Mode = "a" })
}

func TestSubtestLeaks(t *testing.T) {
	t.Run("a", func(t *testing.T) {
		Mode = "a" // want "TestSubtestLeaks subtest assigns Mode"
	})
	t.Run("b", func(t *testing.T) {
		t.Cleanup(snapshot())
		Mode = "b"
	})
}

func BenchmarkLeaks(b *testing.B) {
	Mode = "bench" // want "BenchmarkLeaks assigns Mode"
}

func FuzzLeaks(f *testing.F) {
	Mode = "fuzz" // want "FuzzLeaks assigns Mode"
}

func resetWithoutTesting() {
	Mode = ""
}
`

func options() Options {
	return Options{Package: "state", Vars: []string{"Mode", "Cfg", "Table"}, Hint: "use snapshot"}
}

// TestAnalyzer covers a bare, field, element and increment assignment, a
// helper taking a *testing.T, subtest closures, and each restore shape: a
// snapshot helper through t.Cleanup or defer, and an inline closure.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(options())
	if err != nil {
		t.Fatal(err)
	}
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"state/state.go":      stateSource,
		"state/state_test.go": stateTests,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	analysistest.Run(t, dir, analyzer, "state")
}

func TestNewRejectsRenamedVar(t *testing.T) {
	root := sourcetest.Module(t, "example.com/m", "cmd/app/main.go")
	if err := os.WriteFile(filepath.Join(root, "cmd", "app", "main.go"), []byte("package app\n\nvar global int\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := Options{Module: "example.com/m", Package: "example.com/m/cmd/app", Vars: []string{"global"}}
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	opts.Vars = []string{"global", "globalCfg"}
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `globalrestore: vars entry "globalCfg" is no package-level var`) {
		t.Fatalf("want an error naming the missing var, got %v", err)
	}
}

func TestNewRejectsMovedPackage(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "cmd/app/main.go")
	opts := Options{Module: "example.com/m", Package: "example.com/m/cmd/mgs", Vars: []string{"global"}}
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `globalrestore: package "example.com/m/cmd/mgs" has no Go files`) {
		t.Fatalf("want an error naming the moved package, got %v", err)
	}
}

func TestNewRejectsMissingField(t *testing.T) {
	if _, err := New(Options{Package: "state"}); err == nil {
		t.Error("expected no vars to fail at construction")
	}
	if _, err := New(Options{Vars: []string{"Mode"}}); err == nil {
		t.Error("expected no package to fail at construction")
	}
}
