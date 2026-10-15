package wordclass

// Word is one word and its tags. Penn is the Penn Treebank tag EWT gives it,
// such as "VBN", which tells a participle from a finite verb where Class
// cannot.
type Word struct {
	Text  string
	Class Class
	Penn  string
}

// Tag tags one sentence's words, split as UD English EWT splits them:
// punctuation split off and clitics separate, so "don't" is "do" "n't". It
// returns one Word per word, in order, and nil for no words. The result
// depends only on words. Tag is safe for concurrent use; the first call
// decodes the embedded model.
func Tag(words []string) []Word { return embedded().Tag(words) }

// Tag tags words with m, as the package-level Tag does with the embedded
// model.
func (m *Model) Tag(words []string) []Word {
	if len(words) == 0 {
		return nil
	}

	out := make([]Word, len(words))
	padded := Pad(words)
	scores := make([]int32, len(m.classes))

	var feats []Feature

	prev, prev2 := SentenceStart, SentenceStart2

	for i, w := range words {
		c, ok := m.dict[w]
		if !ok {
			feats = AppendFeatures(feats[:0], padded, i, w, prev, prev2)
			c = m.predict(feats, scores)
		}

		out[i] = Word{Text: w, Class: m.classes[c].class, Penn: m.classes[c].penn}
		prev, prev2 = c, prev
	}

	return out
}
