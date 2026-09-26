package stutter

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{MinPackage: 3, Allow: []string{"config"}})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "sessions", "db", "config")
}

func TestNewRejectsZeroMinPackage(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected min-package 0 to fail at construction")
	}
}
