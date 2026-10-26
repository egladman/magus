package wordclass

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SentenceStart and SentenceStart2 are the classes assumed before a
// sentence's first word.
const (
	SentenceStart  uint16 = 0xffff
	SentenceStart2 uint16 = 0xfffe
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

// Feature is one context feature hashed with FNV-1a; the tagger and the trainer key
// weights by it, so both must extract features through this package.
type Feature uint64

func newFeature(template byte) Feature {
	return (fnvOffset ^ Feature(template)) * fnvPrime
}

// 0xff never occurs in UTF-8, so it separates parts without escaping.
func (h Feature) string(s string) Feature {
	for i := 0; i < len(s); i++ {
		h = (h ^ Feature(s[i])) * fnvPrime
	}
	return (h ^ 0xff) * fnvPrime
}

func (h Feature) tag(t uint16) Feature {
	h = (h ^ Feature(t&0xff)) * fnvPrime
	return (h ^ Feature(t>>8)) * fnvPrime
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

// Pad returns the normalized words with two sentinels on each side, so
// padded[i+2] is word i.
func Pad(words []string) []string {
	padded := make([]string, 0, len(words)+4)
	padded = append(padded, "-START-", "-START2-")
	for _, w := range words {
		padded = append(padded, normalize(w))
	}
	return append(padded, "-END-", "-END2-")
}

// AppendFeatures appends the features of word, the i-th word of the
// sentence Pad made padded from, given the two classes before it.
func AppendFeatures(dst []Feature, padded []string, i int, word string, prev, prev2 uint16) []Feature {
	lower := strings.ToLower(word)
	w := padded[i+2]
	sh := shape(word)
	return append(dst,
		newFeature(fBias),
		newFeature(fSuffix1).string(suffix(lower, 1)),
		newFeature(fSuffix2).string(suffix(lower, 2)),
		newFeature(fSuffix3).string(suffix(lower, 3)),
		newFeature(fSuffix4).string(suffix(lower, 4)),
		newFeature(fPrefix1).string(prefix(word, 1)),
		newFeature(fPrefix2).string(prefix(lower, 2)),
		newFeature(fPrefix3).string(prefix(lower, 3)),
		newFeature(fShape).string(sh),
		newFeature(fPrevTag).tag(prev),
		newFeature(fPrev2Tag).tag(prev2),
		newFeature(fPrevTags).tag(prev).tag(prev2),
		newFeature(fWord).string(w),
		newFeature(fPrevTagWord).tag(prev).string(w),
		newFeature(fPrevWord).string(padded[i+1]),
		newFeature(fPrevSuffix).string(suffix(padded[i+1], 3)),
		newFeature(fPrev2Word).string(padded[i]),
		newFeature(fNextWord).string(padded[i+3]),
		newFeature(fNextSuffix).string(suffix(padded[i+3], 3)),
		newFeature(fNext2Word).string(padded[i+4]),
		newFeature(fPrevWordWord).string(padded[i+1]).string(w),
		newFeature(fWordNextWord).string(w).string(padded[i+3]),
		newFeature(fPrevTagSuffix).tag(prev).string(suffix(lower, 3)),
		newFeature(fShapePrevTag).string(sh).tag(prev),
	)
}
