package ruletext

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{Files: []string{"cli/shell.go"}, Prefix: "magus workspace:"})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "cli")
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Options{Files: []string{"[bad"}, Prefix: "x"}); err == nil {
		t.Error("expected a malformed glob to fail at construction")
	}
	if _, err := New(Options{Files: []string{"cli/shell.go"}}); err == nil {
		t.Error("expected an empty prefix to fail at construction")
	}
}
