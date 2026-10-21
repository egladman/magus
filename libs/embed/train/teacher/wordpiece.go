package teacher

import (
	"bufio"
	"encoding/json"
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
// greedy longest-match WordPiece with "##" continuations, and, when the tokenizer has
// one, the [CLS] A [SEP] template. It is safe for concurrent use.
type WordPiece struct {
	vocab map[string]int32
	// raw added tokens match in the text before normalizing, normalized ones in the
	// normalized text.
	raw, normalized []addedToken
	template        bool
	cls             int32
	sep             int32
	unk             int32
}

// addedToken is a token Hugging Face matches in the text before WordPiece runs.
type addedToken struct {
	content string
	id      int32
	// singleWord tokens match only with no letter, digit or "_" on either side.
	singleWord bool
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
	w := newWordPiece(tokens)
	for _, t := range specialTokens {
		id, ok := w.vocab[t]
		if !ok {
			return nil, fmt.Errorf("vocabulary has no %s", t)
		}
		w.raw = append(w.raw, addedToken{content: t, id: id})
	}
	w.template = true
	w.cls, w.sep, w.unk = w.vocab["[CLS]"], w.vocab["[SEP]"], w.vocab["[UNK]"]
	return w, nil
}

func newWordPiece(tokens []string) *WordPiece {
	w := &WordPiece{vocab: make(map[string]int32, len(tokens))}
	for i, t := range tokens {
		if _, dup := w.vocab[t]; !dup {
			w.vocab[t] = int32(i)
		}
	}
	return w
}

// tokenizerJSON is the subset of a Hugging Face tokenizer.json LoadTokenizerJSON reads.
type tokenizerJSON struct {
	// lstrip and rstrip are not read: the whitespace they take, the pre-tokenizer drops.
	AddedTokens []struct {
		ID         int32  `json:"id"`
		Content    string `json:"content"`
		Normalized bool   `json:"normalized"`
		SingleWord bool   `json:"single_word"`
	} `json:"added_tokens"`
	Normalizer *struct {
		Type         string `json:"type"`
		CleanText    bool   `json:"clean_text"`
		ChineseChars bool   `json:"handle_chinese_chars"`
		StripAccents *bool  `json:"strip_accents"`
		Lowercase    bool   `json:"lowercase"`
	} `json:"normalizer"`
	PreTokenizer *struct {
		Type string `json:"type"`
	} `json:"pre_tokenizer"`
	PostProcessor json.RawMessage `json:"post_processor"`
	Model         struct {
		Type     string           `json:"type"`
		Unk      string           `json:"unk_token"`
		Prefix   string           `json:"continuing_subword_prefix"`
		MaxChars int              `json:"max_input_chars_per_word"`
		Vocab    map[string]int32 `json:"vocab"`
	} `json:"model"`
}

// LoadTokenizerJSON reads a Hugging Face tokenizer.json whose pipeline is the one WordPiece
// reproduces, with no post-processor: Encode then adds no [CLS] or [SEP]. Any other
// normalizer, pre-tokenizer, model or post-processor is an error, as is a vocabulary
// whose ids are not 0 to n-1.
func LoadTokenizerJSON(path string) (*WordPiece, error) {
	var tj tokenizerJSON
	if err := readJSON(path, &tj); err != nil {
		return nil, err
	}
	n, pre, m := tj.Normalizer, tj.PreTokenizer, tj.Model
	switch {
	case n == nil || n.Type != "BertNormalizer" || !n.CleanText || !n.ChineseChars || !n.Lowercase ||
		(n.StripAccents != nil && !*n.StripAccents):
		return nil, fmt.Errorf("%s: normalizer is not BertNormalizer with clean_text, handle_chinese_chars, lowercase and stripped accents", path)
	case pre == nil || pre.Type != "BertPreTokenizer":
		return nil, fmt.Errorf("%s: pre_tokenizer is not BertPreTokenizer", path)
	case len(tj.PostProcessor) > 0 && string(tj.PostProcessor) != "null":
		return nil, fmt.Errorf("%s: post_processor %s is not supported", path, tj.PostProcessor)
	case m.Type != "WordPiece" || m.Prefix != "##" || m.MaxChars != maxCharsPerWord:
		return nil, fmt.Errorf("%s: model is not WordPiece with ## continuations and %d chars per word", path, maxCharsPerWord)
	}
	tokens := make([]string, len(m.Vocab))
	for t, id := range m.Vocab {
		if id < 0 || int(id) >= len(tokens) || tokens[id] != "" {
			return nil, fmt.Errorf("%s: token ids are not 0 to %d", path, len(tokens)-1)
		}
		tokens[id] = t
	}
	w := newWordPiece(tokens)
	unk, ok := w.vocab[m.Unk]
	if !ok {
		return nil, fmt.Errorf("%s: vocabulary has no unk_token %q", path, m.Unk)
	}
	w.unk = unk
	for _, a := range tj.AddedTokens {
		if id, ok := w.vocab[a.Content]; !ok || id != a.ID || a.Content == "" {
			return nil, fmt.Errorf("%s: added token %q is not id %d of the vocabulary", path, a.Content, a.ID)
		}
		t := addedToken{content: a.Content, id: a.ID, singleWord: a.SingleWord}
		if a.Normalized {
			t.content = normalize(a.Content)
			w.normalized = append(w.normalized, t)
		} else {
			w.raw = append(w.raw, t)
		}
	}
	return w, nil
}

// Encode returns the word pieces of text, inside [CLS] and [SEP] when the tokenizer has
// that template, keeping the first maxLen ids, [CLS] and [SEP] included. maxLen must be at
// least 2.
func (w *WordPiece) Encode(text string, maxLen int) []int32 {
	if !w.template {
		ids := w.Pieces(text)
		return ids[:min(len(ids), maxLen)]
	}
	ids := append([]int32{w.cls}, w.Pieces(text)...)
	if len(ids) > maxLen-1 {
		ids = ids[:maxLen-1]
	}
	return append(ids, w.sep)
}

// Pieces returns the word pieces of text, with no template and no truncation.
func (w *WordPiece) Pieces(text string) []int32 {
	var ids []int32
	for _, seg := range cut(text, w.raw) {
		if seg.added {
			ids = append(ids, seg.id)
			continue
		}
		for _, sub := range cut(normalize(seg.text), w.normalized) {
			if sub.added {
				ids = append(ids, sub.id)
				continue
			}
			for _, word := range preTokenize(sub.text) {
				ids = w.appendWord(ids, word)
			}
		}
	}
	return ids
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

type segment struct {
	text  string
	id    int32
	added bool
}

// cut splits text around its added tokens, leftmost match first, the longest at a tie.
func cut(text string, added []addedToken) []segment {
	var out []segment
	for from := 0; ; {
		at, best := -1, addedToken{}
		for _, a := range added {
			if i := a.find(text, from); i >= 0 && (at < 0 || i < at || i == at && len(a.content) > len(best.content)) {
				at, best = i, a
			}
		}
		if at < 0 {
			return append(out, segment{text: text[from:]})
		}
		out = append(out, segment{text: text[from:at]}, segment{id: best.id, added: true})
		from = at + len(best.content)
	}
}

// find returns the first index at or after from where a matches text, or -1. The runes
// either side of a single-word match are read in text, so a match right after an earlier
// cut still sees its neighbor.
func (a addedToken) find(text string, from int) int {
	for from <= len(text) {
		i := strings.Index(text[from:], a.content)
		if i < 0 {
			return -1
		}
		i += from
		before, _ := utf8.DecodeLastRuneInString(text[:i])
		after, _ := utf8.DecodeRuneInString(text[i+len(a.content):])
		if !a.singleWord || !isWordRune(before) && !isWordRune(after) {
			return i
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		from = i + size
	}
	return -1
}

// isWordRune is Rust's char::is_alphanumeric or "_", what Hugging Face's single_word
// tests. utf8.RuneError, which an empty side decodes to, is neither.
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }

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
