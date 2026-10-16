package calibrate

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/egladman/magus/libs/proofread"
)

func TestLabeledCasesAgreeWithTheRules(t *testing.T) {
	cases, err := LoadCases(LabeledCases())
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range cases {
		if !c.Agrees() {
			t.Errorf("cases/%s:%d: %s judged as %s is labeled %s, but fires=%v:\n%s",
				c.File, c.Line, c.Rule, c.Kind, c.Label, c.Fires(), c.Text)
		}
	}
}

func TestEveryRuleHasAHitAndAPassCase(t *testing.T) {
	cases, err := LoadCases(LabeledCases())
	if err != nil {
		t.Fatal(err)
	}

	labels := map[proofread.Rule]map[label]bool{}

	for _, c := range cases {
		if labels[c.Rule] == nil {
			labels[c.Rule] = map[label]bool{}
		}

		labels[c.Rule][c.Label] = true
	}

	for _, r := range proofread.Rules() {
		if !labels[r][labelHit] || !labels[r][labelPass] {
			t.Errorf("cases/%s.txtar needs a hit and a pass case, has %v", r, labels[r])
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

	sym := proofread.Symbol{Name: "Close", Owner: "Pool", Callable: true}
	want := []labeledCase{
		{File: "docstub.txtar", Line: 4, Rule: proofread.RuleDocStub, Kind: proofread.KindDocComment, Label: labelHit, Text: "Close closes.", Symbol: sym},
		{File: "docstub.txtar", Line: 6, Rule: proofread.RuleDocStub, Kind: proofread.KindDocComment, Label: labelHit, Text: "Close closes the Pool.", Symbol: sym},
		{
			File: "docstub.txtar", Line: 8, Rule: proofread.RuleDocStub, Kind: proofread.KindDocComment, Label: labelPass,
			Text: "Open dials.\n\nIt blocks.", Symbol: proofread.Symbol{Name: "Open"},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("cases:\n got %+v\nwant %+v", got, want)
	}
}

func TestLoadCasesAcceptsTheLabelACaseCarriedBeforeAMove(t *testing.T) {
	noted := fstest.MapFS{"absolute.txtar": {Data: []byte("-- pass/change-description was=fp:round4:r4-0022 --\n- Nobody declared it.\n")}}
	if got, err := LoadCases(noted); err != nil || len(got) != 1 || got[0].Label != labelPass {
		t.Errorf("LoadCases(was=fp:round4:r4-0022) = %+v, %v; want one pass case", got, err)
	}

	for _, bad := range []string{"was=fp:round4", "was=fp::r4-0022", "was="} {
		fsys := fstest.MapFS{"absolute.txtar": {Data: []byte("-- pass/change-description " + bad + " --\n- Nobody declared it.\n")}}
		if _, err := LoadCases(fsys); err == nil {
			t.Errorf("LoadCases(%s) = nil error; want a malformed note refused", bad)
		}
	}
}

func TestAnInlineCaseIsJudgedAsAnInlineComment(t *testing.T) {
	fsys := fstest.MapFS{"nonspecific.txtar": {Data: []byte("-- fp/review-reply --\nThis is incorrect.\n" +
		"-- pass/review-inline --\nThis is incorrect.\n")}}

	got, err := LoadCases(fsys)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[1].Kind != proofread.KindReviewInline || !got[0].Fires() || got[1].Fires() {
		t.Errorf("cases = %+v; want the reply to fire and the inline comment not", got)
	}
}

func TestLoadCasesReadsAReviewEvent(t *testing.T) {
	fsys := fstest.MapFS{"approve-blocker.txtar": {Data: []byte(
		"-- hit/review-summary event=APPROVE --\nLooks good, but this must fix the lock first.\n")}}

	got, err := LoadCases(fsys)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Event != proofread.EventApprove {
		t.Errorf("cases = %+v; want one APPROVE case", got)
	}

	bad := fstest.MapFS{"approve-blocker.txtar": {Data: []byte("-- hit/review-summary event=MERGE --\nShip it.\n")}}
	if _, err := LoadCases(bad); err == nil {
		t.Error("LoadCases(event=MERGE) = nil error; want an unknown event refused")
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
	for i := range proofread.MinVoiceTexts {
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

	if v := got[1].Voice; v == nil || v.Kinds[proofread.KindReviewReply].Texts != proofread.MinVoiceTexts+1 || got[1].Text != "I think it works." {
		t.Errorf("voice-drift case = %+v, want one judged under a voice of %d replies", got[1], proofread.MinVoiceTexts+1)
	}
}

func TestLoadCasesRefusesAFileItCannotRead(t *testing.T) {
	cases := map[string]struct{ name, data, want string }{
		"no such rule":     {"nope.txtar", "", `nope.txtar: no rule is named "nope"`},
		"no label":         {"filler.txtar", "-- reference --\nx\n", `filler.txtar:1: section "reference" needs a <label>/<kind> header`},
		"an unknown label": {"filler.txtar", "c\n-- miss/reference --\nx\n", `filler.txtar:2: label "miss" is none of hit, pass, fp, fn or author`},
		"an author kind with no voice": {"filler.txtar", "-- author/reference --\nx\n",
			"filler.txtar: the author sections: a voice measures change-description, review-reply, commit-message and issue, not reference"},
		"a kind not judged": {"filler.txtar", "-- hit/diagnostic --\nx\n", `filler.txtar:1: rule filler does not judge kind "diagnostic"`},
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
	cases := []labeledCase{
		{Rule: proofread.RuleHedge, Kind: proofread.KindReference, Label: labelHit, Text: "It may help."},
		{Rule: proofread.RuleHedge, Kind: proofread.KindReference, Label: labelHit, Text: "It holds."},
		{Rule: proofread.RuleHedge, Kind: proofread.KindReference, Label: labelPass, Text: "It holds."},
		{Rule: proofread.RuleHedge, Kind: proofread.KindReference, Label: labelFalsePositive, Text: "It probably holds."},
		{Rule: proofread.RuleHedge, Kind: proofread.KindReference, Label: labelFalseNegative, Text: "It holds."},
		{Rule: proofread.RuleFiller, Kind: proofread.KindReference, Label: labelPass, Text: "It holds."},
	}

	got := Scores(cases)

	ptr := func(f float64) *float64 { return &f }

	want := []Score{
		{Rule: proofread.RuleFiller, Cases: 1, TN: 1, HeldOut: 1},
		{
			Rule: proofread.RuleHedge, Cases: 5, TP: 1, FP: 1, FN: 2, TN: 1, Precision: ptr(0.5),
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
	tally := NewTally(proofread.KindReviewReply)
	tally.Add("User", "Let me delve into the race, then delve into the lock.")
	tally.Add("User", "The map races.")
	tally.Add("Bot", "The key sorts.")

	rates := tally.Rates()
	if len(rates) != 2 || rates[0].Group != "Bot" || rates[1].Group != "User" || rates[1].Texts != 2 {
		t.Fatalf("groups = %+v", rates)
	}

	for _, r := range rates[1].Rules {
		if r.Rule == proofread.RuleBuzzword {
			want := rate{
				Rule: proofread.RuleBuzzword, Decision: proofread.DecisionDeny, Findings: 2, PerThousand: 1000, Texts: 1,
				Share: 0.5, DeniedTexts: 1,
			}
			if r != want {
				t.Errorf("buzzword = %+v, want %+v", r, want)
			}

			return
		}
	}

	t.Error("no buzzword rate for review-reply")
}

func TestTallyLeavesOutRulesOffForTheKind(t *testing.T) {
	for _, r := range NewTally(proofread.KindReviewReply).rules {
		if r.Rule == proofread.RuleDash || r.Rule == proofread.RuleHedge {
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
		reaches := func(n int) bool { return wilsonLower(float64(n-wrong)/float64(n), n) >= denyLower }
		if !reaches(n) || reaches(n-1) {
			t.Errorf("%d wrong: deny at %d is %v and at %d is %v, want only the first", wrong, n, reaches(n), n-1, reaches(n-1))
		}
	}

	if n, ok := firingsNeeded(1, denyLower); !ok || n != 35 {
		t.Errorf("deny at precision 1 needs %d %v, want 35", n, ok)
	}

	if n, ok := firingsNeeded(1, adviseLower); !ok || n != gateFirings {
		t.Errorf("advise at precision 1 needs %d %v, want the floor %d", n, ok, gateFirings)
	}

	if _, ok := firingsNeeded(0.9, denyLower); ok {
		t.Error("a precision of 0.9 reaches a lower bound of 0.9")
	}
}

func TestHeldOutIsAStableFifth(t *testing.T) {
	held := 0

	for i := range 2000 {
		c := labeledCase{Text: fmt.Sprintf("The cache key sorts input %d.", i)}
		if c.HeldOut() != (labeledCase{Text: c.Text, Rule: proofread.RuleHedge, Label: labelHit, Line: i}).HeldOut() {
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
			if s := fmt.Sprintf(format, i); (labeledCase{Text: s}).HeldOut() == held {
				return s
			}
		}
	}

	cases := []labeledCase{
		{Rule: proofread.RuleHedge, Kind: proofread.KindReference, Label: labelHit, Text: text(true, "It may help runner %d.")},
		{Rule: proofread.RuleHedge, Kind: proofread.KindReference, Label: labelFalsePositive, Text: text(true, "It probably holds for %d.")},
		{Rule: proofread.RuleHedge, Kind: proofread.KindReference, Label: labelHit, Text: text(false, "It may help runner %d.")},
		{Rule: proofread.RuleHedge, Kind: proofread.KindReference, Label: labelPass, Text: text(true, "Runner %d holds.")},
	}

	got := Scores(cases)
	if len(got) != 1 || got[0].HeldOut != 3 || got[0].HeldOutPrecision == nil || *got[0].HeldOutPrecision != 0.5 {
		t.Errorf("scores: %s", scoreText(got))
	}

	if none := Scores(cases[2:3]); none[0].HeldOut != 0 || none[0].HeldOutPrecision != nil {
		t.Errorf("no case held out: %s", scoreText(none))
	}
}

// shipping returns a rule whose strictest shipped default is d, so a gate
// test holds whatever the evidence ceiling caps today.
func shipping(t *testing.T, d proofread.Decision) proofread.Rule {
	t.Helper()

	for _, doc := range proofread.Catalog() {
		strictest := proofread.DecisionOff
		for _, k := range doc.Kinds {
			if decisionRank(doc.Decisions[k]) > decisionRank(strictest) {
				strictest = doc.Decisions[k]
			}
		}

		if strictest == d {
			return doc.Name
		}
	}

	t.Fatalf("no rule ships %s", d)

	return ""
}

func TestGatesHoldTheShippedDefaultAgainstTheEvidence(t *testing.T) {
	gate := func(rule proofread.Rule, tp, fp int) Gate { return Gates([]Score{{Rule: rule, TP: tp, FP: fp}})[0] }
	deny, advise, off := shipping(t, proofread.DecisionDeny), shipping(t, proofread.DecisionAdvise), shipping(t, proofread.DecisionOff)

	needs := func(g Gate) string {
		if g.Needs == nil {
			return "unreachable"
		}

		return fmt.Sprint(*g.Needs)
	}

	cases := map[string]struct {
		gate              Gate
		shipped, supports proofread.Decision
		pass              bool
		needs             string
	}{
		"deny at 0 of 35":                 {gate(deny, 35, 0), proofread.DecisionDeny, proofread.DecisionDeny, true, "0"},
		"deny at 0 of 10":                 {gate(deny, 10, 0), proofread.DecisionDeny, proofread.DecisionOff, false, "25"},
		"deny at 1 of 10":                 {gate(deny, 9, 1), proofread.DecisionDeny, proofread.DecisionOff, false, "unreachable"},
		"deny with no firing":             {gate(deny, 0, 0), proofread.DecisionDeny, proofread.DecisionOff, false, "35"},
		"deny at 4 of 30 supports advise": {gate(deny, 26, 4), proofread.DecisionDeny, proofread.DecisionAdvise, false, "unreachable"},
		"advise at 4 of 30":               {gate(advise, 26, 4), proofread.DecisionAdvise, proofread.DecisionAdvise, true, "0"},
		"advise at 0 of 29":               {gate(advise, 29, 0), proofread.DecisionAdvise, proofread.DecisionOff, false, "1"},
		"off with no firing":              {gate(off, 0, 0), proofread.DecisionOff, proofread.DecisionOff, true, "0"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g := tc.gate
			if g.Shipped != tc.shipped || g.Supports != tc.supports || g.Pass != tc.pass || needs(g) != tc.needs {
				t.Errorf("shipped %s supports %s pass %v needs %s, want %s %s %v %s",
					g.Shipped, g.Supports, g.Pass, needs(g), tc.shipped, tc.supports, tc.pass, tc.needs)
			}

			if deny := g.Shipped == proofread.DecisionDeny; (len(g.Unmeasured) == 2) != deny {
				t.Errorf("unmeasured %v on a %s rule", g.Unmeasured, g.Shipped)
			}
		})
	}

	g := gate(proofread.RuleHedge, 9, 1)
	if g.Dimension != proofread.DimensionEvidence || g.Firings != 10 || g.Wrong != 1 || *g.Precision != 0.9 ||
		math.Abs(*g.Lower-wilsonLower(0.9, 10)) > 1e-12 {
		t.Errorf("gate = %+v", g)
	}
}
