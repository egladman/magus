package asciistrings

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"
	"golang.org/x/tools/go/analysis/analysistest"
)

// TestNewRejectsDeadScope fails at load when a listed file no longer exists.
func TestNewRejectsDeadScope(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "types/describe.go")
	opts := Options{Module: "example.com/m", Files: []string{"types/describe.go"}}
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	opts.Files = append(opts.Files, "types/gone.go")
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `asciistrings: files pattern "types/gone.go"`) {
		t.Fatalf("want an error naming the dead pattern, got %v", err)
	}
}

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
