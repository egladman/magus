package fieldwise

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer runs the zero Options over complete, where only a value asserted
// on every field it declares is reported, and each value changed between its
// assertions is not.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "complete")
}

// TestReportPartial runs over results, whose test files pin each shape
// reported and each one left alone; external_test.go is the same types seen
// from outside the package. Result has more fields than any test asserts.
func TestReportPartial(t *testing.T) {
	analyzer, err := New(Options{ReportPartial: true})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "results")
}
