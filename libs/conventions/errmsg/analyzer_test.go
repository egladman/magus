package errmsg

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "errs")
}

func TestAnalyzerReportsOnlyTheRulesNamedOutsideAllowedFiles(t *testing.T) {
	analyzer, err := New(Options{
		Rules: []Rule{RuleJoin, RuleSentences, RuleWrap},
		Allow: []AllowEntry{
			{File: "quiet/*.go", Rule: RuleJoin, Reason: "staged"},
			{File: "quiet/quiet.go", Rule: RuleSentences, Reason: "staged"},
		},
		Hint: "see the runbook",
	})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "quiet")
}

func TestAnalyzerJudgesPrefixesAgainstTheModulesPackages(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("testdata", "origin"))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	analyzer, err := New(Options{
		Module:     "example.com/m",
		Rules:      []Rule{RuleOrigin, RuleStutter},
		Operations: []string{"run"},
	})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, dir, analyzer, "example.com/m/...")
}

func TestNewRejectsAnOperationNamingNoPackage(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "run/run.go")
	_, err := New(Options{Module: "example.com/m", Operations: []string{"run", "usage"}})
	if err == nil || !strings.Contains(err.Error(), `errmsg: operation "usage" names no package`) {
		t.Fatalf("want an error naming the dead operation, got %v", err)
	}
}

// TestNewRejectsDeadScope fails at load when a file it names has moved.
func TestNewRejectsDeadScope(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "internal/guard/shell.go")
	opts := Options{
		Module: "example.com/m",
		Files:  []string{"internal/guard/*.go"},
		Allow:  []AllowEntry{{File: "internal/guard/shell.go", Reason: "staged"}},
	}
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	opts.Allow[0].File = "internal/guard/sh.go"
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `errmsg: allow pattern "internal/guard/sh.go"`) {
		t.Fatalf("want an error naming the dead pattern, got %v", err)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	for name, opts := range map[string]Options{
		"an unknown rule":         {Rules: []Rule{"error-case"}},
		"an unreasoned allow":     {Allow: []AllowEntry{{File: "errs/*.go"}}},
		"an allow with no file":   {Allow: []AllowEntry{{Reason: "staged"}}},
		"an allow naming no rule": {Allow: []AllowEntry{{File: "errs/*.go", Rule: "message-length", Reason: "staged"}}},
		"a malformed glob":        {Files: []string{"[bad"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(opts); err == nil {
				t.Error("want an error at construction")
			}
		})
	}
}
