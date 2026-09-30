package hostagnostic

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"
	"golang.org/x/tools/go/analysis/analysistest"
)

// hosts mirrors the repository's own settings, so the matcher is graded on the
// configuration it runs with. Cursor is a pattern rather than a host: the word
// is a terminal position and a pagination token far more often than the host.
var hosts = Options{
	Hosts: []string{"claude", "opencode", "codex", "aider", "windsurf"},
	HostPatterns: []string{
		`\(Cursor\)`,
		`(?i)\bcursor (hooks?|ide|editor|rules)\b`,
		`(?i)[!=]=\s*"cursor"`,
	},
}

// TestAnalyzer names a module the testdata is not in, as a nested module with
// its own name is in the tree: its packages are still scanned and still skipped
// by directory.
func TestAnalyzer(t *testing.T) {
	opts := hosts
	opts.SkipDirs = []string{"gen"}
	analyzer, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "hosts", "hosts/gen")
}

// recorder collects what analysistest would fail on. It reads '// want' from an
// excluded file only for a few named analyzers, so that case is asserted here.
type recorder struct{ errs []string }

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// TestAnalyzerReadsExcludedFiles holds the rule on a file this platform's build
// constraints drop, as the tree walk it replaced did, and carries the hint.
func TestAnalyzerReadsExcludedFiles(t *testing.T) {
	opts := hosts
	opts.Hint = "see docs/hosts.md"
	analyzer, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range analysistest.Run(&recorder{}, analysistest.TestData(), analyzer, "tagged") {
		for _, d := range r.Diagnostics {
			p := r.Pass.Fset.Position(d.Pos)
			got = append(got, fmt.Sprintf("%s:%d", filepath.Base(p.Filename), p.Line))
			if !strings.HasSuffix(d.Message, "; see docs/hosts.md") {
				t.Errorf("message %q does not end with the hint", d.Message)
			}
		}
	}
	if len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != "tagged_never.go:5" }) {
		t.Fatalf("diagnostics at %v, want only tagged_never.go:5", got)
	}
}

func TestNewRequiresHosts(t *testing.T) {
	if _, err := New(Options{}); err == nil || !strings.Contains(err.Error(), "hosts and host-patterns are both empty") {
		t.Fatalf("want an error for no hosts, got %v", err)
	}
}

func TestNewRejectsBadPattern(t *testing.T) {
	if _, err := New(Options{HostPatterns: []string{"("}}); err == nil || !strings.Contains(err.Error(), `host-patterns "("`) {
		t.Fatalf("want an error naming the pattern, got %v", err)
	}
}

// TestNewChecksSkipDirs fails at construction on a skip entry naming no
// directory that holds Go files: the tree moved and the setting skips nothing.
func TestNewChecksSkipDirs(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "app/gen/gen.go", "app/app.go")
	opts := hosts
	opts.Module = "example.com/m"
	opts.SkipDirs = []string{"gen"}
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	opts.SkipDirs = []string{"gen", "generated"}
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `skip-dirs entry "generated"`) {
		t.Fatalf("want an error naming the dead entry, got %v", err)
	}
}

// TestLine grades the matcher against lines, because a tree that reports
// nothing is equally consistent with a matcher that matches nothing. Cursor is
// the case that needs it, and the negative cases are why.
func TestLine(t *testing.T) {
	m, err := newMatcher(hosts.Hosts, hosts.HostPatterns)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		line string
		want bool
	}{
		{`if host == "cursor" {`, true},
		{`return h.Host != "cursor"`, true},
		{`// Cursor hooks fire after the write, not before it.`, true},
		{`// on a host with no pre-write file hook (Cursor), the deny lands late`, true},
		{`fmt.Println("paste this into your Claude settings")`, true},

		{`cursor := paramString(req.Params, "cursor", "")`, false},
		{`// Cursor reports where the cursor is, in 1-based terminal coordinates.`, false},
		{"\tCursor DiffCursor `json:\"cursor\" yaml:\"cursor\"`", false},
		{`"cursor-hook.buzz",`, false},
		{`filepath.Join(root, ".cursor", "hooks.json"),`, false},
		{`filepath.Join(root, ".claude", "settings.json"),`, false},
		{`case "claude":`, false},
	} {
		if got := m.line(tc.line); got != tc.want {
			t.Errorf("line(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}
