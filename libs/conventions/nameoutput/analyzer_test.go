package nameoutput

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"

	"golang.org/x/tools/go/analysis/analysistest"
)

func options(pkg string) Options {
	return Options{Package: pkg, Case: "outputName", Emitters: []string{"emitNames", "emitNamesOf", "outputDst"}}
}

// TestAnalyzer reports the arm that prints directly and passes the one that
// delegates to a helper reaching emitNames, which only the closure knows.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(options("cli"))
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "cli")
}

// TestAnalyzerRenamed keeps the rule from going quiet when the constant it
// looks for no longer exists.
func TestAnalyzerRenamed(t *testing.T) {
	analyzer, err := New(options("renamed"))
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "renamed")
}

// TestNewRejectsMovedPackage fails at load when the package it guards moved.
func TestNewRejectsMovedPackage(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "cmd/magus/main.go")
	opts := options("example.com/m/cmd/magus")
	opts.Module = "example.com/m"
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	opts.Package = "example.com/m/cmd/mgs"
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `nameoutput: package "example.com/m/cmd/mgs" has no Go files`) {
		t.Fatalf("want an error naming the moved package, got %v", err)
	}
}

func TestNewRejectsMissingField(t *testing.T) {
	if _, err := New(Options{Package: "cli", Case: "outputName"}); err == nil {
		t.Fatal("expected no emitters to fail at construction")
	}
}
