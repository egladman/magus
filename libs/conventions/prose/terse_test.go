package prose

import (
	"fmt"
	"strings"
	"testing"
)

func sentenceOver(line, words int) Finding {
	return Finding{Rule: RuleTerseSentence, Line: line, Message: fmt.Sprintf(
		"Keep a skill sentence to 25 words (this one has %d): split it, or make its steps a list.", words)}
}

func paragraphOver(line, words int) Finding {
	return Finding{Rule: RuleTerseParagraph, Line: line, Message: fmt.Sprintf(
		"Keep a skill paragraph to 60 words (this one has %d): cut what a heading or a list already says, "+
			"or split the steps into a list.", words)}
}

func TestTerseSentenceReportsASentenceOverTheCap(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"at the cap", longSentence(25), nil},
		{"over the cap", longSentence(26), []Finding{sentenceOver(1, 26)}},
		{"the second sentence, wrapped", "Short one.\n" + wordRun(20) + "\n" + longSentence(20),
			[]Finding{sentenceOver(2, 40)}},
		{"a sentence opening on a later line", "Short one.\n\n" + longSentence(26), []Finding{sentenceOver(3, 26)}},
		{"a code span is one word", wordRun(20) + " `" + wordRun(20) + "` end.", nil},
		{"a bold sentence ends at its stop", "**" + longSentence(20) + "** " + longSentence(20), nil},
		{"a sentence with no closing stop", wordRun(26), []Finding{sentenceOver(1, 26)}},
		{"each list item is its own", "- " + longSentence(20) + "\n- " + longSentence(20), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, SurfaceSkill), tc.want)
		})
	}
}

func TestTerseParagraphReportsAParagraphOverTheCap(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"at the cap", sentences(60), nil},
		{"over the cap", "Lead.\n\n" + sentences(70), []Finding{paragraphOver(3, 70)}},
		{"a list item over the cap", "- " + sentences(70) + "\n- Short.", []Finding{paragraphOver(1, 70)}},
		{"items under the cap in a list over it", "- " + sentences(40) + "\n- " + sentences(40), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, SurfaceSkill), tc.want)
		})
	}
}

// What is not a sentence carries more words than either cap, so a part the
// rules fail to skip shows up as a finding.
func TestTerseRulesSkipWhatIsNotASentence(t *testing.T) {
	long := wordRun(100)
	page := "---\nname: " + long + "\ndescription: \"Short.\"\nmetadata:\n  source: " + long + "\n---\n\n" +
		"# " + long + "\n\n" +
		"```sh\n" + long + "\n```\n\n" +
		"| Flag | Meaning |\n| ---- | ------- |\n| -x | " + long + " |\n\n" +
		"- `magus run " + long + "`\n" +
		"- `magus run go-build .`, `magus affected ci`.\n"

	assertFindings(t, JudgeText(page, SurfaceSkill), nil)
}

func TestTerseRulesJudgeTheFrontMatterDescription(t *testing.T) {
	page := "---\nname: x\ndescription: \"" + longSentence(26) + "\"\n---\n\n# X\n"

	assertFindings(t, JudgeText(page, SurfaceSkill), []Finding{sentenceOver(3, 26)})
}

func TestWordyNamesTheShorterPhrase(t *testing.T) {
	cases := []struct{ text, match, want string }{
		{"Run it in order to replay.", "in order to", "'to'"},
		{"It is able to replay.", "is able to", "'can'"},
		{"It fails due to the fact that the key moved.", "due to the fact that", "'because'"},
		{"Make sure that it runs.", "Make sure that", "'ensure'"},
		{"In the event that it fails, rerun.", "In the event that", "'if'"},
		{"Ask whether or not it ran.", "whether or not", "'whether'"},
		{"It holds a number of keys.", "a number of", "'several' or the count"},
		{"At this point the cache is warm.", "At this point", "'now'"},
		{"Note the fact that it ran.", "the fact that", "'that'"},
		{"Read it prior to a run.", "prior to", "'before'"},
	}

	for _, tc := range cases {
		t.Run(tc.match, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, SurfaceSkill), []Finding{{
				Rule: RuleWordy, Message: fmt.Sprintf("Write %s, not '%s'.", tc.want, tc.match), Match: tc.match, Line: 1,
			}})
		})
	}
}

func TestWordyReadsAWrappedPhraseAndSkipsAMention(t *testing.T) {
	text := "Run it in order\nto replay.\n\nWrite \"to\", not \"in order to\", and not `in order to`.\n"

	assertFindings(t, JudgeText(text, SurfaceSkill), []Finding{{
		Rule: RuleWordy, Message: "Write 'to', not 'in order to'.", Match: "in order to", Line: 1,
	}})
}

// Each wordy phrase is found whole, ahead of a shorter phrase it starts with,
// and names a replacement with fewer words than itself.
func TestWordyPhrasesEachNameAShorterPhrase(t *testing.T) {
	for _, w := range wordyPhrases {
		want := strings.SplitN(w.want, "'", 3)[1]
		if len(strings.Fields(want)) >= len(strings.Fields(w.phrase)) {
			t.Errorf("%q: %q is no shorter", w.phrase, want)
		}

		if got := wordyPattern.FindString(w.phrase); got != w.phrase {
			t.Errorf("%q: the pattern finds %q", w.phrase, got)
		}
	}
}

func TestTerseRulesJudgeOnlyASkill(t *testing.T) {
	text := longSentence(26) + "\n\nRun it in order to replay.\n"

	for _, s := range []Surface{SurfaceMarkdown, SurfacePullRequest} {
		for _, f := range JudgeText(text, s) {
			if f.Rule == RuleTerseSentence || f.Rule == RuleWordy {
				t.Errorf("%s: %s judged it", s, f.Rule)
			}
		}
	}
}
