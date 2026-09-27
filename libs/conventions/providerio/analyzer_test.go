package providerio

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"
	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer governs only "queue": oci.go carries the same construction outside
// it and reports nothing, allowed.go is exempted by name, sdk.go's import fires
// regardless of Dirs, and blank/blank.go proves a blank import does not panic.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{
		Dirs:            []string{"queue"},
		Allow:           []AllowEntry{{File: "queue/allowed.go", Reason: "test fixture: the file this test's Allow entry names"}},
		ProviderImports: []string{"go-github", "go-gitlab"},
		Hint:            "provider I/O lives in scripts",
	})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "queue", "queue/blank", "oci", "sdk")
}

// TestNewRejectsNoDirs fails at construction with an empty Dirs: a rule that
// governs nothing would silently pass every file forever.
func TestNewRejectsNoDirs(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected an error for empty Dirs")
	}
}

// TestNewRejectsUnreasonedAllow fails at construction on an allow entry with no
// reason: an allowlist that does not have to say why is one nobody reads.
func TestNewRejectsUnreasonedAllow(t *testing.T) {
	_, err := New(Options{Dirs: []string{"queue"}, Allow: []AllowEntry{{File: "queue/allowed.go"}}})
	if err == nil || !strings.Contains(err.Error(), "needs both file and reason") {
		t.Fatalf("want an error naming the missing reason, got %v", err)
	}
}

// TestNewChecksAllowFileExists passes when an allow entry's file is really
// there, resolved from the module [sourcetest.Module] writes.
func TestNewChecksAllowFileExists(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "queue/present.go")
	opts := Options{Module: "example.com/m", Dirs: []string{"queue"}, Allow: []AllowEntry{{File: "queue/present.go", Reason: "test"}}}
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
}

// TestNewRejectsMovedDir fails at construction when a governed directory holds
// no Go files: the code moved, and the rule would govern nothing there.
func TestNewRejectsMovedDir(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "internal/queue/provider/host.go")
	opts := Options{Module: "example.com/m", Dirs: []string{"internal/queue", "internal/job"}}
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `providerio: dirs entry "internal/job" holds no Go files`) {
		t.Fatalf("want an error naming the dead dir, got %v", err)
	}
}

// TestNewRejectsMovedAllowFile fails at construction when an allow entry names a
// file the tree no longer has, the same guarantee the other analyzers give: a
// stale exemption reads as clean rather than as a gap.
func TestNewRejectsMovedAllowFile(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "queue/present.go")
	opts := Options{Module: "example.com/m", Dirs: []string{"queue"}, Allow: []AllowEntry{{File: "queue/moved.go", Reason: "test"}}}
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), "queue/moved.go") {
		t.Fatalf("want an error naming the moved file, got %v", err)
	}
}
