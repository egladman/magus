package coldread

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

// only builds an analyzer running the one named check, so each testdata package
// holds the cases of its own check without tripping the others.
func only(t *testing.T, check Check, opts Options) *analysis.Analyzer {
	t.Helper()

	for _, c := range checks {
		if c != check {
			opts.Disable = append(opts.Disable, c)
		}
	}

	analyzer, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}

	return analyzer
}

// TestAnalyzer covers the default rule in one pass over the asides package: the
// prose aside is reported, and the shapes that spell a spaced hyphen for another
// reason stay silent. The list cases carry the weight, because a doc-list bullet
// is the one shape gofmt itself writes and a linter fighting the formatter is
// unfixable by definition.
func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), only(t, CheckAside, Options{}), "asides")
}

// TestAnalyzerWrapped checks the opt-in half: a line ending in a spaced hyphen
// carries the aside onto the next line, and a line holding both shapes still
// reports once.
func TestAnalyzerWrapped(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), only(t, CheckAside, Options{Wrapped: true}), "wrapped")
}

// TestAnalyzerAllow checks that the glob excuses a FILE rather than a comment:
// both files in the package carry the same aside and only one is reported.
func TestAnalyzerAllow(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), only(t, CheckAside, Options{Allow: []string{"*_gen.go"}}), "allowed")
}

// TestIntentChecks runs each intent check over its own package of reported and
// silent cases.
func TestIntentChecks(t *testing.T) {
	for _, check := range []Check{CheckRestate, CheckSteps, CheckHistory, CheckDocstub, CheckCommentedCode} {
		t.Run(string(check), func(t *testing.T) {
			analysistest.Run(t, analysistest.TestData(), only(t, check, Options{}), string(check))
		})
	}
}

// TestMarkersNeverReport runs every check at once over comments that open with a
// marker and would otherwise trip one: TODO, compat(until:) and Deprecated carry
// meaning a person or tool acts on.
func TestMarkersNeverReport(t *testing.T) {
	analyzer, err := New(Options{Disable: []Check{CheckAside}})
	if err != nil {
		t.Fatal(err)
	}

	analysistest.Run(t, analysistest.TestData(), analyzer, "markers")
}

// TestDisableSilencesEveryCheck proves Disable reaches the run: the silenced
// package trips every check, and with all of them off nothing reports.
func TestDisableSilencesEveryCheck(t *testing.T) {
	analyzer, err := New(Options{Disable: slices.Clone(checks)})
	if err != nil {
		t.Fatal(err)
	}

	// The package carries no want comments, so any diagnostic fails the run.
	analysistest.Run(t, analysistest.TestData(), analyzer, "silenced")
}

func TestNewRejectsUnknownCheck(t *testing.T) {
	_, err := New(Options{Disable: []Check{"restates"}})
	if err == nil || !strings.Contains(err.Error(), `"restates"`) {
		t.Fatalf("want an error naming the unknown check, got %v", err)
	}
}

// TestWords pins the tokenizing restate and docstub share: identifiers split at
// case and underscore boundaries with acronyms whole, a trailing s folds, and
// stop words drop.
func TestWords(t *testing.T) {
	cases := map[string][]string{
		"Get the users":             {"get", "user"},
		"HTTPServer":                {"http", "server"},
		"parse_config_file":         {"parse", "config", "file"},
		"NewFoo creates a new Foo.": {"new", "foo", "create", "new", "foo"},
		"class":                     {"class"},
	}

	for in, want := range cases {
		if got := words(in); !slices.Equal(got, want) {
			t.Errorf("words(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHistoryPhrase covers the "used to" split testdata cannot show compactly:
// a habit is history, a purpose is not.
func TestHistoryPhrase(t *testing.T) {
	cases := map[string]int{
		"it used to panic":           3,
		"the key is used to sign":    -1,
		"was used to build":          -1,
		"previously nil":             0,
		"see `used to` in the notes": -1,
		"now correctly rejects":      0,
		"now rejects":                -1,
		"instead of a map":           -1,
	}

	for in, want := range cases {
		if got, _ := historyPhrase(in); got != want {
			t.Errorf("historyPhrase(%q) = %d, want %d", in, got, want)
		}
	}
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

func TestNamedStep(t *testing.T) {
	cases := map[string]bool{
		"Step 1: load":  true,
		"step2 resolve": true,
		"STEP 10.":      true,
		"step 1a":       false,
		"step_1":        false,
		"steps 1":       false,
		"step":          false,
		"stepping":      false,
	}

	for in, want := range cases {
		if got := namedStep(in); got != want {
			t.Errorf("namedStep(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestHasCodeSignal pins the tokens commentedcode accepts as code, and that a
// brace inside a string literal is not one.
func TestHasCodeSignal(t *testing.T) {
	cases := map[string]bool{
		"x := compute()":           true,
		"fmt.Println(x)":           true,
		"return err":               true,
		"a; b":                     true,
		"if ok {":                  true,
		"Retry (bounded)":          false,
		"See fmt.Println for this": false,
		"we return early":          false,
		`log("{")`:                 false,
	}

	for in, want := range cases {
		if got := hasCodeSignal(in); got != want {
			t.Errorf("hasCodeSignal(%q) = %v, want %v", in, got, want)
		}
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
