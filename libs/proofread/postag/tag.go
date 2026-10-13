package postag

import "github.com/egladman/magus/libs/proofread/postag/internal/feature"

// Tagged is one word's tags. XPOS is the Penn Treebank tag, such as "VBN".
type Tagged struct {
	UPOS UPOS
	XPOS string
}

// Tag tags one sentence's words, tokenized as UD English EWT tokenizes them:
// punctuation split off and clitics separate, so "don't" is "do" "n't". It
// returns one Tagged per word, in order, and nil for no words. The result
// depends only on words. Tag is safe for concurrent use; the first call
// decodes the embedded model.
//
// Each class the model predicts is an XPOS and UPOS pair, not an XPOS mapped to
// a UPOS afterwards: EWT splits IN into ADP and SCONJ and RB into ADV and PART
// by context, which a mapping from XPOS and word cannot recover.
func Tag(words []string) []Tagged {
	if len(words) == 0 {
		return nil
	}
	m := loadModel()
	out := make([]Tagged, len(words))
	context := feature.Padded(words)
	scores := make([]int32, len(m.classes))
	var feats []feature.Hash
	prev, prev2 := feature.Start, feature.Start2
	for i, w := range words {
		c, ok := m.dict[w]
		if !ok {
			feats = feature.Extract(feats[:0], context, i, w, prev, prev2)
			c = m.predict(feats, scores)
		}
		out[i] = Tagged{UPOS: m.classes[c].upos, XPOS: m.classes[c].xpos}
		prev, prev2 = c, prev
	}
	return out
}
