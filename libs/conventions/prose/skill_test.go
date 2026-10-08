package prose

import (
	"slices"
	"strings"
	"testing"
)

// renderForms renders a skill body in both forms, failing the test on a body
// that does not render.
func renderForms(t *testing.T, body string) (short, full string) {
	t.Helper()

	tree, err := parseSkill(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	forms := make([]string, 2)

	for i, f := range []bool{false, true} {
		form, err := renderSkill(body, tree, f)
		if err != nil {
			t.Fatalf("render: %v", err)
		}

		forms[i] = form.text
	}

	return forms[0], forms[1]
}

func TestRenderSkillTakesEachArmTheWayTheVariantDoes(t *testing.T) {
	cases := []struct {
		name, body, short, full string
	}{
		{"text outside any arm", "Run it.", "Run it.", "Run it."},
		{"a full arm", "Run it{{if .Full}}, since the cache keys it{{end}}.", "Run it.", "Run it, since the cache keys it."},
		{"an else arm", "Run it{{if .Full}} first, always{{else}} first{{end}}.", "Run it first.", "Run it first, always."},
		{"a short arm", "Run it.{{if .Short}} Full says why.{{end}}", "Run it. Full says why.", "Run it."},
		{"an Is arm", `{{if .Is "short"}}S{{else if .Is "full"}}F{{end}}`, "S", "F"},
		{"an Is arm no form takes", `{{if .Is "minimal"}}M{{else if .Full}}F{{else}}S{{end}}`, "S", "F"},
		{"a short arm nested in a full arm", "A{{if .Full}}B{{if .Short}}C{{else}}D{{end}}{{end}}E", "AE", "ABDE"},
		{"a full arm nested in an else arm", "A{{if .Full}}B{{else}}C{{if .Full}}D{{end}}E{{end}}", "ACE", "AB"},
		{"trim markers", "A\n{{- if .Full}}\nB{{end}}\nC", "A\nC", "A\nB\nC"},
		{"a lookup renders its argument", `Run {{cmd "agent install"}} and {{"{{.Field}}"}}.`,
			"Run agent install and {{.Field}}.", "Run agent install and {{.Field}}."},
		{"a comment renders nothing", "A{{/* why */}}B", "AB", "AB"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			short, full := renderForms(t, tc.body)
			if short != tc.short || full != tc.full {
				t.Errorf("render %q:\n got short %q full %q\nwant short %q full %q", tc.body, short, full, tc.short, tc.full)
			}
		})
	}
}

// longSentence is one sentence of n words.
func longSentence(n int) string { return wordRun(n-1) + " end." }

func TestJudgeSkillSourceHoldsOnlyTheShortFormToTheTerseRules(t *testing.T) {
	long := longSentence(maxSkillSentenceWords + 1)
	terse := Finding{
		Rule: RuleTerseSentence, Line: 3,
		Message: "Keep a skill sentence to 35 words (this one has 36): split it, or make its steps a list.",
	}

	cases := []struct {
		name string
		body string
		want []Finding
	}{
		{"outside any arm", "# Skill\n\n" + long + "\n", []Finding{terse}},
		{"in a full arm", "# Skill\n\n{{if .Full}}" + long + "{{end}}\n", nil},
		{"in an else arm", "# Skill\n\n{{if .Full}}Short.{{else}}" + long + "{{end}}\n", []Finding{terse}},
		{"in a short arm nested in a full arm", "# Skill\n\n{{if .Full}}{{if .Short}}" + long + "{{end}}{{end}}\n", nil},
		{"in an else arm nested in an else arm", "# Skill\n\n{{if .Full}}A.{{else}}{{if .Full}}B.{{else}}" + long +
			"{{end}}{{end}}\n", []Finding{terse}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.body, SurfaceSkillSource), tc.want)
		})
	}
}

func TestJudgeSkillSourceHoldsFullOnlyTextToTheMarkdownRules(t *testing.T) {
	body := "# Skill\n\nRun it.{{if .Full}} The cache simply keys it, in order to replay.{{end}}\n"

	assertFindings(t, JudgeText(body, SurfaceSkillSource), []Finding{
		{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply", Line: 3},
	})
}

func TestJudgeSkillSourceReportsSharedTextOnce(t *testing.T) {
	body := "# Skill\n\nThe cache simply keys it{{if .Full}}, always{{else}}, now{{end}}.\n"

	assertFindings(t, JudgeText(body, SurfaceSkillSource), []Finding{
		{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply", Line: 3},
	})
}

// A finding names the source line its text sits on, however many lines the
// arms above it dropped from the form it was found in.
func TestJudgeSkillSourceReportsAtTheSourceLine(t *testing.T) {
	body := "# Skill\n\n{{if .Full}}One.\n\nTwo.\n\nThree.\n{{end}}\nRun it in order to replay.\n\n" +
		"{{if .Full}}Long.\n{{else}}Short.\n{{end}}\nThe cache simply keys it.\n"

	assertFindings(t, JudgeText(body, SurfaceSkillSource), []Finding{
		{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply", Line: 14},
		{Rule: RuleWordy, Message: "Write 'to', not 'in order to'.", Match: "in order to", Line: 9},
	})
}

func TestJudgeSkillSourceReportsABodyThatDoesNotRender(t *testing.T) {
	cases := map[string]string{
		"a missing end":             "Run it{{if .Full}}, since.",
		"a stray else":              "Run it{{else}}.",
		"a branch on another field": "Run it{{if .Verbose}}, since{{end}}.",
		"a range":                   "{{range .Steps}}step{{end}}",
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got := JudgeText(body, SurfaceSkillSource)
			if len(got) != 1 || got[0].Rule != RuleTemplate || !strings.HasPrefix(got[0].Message, "The skill body does not render: ") {
				t.Errorf("findings = %#v, want one template finding", got)
			}
		})
	}
}

func TestSourceLinesMapsEachRenderedLine(t *testing.T) {
	body := "a\n{{if .Full}}b\nc\n{{end}}d\ne"

	tree, err := parseSkill(body)
	if err != nil {
		t.Fatal(err)
	}

	form, err := renderSkill(body, tree, false)
	if err != nil {
		t.Fatal(err)
	}

	if form.text != "a\nd\ne" {
		t.Fatalf("short = %q", form.text)
	}

	if got, want := form.lines, []int{1, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("lines = %v, want %v", got, want)
	}
}
