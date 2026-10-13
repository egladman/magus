package proofread

import (
	"strings"
	"testing"
)

func skillOnly(body string, rule Rule) []Finding {
	return JudgeText(body, KindSkillTemplate, WithOnly(rule))
}

func TestSkillDescriptionBudgetReportsALongDescriptionAndARepeatedTwin(t *testing.T) {
	long := wordRun(maxDescriptionWords + 1)
	short := "Run builds and tests through magus targets before any raw language tool."
	own := "Full reference copy with worked examples; load when a step is underspecified."

	cases := []struct {
		name, body string
		want       []Finding
	}{
		{
			"over the budget in both forms", "---\nname: x\ndescription: " + long + "\n---\n\n# X\n",
			[]Finding{
				{
					Line: 3,
					Message: "Cut the description to 100 words (it has 101): it loads into every session whether or " +
						"not the skill is used.",
				},
				{
					Line: 3,
					Message: "Give the full form a description of its own: 100% of its words repeat the short form's, " +
						"and both load into every session.",
				},
			},
		},
		{
			"a twin restating its sibling", "---\ndescription: {{if .Full}}" + short + " Examples kept." +
				"{{else}}" + short + "{{end}}\n---\n\n# X\n",
			[]Finding{{
				Line: 2,
				Message: "Give the full form a description of its own: 80% of its words repeat the short form's, " +
					"and both load into every session.",
			}},
		},
		{
			"a twin with its own line", "---\ndescription: {{if .Full}}" + own + "{{else}}" + short + "{{end}}\n---\n\n# X\n",
			nil,
		},
		{"no frontmatter", "# X\n\nRun it.\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.want {
				tc.want[i].Rule, tc.want[i].Decision = RuleSkillDescriptionBudget, DecisionAdvise
			}

			assertFindings(t, skillOnly(tc.body, RuleSkillDescriptionBudget), tc.want)
		})
	}
}

func TestSkillDescriptionBudgetJudgesEachSkillOfASet(t *testing.T) {
	set := "---\nname: a\ndescription: {{if .Full}}Read the examples.{{else}}Run it.{{end}}\n---\n---\nname: b\n" +
		"description: " + wordRun(maxDescriptionWords+1) +
		"\n---\n"

	assertFindings(t, skillOnly(set, RuleSkillDescriptionBudget), []Finding{
		{
			Rule: RuleSkillDescriptionBudget, Decision: DecisionAdvise, Line: 7,
			Message: "Cut the description to 100 words (it has 101): it loads into every session whether or " +
				"not the skill is used.",
		},
		{
			Rule: RuleSkillDescriptionBudget, Decision: DecisionAdvise, Line: 7,
			Message: "Give the full form a description of its own: 100% of its words repeat the short form's, " +
				"and both load into every session.",
		},
	})
}

func TestSkillSetDescriptionBudgetSumsBothFormsOfEverySkill(t *testing.T) {
	entry := func(name string, short, full int) string {
		return "---\nname: " + name + "\ndescription: \"{{if .Full}}" + wordRun(full) + "{{else}}" + wordRun(short) +
			"{{end}}\"\n---\n"
	}

	cases := []struct {
		name, body string
		want       []Finding
	}{
		{
			"past the budget",
			entry("big", 900, 100) + entry("mid", 700, 100) + entry("small", 200, 101),
			[]Finding{{
				Rule: RuleSkillSetDescriptionBudget, Decision: DecisionAdvise,
				Message: "Cut the set's descriptions to 2100 words (they have 2101): every one loads into every " +
					"session. The largest are big 1000, mid 800, small 301; the full twins carry 301 (14%).",
			}},
		},
		{"at the budget", entry("big", 900, 100) + entry("mid", 700, 100) + entry("small", 200, 100), nil},
		{"one skill", entry("only", 90, 20), nil},
		{"no frontmatter", "# X\n\nRun it.\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, skillOnly(tc.body, RuleSkillSetDescriptionBudget), tc.want)
		})
	}
}

func TestInstructionPositionJudgesOnlyALongFullForm(t *testing.T) {
	filler := strings.Repeat(wordRun(9)+" end.\n\n", minPositionWords/10)

	cases := []struct {
		name, body string
		want       []Finding
	}{
		{
			"past the middle of a long full form", "# X\n\n" + filler + "You must run it.\n",
			[]Finding{{
				Rule: RuleInstructionPosition, Decision: DecisionAdvise, Line: 2*minPositionWords/10 + 3,
				Message: "Move the first must or never up: it sits 99% of the way into a 1505-word body, past " +
					"the middle a weaker model reads least.",
			}},
		},
		{"near the top", "# X\n\nYou must run it.\n\n" + filler, nil},
		{"a short full form", "# X\n\nRun it.\n\nYou must run it.\n", nil},
		{"only in the short form", "# X\n\n" + filler + "{{if .Full}}Run it.{{else}}You must run it.{{end}}\n", nil},
		{"no instruction", "# X\n\n" + filler, nil},
		{"a must in code", "# X\n\n" + filler + "Run `must`.\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, skillOnly(tc.body, RuleInstructionPosition), tc.want)
		})
	}
}
