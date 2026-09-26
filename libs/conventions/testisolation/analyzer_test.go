package testisolation

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer covers reach through an intermediate import, a TestMain in the
// external test package serving the internal one, a binary that links nothing,
// and an external test package that alone brings the link in.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{Package: "sockdir", Calls: []string{"testkit.Main", "testkit.Isolated"}})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "leaky", "guarded", "pure", "extonly")
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Options{Package: "sockdir"}); err == nil {
		t.Error("expected no calls to fail at construction")
	}
	if _, err := New(Options{Package: "sockdir", Calls: []string{"Main"}}); err == nil {
		t.Error("expected an unqualified call to fail at construction")
	}
}
