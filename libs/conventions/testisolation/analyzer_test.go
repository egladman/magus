package testisolation

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"

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

// TestNewRejectsMovedPackage fails at load when the guarded package moved, so
// no binary could ever be found to link it.
func TestNewRejectsMovedPackage(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "internal/proc/sockdir/sockdir.go")
	opts := Options{Module: "example.com/m", Package: "example.com/m/internal/proc/sockdir", Calls: []string{"testkit.Main"}}
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	opts.Package = "example.com/m/internal/sockdir"
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `testisolation: package "example.com/m/internal/sockdir" has no Go files`) {
		t.Fatalf("want an error naming the moved package, got %v", err)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Options{Package: "sockdir"}); err == nil {
		t.Error("expected no calls to fail at construction")
	}
	if _, err := New(Options{Package: "sockdir", Calls: []string{"Main"}}); err == nil {
		t.Error("expected an unqualified call to fail at construction")
	}
}
