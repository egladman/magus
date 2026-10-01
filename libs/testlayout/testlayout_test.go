package testlayout

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer covers the default rule: a test file in an external test package is
// reported, and nothing about names is, since pairing belongs to testpair.
//
// concurrency_test.go is the external package (package layout_test). Its pass holds
// no source files at all, which is the shape the rule has to reach.
func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "layout")
}

// TestAnalyzerReportUnixSuffix checks that a _unix file name is reported whether it
// holds tests or source, with the stem the source carries in the suggested names.
func TestAnalyzerReportUnixSuffix(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), New(Options{ReportUnixSuffix: true}), "unixsuffix")
}

// TestAnalyzerReportMainTests checks the opt-in option: a _test.go in package
// main is reported, and the same analyzer run over layout (which holds no main or
// main_test package files) reports exactly what the default analyzer already
// does, proving the option adds nothing there.
func TestAnalyzerReportMainTests(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), New(Options{ReportMainTests: true}), "mainpkg", "layout")
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

// TestUnixSuffixed pins which names carry a unix segment: one followed only by
// other build suffixes counts, and a stem that merely is the word does not.
func TestUnixSuffixed(t *testing.T) {
	cases := map[string]bool{
		"relay_unix.go":            true,
		"relay_unix_test.go":       true,
		"relay_unix_amd64_test.go": true,
		"unix.go":                  false,
		"unix_test.go":             false,
		"relay_unix_cache.go":      false,
		"relay_linux.go":           false,
	}

	for name, want := range cases {
		if got := unixSuffixed(name); got != want {
			t.Errorf("unixSuffixed(%q) = %v, want %v", name, got, want)
		}
	}
}
