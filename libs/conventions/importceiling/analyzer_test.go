package importceiling

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer counts distinct imports across files, ignores the test file's,
// and leaves a package at its ceiling alone.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{Rules: []Rule{
		{Package: "app", Prefix: "engine/", Max: 2},
		{Package: "worker", Prefix: "engine/", Max: 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "app", "worker")
}

func TestNewRejectsIncompleteRule(t *testing.T) {
	if _, err := New(Options{Rules: []Rule{{Package: "app", Max: 2}}}); err == nil {
		t.Fatal("expected a rule with no prefix to fail at construction")
	}
}
