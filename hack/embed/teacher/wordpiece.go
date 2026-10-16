package teacher

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// specialTokens are the added tokens of the BERT uncased tokenizer.json both teachers ship.
// Hugging Face matches them in the raw text before normalizing, so "[SEP]" in a document
// becomes id 102, not "[", "sep", "]".
var specialTokens = []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]", "[MASK]"}

const maxCharsPerWord = 100

// WordPiece reproduces the Hugging Face tokenizers pipeline of bert-base-uncased:
// BertNormalizer (clean text, pad CJK, strip accents, lowercase), BertPreTokenizer,
// greedy longest-match WordPiece with "##" continuations, and the [CLS] A [SEP] template.
// It is safe for concurrent use.
type WordPiece struct {
	vocab map[string]int32
	cls   int32
	sep   int32
	unk   int32
}

// LoadWordPiece reads a vocab.txt: one token per line, the line number is its id.
func LoadWordPiece(path string) (*WordPiece, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var tokens []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		tokens = append(tokens, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return NewWordPiece(tokens)
}

// NewWordPiece builds a tokenizer whose token ids are the indexes into tokens. tokens must
// hold every special token.
func NewWordPiece(tokens []string) (*WordPiece, error) {
	w := &WordPiece{vocab: make(map[string]int32, len(tokens))}
	for i, t := range tokens {
		if _, dup := w.vocab[t]; !dup {
			w.vocab[t] = int32(i)
		}
	}
	for _, t := range specialTokens {
		if _, ok := w.vocab[t]; !ok {
			return nil, fmt.Errorf("vocabulary has no %s", t)
		}
	}
	w.cls, w.sep, w.unk = w.vocab["[CLS]"], w.vocab["[SEP]"], w.vocab["[UNK]"]
	return w, nil
}

// Encode returns [CLS] tokens [SEP], keeping the first maxLen-2 word pieces. maxLen must be
// at least 2.
func (w *WordPiece) Encode(text string, maxLen int) []int32 {
	ids := []int32{w.cls}
	for _, seg := range splitSpecial(text) {
		if id, ok := w.vocab[seg]; ok && isSpecial(seg) {
			ids = append(ids, id)
			continue
		}
		for _, word := range preTokenize(normalize(seg)) {
			ids = w.appendWord(ids, word)
		}
	}
	if len(ids) > maxLen-1 {
		ids = ids[:maxLen-1]
	}
	return append(ids, w.sep)
}

func (w *WordPiece) appendWord(ids []int32, word string) []int32 {
	if utf8.RuneCountInString(word) > maxCharsPerWord {
		return append(ids, w.unk)
	}
	start := len(ids)
	for rest, prefix := word, ""; rest != ""; prefix = "##" {
		end := len(rest)
		for end > 0 {
			if id, ok := w.vocab[prefix+rest[:end]]; ok {
				ids = append(ids, id)
				break
			}
			_, size := utf8.DecodeLastRuneInString(rest[:end])
			end -= size
		}
		if end == 0 {
			return append(ids[:start], w.unk)
		}
		rest = rest[end:]
	}
	return ids
}

func isSpecial(s string) bool {
	for _, t := range specialTokens {
		if s == t {
			return true
		}
	}
	return false
}

// splitSpecial cuts text around every special token, leftmost match first.
func splitSpecial(text string) []string {
	var out []string
	for {
		at, tok := -1, ""
		for _, t := range specialTokens {
			if i := strings.Index(text, t); i >= 0 && (at < 0 || i < at) {
				at, tok = i, t
			}
		}
		if at < 0 {
			return append(out, text)
		}
		out = append(out, text[:at], tok)
		text = text[at+len(tok):]
	}
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == 0 || r == utf8.RuneError || isControl(r):
		case isWhitespace(r):
			b.WriteByte(' ')
		case isCJK(r):
			b.WriteByte(' ')
			b.WriteRune(r)
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	var out strings.Builder
	for _, r := range norm.NFD.String(b.String()) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		out.WriteRune(unicode.ToLower(r))
	}
	return out.String()
}

func preTokenize(s string) []string {
	var words []string
	start := -1
	for i, r := range s {
		switch {
		case isWhitespace(r):
			if start >= 0 {
				words = append(words, s[start:i])
			}
			start = -1
		case isPunct(r):
			if start >= 0 {
				words = append(words, s[start:i])
			}
			words = append(words, string(r))
			start = -1
		case start < 0:
			start = i
		}
	}
	if start >= 0 {
		words = append(words, s[start:])
	}
	return words
}

// isWhitespace is Rust's char::is_whitespace, the Unicode White_Space property.
func isWhitespace(r rune) bool { return unicode.IsSpace(r) }

// isControl is the tokenizers crate's is_control: categories Cc, Cf, Cn, Co and Cs, except
// tab, newline and carriage return. Vertical tab, form feed and NEL are Cc, so they are
// dropped rather than turned into spaces.
func isControl(r rune) bool {
	switch r {
	case '\t', '\n', '\r':
		return false
	}
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Co, unicode.Cs) || !unicode.In(r,
		unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z, unicode.C)
}

// isPunct is BertPreTokenizer's split set: ASCII punctuation, which includes symbols like
// $ + < = > ^ ` | ~, plus every Unicode P category.
func isPunct(r rune) bool {
	if r < 0x80 {
		return r > ' ' && r < 0x7f && !('0' <= r && r <= '9' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z')
	}
	return unicode.IsPunct(r)
}

// isCJK is the CJK Unified Ideographs block set BERT pads with spaces.
func isCJK(r rune) bool {
	return 0x4E00 <= r && r <= 0x9FFF || 0x3400 <= r && r <= 0x4DBF || 0x20000 <= r && r <= 0x2A6DF ||
		0x2A700 <= r && r <= 0x2B73F || 0x2B740 <= r && r <= 0x2B81F || 0x2B820 <= r && r <= 0x2CEAF ||
		0xF900 <= r && r <= 0xFAFF || 0x2F800 <= r && r <= 0x2FA1F
}
