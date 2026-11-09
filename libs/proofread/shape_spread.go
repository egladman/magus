package proofread

import (
	"math"

	"github.com/egladman/magus/libs/proofread/internal/wordclass"
)

// minSpreadUnits is the fewest paragraphs, or list items, a spread over them
// is measured on. Two units have a spread, but not one worth ranking.
const minSpreadUnits = 3

// spread is what [MeasureShape] gathers for its spread measures.
type spread struct {
	sentences      [][]string
	paragraphWords []int
	itemWords      []int
	itemOpeners    []string
}

// add records one paragraph's sentences. item reports whether the paragraph
// is a list item.
func (sp *spread) add(split [][]string, item bool) {
	if len(split) == 0 {
		return
	}

	var words int
	for _, sentence := range split {
		words += len(sentence)
	}

	sp.paragraphWords = append(sp.paragraphWords, words)

	if item {
		sp.itemWords = append(sp.itemWords, words)
		sp.itemOpeners = append(sp.itemOpeners, sentenceOpener(split[0]))
	}
}

// measure sets s's spread measures, and withholds each one its text is too
// short for.
func (sp *spread) measure(s *Shape) {
	if len(sp.paragraphWords) >= minSpreadUnits {
		s.ParagraphWordsStdDev = round2(stdDev(sp.paragraphWords))
	} else {
		s.Withheld = append(s.Withheld, "paragraph_words_sd")
	}

	if len(sp.itemWords) >= minSpreadUnits {
		s.ItemWordsStdDev = round2(stdDev(sp.itemWords))
		s.ItemOpenerShare = round4(topShare(sp.itemOpeners))
	} else {
		s.Withheld = append(s.Withheld, "item_words_sd", "item_opener_share")
	}

	if len(sp.sentences) < minShapeSentences {
		s.Withheld = append(s.Withheld, "sentence_opener_share", "opener_class_entropy", "subordinate_share")

		return
	}

	openers := make([]string, 0, len(sp.sentences))
	classes := make([]string, 0, len(sp.sentences))

	var subordinate int

	for _, sentence := range sp.sentences {
		openers = append(openers, sentenceOpener(sentence))

		t := tagSentence(sentence)
		if c, ok := openerClass(t); ok {
			classes = append(classes, c.String())
		}

		for _, w := range t.tags {
			if w.Class == wordclass.SubordinatingConjunction {
				subordinate++

				break
			}
		}
	}

	s.SentenceOpenerShare = round4(topShare(openers))
	s.OpenerClassEntropy = round4(entropy(classes))
	s.SubordinateShare = round4(float64(subordinate) / float64(len(sp.sentences)))
}

// sentenceOpener is the first word of sentence as [lexical] reads it, or ""
// when it opens on a code span or on punctuation alone.
func sentenceOpener(sentence []string) string {
	for _, w := range sentence {
		if w == codeWord {
			return ""
		}

		if l := lexical(w); l != "" {
			return l
		}
	}

	return ""
}

// openerClass is the class of t's first word that is not punctuation.
func openerClass(t tagged) (wordclass.Class, bool) {
	for _, w := range t.tags {
		if w.Class != wordclass.Punctuation {
			return w.Class, true
		}
	}

	return 0, false
}

// stdDev is the population standard deviation of ns, which is not empty.
func stdDev(ns []int) float64 {
	var sum float64
	for _, n := range ns {
		sum += float64(n)
	}

	mean := sum / float64(len(ns))

	var squares float64
	for _, n := range ns {
		squares += (float64(n) - mean) * (float64(n) - mean)
	}

	return math.Sqrt(squares / float64(len(ns)))
}

// topShare is the share of values equal to the most common one. An empty
// value, an opener [sentenceOpener] could not read, counts toward the total and
// never as the most common.
func topShare(values []string) float64 {
	counts := map[string]int{}

	var top int

	for _, v := range values {
		if v == "" {
			continue
		}

		counts[v]++
		top = max(top, counts[v])
	}

	return float64(top) / float64(len(values))
}

// entropy is the Shannon entropy, in bits, of values, and 0 for none.
func entropy(values []string) float64 {
	counts := map[string]int{}
	for _, v := range values {
		counts[v]++
	}

	var bits float64

	for _, n := range counts {
		p := float64(n) / float64(len(values))
		bits -= p * math.Log2(p)
	}

	return bits
}
