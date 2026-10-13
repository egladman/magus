package proofread

import (
	"math"
	"regexp"
	"strings"
	"unicode"
)

// Shape is what a text measures on the features published shape-of-writing
// detection work reads, from its words and, for Grammar, TemplateRate and
// TagCompression, their part-of-speech tags. Like [Metrics] it is
// information: no rule reads it.
//
// Words, Sentences, SentenceWordsMean and SentenceWordsStdDev are
// [Measure]'s. Rates are per [Shape.Words], where a code span counts one
// word. The lexical measures (MATTR, WordEntropy, HapaxRatio, LexicalDensity,
// FunctionWords and AdjacentOverlap) read the words outside code spans,
// lowercased, with the punctuation around each trimmed; a word of
// punctuation alone, such as a spaced dash, is not one.
type Shape struct {
	Words     int `json:"words"`
	Sentences int `json:"sentences"`
	// MATTR is the moving-average type-token ratio over 50-word windows
	// (Covington and McFall 2010).
	MATTR float64 `json:"mattr"`
	// WordEntropy is the Shannon entropy, in bits, of the word types.
	WordEntropy float64 `json:"word_entropy"`
	// HapaxRatio is the word types seen once over the words.
	HapaxRatio float64 `json:"hapax_ratio"`
	// LexicalDensity is the words outside the function-word list over all
	// words.
	LexicalDensity float64 `json:"lexical_density"`
	// FunctionWords is each word of a fixed list of English function words
	// per 1000 words, every word of the list present.
	FunctionWords       map[string]float64 `json:"function_words,omitempty"`
	SentenceWordsMean   float64            `json:"sentence_words_mean"`
	SentenceWordsStdDev float64            `json:"sentence_words_sd"`
	// ParagraphWordsMean is the mean words per paragraph or list item.
	ParagraphWordsMean float64 `json:"paragraph_words_mean"`
	// Punctuation is per 100 words: colon, semicolon, paren (opening), dash
	// (em, en and a spaced hyphen), comma, exclamation and question.
	Punctuation map[string]float64 `json:"punctuation,omitempty"`
	// Layout is per 100 words: heading, bold, list_item and code_span.
	Layout map[string]float64 `json:"layout,omitempty"`
	// Triads are three-item lists, "X, Y, and Z" or "X, Y and Z", per 1000
	// words.
	Triads float64 `json:"triads_per_1000"`
	// RhetoricalQuestionsPer1000 are questions answered by the next sentence
	// of their paragraph, per 1000 words.
	RhetoricalQuestionsPer1000 float64 `json:"rhetorical_questions_per_1000"`
	// NotXButYPer1000 is contrasts such as "not X but Y", "isn't X, it's Y"
	// and "it's not just X", per 1000 words.
	NotXButYPer1000 float64 `json:"not_x_but_y_per_1000"`
	// AdjacentOverlap is the mean Jaccard similarity of the content words of
	// each sentence and the next.
	AdjacentOverlap float64 `json:"adjacent_overlap"`
	// Grammar is per 1000 words, read from the part-of-speech tags of the
	// words outside code spans: participial_clause, nominalization,
	// agentless_passive, phrasal_coordination, downtoner, concessive_though
	// and present_participle_postmodifier, after Biber (1988) as Reinhart et
	// al. (PNAS 2025) compared them.
	Grammar map[string]float64 `json:"grammar_per_1000,omitempty"`
	// TemplateRate is the share of the text's UPOS 4-grams that occur in it
	// at least twice, after Shaib et al. (EMNLP 2024).
	TemplateRate float64 `json:"template_rate"`
	// TagCompression is the gzip size of the space-joined UPOS sequence over
	// its raw size; repetitive syntax compresses further.
	TagCompression float64 `json:"tag_compression"`
	// Withheld names, by JSON name, each measure the text is too short for,
	// which stays zero: MATTR, WordEntropy, HapaxRatio and LexicalDensity
	// under 100 words outside code spans, FunctionWords, Grammar,
	// TemplateRate and TagCompression under 200, and SentenceWordsMean,
	// SentenceWordsStdDev and AdjacentOverlap under 5 sentences.
	Withheld []string `json:"withheld,omitempty"`
}

const (
	minLexicalWords   = 100
	minFunctionWords  = 200
	minShapeSentences = 5
	mattrWindow       = 50
	shapeItem         = "[\\w'\\x{2019}`-]+(?:\\s+[\\w'\\x{2019}`-]+){0,2}"
)

var (
	// series matches a list of items joined by commas and a final "and", from
	// the start of a clause; triads keeps the series of exactly three.
	series = regexp.MustCompile(`(?:^|[^\w'\x{2019}` + "`" + `-]\s*)(` + shapeItem + `(?:,\s+` + shapeItem + `)+,?\s+and\s+` +
		shapeItem + `)`)
	finalAnd   = regexp.MustCompile(`\s+and\s+`)
	contrastXY = regexp.MustCompile(`(?i)\b(?:it|this|that)(?:['\x{2019}]s|\s+is)\s+not\s+(?:just|only|merely)\b|` +
		`\b(?:isn['\x{2019}]t|is\s+not|aren['\x{2019}]t|are\s+not|wasn['\x{2019}]t|was\s+not)\s+[^.!?;]{1,40}?` +
		`(?:[,;]|\s--?|\s?[\x{2014}\x{2013}])\s*(?:it|they|that|this)(?:['\x{2019}](?:s|re)|\s+is|\s+are)\b|` +
		`\bnot\s+[^.!?;:,]{1,40}?,?\s+but\b`)
	boldSpan   = regexp.MustCompile(`\*\*[^*\s][^*]*\*\*|__[^_\s][^_]*__`)
	spacedDash = regexp.MustCompile(`\s--?\s|\x{2014}|\x{2013}`)
)

// MeasureShape returns the shape of text read as kind, from the prose
// [Measure] reads, split into sentences as it splits them.
func MeasureShape(text string, kind Kind) Shape {
	paras := readKind(text, kind)
	m := measure(paras)
	s := Shape{Words: m.Words, Sentences: m.Sentences}

	var (
		words                              []string
		sentences                          [][]string
		paragraphCount                     int
		triads, answered, contrasts        int
		colons, semicolons, parens, dashes int
		commas, exclamations, questions    int
		headings, bold, items, spans       int
	)

	for _, para := range paras {
		bold += len(boldSpan.FindAllString(para.text, -1))

		if para.head.heading {
			headings++

			continue
		}

		if para.head.item {
			items++
		}

		spans += len(anySpan.FindAllString(para.text, -1))
		prose := maskSpans(para.text)

		colons += strings.Count(prose, ":")
		semicolons += strings.Count(prose, ";")
		parens += strings.Count(prose, "(")
		dashes += len(spacedDash.FindAllString(prose, -1))
		commas += strings.Count(prose, ",")
		exclamations += strings.Count(prose, "!")
		questions += strings.Count(prose, "?")
		triads += countTriads(prose)
		contrasts += len(contrastXY.FindAllString(prose, -1))

		split := splitSentences(prose)
		if len(split) > 0 {
			paragraphCount++
		}

		for i, sentence := range split {
			if i+1 < len(split) && asks(sentence) && !asks(split[i+1]) {
				answered++
			}

			for _, w := range sentence {
				if l := lexical(w); w != codeWord && l != "" {
					words = append(words, l)
				}
			}
		}

		sentences = append(sentences, split...)
	}

	withhold := func(names ...string) { s.Withheld = append(s.Withheld, names...) }

	if m.Words > 0 {
		per := func(n, scale int) float64 { return round2(float64(scale*n) / float64(m.Words)) }

		s.Punctuation = map[string]float64{
			"colon": per(colons, 100), "semicolon": per(semicolons, 100), "paren": per(parens, 100),
			"dash": per(dashes, 100), "comma": per(commas, 100), "exclamation": per(exclamations, 100),
			"question": per(questions, 100),
		}
		s.Layout = map[string]float64{
			"heading": per(headings, 100), "bold": per(bold, 100), "list_item": per(items, 100),
			"code_span": per(spans, 100),
		}
		s.Triads = per(triads, 1000)
		s.RhetoricalQuestionsPer1000 = per(answered, 1000)
		s.NotXButYPer1000 = per(contrasts, 1000)
		s.ParagraphWordsMean = round2(float64(m.Words) / float64(paragraphCount))
	}

	if len(words) >= minLexicalWords {
		entropy, hapax := lexicalRichness(words)
		s.MATTR, s.WordEntropy, s.HapaxRatio = round4(mattr(words)), round4(entropy), round4(hapax)
		s.LexicalDensity = round4(lexicalDensity(words))
	} else {
		withhold("mattr", "word_entropy", "hapax_ratio", "lexical_density")
	}

	if len(words) >= minFunctionWords {
		s.FunctionWords = functionWordRates(words)
		g := measureGrammar(sentences, m.Words)
		s.Grammar, s.TemplateRate, s.TagCompression = g.rates, g.templateRate, g.tagCompression
	} else {
		withhold("function_words", "grammar_per_1000", "template_rate", "tag_compression")
	}

	if m.Sentences >= minShapeSentences {
		s.SentenceWordsMean, s.SentenceWordsStdDev = m.SentenceWordsMean, m.SentenceWordsSD
		s.AdjacentOverlap = round4(adjacentOverlap(sentences))
	} else {
		withhold("sentence_words_mean", "sentence_words_sd", "adjacent_overlap")
	}

	return s
}

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

// lexical is word as the lexical measures count it: lowercased, its
// surrounding punctuation trimmed and a curly apostrophe made straight.
func lexical(word string) string {
	w := strings.TrimFunc(word, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })

	return strings.ReplaceAll(strings.ToLower(w), "’", "'")
}

func asks(sentence []string) bool {
	last := strings.TrimRight(sentence[len(sentence)-1], `"')]*_`+"`")

	return strings.HasSuffix(last, "?")
}

func countTriads(prose string) int {
	n := 0

	for _, match := range series.FindAllStringSubmatch(prose, -1) {
		list := match[1]
		bounds := finalAnd.FindAllStringIndex(list, -1)
		head := strings.TrimRight(list[:bounds[len(bounds)-1][0]], ",")

		if strings.Count(head, ",") == 1 {
			n++
		}
	}

	return n
}

// mattr averages the type-token ratio of every window of [mattrWindow] words;
// words holds at least that many.
func mattr(words []string) float64 {
	counts := map[string]int{}
	types := 0

	add := func(w string, by int) {
		if counts[w] == 0 {
			types++
		}

		counts[w] += by

		if counts[w] == 0 {
			types--
		}
	}

	for _, w := range words[:mattrWindow] {
		add(w, 1)
	}

	sum := float64(types)

	for i := mattrWindow; i < len(words); i++ {
		add(words[i-mattrWindow], -1)
		add(words[i], 1)
		sum += float64(types)
	}

	return sum / float64(len(words)-mattrWindow+1) / mattrWindow
}

// lexicalRichness returns the Shannon entropy of words' types in bits and the
// share of words whose type is seen once.
func lexicalRichness(words []string) (entropy, hapax float64) {
	counts := map[string]int{}
	for _, w := range words {
		counts[w]++
	}

	n := float64(len(words))
	once := 0

	for _, c := range counts {
		p := float64(c) / n
		entropy -= p * math.Log2(p)

		if c == 1 {
			once++
		}
	}

	return entropy, float64(once) / n
}

func lexicalDensity(words []string) float64 {
	content := 0

	for _, w := range words {
		if !isShapeFunctionWord[w] {
			content++
		}
	}

	return float64(content) / float64(len(words))
}

func functionWordRates(words []string) map[string]float64 {
	counts := map[string]int{}

	for _, w := range words {
		if isShapeFunctionWord[w] {
			counts[w]++
		}
	}

	rates := make(map[string]float64, len(shapeFunctionWords))
	for _, w := range shapeFunctionWords {
		rates[w] = round2(1000 * float64(counts[w]) / float64(len(words)))
	}

	return rates
}

// adjacentOverlap averages the Jaccard similarity of the content words of
// each sentence and the next, over the pairs that hold a content word.
func adjacentOverlap(sentences [][]string) float64 {
	content := func(sentence []string) map[string]bool {
		set := map[string]bool{}

		for _, w := range sentence {
			if w == codeWord {
				continue
			}

			if l := lexical(w); l != "" && !isShapeFunctionWord[l] {
				set[l] = true
			}
		}

		return set
	}

	var sum float64

	pairs := 0

	for i := 0; i+1 < len(sentences); i++ {
		a, b := content(sentences[i]), content(sentences[i+1])
		if len(a)+len(b) == 0 {
			continue
		}

		sum += overlap(a, b)
		pairs++
	}

	if pairs == 0 {
		return 0
	}

	return sum / float64(pairs)
}
