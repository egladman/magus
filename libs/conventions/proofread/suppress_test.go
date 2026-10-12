package proofread

import (
	"slices"
	"strings"
	"testing"
)

const (
	simplyMsg = "Drop 'Simply': state the fact."
	noReason  = "Add a reason to this suppression: it suppresses nothing without one."
)

func simplyAt(line int) Finding {
	return Finding{Rule: RuleFiller, Message: simplyMsg, Match: "Simply", Line: line}
}

func unused(rules string, match string, line int) Finding {
	return Finding{
		Rule: RuleSuppressionUnused, Match: match, Line: line,
		Message: "Remove the suppression of " + rules + ": it matches no finding.",
	}
}

func reasonless(match string, line int) Finding {
	return Finding{Rule: RuleSuppressionUnused, Match: match, Line: line, Message: noReason}
}

func TestSuppressionCoversWhatItNamesWithAReason(t *testing.T) {
	const off = "<!-- proofread off filler: quoted from the user -->"

	cases := []struct {
		name string
		text string
		want []Finding
	}{
		{"the line after the comment", off + "\nSimply ask.\n", nil},
		{"the comment's own line", "Simply ask. " + off + "\n", nil},
		{"not two lines after", off + "\nAsk.\nSimply ask.\n", []Finding{simplyAt(3), unused("filler", off, 1)}},
		{"another rule is not covered", "<!-- proofread off wordy: quoted -->\nSimply ask.\n", []Finding{
			simplyAt(2), unused("wordy", "<!-- proofread off wordy: quoted -->", 1),
		}},
		{"two rules, one idle", "<!-- proofread off filler, wordy: quoted -->\nSimply ask.\n", []Finding{
			unused("wordy", "<!-- proofread off filler, wordy: quoted -->", 1),
		}},
		{"a block up to the on comment", off + "\nSimply ask.\nSimply go.\n<!-- proofread on -->\nSimply stop.\n",
			[]Finding{
				{Rule: RuleFiller, Message: simplyMsg, Match: "Simply", Line: 5},
			}},
		{"an on comment closes only its rules",
			"<!-- proofread off filler: a -->\n<!-- proofread off wordy: b -->\nSimply ask in order to go.\n" +
				"<!-- proofread on filler -->\nSimply ask in order to go.\n",
			[]Finding{
				simplyAt(5),
				{Rule: RuleWordy, Message: "Write 'to', not 'in order to'.", Match: "in order to", Line: 5},
			}},
		{"a comment with no reason", "<!-- proofread off filler -->\nSimply ask.\n", []Finding{
			simplyAt(2), reasonless("<!-- proofread off filler -->", 1),
		}},
		{"a colon with no reason", "<!-- proofread off filler:  -->\nSimply ask.\n", []Finding{
			simplyAt(2), reasonless("<!-- proofread off filler:  -->", 1),
		}},
		{"a comment that matched nothing", off + "\nAsk.\n", []Finding{unused("filler", off, 1)}},
		{"a code span holds no suppression", "Write `" + off + "` first.\nSimply ask.\n", []Finding{simplyAt(2)}},
		{"a fenced block holds no suppression", "```\n" + off + "\n```\nSimply ask.\n", []Finding{simplyAt(4)}},
		{"a tilde fence", "~~~\n" + off + "\n~~~\nSimply ask.\n", []Finding{simplyAt(4)}},
		{"plain text", "proofread:ignore filler quoted from the user\nSimply ask.\n", nil},
		{"plain text with no reason", "proofread:ignore filler\nSimply ask.\n", []Finding{
			simplyAt(2), reasonless("proofread:ignore filler", 1),
		}},
		{"plain text names two rules", "proofread:ignore filler,wordy quoted\nSimply ask.\n", []Finding{
			unused("wordy", "proofread:ignore filler,wordy quoted", 1),
		}},
		{"plain text after other words", "Quoted text. proofread:ignore filler the user's words\nSimply ask.\n", nil},
		{"plain text unused", "proofread:ignore filler quoted\nAsk.\n", []Finding{
			unused("filler", "proofread:ignore filler quoted", 1),
		}},
		{"no suppression at all", "Simply ask.\n", []Finding{simplyAt(1)}},
	}

	opts := WithOnly(RuleFiller, RuleWordy, RuleSuppressionUnused)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindReference, opts), tc.want)
		})
	}
}

func TestSuppressionAppliesToEveryKind(t *testing.T) {
	for _, kind := range everyKind {
		if kind == KindAgentInstructionsTemplate || !slices.Contains(KindRules(kind), RuleFiller) {
			continue
		}

		t.Run(string(kind), func(t *testing.T) {
			text := "Title\n\nproofread:ignore filler quoted\nSimply ask.\n"
			got := JudgeText(text, kind, WithOnly(RuleFiller, RuleSuppressionUnused))

			assertFindings(t, got, nil)
		})
	}
}

func TestSuppressionInADocCommentCoversBudgets(t *testing.T) {
	long := strings.Repeat("word ", 260)

	cases := []struct {
		name string
		doc  string
		want []Rule
	}{
		{"budget with no suppression", long, []Rule{RuleCommentBlock}},
		{"a suppression anywhere covers the budget", long + "\n\nproofread:ignore comment-block generated table", nil},
		{"a suppression of another rule does not", long + "\n\nproofread:ignore filler generated table",
			[]Rule{RuleCommentBlock, RuleSuppressionUnused}},
		{"a line finding", "proofread:ignore filler quoted\nSimply ask.", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []Rule

			for _, f := range Judge(Symbol{Name: "Resolve", Doc: tc.doc}, houseOn,
				WithOnly(RuleCommentBlock, RuleFiller, RuleSuppressionUnused)) {
				got = append(got, f.Rule)
			}

			if !slices.Equal(got, tc.want) {
				t.Errorf("rules = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSuppressionUnusedIsReportedAtItsOwnPosition(t *testing.T) {
	const comment = "<!-- proofread off filler: quoted -->"

	got := JudgeText("Ask.\n  "+comment+"\n", KindReference, WithOnly(RuleSuppressionUnused))
	want := unused("filler", comment, 2)
	want.Column, want.EndLine, want.EndColumn = 3, 2, 3+len(comment)

	assertFindings(t, got, []Finding{want})
}

func TestSuppressionUnusedFollowsItsDecision(t *testing.T) {
	const text = "<!-- proofread off filler: quoted -->\nAsk.\n"

	advised := unused("filler", "<!-- proofread off filler: quoted -->", 1)
	advised.Decision = DecisionAdvise

	cases := []struct {
		name string
		opts []Option
		want []Finding
	}{
		{"deny by default", nil, []Finding{unused("filler", "<!-- proofread off filler: quoted -->", 1)}},
		{"advise by a table", []Option{WithDecisions(map[Rule]Decision{RuleSuppressionUnused: DecisionAdvise})},
			[]Finding{advised}},
		{"off by a table", []Option{WithDecisions(map[Rule]Decision{RuleSuppressionUnused: DecisionOff})}, nil},
		{"off when only names another rule", []Option{WithOnly(RuleFiller)}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(text, KindReference, tc.opts...), tc.want)
		})
	}
}

func TestSuppressionStillSuppressesWhenItsRuleIsOff(t *testing.T) {
	const text = "<!-- proofread off filler: quoted -->\nSimply ask.\n"

	off := WithDecisions(map[Rule]Decision{RuleSuppressionUnused: DecisionOff})

	assertFindings(t, JudgeText(text, KindReference, off), nil)
	assertFindings(t, JudgeText("<!-- proofread off filler -->\nSimply ask.\n", KindReference, off), []Finding{simplyAt(2)})
}

func TestSuppressionUnusedIsADefaultDenyRuleOnEveryKind(t *testing.T) {
	var doc RuleDoc

	for _, d := range Catalog() {
		if d.Name == RuleSuppressionUnused {
			doc = d
		}
	}

	if doc.Code != "PRF1090" || doc.House {
		t.Fatalf("catalog entry = %+v, want PRF1090 and not house", doc)
	}

	if len(doc.Kinds) != len(everyKind) {
		t.Errorf("judges %d kinds, want %d", len(doc.Kinds), len(everyKind))
	}

	for _, k := range doc.Kinds {
		if doc.Decisions[k] != DecisionDeny {
			t.Errorf("%s: default %q, want deny", k, doc.Decisions[k])
		}
	}
}

func TestEveryKindIsListedInEveryKind(t *testing.T) {
	seen := map[Kind]bool{}

	for _, c := range checks {
		for _, k := range c.on {
			seen[k] = true
		}
	}

	for k := range seen {
		if !slices.Contains(everyKind, k) {
			t.Errorf("kind %q is judged by a rule and missing from everyKind", k)
		}
	}
}

func TestClosesFenceNeedsAsLongAFenceAndNothingAfterIt(t *testing.T) {
	cases := []struct {
		line, marker string
		want         bool
	}{
		{"```", "```", true},
		{"````", "```", true},
		{"  ```  ", "```", true},
		{"```", "````", false},
		{"```go", "```", false},
		{"~~~", "```", false},
		{"~~~", "~~~", true},
		{"text", "```", false},
	}

	for _, tc := range cases {
		if got := closesFence(tc.line, tc.marker); got != tc.want {
			t.Errorf("closesFence(%q, %q) = %v, want %v", tc.line, tc.marker, got, tc.want)
		}
	}
}
