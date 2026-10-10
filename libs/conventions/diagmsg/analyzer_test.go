package diagmsg

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"
	"github.com/egladman/magus/libs/conventions/prose"
	"golang.org/x/tools/go/analysis/analysistest"
)

var sources = Options{
	MaxRunes: 60,
	Calls: []Call{
		{Func: "diag.Errorf", Arg: 1, Format: true},
		{Func: "(diag.Domain).Errorf", Arg: 1, Format: true},
		{Func: "diag.Format", Arg: 1},
	},
	Fields:   []Field{{Type: "guard.Verdict", Field: "Deny"}, {Type: "guard.Verdict", Field: "Lead"}},
	Prefixes: []string{"magus workspace:"},
}

func TestAnalyzer(t *testing.T) {
	analyzer, err := New(sources)
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "cli")
}

func TestAnalyzerReportsOnlyTheRulesNamedOutsideAllowedFiles(t *testing.T) {
	opts := sources
	opts.Rules = []prose.Rule{prose.RuleMessageRationale, prose.RuleMessageTag}
	opts.Allow = []AllowEntry{{File: "quiet/*.go", Rule: prose.RuleMessageRationale, Reason: "staged"}}
	opts.Hint = "see the runbook"
	analyzer, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "quiet")
}

// TestNewRejectsDeadScope fails at load when a file it names has moved.
func TestNewRejectsDeadScope(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "internal/guard/shell.go")
	opts := sources
	opts.Module = "example.com/m"
	opts.Files = []string{"internal/guard/*.go"}
	opts.Allow = []AllowEntry{{File: "internal/guard/shell.go", Reason: "staged"}}
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	opts.Allow[0].File = "internal/guard/sh.go"
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `diagmsg: allow pattern "internal/guard/sh.go"`) {
		t.Fatalf("want an error naming the dead pattern, got %v", err)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Options)
	}{
		{"nothing to judge", func(o *Options) { o.Calls, o.Fields, o.Prefixes = nil, nil, nil }},
		{"a negative cap", func(o *Options) { o.MaxRunes = -1 }},
		{"an unqualified call", func(o *Options) { o.Calls = []Call{{Func: "Errorf"}} }},
		{"a negative argument", func(o *Options) { o.Calls = []Call{{Func: "diag.Errorf", Arg: -1}} }},
		{"a field with no name", func(o *Options) { o.Fields = []Field{{Type: "guard.Verdict"}} }},
		{"an empty prefix", func(o *Options) { o.Prefixes = []string{""} }},
		{"a rule for another kind", func(o *Options) { o.Rules = []prose.Rule{prose.RuleFiller} }},
		{"an unreasoned allow", func(o *Options) { o.Allow = []AllowEntry{{File: "cli/*.go"}} }},
		{"an allow for another kind's rule", func(o *Options) {
			o.Allow = []AllowEntry{{File: "cli/*.go", Rule: prose.RuleHedge, Reason: "x"}}
		}},
		{"a malformed glob", func(o *Options) { o.Files = []string{"[bad"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := sources
			tc.edit(&opts)
			if _, err := New(opts); err == nil {
				t.Error("want an error at construction")
			}
		})
	}
}
