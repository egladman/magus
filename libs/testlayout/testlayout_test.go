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

// TestAnalyzerReportUnpaired checks the opt-in half: the cross-cutting shape
// sprawl leaves alone is reported once ReportUnpaired is set, and a narrowing name
// still gets the narrowing message rather than the weaker unpaired one.
func TestAnalyzerReportUnpaired(t *testing.T) {
	analyzer, err := New(Options{ReportUnpaired: true})
	if err != nil {
		t.Fatal(err)
	}

	analysistest.Run(t, analysistest.TestData(), analyzer, "unpaired")
}

// TestAnalyzerHonorMarker checks the exits from the ReportUnpaired rule once the
// marker is honored: a marker with a reason above the package clause, and a
// platform family standing in for the missing source file. An empty reason, a
// marker below the package clause, and a marked file that narrows a source name
// are all still reported.
func TestAnalyzerHonorMarker(t *testing.T) {
	analyzer, err := New(Options{ReportUnpaired: true, HonorMarker: true})
	if err != nil {
		t.Fatal(err)
	}

	analysistest.Run(t, analysistest.TestData(), analyzer, "crosscutting")
}

// TestAnalyzerStrict checks what a tree adds on top of ReportUnpaired: the marker,
// left unhonored, excuses nothing, benchmark files lose their exemption, and a
// _unix file name is reported whether it holds tests or source. Allow is then the
// one exit left, which conventions_test.go takes.
func TestAnalyzerStrict(t *testing.T) {
	analyzer, err := New(Options{
		Allow:            []string{"conventions_test.go"},
		ReportUnpaired:   true,
		PairBenchmarks:   true,
		ReportUnixSuffix: true,
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

// TestAnalyzerReportMainTests checks the opt-in option: a _test.go in package
// main is reported, and the same analyzer run over sprawl (which holds no main or
// main_test package files) reports exactly what the default analyzer already
// does, proving the option adds nothing there.
func TestAnalyzerReportMainTests(t *testing.T) {
	analyzer, err := New(Options{ReportMainTests: true})
	if err != nil {
		t.Fatal(err)
	}

	analysistest.Run(t, analysistest.TestData(), analyzer, "mainpkg", "sprawl")
}

// TestIsMainTestPackage pins the exact package names [Options.ReportMainTests]
// targets: the binary's own package and its external test variant, and no
// other.
func TestIsMainTestPackage(t *testing.T) {
	cases := map[string]bool{
		"main":      true,
		"main_test": true,
		"widget":    false,
	}

	for name, want := range cases {
		if got := isMainTestPackage(name); got != want {
			t.Errorf("isMainTestPackage(%q) = %v, want %v", name, got, want)
		}
	}
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
