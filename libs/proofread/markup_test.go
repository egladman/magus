package proofread

import (
	"slices"
	"testing"
)

func markupMatches(t *testing.T, rule Rule, body string) []string {
	t.Helper()
	return matches(JudgeText("fix(cache): keep the key stable\nThe key drifted.\n\n"+body, KindChangeDescription, houseOn), rule)
}

func assertMarkup(t *testing.T, rule Rule, body string, want []string) {
	t.Helper()
	if got := markupMatches(t, rule, body); !slices.Equal(got, want) {
		t.Errorf("%s findings:\n got %q\nwant %q", rule, got, want)
	}
}

func TestEmphasisReportsBoldAndItalic(t *testing.T) {
	assertMarkup(t, RuleEmphasis, "- The key is **stable** now.\n- It is *never* reset.\n",
		[]string{"4:**stable**", "5:*never*"})
}

func TestEmphasisLeavesCodeAndSnakeCaseAlone(t *testing.T) {
	assertMarkup(t, RuleEmphasis, "- `**kwargs` and `__init__` stay code.\n", nil)
}

func TestBareSymbolReportsACall(t *testing.T) {
	assertMarkup(t, RuleBareSymbol, "- The test calls prepareLoad() once.\n", []string{"4:prepareLoad()"})
}

func TestBareSymbolReportsADottedName(t *testing.T) {
	assertMarkup(t, RuleBareSymbol, "- It wraps the pattern in fmt.Sprintf.\n", []string{"4:fmt.Sprintf"})
}

func TestBareSymbolReportsLowerCamelAndSnakeCase(t *testing.T) {
	assertMarkup(t, RuleBareSymbol, "- localBranchesFile lists them for go_test.\n",
		[]string{"4:localBranchesFile", "4:go_test"})
}

func TestBareSymbolLeavesBackticksProductsAndURLsAlone(t *testing.T) {
	assertMarkup(t, RuleBareSymbol,
		"- `prepareLoad` runs on macOS and Node.js, per https://example.com/fooBar.\n- GitHub and TypeScript pass.\n", nil)
}
