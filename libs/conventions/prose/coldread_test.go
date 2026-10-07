package prose

import (
	"reflect"
	"testing"
)

// The doc-comment cases below are libs/coldread's testdata, each doc written the
// way an index records it: comment markers and the marker's padding space gone.

func asideFinding(match string) Finding {
	return Finding{Rule: RuleAside, Message: asideMessage, Match: match}
}

func wrappedFinding(match string) Finding {
	return Finding{Rule: RuleAside, Message: wrappedMessage, Match: match}
}

func historyFinding(phrase string) Finding {
	return Finding{
		Rule: RuleHistory,
		Message: `Comment narrates a change ("` + phrase + `"); describe the code as it stands ` +
			"and leave its history to the commit message.",
		Match: phrase,
	}
}

func TestAsideReportsASpacedHyphenOutsideTheExemptShapes(t *testing.T) {
	cases := []judgeCase{
		{
			name: "a prose aside",
			doc: "Resolve answers the question the caller asked - not the one it meant.\n\n" +
				"A second paragraph with no aside stays silent.",
			want: []Finding{asideFinding("asked - not")},
		},
		{
			name: "doc-list bullets",
			doc: "Bullets holds the doc-list shapes gofmt formats, none of which is an aside:\n\n" +
				"  - the first item\n  - the second item\n\nand the numbered form:\n\n" +
				" 1. the first step\n 2. the second step",
		},
		{
			name: "an aside inside a list item",
			doc: "Loose reports the aside inside a list item while leaving the marker alone:\n\n" +
				"  - the item body carries an aside - which is reported\n  - this one does not",
			want: []Finding{asideFinding("aside - which")},
		},
		{
			name: "a list item's continuation line",
			doc: "Wrapped keeps a list item's continuation line as prose:\n\n" +
				"  - the item runs past the width of a line and wraps onto\n" +
				"    a second one, where the aside still counts - like this",
			want: []Finding{asideFinding("counts - like")},
		},
		{
			name: "a preformatted command table",
			doc: "Preformatted holds a command table, which godoc renders verbatim:\n\n" +
				"\tmagus run build .   - compiles the binary\n\tmagus run test .    - runs the suite\n\n" +
				"and prose resumes here.",
		},
		{
			name: "a backtick literal",
			doc: "Literal keeps the spaced hyphen inside backticks, where it is an argument:\n\n" +
				"The supported install is `tar -xf - -C <dir>`, nothing else.",
		},
		{
			name: "a URL",
			doc:  "Link carries a URL, which holds no spaces and so cannot spell the aside:\n\nSee https://example.com/a-b-c for the background.",
		},
		{
			name: "a digit on each side",
			doc:  "Arithmetic exempts a hyphen with a digit on each side:\n\nThe window is 3 - 5 seconds, and residual = 100 - 30 is subtraction.",
		},
		{
			name: "identifiers are not exempt",
			doc:  "Identifiers are NOT exempt, which is the documented cost of the digit rule:\n\nThe last index is n - 1, which reads as prose from out here.",
			want: []Finding{asideFinding("n - 1,")},
		},
		{
			name: "an aside wrapped onto the next line",
			doc: "Trailing carries the aside onto the next line, which the default rule leaves\n" +
				"alone because the fix rewraps a paragraph -\nsee the wrapped testdata package for the opt-in half.",
			want: []Finding{wrappedFinding("paragraph -")},
		},
	}

	runJudgeCases(t, cases)
}

func TestAsideReportsAHyphenEndingALine(t *testing.T) {
	cases := []judgeCase{
		{
			name: "a line ending in a spaced hyphen",
			doc: "Carry states the shape the default rule leaves alone.\n\n" +
				"The lock is taken before the check -\nwhich is what makes the fast path safe.",
			want: []Finding{wrappedFinding("check -")},
		},
		{
			name: "both shapes on one line report once",
			doc: "Once proves a line holding both shapes reports once, on the inline one.\n\n" +
				"The inline aside wins - and this line also ends in -\nso the count stays one.",
			want: []Finding{asideFinding("wins - and")},
		},
		{
			name: "a broken compound word",
			doc: "Compound keeps a trailing hyphen with no space before it, which is a broken\nword rather than an aside:\n\n" +
				"  - an item whose text ends in a compound word-",
		},
		{
			name: "a preformatted line ending in a hyphen",
			doc:  "Preformatted keeps a code line ending in a hyphen, which is an argument:\n\n\tmagus run build . -",
		},
	}

	runJudgeCases(t, cases)
}

func TestHistoryReportsAPhraseNarratingTheChange(t *testing.T) {
	cases := []judgeCase{
		{
			name: "used to",
			doc:  "Load reads the manifest. It used to read the lock file too.",
			want: []Finding{historyFinding("used to")},
		},
		{
			name: "previously",
			doc:  "Parse returns an error for an empty input; previously it returned nil.",
			want: []Finding{historyFinding("previously")},
		},
		{
			name: "now correctly",
			doc:  "This now correctly rejects a nil key.",
			want: []Finding{historyFinding("now correctly")},
		},
		{
			name: "the old behavior",
			doc:  "Keeps the old behavior for callers that pass a path.",
			want: []Finding{historyFinding("the old behavior")},
		},
		{
			name: "a purpose after is",
			doc:  "Sign takes the key that is used to sign every envelope, which is what a\nverifier checks.",
		},
		{
			name: "a purpose after a bare noun reads as a habit",
			doc: "SignBare pins the known false positive: a purpose after a bare noun reads the\n" +
				"same as a habit, so the key used to sign is reported.",
			want: []Finding{historyFinding("used to")},
		},
		{
			name: "now and instead of describe the code",
			doc: "Walk reads the directory now, before the caller can change it, instead of\n" +
				"deferring the read. The rejected alternative is what `used to` would say.",
		},
		{
			name: "a preformatted command table",
			doc:  "Example keeps its command table verbatim, whatever it says:\n\n\ttool --previously-set  rerun with the old flag",
		},
	}

	runJudgeCases(t, cases)
}

func TestDocStubReportsADocThatOnlyRepeatsTheName(t *testing.T) {
	cases := []struct {
		name string
		sym  Symbol
		want []Finding
	}{
		{
			name: "a type named twice",
			sym:  Symbol{Name: "Client", Doc: "Client is a Client."},
			want: []Finding{docStubFinding("Client", "Client is a Client.")},
		},
		{
			name: "a constructor creating its type",
			sym:  Symbol{Name: "NewClient", Callable: true, Doc: "NewClient creates a new Client."},
			want: []Finding{docStubFinding("NewClient", "NewClient creates a new Client.")},
		},
		{
			name: "a method name and an ellipsis",
			sym:  Symbol{Name: "Close", Owner: "Client", Callable: true, Doc: "Close ..."},
			want: []Finding{docStubFinding("Close", "Close ...")},
		},
		{
			name: "a constant named in words",
			sym:  Symbol{Name: "DefaultTimeout", Doc: "DefaultTimeout is the default timeout."},
			want: []Finding{docStubFinding("DefaultTimeout", "DefaultTimeout is the default timeout.")},
		},
		{name: "a contract", sym: Symbol{Name: "NewServer", Callable: true, Doc: "NewServer validates the config and does not dial."}},
		{name: "an implemented interface", sym: Symbol{Name: "String", Owner: "Client", Callable: true, Doc: "String implements fmt.Stringer."}},
		{name: "an edge case", sym: Symbol{Name: "Reset", Owner: "Client", Callable: true, Doc: "Reset returns the client to its zero state; it is safe on a nil client."}},
		{
			name: "a second paragraph",
			sym: Symbol{Name: "Options", Doc: "Options is a Options.\n\n" +
				"The second paragraph is the contract, so the first line is scaffolding."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, Judge(tc.sym), tc.want)
		})
	}
}

func docStubFinding(name, match string) Finding {
	return Finding{
		Rule: RuleDocStub,
		Message: "Doc comment only repeats the name " + name + "; state the contract a caller " +
			"relies on (edge cases, errors, ownership), or keep it to what the name cannot say.",
		Match: match,
	}
}

func TestMarkersSilenceHistoryAndDocStub(t *testing.T) {
	cases := []struct {
		name string
		sym  Symbol
		want []Finding
	}{
		{name: "TODO", sym: Symbol{Name: "Client", Doc: "TODO: Client is a Client until the pool lands."}},
		{name: "Deprecated", sym: Symbol{Name: "Open", Callable: true, Doc: "Deprecated: Open is Open; use Dial."}},
		{name: "a bare TODO", sym: Symbol{Name: "Count", Doc: "TODO count"}},
		{name: "FIXME", sym: Symbol{Name: "Run", Doc: "FIXME: step 1 of 2 runs twice."}},
		{name: "BUG", sym: Symbol{Name: "Run", Doc: "BUG(eli): x := count"}},
		{
			name: "compat on the first line silences the second",
			sym: Symbol{Name: "Read", Doc: "compat(until: no store still serves v1 rows): this used to read v1 rows,\n" +
				"and previously dropped them."},
		},
		{
			name: "a word opening with a marker is not one",
			sym:  Symbol{Name: "Pile", Doc: "TODOS used to pile up."},
			want: []Finding{historyFinding("used to")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, Judge(tc.sym), tc.want)
		})
	}
}

// TestWords pins the tokenizing docstub relies on: identifiers split at case and
// underscore boundaries with acronyms whole, a trailing s folds, and stop words
// drop.
func TestWords(t *testing.T) {
	cases := map[string][]string{
		"Get the users":             {"get", "user"},
		"HTTPServer":                {"http", "server"},
		"parse_config_file":         {"parse", "config", "file"},
		"NewFoo creates a new Foo.": {"new", "foo", "create", "new", "foo"},
		"class":                     {"class"},
	}

	for in, want := range cases {
		if got := words(in); !reflect.DeepEqual(got, want) {
			t.Errorf("words(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHistoryPhrase covers the "used to" split: a habit is history, a purpose
// is not.
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

// TestBlankBackticksBlanksAnUnterminatedSpanToTheEndOfTheLine pins the
// unterminated-span decision: the second half of a span opened on the previous
// line is still scanned, a false positive chosen over letting one stray
// backtick silence everything after it.
func TestBlankBackticksBlanksAnUnterminatedSpanToTheEndOfTheLine(t *testing.T) {
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

// TestListMarkerRejectsABareMarker pins the shapes go/doc/comment reads as a
// list item, plus the bare marker that is not one: a line holding a lone hyphen
// must not put the rest of the doc into list mode.
func TestListMarkerRejectsABareMarker(t *testing.T) {
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
