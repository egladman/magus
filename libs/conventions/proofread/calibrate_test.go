package proofread

import (
	"fmt"
	"math"
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

func TestLoadCasesJudgesAFileUnderItsAuthorSections(t *testing.T) {
	var authored strings.Builder
	for i := range MinVoiceTexts {
		fmt.Fprintf(&authored, "I think case %d works. Let's merge it.\n", i)
	}

	fsys := fstest.MapFS{
		"voice-drift.txtar": {Data: []byte("-- author/review-reply --\n" + authored.String() +
			"-- author/review-reply whole --\nIt's fine.\nShip it.\n-- pass/review-reply --\nI think it works.\n")},
		"filler.txtar": {Data: []byte("-- hit/reference --\nIt simply works.\n")},
	}

	got, err := LoadCases(fsys)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0].Voice != nil {
		t.Fatalf("cases = %+v, want the filler case with no voice and one voice-drift case", got)
	}

	if v := got[1].Voice; v == nil || v.Kinds[KindReviewReply].Texts != MinVoiceTexts+1 || got[1].Text != "I think it works." {
		t.Errorf("voice-drift case = %+v, want one judged under a voice of %d replies", got[1], MinVoiceTexts+1)
	}
}

func TestLoadCasesRefusesAFileItCannotRead(t *testing.T) {
	cases := map[string]struct{ name, data, want string }{
		"no such rule":     {"nope.txtar", "", `nope.txtar: no rule is named "nope"`},
		"no label":         {"filler.txtar", "-- reference --\nx\n", `filler.txtar:1: section "reference" needs a <label>/<kind> header`},
		"an unknown label": {"filler.txtar", "c\n-- miss/reference --\nx\n", `filler.txtar:2: label "miss" is none of hit, pass, fp, fn or author`},
		"an author kind with no voice": {"filler.txtar", "-- author/reference --\nx\n",
			"filler.txtar: the author sections: a voice measures change-description, review-reply, commit-message and issue, not reference"},
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
		{Rule: RuleFiller, Cases: 1, TN: 1, HeldOut: 1},
		{
			Rule: RuleHedge, Cases: 5, TP: 1, FP: 1, FN: 2, TN: 1, Precision: ptr(0.5),
			Lower: ptr(wilsonLower(0.5, 2)), Recall: ptr(1.0 / 3), HeldOut: 3, Disagree: 1,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("scores:\n got %s\nwant %s", scoreText(got), scoreText(want))
	}
}

func scoreText(scores []Score) string {
	var b strings.Builder

	for _, s := range scores {
		fmt.Fprintf(&b, "%s %d/%d/%d/%d/%d disagree %d precision %s lower %s recall %s held out %d at %s\n",
			s.Rule, s.Cases, s.TP, s.FP, s.FN, s.TN, s.Disagree, ratioText(s.Precision), ratioText(s.Lower), ratioText(s.Recall),
			s.HeldOut, ratioText(s.HeldOutPrecision))
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

func TestWilsonLowerMatchesTheMethodTable(t *testing.T) {
	cases := []struct {
		n, wrong int
		want     float64
	}{
		{20, 0, 0.839}, {20, 2, 0.699}, {30, 0, 0.886}, {30, 4, 0.703}, {50, 1, 0.895}, {100, 4, 0.902},
	}

	for _, tc := range cases {
		p := float64(tc.n-tc.wrong) / float64(tc.n)
		if got := wilsonLower(p, tc.n); math.Abs(got-tc.want) > 0.0005 {
			t.Errorf("%d wrong of %d: lower bound %.4f, want %.3f", tc.wrong, tc.n, got, tc.want)
		}
	}
}

func TestDenyLowerTakesZeroOf35OneOf53TwoOf69(t *testing.T) {
	for wrong, n := range []int{35, 53, 69} {
		reaches := func(n int) bool { return wilsonLower(float64(n-wrong)/float64(n), n) >= DenyLower }
		if !reaches(n) || reaches(n-1) {
			t.Errorf("%d wrong: deny at %d is %v and at %d is %v, want only the first", wrong, n, reaches(n), n-1, reaches(n-1))
		}
	}

	if n, ok := firingsNeeded(1, DenyLower); !ok || n != 35 {
		t.Errorf("deny at precision 1 needs %d %v, want 35", n, ok)
	}

	if n, ok := firingsNeeded(1, AdviseLower); !ok || n != GateFirings {
		t.Errorf("advise at precision 1 needs %d %v, want the floor %d", n, ok, GateFirings)
	}

	if _, ok := firingsNeeded(0.9, DenyLower); ok {
		t.Error("a precision of 0.9 reaches a lower bound of 0.9")
	}
}

func TestHeldOutIsAStableFifth(t *testing.T) {
	held := 0

	for i := range 2000 {
		c := Case{Text: fmt.Sprintf("The cache key sorts input %d.", i)}
		if c.HeldOut() != (Case{Text: c.Text, Rule: RuleHedge, Label: LabelHit, Line: i}).HeldOut() {
			t.Fatalf("%q: the split depends on more than the text", c.Text)
		}

		if c.HeldOut() {
			held++
		}
	}

	if held < 340 || held > 460 {
		t.Errorf("%d of 2000 held out, want about 400", held)
	}
}

func TestCalibrateScoresTheHeldOutCasesAlone(t *testing.T) {
	text := func(held bool, format string) string {
		for i := 0; ; i++ {
			if s := fmt.Sprintf(format, i); (Case{Text: s}).HeldOut() == held {
				return s
			}
		}
	}

	cases := []Case{
		{Rule: RuleHedge, Kind: KindReference, Label: LabelHit, Text: text(true, "It may help runner %d.")},
		{Rule: RuleHedge, Kind: KindReference, Label: LabelFalsePositive, Text: text(true, "It probably holds for %d.")},
		{Rule: RuleHedge, Kind: KindReference, Label: LabelHit, Text: text(false, "It may help runner %d.")},
		{Rule: RuleHedge, Kind: KindReference, Label: LabelPass, Text: text(true, "Runner %d holds.")},
	}

	got := Calibrate(cases)
	if len(got) != 1 || got[0].HeldOut != 3 || got[0].HeldOutPrecision == nil || *got[0].HeldOutPrecision != 0.5 {
		t.Errorf("scores: %s", scoreText(got))
	}

	if none := Calibrate(cases[2:3]); none[0].HeldOut != 0 || none[0].HeldOutPrecision != nil {
		t.Errorf("no case held out: %s", scoreText(none))
	}
}

func TestGatesHoldTheShippedDefaultAgainstTheEvidence(t *testing.T) {
	gate := func(rule Rule, tp, fp int) Gate { return Gates([]Score{{Rule: rule, TP: tp, FP: fp}})[0] }

	needs := func(g Gate) string {
		if g.Needs == nil {
			return "unreachable"
		}

		return fmt.Sprint(*g.Needs)
	}

	cases := map[string]struct {
		gate              Gate
		shipped, supports Decision
		pass              bool
		needs             string
	}{
		"deny at 0 of 35":                 {gate(RuleHedge, 35, 0), DecisionDeny, DecisionDeny, true, "0"},
		"deny at 0 of 10":                 {gate(RuleHedge, 10, 0), DecisionDeny, DecisionOff, false, "25"},
		"deny at 1 of 10":                 {gate(RuleHedge, 9, 1), DecisionDeny, DecisionOff, false, "unreachable"},
		"deny with no firing":             {gate(RuleHedge, 0, 0), DecisionDeny, DecisionOff, false, "35"},
		"deny at 4 of 30 supports advise": {gate(RuleHedge, 26, 4), DecisionDeny, DecisionAdvise, false, "unreachable"},
		"advise at 4 of 30":               {gate(RuleVerdict, 26, 4), DecisionAdvise, DecisionAdvise, true, "0"},
		"advise at 0 of 29":               {gate(RuleVerdict, 29, 0), DecisionAdvise, DecisionOff, false, "1"},
		"house style with no firing":      {gate(RuleDash, 0, 0), DecisionOff, DecisionOff, true, "0"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g := tc.gate
			if g.Shipped != tc.shipped || g.Supports != tc.supports || g.Pass != tc.pass || needs(g) != tc.needs {
				t.Errorf("shipped %s supports %s pass %v needs %s, want %s %s %v %s",
					g.Shipped, g.Supports, g.Pass, needs(g), tc.shipped, tc.supports, tc.pass, tc.needs)
			}

			if deny := g.Shipped == DecisionDeny; (len(g.Unmeasured) == 2) != deny {
				t.Errorf("unmeasured %v on a %s rule", g.Unmeasured, g.Shipped)
			}
		})
	}

	g := gate(RuleHedge, 9, 1)
	if g.Dimension != DimensionEvidence || g.Firings != 10 || g.Wrong != 1 || *g.Precision != 0.9 ||
		math.Abs(*g.Lower-wilsonLower(0.9, 10)) > 1e-12 {
		t.Errorf("gate = %+v", g)
	}
}
