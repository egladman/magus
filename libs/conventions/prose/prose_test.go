package prose

import (
	"reflect"
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

func assertFindings(t *testing.T, got, want []Finding) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings:\n got %#v\nwant %#v", got, want)
	}
}

func TestRulesListsEveryRuleInReportOrder(t *testing.T) {
	want := []Rule{
		RuleCommentBlock, RuleCommentSentence, RuleFiller, RuleTerms,
		RuleNameSuffix, RuleAside, RuleHistory, RuleDocStub,
		RuleLeadContext, RuleReplyVoice, RuleTense, RuleHedge, RuleAttribution,
		RuleTerseSentence, RuleTerseParagraph, RuleWordy, RuleBareRule,
		RuleSecondPerson, RuleStepVerb, RuleCondescension,
		RuleMessageLength, RuleMessageRationale, RuleMessageCommands, RuleMessageTag, RuleTemplate,
	}

	if got := Rules(); !reflect.DeepEqual(got, want) {
		t.Errorf("Rules() = %q, want %q", got, want)
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
