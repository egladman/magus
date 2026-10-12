package proofread

import (
	"fmt"
	"regexp"
	"strings"
)

// Measured 2026-10-07 over the short form of the 18 skills, a code span
// counted as one word: 1264 sentences ran p50 14 words, p90 30, p95 35, p99
// 51; 642 paragraphs and list items ran p50 27, p90 64, p95 78, p99 125. Caps
// near p95 trimmed only the long tail, 5.5% of the bytes. Every load spends
// these words, so the caps sit below p90 and hold a skill to a terse runbook.
const (
	maxSkillSentenceWords  = 25
	maxSkillParagraphWords = 60
)

// wordyPhrases are phrases with a shorter equivalent, each with what to write
// instead. A phrase matches in any case at word boundaries, and never inside
// code or quotes, where it is a mention.
var wordyPhrases = []struct{ phrase, want string }{
	{"in order to", "'to'"},
	{"in order for", "'for'"},
	{"so as to", "'to'"},
	{"is able to", "'can'"},
	{"are able to", "'can'"},
	{"be able to", "'can'"},
	{"has the ability to", "'can'"},
	{"have the ability to", "'can'"},
	{"is capable of", "'can'"},
	{"due to the fact that", "'because'"},
	{"owing to the fact that", "'because'"},
	{"despite the fact that", "'although'"},
	{"in spite of the fact that", "'although'"},
	{"the reason is that", "'because'"},
	{"the reason why", "'why'"},
	{"the fact that", "'that'"},
	{"make sure that", "'ensure'"},
	{"make sure", "'ensure'"},
	{"in the event that", "'if'"},
	{"whether or not", "'whether'"},
	{"a number of", "'several' or the count"},
	{"at this point in time", "'now'"},
	{"at this point", "'now'"},
	{"at the moment", "'now'"},
	{"at the present time", "'now'"},
	{"prior to", "'before'"},
	{"subsequent to", "'after'"},
	{"with respect to", "'about'"},
	{"with regard to", "'about'"},
	{"in regard to", "'about'"},
	{"for the purpose of", "'to' or 'for'"},
	{"in the case of", "'for'"},
	{"it is possible to", "'you can'"},
	{"there is no need to", "'no need to'"},
	{"each and every", "'every'"},
	{"any and all", "'all'"},
	{"a lot of", "'many'"},
	{"until such time as", "'until'"},
	{"by means of", "'by'"},
	{"as well as", "'and'"},
	{"along with", "'with'"},
}

// skillOnlyWordy are the wordyPhrases only a skill refuses. Each has a sense
// the shorter phrase lacks: "whether or not" is correct English, "at the
// moment" can name a point in time, "the fact that" can have "fact" as its
// noun, and "in order for" can mean in sequence. A skill pays for every word in
// every session that loads it, so it gives those up anyway.
var skillOnlyWordy = wordSet(
	"in order for", "be able to", "the fact that", "make sure", "whether or not", "at this point",
	"at the moment", "there is no need to", "a lot of", "as well as", "along with",
)

var (
	// wordyPattern matches every one of wordyPhrases.
	wordyPattern = compileWordy(func(string) bool { return true })
	// writtenWordyPattern matches the phrases outside skillOnlyWordy.
	writtenWordyPattern = compileWordy(func(phrase string) bool { return !skillOnlyWordy[phrase] })
)

// compileWordy matches each of wordyPhrases that keep admits, the longest
// first where two start at one word.
func compileWordy(keep func(phrase string) bool) *regexp.Regexp {
	var alts []string

	for _, w := range wordyPhrases {
		if keep(w.phrase) {
			alts = append(alts, strings.ReplaceAll(regexp.QuoteMeta(w.phrase), " ", `\s+`))
		}
	}

	return regexp.MustCompile(`(?i)\b(?:` + strings.Join(alts, "|") + `)\b`)
}

func wordy(in input) []Finding {
	pattern := writtenWordyPattern
	if in.kind == KindAgentInstructions {
		pattern = wordyPattern
	}

	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, at := range pattern.FindAllStringIndex(para.text, -1) {
			m := para.text[at[0]:at[1]]
			want := wordyWant(m)

			var replacements []string
			for _, q := range quotedWant.FindAllStringSubmatch(want, -1) {
				replacements = append(replacements, matchCase(q[1], m))
			}

			out = append(out, Finding{
				Message: fmt.Sprintf("Write %s, not '%s'.", want, m), Match: m, Line: para.lineAt(at[0]),
				Replacements: replacements,
			})
		}
	}

	return out
}

// quotedWant is a phrase a wordyPhrases want quotes: each is a replacement,
// and the words outside quotes ("or the count") are advice no text replaces.
var quotedWant = regexp.MustCompile(`'([^']+)'`)

// wordyWant is what to write in place of the matched phrase m.
func wordyWant(m string) string {
	key := strings.ToLower(strings.Join(strings.Fields(m), " "))
	for _, w := range wordyPhrases {
		if w.phrase == key {
			return w.want
		}
	}

	return "fewer words"
}

// terseSentence reports each sentence over the cap, at the line it starts on.
// A heading and a list item that is only a command are not sentences.
func terseSentence(in input) []Finding {
	var out []Finding

	for _, para := range terseParagraphs(in.prose) {
		for _, s := range sentenceSpans(para.text) {
			if s.words > maxSkillSentenceWords {
				out = append(out, Finding{
					Message: fmt.Sprintf("Keep a skill sentence to %d words (this one has %d): split it, "+
						"or make its steps a list.", maxSkillSentenceWords, s.words),
					Line: para.lineAt(s.start),
				})
			}
		}
	}

	return out
}

// terseParagraph reports each paragraph or list item over the cap, at the
// line it opens on.
func terseParagraph(in input) []Finding {
	var out []Finding

	for _, para := range terseParagraphs(in.prose) {
		if n := len(strings.Fields(para.text)); n > maxSkillParagraphWords {
			out = append(out, Finding{
				Message: fmt.Sprintf("Keep a skill paragraph to %d words (this one has %d): cut what a "+
					"heading or a list already says, or split the steps into a list.", maxSkillParagraphWords, n),
				Line: para.head.line,
			})
		}
	}

	return out
}

// terseParagraphs are the paragraphs and list items the word caps judge. A
// code span counts as one word, so a list item that is a command counts as
// one too; a heading is a label, not a sentence.
func terseParagraphs(prose []proseLine) []paragraph {
	var out []paragraph

	for _, para := range paragraphs(prose, blankBackticks) {
		if !para.head.heading {
			out = append(out, para)
		}
	}

	return out
}

// sentenceSpan is one sentence of a paragraph: where it starts and how many
// words it holds.
type sentenceSpan struct{ start, words int }

// sentenceSpans splits text into sentences at a word that closes one.
func sentenceSpans(text string) []sentenceSpan {
	var out []sentenceSpan

	cur := sentenceSpan{start: -1}
	space := func(b byte) bool { return b == ' ' || b == '\t' }

	for i := 0; i < len(text); {
		for i < len(text) && space(text[i]) {
			i++
		}

		if i == len(text) {
			break
		}

		end := i
		for end < len(text) && !space(text[end]) {
			end++
		}

		if cur.start < 0 {
			cur.start = i
		}

		cur.words++

		if endsSentence(text[i:end]) {
			out = append(out, cur)
			cur = sentenceSpan{start: -1}
		}

		i = end
	}

	if cur.words > 0 {
		out = append(out, cur)
	}

	return out
}
