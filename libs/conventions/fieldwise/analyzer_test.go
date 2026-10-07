package fieldwise

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer runs the zero Options over complete, where only a value asserted
// on every field it declares is reported.
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

// TestMinFields raises the floor to three: two fields of one value pass, three
// are reported, with the hint appended.
func TestMinFields(t *testing.T) {
	analyzer, err := New(Options{MinFields: 3, ReportPartial: true, Hint: "results are compared whole here"})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "threefields")
}

func TestNewRejectsOneField(t *testing.T) {
	if _, err := New(Options{MinFields: 1}); err == nil {
		t.Fatal("expected min-fields 1 to fail at construction")
	}
}
