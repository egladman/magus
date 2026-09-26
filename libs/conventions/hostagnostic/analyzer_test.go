package hostagnostic

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer names a module the testdata is not in, as a nested module with
// its own name is in the tree: its packages are still scanned and still skipped
// by directory.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{Module: "example.com/m", SkipDirs: []string{"gen"}})
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
// constraints drop, as the tree walk it replaced did.
func TestAnalyzerReadsExcludedFiles(t *testing.T) {
	analyzer, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range analysistest.Run(&recorder{}, analysistest.TestData(), analyzer, "tagged") {
		for _, d := range r.Diagnostics {
			p := r.Pass.Fset.Position(d.Pos)
			got = append(got, fmt.Sprintf("%s:%d", filepath.Base(p.Filename), p.Line))
		}
	}
	if len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != "tagged_never.go:5" }) {
		t.Fatalf("diagnostics at %v, want only tagged_never.go:5", got)
	}
}

// TestLine grades the matcher against lines, because a tree that reports
// nothing is equally consistent with a matcher that matches nothing. Cursor is
// the case that needs it, and the negative cases are why.
func TestLine(t *testing.T) {
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
		{`"cursor-hook.sh",`, false},
		{`filepath.Join(root, ".cursor", "hooks.json"),`, false},
		{`filepath.Join(root, ".claude", "settings.json"),`, false},
	} {
		if got := Line(tc.line); got != tc.want {
			t.Errorf("Line(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}
