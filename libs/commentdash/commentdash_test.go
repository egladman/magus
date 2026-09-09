package commentdash

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer covers the default rule in one pass over the asides package: the
// prose aside is reported, and the shapes that spell a spaced hyphen for another
// reason stay silent. The list cases carry the weight, because a doc-list bullet
// is the one shape gofmt itself writes and a linter fighting the formatter is
// unfixable by definition.
func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "asides")
}

// TestAnalyzerWrapped checks the opt-in half: a line ending in a spaced hyphen
// carries the aside onto the next line, and a line holding both shapes still
// reports once.
func TestAnalyzerWrapped(t *testing.T) {
	analyzer, err := New(Options{Wrapped: true})
	if err != nil {
		t.Fatal(err)
	}

	analysistest.Run(t, analysistest.TestData(), analyzer, "wrapped")
}

// TestAnalyzerAllow checks that the glob excuses a FILE rather than a comment:
// both files in the package carry the same aside and only one is reported.
func TestAnalyzerAllow(t *testing.T) {
	analyzer, err := New(Options{Allow: []string{"*_gen.go"}})
	if err != nil {
		t.Fatal(err)
	}

	analysistest.Run(t, analysistest.TestData(), analyzer, "allowed")
}

// TestNewRejectsMalformedGlob pins the reason New validates at construction: the
// patterns are otherwise reached once per file, so a typo would lint clean until
// it met a file holding an aside and then fail the run from somewhere unrelated
// to the mistake.
func TestNewRejectsMalformedGlob(t *testing.T) {
	_, err := New(Options{Allow: []string{"*_ok.go", "[bad"}})
	if err == nil {
		t.Fatal("expected an error for a malformed glob")
	}

	if !strings.Contains(err.Error(), "[bad") {
		t.Errorf("error should name the offending pattern, got: %v", err)
	}
}

// TestBlankBackticks pins the unterminated-span decision, which analysistest
// cannot show on its own: a backtick with no closer blanks to the end of the
// line, so the second half of a span opened on the previous line is still
// scanned. That is a known false positive, chosen over carrying the state across
// lines, where one stray backtick would silence everything after it.
func TestBlankBackticks(t *testing.T) {
	cases := map[string]string{
		"plain prose - here":     "plain prose - here",
		"a `x - y` b":            "a ####### b",
		"a `x - y":               "a ######",
		"`x` - `y`":              "### - ###",
		"a ` b - c":              "a #######",
		"back to back `a``b - c": "back to back #########",
	}

	for in, want := range cases {
		if got := blankBackticks(in); got != want {
			t.Errorf("blankBackticks(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestListMarker pins the shapes go/doc/comment reads as a list item, plus the
// bare marker that is not one: a line holding a lone hyphen must not put the
// rest of the comment into list mode, where every indented line after it would
// stop counting as preformatted.
func TestListMarker(t *testing.T) {
	cases := map[string]int{
		"- item":   2,
		"* item":   2,
		"+ item":   2,
		"• item":   len("•") + 1,
		"1. step":  3,
		"12) step": 4,
		"-":        0,
		"-item":    0,
		"1.step":   0,
		"item":     0,
		"":         0,
	}

	for in, want := range cases {
		if got := listMarker(in); got != want {
			t.Errorf("listMarker(%q) = %d, want %d", in, got, want)
		}
	}
}
