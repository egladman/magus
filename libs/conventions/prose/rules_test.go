package prose

import (
	"strings"
	"testing"
)

// The cases below are the vale styles' own .test.yml cases, carried over when
// the rules moved to Go.

func TestCommentBlockReportsADocOverTheWordBudget(t *testing.T) {
	cases := []judgeCase{
		{
			name: "a block over 250 words",
			doc:  strings.Repeat("The cache keys a run on what it reads, so an undeclared input replays a stale verdict.\n", 17),
			want: []Finding{{
				Rule:    RuleCommentBlock,
				Message: "Keep a comment block under 250 words: say why, and move the rest to docs.",
			}},
		},
		{name: "a short block", doc: "The cache keys a run on what it reads. A miss reruns it.\n"},
		{
			name: "code dropped from the count",
			doc: "The cache keys a run on what it reads.\n\n" +
				"\t" + strings.Repeat("word ", 300) + "\n\n" +
				"```\n" + strings.Repeat("word ", 300) + "\n```\n",
		},
	}

	runJudgeCases(t, cases)
}

func TestCommentSentenceReportsASentenceOverTheWordBudget(t *testing.T) {
	cases := []judgeCase{
		{
			name: "a sentence over 60 words",
			doc: "The cache keys a run on the cache keys a run on what it reads and the cache keys a run\n" +
				"on what it reads and the cache keys a run on what it reads and the cache keys a run on\n" +
				"what it reads and the cache keys a run on what it reads and the cache keys a run on what\n" +
				"it reads and the cache keys a run on what it reads and the cache keys a run on what it\n" +
				"reads.\n",
			want: []Finding{{Rule: RuleCommentSentence, Message: "Keep a comment sentence under 60 words: split it."}},
		},
		{name: "short sentences", doc: "The cache keys a run on what it reads. A miss reruns it.\n"},
		{
			name: "a paragraph break ends the count",
			doc:  strings.Repeat("word ", 40) + "\n\n" + strings.Repeat("word ", 40) + "\n",
		},
	}

	runJudgeCases(t, cases)
}

func TestFillerReportsThroatClearingAndFillerAdverbs(t *testing.T) {
	cases := []judgeCase{
		{
			name: "throat-clearing",
			doc:  "Note that the cache keys a run on what it reads.\n",
			want: []Finding{{Rule: RuleFiller, Message: "Drop 'Note that': state the fact.", Match: "Note that"}},
		},
		{
			name: "a filler adverb",
			doc:  "It simply rereads the file.\n",
			want: []Finding{{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply"}},
		},
		{name: "the fact alone", doc: "The cache keys a run on what it reads. A miss reruns it.\n"},
		{name: "a note that names the notes feature", doc: "It records a note that the queue reads. It is not this function's job.\n"},
		{name: "a backtick span is a literal", doc: "It reads the `simply` flag.\n"},
		{
			name: "a phrase wrapped across lines",
			doc:  "The cache keys a run on what it reads. It is\nimportant to declare inputs.\n",
			want: []Finding{{
				Rule:    RuleFiller,
				Message: "Drop 'It is important to': state the fact.",
				Match:   "It is important to",
			}},
		},
	}

	runJudgeCases(t, cases)
}

func TestTermsReportsTheHyphenatedSubagent(t *testing.T) {
	cases := []judgeCase{
		{
			name: "a hyphenated subagent",
			doc:  "It hands the work to a sub-agent.\n",
			want: []Finding{{Rule: RuleTerms, Message: "Write 'subagent', not 'sub-agent'.", Match: "sub-agent"}},
		},
		{name: "the one spelling", doc: "It hands the work to subagents.\n"},
		{
			name: "a capitalized plural",
			doc:  "Sub-Agents share the lease.\n",
			want: []Finding{{Rule: RuleTerms, Message: "Write 'subagents', not 'Sub-Agents'.", Match: "Sub-Agents"}},
		},
	}

	runJudgeCases(t, cases)
}

func TestNameSuffixReportsACallableNameEndingInOfOrFor(t *testing.T) {
	cases := []struct {
		name string
		want []Finding
	}{
		// A name ending in For.
		{name: "parseConfig"},
		{name: "configFor", want: []Finding{nameSuffixFinding("configFor")}},
		// A name ending in Of.
		{name: "valueOf", want: []Finding{nameSuffixFinding("valueOf")}},
		// Of and For only as whole trailing words.
		{name: "newClient"},
		{name: "ForEach"},
		{name: "Platform"},
		{name: "Offset"},
		// Snake case.
		{name: "value_of", want: []Finding{nameSuffixFinding("value_of")}},
		{name: "for"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, Judge(Symbol{Name: tc.name, Callable: true}), tc.want)
		})
	}
}

func TestNameSuffixSkipsANameThatIsNotCallable(t *testing.T) {
	assertFindings(t, Judge(Symbol{Name: "valueOf"}), nil)
}

func nameSuffixFinding(name string) Finding {
	return Finding{
		Rule:    RuleNameSuffix,
		Message: "Rename '" + name + "': no function or method name ends in the word Of or For.",
		Match:   name,
	}
}
