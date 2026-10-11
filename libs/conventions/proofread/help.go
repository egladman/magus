package proofread

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// RuleHelpSentence reports a sentence of help text over 40 words.
	RuleHelpSentence Rule = "help-sentence"
	// RuleHelpLength reports help text over 240 runes.
	RuleHelpLength Rule = "help-length"
)

const (
	helpSentenceWords = 40
	helpRunes         = 240
)

var helpChecks = []check{
	{rule: RuleHelpSentence, on: help, judge: helpSentence},
	{rule: RuleHelpLength, on: help, judge: helpLength},
}

var helpTexts = map[Rule]ruleText{
	RuleHelpSentence: {
		code:    "PRF9010",
		catches: "a sentence of help text over 40 words",
		why: "A reader scans help in a terminal while deciding what to type. The federal plain-language quick " +
			"tips ask for no sentence over 40 words. Over the 326 flag usage strings magus binds (median " +
			"11 words, 90th percentile 25, longest 56) one runs past it.",
	},
	RuleHelpLength: {
		code:    "PRF9011",
		catches: "help text over 240 runes",
		why: "A flag's help wraps in a table of flags, so a long one pushes the next flag off the screen. " +
			"Over the same 326 strings the median is 69 runes and the 90th percentile 146; 5 run past 240, " +
			"and the longest is 364.",
	},
}

// judgeHelp judges one command's or flag's help text as plain text: no
// Markdown is read, so a backtick still marks a literal and an indented line
// still reads as an example. A URL is not prose.
func judgeHelp(raw []string, o options) []Finding {
	lines := make([]string, len(raw))
	for i, ln := range raw {
		lines[i] = bareURL.ReplaceAllString(ln, "")
	}

	return run(input{
		kind: KindCLIHelp, prose: readProse(lines, false), lines: lines, source: raw,
		text: strings.Join(raw, "\n"), opts: o,
	})
}

func helpSentence(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, blankBackticks) {
		for _, s := range sentenceSpans(para.text) {
			if s.words > helpSentenceWords {
				out = append(out, Finding{
					Message: fmt.Sprintf("Keep a help sentence to %d words (this one has %d): split it, or move the "+
						"detail to the docs.", helpSentenceWords, s.words),
					Line: para.lineAt(s.start),
				})
			}
		}
	}

	return out
}

func helpLength(in input) []Finding {
	n := utf8.RuneCountInString(strings.TrimSpace(in.text))
	if n <= helpRunes {
		return nil
	}

	return []Finding{{
		Message: fmt.Sprintf("Keep help text to %d runes (this has %d): say what the flag does, and move the "+
			"rest to the docs.", helpRunes, n),
		Line: 1,
	}}
}
