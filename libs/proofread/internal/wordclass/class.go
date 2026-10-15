package wordclass

import "strconv"

// Class is a word's part of speech, one of the 17 Universal Dependencies
// tags. The zero value is no class.
type Class uint8

const (
	Adjective Class = iota + 1
	Adposition
	Adverb
	Auxiliary
	CoordinatingConjunction
	Determiner
	Interjection
	Noun
	Numeral
	Particle
	Pronoun
	ProperNoun
	Punctuation
	SubordinatingConjunction
	Symbol
	Verb
	Other
)

var classNames = [...]string{
	Adjective: "ADJ", Adposition: "ADP", Adverb: "ADV", Auxiliary: "AUX", CoordinatingConjunction: "CCONJ",
	Determiner: "DET", Interjection: "INTJ", Noun: "NOUN", Numeral: "NUM", Particle: "PART", Pronoun: "PRON",
	ProperNoun: "PROPN", Punctuation: "PUNCT", SubordinatingConjunction: "SCONJ", Symbol: "SYM", Verb: "VERB",
	Other: "X",
}

// String returns the class as UD spells it, such as "NOUN", or "Class(n)"
// for a value that is not one of the constants.
func (c Class) String() string {
	if int(c) < len(classNames) && classNames[c] != "" {
		return classNames[c]
	}
	return "Class(" + strconv.Itoa(int(c)) + ")"
}

func parseClass(s string) (Class, bool) {
	for c, name := range classNames {
		if name == s && name != "" {
			return Class(c), true
		}
	}
	return 0, false
}
