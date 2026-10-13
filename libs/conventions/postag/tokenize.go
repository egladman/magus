package postag

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Tokenize splits one sentence's text into the words [Tag] reads, as UD
// English EWT tokenizes them: punctuation is a word of its own, a clitic is
// split from its host ("don't" is "do" "n't", "it's" is "it" "'s", "can't" is
// "ca" "n't", "cannot" is "can" "not"), and a run of "...", "?!" or "!!" is
// one word. A hyphenated word, a URL, a path, an email address, a number such
// as "3.14" or "1,000" and an abbreviation such as "e.g." or "Dr." stay
// whole. It returns nil for text with no words.
//
// EWT splits most hyphenated compounds ("full-fledged" is "full" "-"
// "fledged"); keeping them whole costs little, since the tagger reads an
// unknown word by its suffix.
func Tokenize(sentence string) []string {
	var out []string
	for _, field := range strings.Fields(sentence) {
		if whole(field) {
			out = appendWhole(out, field)
			continue
		}
		for _, piece := range splitInside(field) {
			out = appendWord(out, piece)
		}
	}
	return out
}

const (
	opening = `([{"'` + "`“‘<$*"
	closing = `)]}"'` + "”’>,;:%*"
	ends    = ".!?…"
)

// abbreviations end in a period that is part of the word, not the sentence's.
var abbreviations = map[string]bool{
	"approx": true, "ave": true, "blvd": true, "co": true, "corp": true, "dept": true, "dr": true, "etc": true,
	"gen": true, "inc": true, "jr": true, "ltd": true, "mr": true, "mrs": true, "ms": true, "prof": true,
	"sr": true, "st": true, "vs": true,
}

// clitics are the suffixes EWT splits from their host.
var clitics = []string{"n't", "'re", "'ve", "'ll", "'s", "'d", "'m"}

// fused are the words EWT splits though no apostrophe marks the seam.
var fused = map[string]int{"cannot": 3, "gonna": 3, "wanna": 3, "gotta": 3}

func whole(field string) bool {
	field = strings.TrimLeft(field, opening)
	return strings.Contains(field, "://") || strings.Contains(strings.Trim(field, "@"), "@") ||
		strings.HasPrefix(field, "www.") || strings.HasPrefix(field, "/") || strings.HasPrefix(field, "./") ||
		strings.HasPrefix(field, "../") || strings.HasPrefix(field, "~/")
}

// appendWhole keeps a URL, path or address whole but for the punctuation
// around it, so the period that ends a sentence on a link is still its own.
func appendWhole(out []string, field string) []string {
	lead, core := peelLeading(field)
	out = append(out, lead...)
	core, trail := peelTrailing(core, func(string) bool { return false })
	if core != "" {
		out = append(out, core)
	}
	return append(out, trail...)
}

// splitInside splits field at the dashes and the inner runs of dots that join
// two words with no space, as in "ran—twice" and "point...lol".
func splitInside(field string) []string {
	var out []string
	for field != "" {
		at, size := strings.IndexAny(field, "—–"), 0
		if at >= 0 {
			_, size = utf8.DecodeRuneInString(field[at:])
		}
		for _, run := range []string{"--", ".."} {
			j := strings.Index(field, run)
			if j < 0 || (at >= 0 && j > at) {
				continue
			}
			n := len(field[j:]) - len(strings.TrimLeft(field[j:], run[:1]))
			if run == ".." && (j == 0 || j+n == len(field)) {
				continue
			}
			at, size = j, n
		}
		if at < 0 {
			return append(out, field)
		}
		out = append(out, field[:at], field[at:at+size])
		field = field[at+size:]
	}
	return out
}

func appendWord(out []string, word string) []string {
	if word == "" {
		return out
	}
	lead, core := peelLeading(word)
	out = append(out, lead...)
	core, trail := peelTrailing(core, abbreviation)
	out = appendClitics(out, core)
	return append(out, trail...)
}

func peelLeading(word string) ([]string, string) {
	var lead []string
	for word != "" {
		r, size := utf8.DecodeRuneInString(word)
		if !strings.ContainsRune(opening, r) || startsClitic(word) {
			break
		}
		lead = append(lead, word[:size])
		word = word[size:]
	}
	return lead, word
}

// startsClitic reports whether word, a quote mark leading it, is itself a
// clitic, as in "I 'm" typed with a space.
func startsClitic(word string) bool {
	for _, c := range clitics {
		if c[0] == '\'' && strings.EqualFold(straight(word), c) {
			return true
		}
	}
	return false
}

// peelTrailing returns word without its closing punctuation, and that
// punctuation as words in order. keep reports whether a final period belongs
// to the word.
func peelTrailing(word string, keep func(string) bool) (string, []string) {
	var trail []string
	for word != "" {
		r, size := utf8.DecodeLastRuneInString(word)
		switch {
		case strings.ContainsRune(ends, r):
			if r == '.' && keep(word) {
				return word, trail
			}
			run := len(word) - len(strings.TrimRight(word, ends))
			if run == len(word) {
				return "", append([]string{word}, trail...)
			}
			trail = append([]string{word[len(word)-run:]}, trail...)
			word = word[:len(word)-run]
		case strings.ContainsRune(closing, r) && size < len(word):
			trail = append([]string{word[len(word)-size:]}, trail...)
			word = word[:len(word)-size]
		default:
			return word, trail
		}
	}
	return word, trail
}

// abbreviation reports whether word's final period is its own: "e.g." and
// "U.S." are short letter groups between periods, "J." is an initial, and
// "Dr." is listed. "README.md." ends a sentence.
func abbreviation(word string) bool {
	stem := strings.TrimSuffix(word, ".")
	if groups := strings.Split(stem, "."); len(groups) > 1 && !slices.ContainsFunc(groups, notInitials) {
		return true
	}
	if r, size := utf8.DecodeRuneInString(stem); size == len(stem) && unicode.IsUpper(r) && r != 'I' {
		return true
	}
	return abbreviations[strings.ToLower(stem)]
}

func notInitials(group string) bool {
	return group == "" || len(group) > 2 || strings.IndexFunc(group, func(r rune) bool { return !unicode.IsLetter(r) }) >= 0
}

func appendClitics(out []string, word string) []string {
	if word == "" {
		return out
	}
	lower := strings.ToLower(straight(word))
	if n, ok := fused[lower]; ok {
		return append(out, word[:n], word[n:])
	}
	for _, c := range clitics {
		if len(lower) > len(c) && strings.HasSuffix(lower, c) {
			cut := len(word) - suffixBytes(word, len(c))
			return append(out, word[:cut], word[cut:])
		}
	}
	return append(out, word)
}

// suffixBytes is the byte length of word's last n runes once a curly
// apostrophe is counted as the one rune it is.
func suffixBytes(word string, n int) int {
	i := len(word)
	for range n {
		_, size := utf8.DecodeLastRuneInString(word[:i])
		i -= size
	}
	return len(word) - i
}

func straight(word string) string { return strings.ReplaceAll(word, "’", "'") }
