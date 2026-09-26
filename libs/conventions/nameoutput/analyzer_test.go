package nameoutput

import (
	"testing"

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

func TestNewRejectsMissingField(t *testing.T) {
	if _, err := New(Options{Package: "cli", Case: "outputName"}); err == nil {
		t.Fatal("expected no emitters to fail at construction")
	}
}
