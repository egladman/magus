package prose

import (
	"reflect"
	"slices"
	"testing"
)

// judgeCase judges Doc alone on a symbol whose name trips no rule, so a case
// sees only the findings its doc earns.
type judgeCase struct {
	name string
	doc  string
	want []Finding
}

func runJudgeCases(t *testing.T, cases []judgeCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, Judge(Symbol{Name: "Resolve", Callable: true, Doc: tc.doc}), tc.want)
		})
	}
}

// assertFindings reads a want with no Severity as [SeverityError], so a case
// names a severity only where it is advisory.
func assertFindings(t *testing.T, got, want []Finding) {
	t.Helper()

	want = slices.Clone(want)
	for i := range want {
		if want[i].Severity == "" {
			want[i].Severity = SeverityError
		}
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings:\n got %#v\nwant %#v", got, want)
	}
}

// ruleNames lists the rules cs runs, in order. tone_test.go and slop_test.go
// pin the order inside [toneChecks] and [slopChecks].
func ruleNames(cs []check) []Rule {
	out := make([]Rule, len(cs))
	for i, c := range cs {
		out[i] = c.rule
	}

	return out
}

func TestRulesListsEveryRuleInReportOrder(t *testing.T) {
	want := slices.Concat([]Rule{
		RuleCommentBlock, RuleCommentSentence, RuleFiller, RuleTerms,
		RuleNameSuffix, RuleAside, RuleHistory, RuleDocStub,
		RuleLeadContext, RuleReplyVoice, RuleTense, RuleHedge, RuleAttribution,
		RuleTerseSentence, RuleTerseParagraph, RuleWordy, RuleBareRule,
		RuleSecondPerson, RuleStepVerb, RuleCondescension,
	}, ruleNames(toneChecks), ruleNames(slopChecks), []Rule{RuleTemplate})

	if got := Rules(); !reflect.DeepEqual(got, want) {
		t.Errorf("Rules() = %q, want %q", got, want)
	}
}

func TestJudgeTextOptionsChooseTheRules(t *testing.T) {
	const text = "Simply ask the sub-agent.\n"

	filler := Finding{Rule: RuleFiller, Message: "Drop 'Simply': state the fact.", Match: "Simply", Line: 1}
	terms := Finding{Rule: RuleTerms, Message: "Write 'subagent', not 'sub-agent'.", Match: "sub-agent", Line: 1}

	cases := []struct {
		name string
		opts []Option
		want []Finding
	}{
		{"every rule by default", nil, []Finding{filler, terms}},
		{"only", []Option{WithOnly(RuleTerms)}, []Finding{terms}},
		{"skip", []Option{WithSkip(RuleTerms)}, []Finding{filler}},
		{"the collaborative profile leaves the glossary out", []Option{WithProfile(ProfileCollaborative)}, []Finding{filler}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(text, KindMarkdown, tc.opts...), tc.want)
		})
	}
}

func TestJudgeReportsInRuleOrderThenTextOrder(t *testing.T) {
	s := Symbol{
		Name:     "valueOf",
		Callable: true,
		Doc:      "It used to read a sub-agent - basically twice. It simply rereads a Sub-agent.",
	}

	want := []Finding{
		{Rule: RuleFiller, Message: "Drop 'basically': state the fact.", Match: "basically"},
		{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply"},
		{Rule: RuleTerms, Message: "Write 'subagent', not 'sub-agent'.", Match: "sub-agent"},
		{Rule: RuleTerms, Message: "Write 'subagent', not 'Sub-agent'.", Match: "Sub-agent"},
		nameSuffixFinding("valueOf"),
		{Rule: RuleAside, Message: asideMessage, Match: "sub-agent - basically"},
		{
			Rule:    RuleHistory,
			Message: `Comment narrates a change ("used to"); describe the code as it stands and leave its history to the commit message.`,
			Match:   "used to",
		},
	}

	assertFindings(t, Judge(s), want)
}

func TestJudgeFindsNothingInAnEmptyDoc(t *testing.T) {
	assertFindings(t, Judge(Symbol{Name: "Resolve", Owner: "Client", Callable: true}), nil)
}

func TestProseLinesDropCodeAndKeepListItems(t *testing.T) {
	doc := "  Intro line.\n" +
		"  wraps here.\n" +
		"\n" +
		"    preformatted - code\n" +
		"\n" +
		"  - an item\n" +
		"    continues\n" +
		"  ```\n" +
		"  fenced - code\n" +
		"  ```\n" +
		"  After the fence."

	want := []proseLine{
		{text: "Intro line.", line: 1, start: 0, opens: true, paragraph: true},
		{text: "wraps here.", line: 2, start: 0},
		{text: "- an item", line: 6, start: 2, opens: true, paragraph: true, item: true},
		{text: "  continues", line: 7, start: 2},
		{text: "After the fence.", line: 11, start: 0, opens: true, paragraph: true},
	}

	if got := proseLines(doc); !reflect.DeepEqual(got, want) {
		t.Errorf("proseLines:\n got %#v\nwant %#v", got, want)
	}
}

// TestProseLinesOpenAnItemPerMarkerLine pins that each line opening with a
// marker is its own item, and that a spaced hyphen later in the line, inside a
// backtick span or not, opens nothing.
func TestProseLinesOpenAnItemPerMarkerLine(t *testing.T) {
	doc := "Rules are derived:\n    - one property;\n   - the next - here `a  - b`"

	want := []proseLine{
		{text: "Rules are derived:", line: 1, start: 0, opens: true, paragraph: true},
		{text: "    - one property;", line: 2, start: 6, opens: true, paragraph: true, item: true},
		{text: "   - the next - here `a  - b`", line: 3, start: 5, opens: true, item: true},
	}

	if got := proseLines(doc); !reflect.DeepEqual(got, want) {
		t.Errorf("proseLines:\n got %#v\nwant %#v", got, want)
	}
}

// TestProseLinesOpenAParagraphWhereAListStartsAndEnds pins Vale's text scope
// for a list with no blank line around it: the list is one paragraph, apart
// from the lines on either side, and only its first item opens it.
func TestProseLinesOpenAParagraphWhereAListStartsAndEnds(t *testing.T) {
	doc := "Lead:\n  - one\n    wraps\n  - two\nAfter the list."

	want := []proseLine{
		{text: "Lead:", line: 1, start: 0, opens: true, paragraph: true},
		{text: "  - one", line: 2, start: 4, opens: true, paragraph: true, item: true},
		{text: "    wraps", line: 3, start: 4},
		{text: "  - two", line: 4, start: 4, opens: true, item: true},
		{text: "After the list.", line: 5, start: 0, opens: true, paragraph: true},
	}

	if got := proseLines(doc); !reflect.DeepEqual(got, want) {
		t.Errorf("proseLines:\n got %#v\nwant %#v", got, want)
	}
}
