package hostvocab

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{
		Files: []string{"guard/*.go"},
		Words: []string{"Read", "Write", "Bash", "Task"},
	})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "guard", "graph")
}

// TestNewRejectsDeadScope fails at load when a files pattern matches nothing:
// the guard moved, and the rule would otherwise stop looking without a word.
func TestNewRejectsDeadScope(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "internal/guard/guard.go")
	opts := Options{Module: "example.com/m", Files: []string{"internal/guard/*.go"}, Words: []string{"Read"}}
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	opts.Files = append(opts.Files, "internal/gaurd/*.go")
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `hostvocab: files pattern "internal/gaurd/*.go"`) {
		t.Fatalf("want an error naming the dead pattern, got %v", err)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Options{Files: []string{"[bad"}, Words: []string{"Read"}}); err == nil {
		t.Error("expected a malformed glob to fail at construction")
	}
	if _, err := New(Options{Files: []string{"guard/*.go"}}); err == nil {
		t.Error("expected an empty word list to fail at construction")
	}
}
