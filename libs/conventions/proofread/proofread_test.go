package proofread

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
			assertFindings(t, Judge(Symbol{Name: "Resolve", Callable: true, Doc: tc.doc}, houseOn), tc.want)
		})
	}
}

// houseOn turns every house rule on at [DecisionDeny], so a case judges by
// every rule as this repository does.
var houseOn = WithDecisions(houseDecisions(DecisionDeny))

func houseDecisions(d Decision) map[Rule]Decision {
	out := map[Rule]Decision{}

	for _, c := range checks {
		if c.house {
			out[c.rule] = d
		}
	}

	return out
}

// assertFindings reads a want with no Decision as [DecisionDeny], so a case
// names a decision only where it advises, and fills in each want's code and
// page from its rule.
func assertFindings(t *testing.T, got, want []Finding) {
	t.Helper()

	want = slices.Clone(want)
	for i := range want {
		if want[i].Decision == "" {
			want[i].Decision = DecisionDeny
		}

		want[i].Code = ruleTexts[want[i].Rule].code
		want[i].URL = ruleBase + string(want[i].Rule) + "/"
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

	advisedTerms := terms
	advisedTerms.Decision = DecisionAdvise

	cases := []struct {
		name string
		opts []Option
		want []Finding
	}{
		{"house style is off by default", nil, []Finding{filler}},
		{"a table turns a house rule on", []Option{WithDecisions(map[Rule]Decision{RuleTerms: DecisionDeny})},
			[]Finding{filler, terms}},
		{"a table sets the decision", []Option{WithDecisions(map[Rule]Decision{RuleTerms: DecisionAdvise})},
			[]Finding{filler, advisedTerms}},
		{"a table turns a rule off", []Option{WithDecisions(map[Rule]Decision{RuleFiller: DecisionOff})}, nil},
		{"only", []Option{houseOn, WithOnly(RuleTerms)}, []Finding{terms}},
		{"only leaves an off rule off", []Option{WithOnly(RuleTerms)}, nil},
		{"a later table adds to the first", []Option{
			houseOn, WithDecisions(map[Rule]Decision{RuleTerms: DecisionOff}),
		}, []Finding{filler}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(text, KindReference, tc.opts...), tc.want)
		})
	}
}

// A table that names a rule sets every finding's decision, the ones the rule
// marked to advise itself included; with no table, the rule's mark stands.
func TestDecisionsOverrideWhatARuleMarked(t *testing.T) {
	const text = "perf: x\nThe server's warm caches lagged behind edits, so the first query rebuilt the graph inline."

	marked := func(opts ...Option) []Decision {
		var out []Decision

		for _, f := range JudgeText(text, KindChangeDescription, append(opts, WithOnly(RuleLeadContext))...) {
			out = append(out, f.Decision)
		}

		return out
	}

	if got := marked(); !slices.Equal(got, []Decision{DecisionAdvise}) {
		t.Errorf("with no table: got %q, want advise", got)
	}

	if got := marked(WithDecisions(map[Rule]Decision{RuleLeadContext: DecisionDeny})); !slices.Equal(got,
		[]Decision{DecisionDeny}) {
		t.Errorf("with deny in the table: got %q, want deny", got)
	}
}

func TestCatalogDocumentsEveryRuleWithAUniqueCode(t *testing.T) {
	codes := map[string]Rule{}

	for _, doc := range Catalog() {
		switch {
		case doc.Catches == "" || doc.Why == "":
			t.Errorf("%s: no catches or why", doc.Name)
		case len(doc.Code) != 7 || doc.Code[:3] != "PRF":
			t.Errorf("%s: code %q is not PRF and four digits", doc.Name, doc.Code)
		case codes[string(doc.Code)] != "":
			t.Errorf("%s: code %s is %s's", doc.Name, doc.Code, codes[string(doc.Code)])
		}

		codes[string(doc.Code)] = doc.Name

		for _, k := range doc.Kinds {
			if d := doc.Decisions[k]; doc.House && d != DecisionOff || !doc.House && d == DecisionOff {
				t.Errorf("%s on %s: default %q, house %v", doc.Name, k, d, doc.House)
			}
		}

		if got, want := prf.URL(doc.Code), ruleBase+string(doc.Name)+"/"; got != want {
			t.Errorf("%s: url %q, want %q", doc.Name, got, want)
		}
	}

	if len(codes) != len(Rules()) || len(ruleTexts) != len(Rules()) {
		t.Errorf("%d codes and %d texts for %d rules", len(codes), len(ruleTexts), len(Rules()))
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

	assertFindings(t, Judge(s, houseOn), want)
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
