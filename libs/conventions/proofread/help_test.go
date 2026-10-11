package proofread

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Flag usage strings below are the ones cmd/magus/gen/cli_flags.go binds.
const (
	usageCache = "Show the live cache key, the ref a run would print, the component classes behind it, " +
		"and what moved since the last recorded run"
	usageRace = "Race-condition diagnostics (watch|replay, comma-combinable); omit to disable. watch: " +
		"attribution-gated fsnotify detection (MGS4001/4002/4004), emitting only when >=2 projects' output " +
		"snapshots confirm a shared write. replay: re-runs cacheable output-declaring projects sequentially " +
		"to content-hash outputs for non-determinism (MGS4003); roughly doubles wall-clock."
	usageOutput = "Output format: text (default), json, yaml, name, jsonl, or template[=<go-template>]. " +
		"Honored by subcommands that emit structured data. A template body renders a Go text/template over " +
		"the same value -o json emits (field names are the json keys); a bare -o template with no body " +
		"lists that output's fields instead of rendering - the json keys usable in -o json and -o " +
		"template, with each field's type and doc."
)

func TestHelpSentenceCapsASentenceAt40Words(t *testing.T) {
	long := strings.Repeat("word ", 41)
	fits := strings.Repeat("word ", 40)

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a usage string of 22 words", usageCache, nil},
		{"exactly 40", fits[:len(fits)-5] + "ends.", nil},
		{"41 words", long + "ends.", []Finding{helpSentenceFinding(42, 1)}},
		{"two short sentences", fits[:100] + ". " + fits[:100] + ".", nil},
		{"the second sentence is the long one", "Short one. " + long + "ends.", []Finding{helpSentenceFinding(42, 1)}},
		{"a wrapped line is one sentence", long[:100] + "\n" + long[:100] + "\n" + long[:100], []Finding{helpSentenceFinding(60, 1)}},
		{"a code span is one word", "Run `" + strings.Repeat("word ", 60) + "` now.", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCLIHelp, WithOnly(RuleHelpSentence)), tc.want)
		})
	}
}

func helpSentenceFinding(words, line int) Finding {
	return Finding{
		Rule: RuleHelpSentence,
		Message: fmt.Sprintf("Keep a help sentence to 40 words (this one has %d): split it, or move the detail to "+
			"the docs.", words),
		Line: line,
	}
}

func TestHelpSentenceReportsTheOutputFlagsLongSentence(t *testing.T) {
	sentence := usageOutput[strings.Index(usageOutput, "A template body"):]

	assertFindings(t, JudgeText(usageOutput, KindCLIHelp, WithOnly(RuleHelpSentence)),
		[]Finding{helpSentenceFinding(len(strings.Fields(sentence)), 1)})
}

func TestHelpLengthCapsTheTextAt240Runes(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a median usage string", "Short for --explain", nil},
		{"a long usage string", usageCache, nil},
		{"240 runes", strings.Repeat("é", 240), nil},
		{"241 runes", strings.Repeat("é", 241), []Finding{helpLengthFinding(241)}},
		{"the race flag", usageRace, []Finding{helpLengthFinding(len([]rune(usageRace)))}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCLIHelp, WithOnly(RuleHelpLength)), tc.want)
		})
	}
}

func helpLengthFinding(n int) Finding {
	return Finding{
		Rule: RuleHelpLength,
		Message: fmt.Sprintf("Keep help text to 240 runes (this has %d): say what the flag does, and move the rest "+
			"to the docs.", n),
		Line: 1,
	}
}

// Help text meets filler, condescension and, where a table turns it on, the
// aside; it is not Markdown and not a message with a verdict and a ref.
func TestCLIHelpMeetsTheWordRules(t *testing.T) {
	cases := []struct {
		name, text string
		opts       []Option
		want       []Finding
	}{
		{
			name: "filler",
			text: "With --cache: list every key input line, so you can simply confirm a declared file was hashed",
			want: []Finding{{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply", Line: 1}},
		},
		{
			name: "a presuming word",
			text: "Pass the project, of course, before the flags",
			want: []Finding{{
				Rule: RuleCondescension,
				Message: "Drop 'of course': it tells the reader how hard a step should feel or what they should " +
					"already know; state the step or the fact, as in 'Run `magus init`.'",
				Match: "of course", Line: 1,
			}},
		},
		{
			name: "the aside in the output flag, once a table turns it on",
			text: usageOutput,
			opts: []Option{houseOn, WithOnly(RuleAside)},
			want: []Finding{{Rule: RuleAside, Message: asideMessage, Match: "rendering - the", Line: 1}},
		},
		{name: "an aside is house style, off without a table", text: "list the keys - not the values"},
		{name: "no message rationale on help", text: "Include every symbol; a diff needs them, because it compares them"},
		{name: "no tag rule on help", text: "output: print records from stdin; writes nothing"},
		{name: "no tell of generated writing in a plain flag", text: "Short for --explain"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCLIHelp, tc.opts...), tc.want)
		})
	}
}

func TestCLIHelpIsJudgedByTheRulesItNamesAndNoOther(t *testing.T) {
	want := []Rule{RuleFiller, RuleTerms, RuleAside, RuleCondescension, RuleLeak, RuleHelpSentence, RuleHelpLength}

	got := KindRules(KindCLIHelp)
	for _, r := range want {
		if !slices.Contains(got, r) {
			t.Errorf("%s does not judge cli-help; rules: %v", r, got)
		}
	}

	for _, r := range []Rule{RuleStaccato, RuleLeadContext, RuleHedge, RuleMessageLength, RuleReplyVoice} {
		if slices.Contains(got, r) {
			t.Errorf("%s judges cli-help", r)
		}
	}
}
