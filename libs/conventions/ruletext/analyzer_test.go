package ruletext

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"
	"golang.org/x/tools/go/analysis/analysistest"
)

// TestNewRejectsDeadScope fails at load when the file it guards has moved.
func TestNewRejectsDeadScope(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "cmd/magus/shell.go")
	opts := Options{Module: "example.com/m", Files: []string{"cmd/magus/shell.go"}, Prefix: "magus workspace:"}
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	opts.Files = []string{"cmd/magus/sh.go"}
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `ruletext: files pattern "cmd/magus/sh.go"`) {
		t.Fatalf("want an error naming the dead pattern, got %v", err)
	}
}

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
