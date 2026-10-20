package textrank

import (
	"strings"
	"unicode"
)

// The fields a ranked document may carry. A field the weight file names no weight for is
// indexed but never scored.
const (
	FieldName      = "name"
	FieldPath      = "path"
	FieldDoc       = "doc"
	FieldSignature = "signature"
	FieldBody      = "body"
)

// SplitIdentifier splits an identifier into its lowercased words, in order: camelCase,
// PascalCase with acronyms (HTTPServer -> http, server), snake_case, kebab-case, dots and
// slashes. Digits stay attached to the word before them (sha256Sum -> sha256, sum).
func SplitIdentifier(s string) []string {
	var parts []string
	var word []rune
	flush := func() {
		if len(word) > 0 {
			parts = append(parts, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if len(word) > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			lowerToUpper := unicode.IsLower(prev) || unicode.IsDigit(prev)
			acronymEnd := unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if lowerToUpper || acronymEnd {
				flush()
			}
		}
		word = append(word, r)
	}
	flush()
	return parts
}

const errorOperator = "!>"

// Tokens lowercases text and splits it on non-word runes. Every identifier-shaped word
// yields the whole word and then its SplitIdentifier parts, so a query for either finds
// it. Nothing is stemmed. The one punctuation token is Buzz's error operator "!>", kept
// whole so a search for what a Buzz function throws finds it; every other non-word rune
// is dropped.
func Tokens(text string) []string {
	var out []string
	emit := func(word string) {
		out = append(out, strings.ToLower(word))
		if parts := SplitIdentifier(word); len(parts) > 1 {
			out = append(out, parts...)
		}
	}
	start := -1
	for i, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			emit(text[start:i])
			start = -1
		}
		if strings.HasPrefix(text[i:], errorOperator) {
			out = append(out, errorOperator)
		}
	}
	if start >= 0 {
		emit(text[start:])
	}
	return out
}
