package proofread

import (
	"math"
	"regexp"
	"strings"
	"unicode"
)

// Metrics describe how hard a text is to read. They are information: no rule
// reads them and no decision follows from them.
type Metrics struct {
	Words     int `json:"words"`
	Sentences int `json:"sentences"`
	// SentenceWordsMean and SentenceWordsSD are the mean and the population
	// standard deviation of words per sentence.
	SentenceWordsMean float64 `json:"sentence_words_mean"`
	SentenceWordsSD   float64 `json:"sentence_words_sd"`
	LongestSentence   int     `json:"longest_sentence"`
	Syllables         int     `json:"syllables"`
	// FleschKincaidGrade is 0.39 words per sentence plus 11.8 syllables per
	// word minus 15.59; FleschReadingEase is 206.835 minus 1.015 words per
	// sentence minus 84.6 syllables per word. Both are 0 for a text with no
	// words.
	FleschKincaidGrade float64 `json:"flesch_kincaid_grade"`
	FleschReadingEase  float64 `json:"flesch_reading_ease"`
}

var anySpan = regexp.MustCompile("`+[^`]*`+")

// codeWord stands in for a masked code span: one word of one syllable that no
// sentence ends on, and that the shape measures can tell from the prose word
// "code".
const codeWord = "`code`"

func maskSpans(s string) string { return anySpan.ReplaceAllString(s, " "+codeWord+" ") }

// Measure returns the metrics of text read as kind. Only prose is measured,
// read as [JudgeText] reads it for the rules: a Markdown kind drops front
// matter, fenced and indented code, tables, HTML, link targets, URLs, what a
// platform wrote and headings, and a titled kind's first line is a sentence of
// its own. A doc comment, a message, help text and a tool description are
// plain text that drops indented code and URLs alone. Each code span counts as
// one word of one syllable, since a span inflates the grade when its
// identifiers are counted as words. A sentence ends at a word closing with
// '.', '!' or '?', or at the end of its paragraph or list item.
func Measure(text string, kind Kind) Metrics { return measure(readKind(text, kind)) }

// readKind returns the paragraphs and list items of text read as kind, the one
// reading [Measure], [MeasureShape] and the voice measures share. Headings
// stay in, marked, for the measures that count them.
func readKind(text string, kind Kind) []paragraph {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	switch kind {
	case KindDocComment, KindMessage, KindCLIHelp, KindToolDescription:
		for i, ln := range raw {
			raw[i] = bareURL.ReplaceAllString(ln, "")
		}

		return paragraphs(readProse(raw, false), keep)
	}

	_, prose := readMarkdown(blankBoilerplate(raw), kind)

	return paragraphs(prose, keep)
}

// readMarkdown reads raw, its boilerplate already blanked, as kind's Markdown:
// the lines with everything but prose blanked, and the prose they hold.
func readMarkdown(raw []string, kind Kind) ([]string, []proseLine) {
	lines := markdownProse(raw, !isReview(kind) && !titled(kind))
	wrapCodeSpans(lines)
	prose := readProse(lines, kind != KindCommitMessage)

	if titled(kind) {
		// The title is a paragraph of its own, however close the description
		// starts below it.
		for i := range prose {
			if prose[i].line > 1 {
				prose[i].opens, prose[i].paragraph = true, true

				break
			}
		}
	}

	return lines, prose
}

// splitSentences splits one paragraph's masked text into sentences of words.
func splitSentences(text string) [][]string {
	var out [][]string

	var sentence []string

	for _, token := range strings.Fields(text) {
		if countWords(token) == 0 {
			continue
		}

		sentence = append(sentence, token)

		if endsSentence(token) {
			out = append(out, sentence)
			sentence = nil
		}
	}

	if len(sentence) > 0 {
		out = append(out, sentence)
	}

	return out
}

func measure(paras []paragraph) Metrics {
	var m Metrics

	var lengths []int

	for _, para := range paras {
		if para.head.heading {
			continue
		}

		for _, sentence := range splitSentences(maskSpans(para.text)) {
			lengths = append(lengths, len(sentence))

			for _, word := range sentence {
				m.Syllables += Syllables(word)
			}
		}
	}

	m.Sentences = len(lengths)

	for _, n := range lengths {
		m.Words += n
		m.LongestSentence = max(m.LongestSentence, n)
	}

	if m.Words == 0 {
		return m
	}

	perSentence := float64(m.Words) / float64(m.Sentences)
	perWord := float64(m.Syllables) / float64(m.Words)

	var squares float64
	for _, n := range lengths {
		squares += (float64(n) - perSentence) * (float64(n) - perSentence)
	}

	m.SentenceWordsMean = round2(perSentence)
	m.SentenceWordsSD = round2(math.Sqrt(squares / float64(m.Sentences)))
	m.FleschKincaidGrade = round2(0.39*perSentence + 11.8*perWord - 15.59)
	m.FleschReadingEase = round2(206.835 - 1.015*perSentence - 84.6*perWord)

	return m
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// Syllables estimates the syllables of an English word without a dictionary:
// each run of vowels (y included) is one, a final silent e is dropped unless
// the word ends in a consonant and "le", and every word has at least one. A
// token holding no letter, a number, counts as one. The estimate is off by
// one on words like "create" and "naive", which is noise a grade averaged over
// a text absorbs.
func Syllables(word string) int {
	w := strings.ToLower(strings.TrimFunc(word, func(r rune) bool { return !unicode.IsLetter(r) }))
	if w == "" {
		return 1
	}

	vowel := func(b byte) bool { return strings.IndexByte("aeiouy", b) >= 0 }

	n := 0
	prev := false

	for i := range len(w) {
		v := vowel(w[i])
		if v && !prev {
			n++
		}

		prev = v
	}

	if l := len(w); n > 1 && w[l-1] == 'e' && !vowel(w[l-2]) && !(w[l-2] == 'l' && l > 2 && !vowel(w[l-3])) {
		n--
	}

	return max(n, 1)
}
