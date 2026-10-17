package benchbeside

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer flags an optimization: comment and a cache type with methods
// where no test holds a benchmark. benched has a benchmark, exempt is named in
// Exempt, and quiet has a cache type with no methods and the marker only
// mid-sentence.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{Exempt: map[string]string{"exempt": "test fixture: the package this test exempts"}})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "comment", "cache", "benched", "exempt", "quiet")
}

func writeTest(t *testing.T, root, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("package pkg\n\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestNewChecksExempt accepts an entry whose package exists and whose tests
// hold no benchmark.
func TestNewChecksExempt(t *testing.T) {
	root := sourcetest.Module(t, "example.com/m", "pkg/pkg.go")
	writeTest(t, root, "pkg/pkg_test.go", "import \"testing\"\n\nfunc TestPkg(t *testing.T) {}\n")
	if _, err := New(Options{Module: "example.com/m", Exempt: map[string]string{"pkg": "no hot path"}}); err != nil {
		t.Fatal(err)
	}
}

// TestNewRejectsMovedPackage fails at load on an entry naming a package with
// no Go files: the code moved and the entry exempts nothing.
func TestNewRejectsMovedPackage(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "pkg/pkg.go")
	opts := Options{Module: "example.com/m", Exempt: map[string]string{"moved": "no hot path"}}
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `benchbeside: exempt "example.com/m/moved" has no Go files`) {
		t.Fatalf("want an error naming the moved package, got %v", err)
	}
}

// TestNewRejectsStaleExemption fails at load once an exempt package's tests
// hold a func Benchmark: the list only shrinks.
func TestNewRejectsStaleExemption(t *testing.T) {
	root := sourcetest.Module(t, "example.com/m", "pkg/pkg.go")
	writeTest(t, root, "pkg/pkg_test.go", "import \"testing\"\n\nfunc BenchmarkPkg(b *testing.B) {}\n")
	opts := Options{Module: "example.com/m", Exempt: map[string]string{"pkg": "no hot path"}}
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `benchbeside: exempt entry "pkg": its tests now hold a func Benchmark`) {
		t.Fatalf("want an error naming the stale entry, got %v", err)
	}
}

// TestNewRejectsUnreasonedExemption fails at load on an entry with no reason:
// an exemption that does not say why is one nobody revisits.
func TestNewRejectsUnreasonedExemption(t *testing.T) {
	_, err := New(Options{Exempt: map[string]string{"pkg": ""}})
	if err == nil || !strings.Contains(err.Error(), `benchbeside: exempt entry "pkg" needs a reason`) {
		t.Fatalf("want an error naming the missing reason, got %v", err)
	}
}
