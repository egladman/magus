package feature

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ModelMagic opens a postag model; the trainer writes it and postag checks it.
const ModelMagic = "postag1\n"

// Start and Start2 are the tags assumed before a sentence's first word.
const (
	Start  uint16 = 0xffff
	Start2 uint16 = 0xfffe
)

const (
	fBias byte = iota
	fSuffix1
	fSuffix2
	fSuffix3
	fSuffix4
	fPrefix1
	fPrefix2
	fPrefix3
	fShape
	fPrevTag
	fPrev2Tag
	fPrevTags
	fWord
	fPrevTagWord
	fPrevWord
	fPrevSuffix
	fPrev2Word
	fNextWord
	fNextSuffix
	fNext2Word
	fPrevWordWord
	fWordNextWord
	fPrevTagSuffix
	fShapePrevTag
)

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// Hash is one feature hashed with FNV-1a; the tagger and the trainer key
// weights by it, so both must extract features through this package.
type Hash uint64

func newFeature(template byte) Hash {
	return (fnvOffset ^ Hash(template)) * fnvPrime
}

// 0xff never occurs in UTF-8, so it separates parts without escaping.
func (h Hash) str(s string) Hash {
	for i := 0; i < len(s); i++ {
		h = (h ^ Hash(s[i])) * fnvPrime
	}
	return (h ^ 0xff) * fnvPrime
}

func (h Hash) tag(t uint16) Hash {
	h = (h ^ Hash(t&0xff)) * fnvPrime
	return (h ^ Hash(t>>8)) * fnvPrime
}

// normalize folds the open classes that are rare as strings but regular as shapes.
func normalize(word string) string {
	switch {
	case strings.Contains(word, "-") && !strings.HasPrefix(word, "-"):
		return "!HYPHEN"
	case len(word) == 4 && isDigits(word):
		return "!YEAR"
	case word != "" && word[0] >= '0' && word[0] <= '9':
		return "!DIGITS"
	}
	return strings.ToLower(word)
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// shape maps each rune to its class (X, x, d, or itself) and collapses runs,
// so "McDonald's" is "XxXx'x".
func shape(word string) string {
	var b strings.Builder
	var last rune
	for _, r := range word {
		c := r
		switch {
		case unicode.IsUpper(r):
			c = 'X'
		case unicode.IsLower(r):
			c = 'x'
		case unicode.IsDigit(r):
			c = 'd'
		}
		if c != last {
			b.WriteRune(c)
			last = c
		}
		if b.Len() >= 8 {
			break
		}
	}
	return b.String()
}

func suffix(s string, n int) string {
	for i := len(s); i > 0; {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
		if n--; n == 0 {
			return s[i:]
		}
	}
	return s
}

func prefix(s string, n int) string {
	for i := 0; i < len(s); {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if n--; n == 0 {
			return s[:i]
		}
	}
	return s
}

// Padded returns the normalized words with two sentinels on each side, so
// context[i+2] is word i.
func Padded(words []string) []string {
	context := make([]string, 0, len(words)+4)
	context = append(context, "-START-", "-START2-")
	for _, w := range words {
		context = append(context, normalize(w))
	}
	return append(context, "-END-", "-END2-")
}

// Extract appends the hashed features of word i, given the two tags before it.
func Extract(dst []Hash, context []string, i int, word string, prev, prev2 uint16) []Hash {
	lower := strings.ToLower(word)
	w := context[i+2]
	sh := shape(word)
	return append(dst,
		newFeature(fBias),
		newFeature(fSuffix1).str(suffix(lower, 1)),
		newFeature(fSuffix2).str(suffix(lower, 2)),
		newFeature(fSuffix3).str(suffix(lower, 3)),
		newFeature(fSuffix4).str(suffix(lower, 4)),
		newFeature(fPrefix1).str(prefix(word, 1)),
		newFeature(fPrefix2).str(prefix(lower, 2)),
		newFeature(fPrefix3).str(prefix(lower, 3)),
		newFeature(fShape).str(sh),
		newFeature(fPrevTag).tag(prev),
		newFeature(fPrev2Tag).tag(prev2),
		newFeature(fPrevTags).tag(prev).tag(prev2),
		newFeature(fWord).str(w),
		newFeature(fPrevTagWord).tag(prev).str(w),
		newFeature(fPrevWord).str(context[i+1]),
		newFeature(fPrevSuffix).str(suffix(context[i+1], 3)),
		newFeature(fPrev2Word).str(context[i]),
		newFeature(fNextWord).str(context[i+3]),
		newFeature(fNextSuffix).str(suffix(context[i+3], 3)),
		newFeature(fNext2Word).str(context[i+4]),
		newFeature(fPrevWordWord).str(context[i+1]).str(w),
		newFeature(fWordNextWord).str(w).str(context[i+3]),
		newFeature(fPrevTagSuffix).tag(prev).str(suffix(lower, 3)),
		newFeature(fShapePrevTag).str(sh).tag(prev),
	)
}
