package stutter

import (
	"fmt"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer runs the zero Options: config's package clause says settings,
// and db is below the default minimum length.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "sessions", "db", "config")
}

// recorder collects what analysistest would fail on, for runs asserting that
// nothing is reported.
type recorder struct{ errs []string }

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// TestAnalyzerSilenced covers the two settings that turn a package off: Allow
// names the package clause, and MinPackageLen raises the floor past it.
func TestAnalyzerSilenced(t *testing.T) {
	for name, opts := range map[string]Options{
		"allow":           {Allow: []string{"sessions", "settings"}},
		"min-package-len": {MinPackageLen: 9},
	} {
		t.Run(name, func(t *testing.T) {
			analyzer, err := New(opts)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range analysistest.Run(&recorder{}, analysistest.TestData(), analyzer, "sessions", "config") {
				if len(r.Diagnostics) > 0 {
					t.Errorf("%s: reported %d names, want none", r.Pass.Pkg.Name(), len(r.Diagnostics))
				}
			}
		})
	}
}

func TestNewRejectsNegativeMinPackageLen(t *testing.T) {
	if _, err := New(Options{MinPackageLen: -1}); err == nil {
		t.Fatal("expected a negative min-package-len to fail at construction")
	}
}
