package wordclass

import "strconv"

// UPOS is a Universal Dependencies part-of-speech tag. The zero value is no tag.
type UPOS uint8

const (
	ADJ UPOS = iota + 1
	ADP
	ADV
	AUX
	CCONJ
	DET
	INTJ
	NOUN
	NUM
	PART
	PRON
	PROPN
	PUNCT
	SCONJ
	SYM
	VERB
	X
)

var uposNames = [...]string{
	ADJ: "ADJ", ADP: "ADP", ADV: "ADV", AUX: "AUX", CCONJ: "CCONJ", DET: "DET",
	INTJ: "INTJ", NOUN: "NOUN", NUM: "NUM", PART: "PART", PRON: "PRON", PROPN: "PROPN",
	PUNCT: "PUNCT", SCONJ: "SCONJ", SYM: "SYM", VERB: "VERB", X: "X",
}

// String returns the tag as UD spells it, such as "NOUN", or "UPOS(n)" for a
// value that is not one of the constants.
func (u UPOS) String() string {
	if int(u) < len(uposNames) && uposNames[u] != "" {
		return uposNames[u]
	}
	return "UPOS(" + strconv.Itoa(int(u)) + ")"
}

func parseUPOS(s string) (UPOS, bool) {
	for u, name := range uposNames {
		if name == s && name != "" {
			return UPOS(u), true
		}
	}
	return 0, false
}
