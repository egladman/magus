package importceiling

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"

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

// TestNewRejectsMovedPackage fails at load when a ratcheted package moved.
func TestNewRejectsMovedPackage(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "cmd/magus/main.go")
	opts := Options{Module: "example.com/m", Rules: []Rule{{Package: "example.com/m/cmd/magus", Prefix: "example.com/m/internal/", Max: 1}}}
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	opts.Rules[0].Package = "example.com/m/cmd/mgs"
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `importceiling: rules.package "example.com/m/cmd/mgs" has no Go files`) {
		t.Fatalf("want an error naming the moved package, got %v", err)
	}
}

func TestNewRejectsIncompleteRule(t *testing.T) {
	if _, err := New(Options{Rules: []Rule{{Package: "app", Max: 2}}}); err == nil {
		t.Fatal("expected a rule with no prefix to fail at construction")
	}
}
