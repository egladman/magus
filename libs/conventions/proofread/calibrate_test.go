package proofread

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLabeledCasesAgreeWithTheRules(t *testing.T) {
	cases, err := LoadCases(LabeledCases())
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range cases {
		if !c.Agrees() {
			t.Errorf("testdata/cases/%s:%d: %s judged as %s is labeled %s, but fires=%v:\n%s",
				c.File, c.Line, c.Rule, c.Kind, c.Label, c.Fires(), c.Text)
		}
	}
}

func TestEveryRuleHasAHitAndAPassCase(t *testing.T) {
	cases, err := LoadCases(LabeledCases())
	if err != nil {
		t.Fatal(err)
	}

	labels := map[Rule]map[Label]bool{}

	for _, c := range cases {
		if labels[c.Rule] == nil {
			labels[c.Rule] = map[Label]bool{}
		}

		labels[c.Rule][c.Label] = true
	}

	for _, r := range Rules() {
		if !labels[r][LabelHit] || !labels[r][LabelPass] {
			t.Errorf("testdata/cases/%s.txtar needs a hit and a pass case, has %v", r, labels[r])
		}
	}
}

func TestLoadCasesReadsHeadersLinesAndWholeSections(t *testing.T) {
	fsys := fstest.MapFS{"docstub.txtar": {Data: []byte("Comment line one.\nline two.\n" +
		"-- hit/doc-comment name=Close owner=Pool callable --\n" +
		"Close closes.\n\nClose closes the Pool.\n" +
		"-- pass/doc-comment name=Open whole --\n" +
		"Open dials.\n\nIt blocks.\n")}}

	got, err := LoadCases(fsys)
	if err != nil {
		t.Fatal(err)
	}

	sym := Symbol{Name: "Close", Owner: "Pool", Callable: true}
	want := []Case{
		{File: "docstub.txtar", Line: 4, Rule: RuleDocStub, Kind: KindDocComment, Label: LabelHit, Text: "Close closes.", Symbol: sym},
		{File: "docstub.txtar", Line: 6, Rule: RuleDocStub, Kind: KindDocComment, Label: LabelHit, Text: "Close closes the Pool.", Symbol: sym},
		{
			File: "docstub.txtar", Line: 8, Rule: RuleDocStub, Kind: KindDocComment, Label: LabelPass,
			Text: "Open dials.\n\nIt blocks.", Symbol: Symbol{Name: "Open"},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("cases:\n got %+v\nwant %+v", got, want)
	}
}

func TestLoadCasesJudgesAChangeDescriptionLineUnderALead(t *testing.T) {
	fsys := fstest.MapFS{"claim.txtar": {Data: []byte("-- hit/change-description --\n- Fixes it.\n" +
		"-- pass/review-reply thread=2 --\nIt holds.\n")}}

	got, err := LoadCases(fsys)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0].Text != caseLead+"- Fixes it." || got[1].Text != "It holds." || got[1].ThreadLength != 2 {
		t.Errorf("cases = %+v", got)
	}
}

func TestLoadCasesRefusesAFileItCannotRead(t *testing.T) {
	cases := map[string]struct{ name, data, want string }{
		"no such rule":      {"nope.txtar", "", `nope.txtar: no rule is named "nope"`},
		"no label":          {"filler.txtar", "-- reference --\nx\n", `filler.txtar:1: section "reference" needs a <label>/<kind> header`},
		"an unknown label":  {"filler.txtar", "c\n-- miss/reference --\nx\n", `filler.txtar:2: label "miss" is none of hit, pass, fp or fn`},
		"a kind not judged": {"filler.txtar", "-- hit/message --\nx\n", `filler.txtar:1: rule filler does not judge kind "message"`},
		"an unknown attr":   {"filler.txtar", "-- hit/reference loud --\nx\n", `filler.txtar:1: unknown attribute "loud"`},
		"a bad thread":      {"long-thread.txtar", "-- hit/review-reply thread=x --\nx\n", `long-thread.txtar:1: thread="x" is not a count`},
		"a later section":   {"filler.txtar", "-- hit/reference --\nx\ny\n-- hit/nope --\nz\n", `filler.txtar:4: rule filler does not judge kind "nope"`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadCases(fstest.MapFS{tc.name: {Data: []byte(tc.data)}})
			if err == nil || err.Error() != tc.want {
				t.Errorf("err = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestCalibrateTalliesEachLabelAsTheRuleJudgesIt(t *testing.T) {
	cases := []Case{
		{Rule: RuleHedge, Kind: KindReference, Label: LabelHit, Text: "It may help."},
		{Rule: RuleHedge, Kind: KindReference, Label: LabelHit, Text: "It holds."},
		{Rule: RuleHedge, Kind: KindReference, Label: LabelPass, Text: "It holds."},
		{Rule: RuleHedge, Kind: KindReference, Label: LabelFalsePositive, Text: "It probably holds."},
		{Rule: RuleHedge, Kind: KindReference, Label: LabelFalseNegative, Text: "It holds."},
		{Rule: RuleFiller, Kind: KindReference, Label: LabelPass, Text: "It holds."},
	}

	got := Calibrate(cases)

	ptr := func(f float64) *float64 { return &f }

	want := []Score{
		{Rule: RuleFiller, Cases: 1, TN: 1},
		{Rule: RuleHedge, Cases: 5, TP: 1, FP: 1, FN: 2, TN: 1, Precision: ptr(0.5), Recall: ptr(1.0 / 3), Disagree: 1},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("scores:\n got %s\nwant %s", scoreText(got), scoreText(want))
	}
}

func scoreText(scores []Score) string {
	var b strings.Builder

	for _, s := range scores {
		fmt.Fprintf(&b, "%s %d/%d/%d/%d/%d disagree %d precision %s recall %s\n",
			s.Rule, s.Cases, s.TP, s.FP, s.FN, s.TN, s.Disagree, ratioText(s.Precision), ratioText(s.Recall))
	}

	return b.String()
}

func ratioText(p *float64) string {
	if p == nil {
		return "null"
	}

	return fmt.Sprintf("%.3f", *p)
}

func TestTallyCountsFindingsAndTextsPerGroup(t *testing.T) {
	tally := NewTally(KindReviewReply)
	tally.Add("User", "Actually, it simply races.")
	tally.Add("User", "The map races.")
	tally.Add("Bot", "Great question, the key sorts.")

	rates := tally.Rates()
	if len(rates) != 2 || rates[0].Group != "Bot" || rates[1].Group != "User" || rates[1].Texts != 2 {
		t.Fatalf("groups = %+v", rates)
	}

	for _, r := range rates[1].Rules {
		if r.Rule == RuleFiller {
			want := Rate{Rule: RuleFiller, Decision: DecisionDeny, Findings: 2, PerThousand: 1000, Texts: 1, Share: 0.5, DeniedTexts: 1}
			if r != want {
				t.Errorf("filler = %+v, want %+v", r, want)
			}

			return
		}
	}

	t.Error("no filler rate for review-reply")
}

func TestTallyLeavesOutRulesOffForTheKind(t *testing.T) {
	for _, r := range NewTally(KindReviewReply).rules {
		if r.Rule == RuleDash || r.Rule == RuleHedge {
			t.Errorf("%s runs on review-reply by default", r.Rule)
		}
	}
}
