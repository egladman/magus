package asciistrings

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{Files: []string{"ui/ui.go"}})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "ui")
}

func TestNewRejectsMalformedGlob(t *testing.T) {
	if _, err := New(Options{Files: []string{"[bad"}}); err == nil {
		t.Fatal("expected a malformed glob to fail at construction")
	}
}
