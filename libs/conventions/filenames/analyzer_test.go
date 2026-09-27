package filenames

import (
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer builds the vocabulary from a module on disk and checks the
// testdata against it. gate is known only from gate_linux.go, so suffixes are
// stripped on both sides; app/gen is skipped by directory.
func TestAnalyzer(t *testing.T) {
	testdata := analysistest.TestData()
	sourcetest.Module(t, "example.com/m", "push/push.go", "gate/gate_linux.go", "time/time.go")
	analyzer, err := New(Options{
		Module:   "example.com/m",
		SkipDirs: []string{"gen"},
		Suffixes: []string{"test", "linux"},
	})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, testdata, analyzer, "app", "app/gen")
}

func TestNewRequiresModule(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected an empty module to fail at construction")
	}
}
