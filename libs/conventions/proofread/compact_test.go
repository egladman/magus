package proofread

import (
	"slices"
	"strings"
	"testing"
)

// compactOnly judges text as agent-compact by one rule alone.
func compactOnly(text string, rule Rule) []Finding {
	return JudgeText(text, KindAgentCompact, WithOnly(rule))
}

func TestCompactFillerCountsTheWordsAndTheSaving(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{
			"articles, a linking verb and you", "Run the gate.\nThe cache is a store you keep.\n",
			[]Finding{{
				Message: "Drop 5 filler words ('the' 2, 'a' 1, 'is' 1, 'you' 1): about 5 tokens saved at 1 token " +
					"a word. Keep every negation, condition, scope and order word.",
				Line: 1,
			}},
		},
		{
			"note that counts two words", "Note that the gate runs.\n",
			[]Finding{{
				Message: "Drop 3 filler words ('note that' 1, 'the' 1): about 3 tokens saved at 1 token a word. " +
					"Keep every negation, condition, scope and order word.",
				Line: 1,
			}},
		},
		{"already compact", "Run gate.\nNever push main.\n", nil},
		{"code and quotes are mentions", "Run `the is a`.\nWord \"the\" stays.\n", nil},
		{"an identifier keeps its underscore", "Call is_valid.\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.want {
				tc.want[i].Rule, tc.want[i].Decision = RuleCompactFiller, DecisionAdvise
			}

			assertFindings(t, compactOnly(tc.text, RuleCompactFiller), tc.want)
		})
	}
}

func TestCompactMarkupReportsEmphasisAndTables(t *testing.T) {
	bold := Finding{
		Rule: RuleCompactMarkup, Decision: DecisionAdvise, Line: 1, Match: "**key:**", Replacements: []string{"key:"},
		Message: "Drop the emphasis in '**key:**': the markers cost tokens and an agent acts on the words.",
	}
	italic := Finding{
		Rule: RuleCompactMarkup, Decision: DecisionAdvise, Line: 1, Match: "*once*", Replacements: []string{"once"},
		Message: "Drop the emphasis in '*once*': the markers cost tokens and an agent acts on the words.",
	}
	table := Finding{
		Rule: RuleCompactMarkup, Decision: DecisionAdvise, Line: 1,
		Message: "Rewrite the table as key: value lines: two facts cost 24 tokens as a table and 11 as lines.",
	}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a bold label", "- **key:** value\n", []Finding{bold}},
		{"an italic word", "Run it *once*.\n", []Finding{italic}},
		{"a table", "| key | value |\n| --- | --- |\n| a | b |\n", []Finding{table}},
		{"a list marker and a product", "* run gate\n* 2 * 3 = 6\n", nil},
		{"snake case and code", "Set max_line_words; run `**x**`.\n", nil},
		{"a table in a fence", "```\n| a | b |\n| - | - |\n```\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, compactOnly(tc.text, RuleCompactMarkup), tc.want)
		})
	}
}

func TestCompactCostlyAbbrevReportsWhatCostsMoreThanTheWord(t *testing.T) {
	finding := func(abbrev, word string) Finding {
		return Finding{
			Rule: RuleCompactCostlyAbbrev, Decision: DecisionAdvise, Line: 1, Match: abbrev, Replacements: []string{word},
			Message: "Write '" + word + "', not '" + abbrev + "': the abbreviation costs more tokens than the word.",
		}
	}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"k8s", "Deploy to k8s.\n", []Finding{finding("k8s", "Kubernetes")}},
		{"w/o and b/c", "Run w/o cache b/c stale.\n", []Finding{finding("w/o", "without"), finding("b/c", "because")}},
		{"e.g.", "A target (e.g. test) runs.\n", []Finding{finding("e.g.", "such as")}},
		{"dropped vowels", "Run tst before cmmt.\n", []Finding{finding("tst", "test"), finding("cmmt", "commit")}},
		{"an abbreviation that saves", "Set cfg dir, open PR.\n", nil},
		{"inside a path or code", "Read docs/w/x and `k8s`.\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, compactOnly(tc.text, RuleCompactCostlyAbbrev), tc.want)
		})
	}
}

func TestCompactSymbolNegationKeepsNegationAWord(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a bang", "Merge driver alone !enough.\n", []Finding{{
			Match: "!enough", Replacements: []string{"not enough"},
			Message: "Write 'not enough', not '!enough': negation stays a word.",
		}}},
		{"a logic symbol", "Cache hit \u2260 pass.\n", []Finding{{
			Match: "\u2260", Replacements: []string{"not equal"},
			Message: "Write 'not equal', not '\u2260': a logic symbol is read wrong more often than its word.",
		}}},
		{"a word", "Merge driver alone not enough.\n", nil},
		{"code and an image", "Run `!true` and see ![shot](a.png).\n", nil},
		{"an exclamation", "Done!\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.want {
				tc.want[i].Rule, tc.want[i].Decision, tc.want[i].Line = RuleCompactSymbolNegation, DecisionAdvise, 1
			}

			assertFindings(t, compactOnly(tc.text, RuleCompactSymbolNegation), tc.want)
		})
	}
}

func TestCompactLineReportsALineHoldingTwoFacts(t *testing.T) {
	half := wordRun(maxCompactLineWords/2 + 1)
	over := Finding{
		Rule: RuleCompactLine, Decision: DecisionAdvise, Line: 1,
		Message: "Split this line: 22 words joining clauses. One fact per line.",
	}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a semicolon", half + "; " + half + "\n", []Finding{over}},
		{"a comma and a conjunction", half + ", and " + wordRun(maxCompactLineWords/2) + "\n", []Finding{over}},
		{"long with no join", wordRun(maxCompactLineWords+5) + "\n", nil},
		{"joined but short", "Run gate; read log.\n", nil},
		{"a heading", "# " + half + "; " + half + "\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, compactOnly(tc.text, RuleCompactLine), tc.want)
		})
	}
}

func TestCompactDuplicateReportsALineRestatingAnEarlierOne(t *testing.T) {
	first := "Magusfile/target/spell contracts (caching, `ctx.needs`, wards, charms, op kinds): magus-buzz-lang."

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{
			"a restatement", first + "\nReview code.\n" +
				"Magusfile/target/spell CONTRACTS (caching, `ctx.needs`, wards, charms, services): magus-buzz-lang.\n",
			[]Finding{{
				Rule: RuleCompactDuplicate, Decision: DecisionAdvise, Line: 3,
				Message: "Merge this line into the earlier 'Magusfile/target/spell contracts (caching, `ctx.needs`, " +
					"wards, charms, op kinds): ...', which says the same: keep every negation, condition, scope " +
					"and order word.",
			}},
		},
		{
			"a different keep word", "Run gate before push to main branch.\nRun gate after push to main branch.\n", nil,
		},
		{"too few words to tell", "Run `ci` here.\nRun `ci` there.\n", nil},
		{"a heading", "# Run gate main branch push\nRun gate main branch push.\n", nil},
		{"different lines", "Run gate before push.\nRead captured log, fix root cause.\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, compactOnly(tc.text, RuleCompactDuplicate), tc.want)
		})
	}
}

// agent-compact drops the rules that hold a sentence to its grammar or its
// length, and keeps the word tells.
func TestAgentCompactTurnsOffTheRulesThatFightIt(t *testing.T) {
	rules := KindRules(KindAgentCompact)

	for _, r := range []Rule{RuleTerseSentence, RuleTerseParagraph, RuleWordy, RuleHeadingCase, RuleIngTail, RuleListParallel} {
		if slices.Contains(rules, r) {
			t.Errorf("%s judges agent-compact", r)
		}
	}

	for _, r := range []Rule{RuleBuzzword, RuleLeak, RuleBareRule, RuleDash, RuleASCII, RuleReplyVoice, RuleSuppressionUnused} {
		if !slices.Contains(rules, r) {
			t.Errorf("%s does not judge agent-compact", r)
		}
	}

	for _, f := range JudgeText("- **Cache:** on\n", KindAgentCompact) {
		if f.Rule == RuleReplyVoice {
			t.Errorf("reply-voice reported the bold label compact-markup owns: %+v", f)
		}
	}
}

func TestParseKindReadsAFormerName(t *testing.T) {
	cases := map[string]Kind{
		"agent-instructions":          KindAgentBrief,
		"agent-instructions-template": KindSkillTemplate,
		"agent-brief":                 KindAgentBrief,
		"poem":                        "poem",
	}

	for name, want := range cases {
		if got := ParseKind(name); got != want {
			t.Errorf("ParseKind(%q) = %q, want %q", name, got, want)
		}
	}

	text := "Follow the rule.\n"
	got, want := JudgeText(text, "agent-instructions", houseOn), JudgeText(text, KindAgentBrief, houseOn)
	if len(want) == 0 || findingRules(got) != findingRules(want) {
		t.Errorf("the former name judged %v, agent-brief %v", got, want)
	}
}

func findingRules(found []Finding) string {
	names := make([]string, len(found))
	for i, f := range found {
		names[i] = string(f.Rule)
	}

	return strings.Join(names, ",")
}
