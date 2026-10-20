package testloops

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	analysischecker "golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"
)

func options() Options {
	return Options{Markers: []string{"SetupProcessGroup", "Setpgid"}, Hint: "use testloops hint"}
}

// TestAnalyzer covers each endless shape (colon, true, until false, the
// arithmetic for), a loop nested in a tied one, a check outside the loop, and
// the reap tests before and after commit 1f388c568 tied them to the test.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(options())
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "flagged", "clean", "unmarked")
}

func TestNewRequiresMarkers(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Error("expected no markers to fail at construction")
	}
}

func TestUntied(t *testing.T) {
	for name, tc := range map[string]struct {
		script string
		want   []string
	}{
		"colon":            {`while :; do x; done`, []string{"while :"}},
		"true":             {"while true\ndo x; done", []string{"while true"}},
		"until":            {`until false; do x; done`, []string{"until false"}},
		"arithmetic":       {`for ((;;)); do x; done`, []string{"for ((;;))"}},
		"spaced arith":     {`for (( ; ; )); do x; done`, []string{"for (( ; ; ))"}},
		"do on the header": {`while : do x; done`, []string{"while :"}},
		"tied in body":     {`while :; do kill -0 $P || exit; done`, nil},
		"tied in cond":     {`while kill -0 $P; do x; done`, nil},
		"nested tied":      {`while :; do for i in 1; do kill -0 $P; done; done`, nil},
		"sibling untied":   {`while :; do kill -0 $P || exit; done; while :; do x; done`, []string{"while :"}},
		"no loop":          {`sleep 1`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := untied(tc.script)
			if len(got) != len(tc.want) {
				t.Fatalf("untied(%q) = %q, want %q", tc.script, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("untied(%q)[%d] = %q, want %q", tc.script, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestSpawnTestsEndTheirLoops runs the analyzer with the root .golangci.yml
// settings over internal/proc/spawn, whose reap tests orphaned shell loops, so
// an untied loop fails here as well as in `magus run lint`.
func TestSpawnTestsEndTheirLoops(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks internal/proc/spawn and its dependencies")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "proc", "spawn")); err != nil {
		t.Skipf("no enclosing magus checkout: %v", err)
	}
	analyzer, err := New(Options{Markers: []string{"Setpgid", "SetupProcessGroup"}})
	if err != nil {
		t.Fatal(err)
	}
	// The root module does not type-check without the experiment its build sets.
	pkgs, err := packages.Load(&packages.Config{
		Mode:  packages.LoadAllSyntax,
		Dir:   root,
		Tests: true,
		Env:   append(os.Environ(), "GOEXPERIMENT=jsonv2"),
	}, "./internal/proc/spawn")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("internal/proc/spawn did not type-check")
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
