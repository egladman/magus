package wordclass

// Tagged is one word and its tags. XPOS is the Penn Treebank tag, such as
// [VBN].
type Tagged struct {
	Word string
	UPOS UPOS
	XPOS XPOS
}

// Tag tags one sentence's words, tokenized as UD English EWT tokenizes them:
// punctuation split off and clitics separate, so "don't" is "do" "n't". It
// returns one Tagged per word, in order, and nil for no words. The result
// depends only on words. Tag is safe for concurrent use; the first call
// decodes the embedded model.
func Tag(words []string) []Tagged { return embedded().Tag(words) }

// Tag tags words with m, as the package-level Tag does with the embedded
// model.
func (m *Model) Tag(words []string) []Tagged {
	if len(words) == 0 {
		return nil
	}

	out := make([]Tagged, len(words))
	context := Padded(words)
	scores := make([]int32, len(m.classes))

	var feats []Hash

	prev, prev2 := Start, Start2

	for i, w := range words {
		c, ok := m.dict[w]
		if !ok {
			feats = Extract(feats[:0], context, i, w, prev, prev2)
			c = m.predict(feats, scores)
		}

		out[i] = Tagged{Word: w, UPOS: m.classes[c].upos, XPOS: m.classes[c].xpos}
		prev, prev2 = c, prev
	}

	return out
}
