package prose

import (
	"fmt"
	"testing"
)

func bareRuleFinding(line int, m string) Finding {
	return Finding{
		Rule: RuleBareRule,
		Message: fmt.Sprintf("Qualify '%s' with what enforces it (a guard, lint or workspace rule, or its id "+
			"in code), or write it as an instruction: a rule is only what magus refuses.", m),
		Match: m, Line: line,
	}
}

func TestBareRuleRefusesGuidanceCalledARule(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a sentence", "Follow the rule before you spawn.", []Finding{bareRuleFinding(1, "rule")}},
		{"a plural", "Four rules, in the order they bite:", []Finding{bareRuleFinding(1, "rules")}},
		{"a heading", "# Skill\n\n## Rules\n\nRun it.", []Finding{bareRuleFinding(3, "Rules")}},
		{"capitals", "Most of it is a naming RULE.", []Finding{bareRuleFinding(1, "RULE")}},
		{"a modifier that names no mechanism", "Strict-mode rules apply.", []Finding{bareRuleFinding(1, "rules")}},
		{"a list item", "- Every rule below carries a stamp.", []Finding{bareRuleFinding(1, "rule")}},
		{
			"the front matter description",
			"---\nname: x\ndescription: \"Rules for developing magus.\"\n---\n\n# X\n",
			[]Finding{bareRuleFinding(3, "Rules")},
		},
		{"a wrapped line", "Read it before\nthe rule applies.", []Finding{bareRuleFinding(2, "rule")}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindSkill), tc.want)
		})
	}
}

func TestBareRulePassesAQualifiedRule(t *testing.T) {
	cases := map[string]string{
		"a guard rule":             "A refused write is a guard rule you are about to hit.",
		"a possessive":             "The guard's rules hold.",
		"a hyphenated qualifier":   "Declare an additive shell-guard rule.",
		"a lint rule":              "The prose judge holds the short form to the terse lint rules.",
		"a workspace rule":         "A workspace spawn rule may restrict it, as any workspace rule may.",
		"a command rule":           "The command rule and the write rule both judge it.",
		"a builtin rule":           "Set a builtin rule to deny.",
		"an id before it":          "It hits the `capture-filter` rule.",
		"an id after it":           "It hits rule `capture-filter`.",
		"a mention in code":        "Write `rule` only for what magus enforces; never add a `.cursor/rules` file.",
		"a mention in quotes":      `The word "rule" names an enforced check.`,
		"a fenced block":           "Run it.\n\n```text\n<!-- rule: an-id -->\n```\n",
		"a longer word holding it": "It overrules the ruler and is unruled.",
	}

	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			for _, f := range JudgeText(text, KindSkill) {
				if f.Rule == RuleBareRule {
					t.Errorf("reported %q", f.Match)
				}
			}
		})
	}
}

func TestBareRuleJudgesOnlyASkill(t *testing.T) {
	for _, s := range []Kind{KindMarkdown, KindPullRequest, KindDoc} {
		for _, f := range JudgeText("Follow the rule.", s) {
			if f.Rule == RuleBareRule {
				t.Errorf("%s: %s judged it", s, f.Rule)
			}
		}
	}
}

// A full copy is installed and loaded like the short one, so the word is
// judged in what only the full form shows, and in shared text only once.
func TestBareRuleJudgesBothFormsOfASkillSource(t *testing.T) {
	body := "# Skill\n\nRun it.{{if .Full}} The rule holds.{{end}}\n\nThe rule binds{{if .Full}}, always{{end}}.\n"

	assertFindings(t, JudgeText(body, KindSkillSource), []Finding{
		bareRuleFinding(3, "rule"),
		bareRuleFinding(5, "rule"),
	})
}
