package hostvocab

import (
	"testing"

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

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Options{Files: []string{"[bad"}, Words: []string{"Read"}}); err == nil {
		t.Error("expected a malformed glob to fail at construction")
	}
	if _, err := New(Options{Files: []string{"guard/*.go"}}); err == nil {
		t.Error("expected an empty word list to fail at construction")
	}
}
