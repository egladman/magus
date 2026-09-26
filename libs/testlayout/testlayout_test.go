package testlayout

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer covers the default rule in one pass over the sprawl package: the
// narrowed files are reported, and the shapes that must not be stay silent: the
// paired file, the conventional export and benchmark names, a build-tag suffix,
// a source file carrying the same suffix as its test, and a cross-cutting concern
// that pairs with nothing.
//
// concurrency_test.go is deliberately an external test package (package
// sprawl_test). That pass holds no source files at all, so the rule only reaches
// the right answer because the sibling listing is read off disk.
func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "sprawl")
}

// TestAnalyzerUnpaired checks the opt-in half: the cross-cutting shape sprawl
// leaves alone is reported once Unpaired is set, and a narrowing name still gets
// the narrowing message rather than the weaker unpaired one.
func TestAnalyzerUnpaired(t *testing.T) {
	analyzer, err := New(Options{Unpaired: true})
	if err != nil {
		t.Fatal(err)
	}

	analysistest.Run(t, analysistest.TestData(), analyzer, "unpaired")
}

// TestAnalyzerCrossCutting checks the exits from the Unpaired rule: a marker with a
// reason above the package clause, and a platform family standing in for the missing
// source file. An empty reason, a marker below the package clause, and a marked file
// that narrows a source name are all still reported.
func TestAnalyzerCrossCutting(t *testing.T) {
	analyzer, err := New(Options{Unpaired: true})
	if err != nil {
		t.Fatal(err)
	}

	analysistest.Run(t, analysistest.TestData(), analyzer, "crosscutting")
}

// TestAnalyzerStrict checks the three options a tree adds on top of Unpaired: the
// marker stops excusing a file, benchmark files lose their exemption, and a _unix
// file name is reported whether it holds tests or source. Allow is then the one
// exit left, which conventions_test.go takes.
func TestAnalyzerStrict(t *testing.T) {
	analyzer, err := New(Options{
		Allow:          []string{"conventions_test.go"},
		Unpaired:       true,
		IgnoreMarker:   true,
		PairBenchmarks: true,
		NoUnixSuffix:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	analysistest.Run(t, analysistest.TestData(), analyzer, "strict")
}

// TestAnalyzerAllow checks that a glob excuses a file the default rule reports:
// widget_conformance_test.go narrows widget.go exactly as resolver_edge_cases
// narrows resolver.
func TestAnalyzerAllow(t *testing.T) {
	analyzer, err := New(Options{Allow: []string{"*_conformance_test.go"}})
	if err != nil {
		t.Fatal(err)
	}

	analysistest.Run(t, analysistest.TestData(), analyzer, "allowed")
}

// TestNewRejectsMalformedGlob pins the reason New validates at construction: the
// patterns are otherwise reached once per test file per package, so a typo would
// lint clean until it met a package containing a non-exempt test file and then
// fail the run from somewhere unrelated to the mistake.
func TestNewRejectsMalformedGlob(t *testing.T) {
	_, err := New(Options{Allow: []string{"*_ok_test.go", "[bad"}})
	if err == nil {
		t.Fatal("expected an error for a malformed glob")
	}

	if !strings.Contains(err.Error(), "[bad") {
		t.Errorf("error should name the offending pattern, got: %v", err)
	}
}
