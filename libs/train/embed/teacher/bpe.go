package teacher

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// gpt2Pattern is the split ByteLevel applies when its use_regex is true.
const gpt2Pattern = `'s|'t|'re|'ve|'m|'ll|'d| ?\p{L}+| ?\p{N}+| ?[^\s\p{L}\p{N}]+|\s+(?!\S)|\s+`

// lookaheadTail is how every supported pattern ends. Go's regexp has no lookahead, so
// the tail is matched by hand in splitter.matchLen.
const lookaheadTail = `|\s+(?!\S)|\s+`

// whiteSpace is Unicode White_Space, what Rust's \s matches; Go's \s is ASCII only.
const whiteSpace = `\t-\r \x{85}\x{A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}`

// BPE reproduces the Hugging Face tokenizers pipeline of a byte-level BPE tokenizer.json
// (GPT-2, OLMo and Qwen style): an optional Unicode normalizer, added tokens matched in
// the text, a pre-tokenizer of regex splits and the byte-to-character map, merges by
// rank, and an optional TemplateProcessing wrapper. It is safe for concurrent use.
type BPE struct {
	vocab        map[string]int
	merges       map[[2]string]int
	ignoreMerges bool
	normalize    func(string) string
	// raw added tokens match in the text before normalizing, normalized ones after.
	raw, normalized []addedToken
	splits          []bpeSplit
	// texts maps an id to the bytes Decode writes for it.
	texts          map[int][]byte
	prefix, suffix []int
}

// bpeSplit is one pre-tokenizer stage: a regex split, a byte mapping, or both.
type bpeSplit struct {
	re          *splitter
	bytes       bool
	prefixSpace bool
}

type bpeJSON struct {
	AddedTokens []struct {
		ID         int    `json:"id"`
		Content    string `json:"content"`
		Normalized bool   `json:"normalized"`
		SingleWord bool   `json:"single_word"`
		LStrip     bool   `json:"lstrip"`
		RStrip     bool   `json:"rstrip"`
	} `json:"added_tokens"`
	Normalizer    *bpeNormalizerJSON    `json:"normalizer"`
	PreTokenizer  *bpePreTokenizerJSON  `json:"pre_tokenizer"`
	PostProcessor *bpePostProcessorJSON `json:"post_processor"`
	Decoder       *struct {
		Type string `json:"type"`
	} `json:"decoder"`
	Model struct {
		Type         string            `json:"type"`
		Vocab        map[string]int    `json:"vocab"`
		Merges       []json.RawMessage `json:"merges"`
		Dropout      *float64          `json:"dropout"`
		Prefix       string            `json:"continuing_subword_prefix"`
		Suffix       string            `json:"end_of_word_suffix"`
		ByteFallback bool              `json:"byte_fallback"`
		IgnoreMerges bool              `json:"ignore_merges"`
	} `json:"model"`
}

type bpeNormalizerJSON struct {
	Type        string              `json:"type"`
	Normalizers []bpeNormalizerJSON `json:"normalizers"`
}

type bpePreTokenizerJSON struct {
	Type          string                `json:"type"`
	Pretokenizers []bpePreTokenizerJSON `json:"pretokenizers"`
	Pattern       *struct {
		Regex  *string `json:"Regex"`
		String *string `json:"String"`
	} `json:"pattern"`
	Behavior         string `json:"behavior"`
	Invert           bool   `json:"invert"`
	AddPrefixSpace   bool   `json:"add_prefix_space"`
	UseRegex         *bool  `json:"use_regex"`
	IndividualDigits bool   `json:"individual_digits"`
}

type bpePostProcessorJSON struct {
	Type   string `json:"type"`
	Single []struct {
		SpecialToken *struct {
			ID string `json:"id"`
		} `json:"SpecialToken"`
		Sequence *struct {
			ID string `json:"id"`
		} `json:"Sequence"`
	} `json:"single"`
	SpecialTokens map[string]struct {
		IDs []int `json:"ids"`
	} `json:"special_tokens"`
}

// LoadBPE reads a byte-level BPE tokenizer.json. Normalizers NFC, NFD, NFKC and NFKD (or a
// Sequence of them), ByteLevel, Digits and Split (Isolated) pre-tokenizers, a ByteLevel decoder,
// and a null, ByteLevel or single-sequence TemplateProcessing post-processor are
// supported. Anything else is an error, as is a vocabulary missing a byte symbol, a merge
// whose result is not a token, or an added token that disagrees with the vocabulary.
func LoadBPE(path string) (*BPE, error) {
	var tj bpeJSON
	if err := readJSON(path, &tj); err != nil {
		return nil, err
	}
	b, err := newBPE(&tj)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

func newBPE(tj *bpeJSON) (*BPE, error) {
	m := tj.Model
	switch {
	case m.Type != "BPE":
		return nil, fmt.Errorf("model type %q is not BPE", m.Type)
	case m.Dropout != nil && *m.Dropout != 0, m.Prefix != "", m.Suffix != "", m.ByteFallback:
		return nil, fmt.Errorf("model uses dropout, subword prefix or suffix, or byte_fallback, which byte-level BPE does not")
	case tj.Decoder == nil || tj.Decoder.Type != "ByteLevel":
		return nil, fmt.Errorf("decoder is not ByteLevel")
	}
	b := &BPE{vocab: m.Vocab, merges: make(map[[2]string]int, len(m.Merges)), ignoreMerges: m.IgnoreMerges}
	if err := b.addMerges(m.Merges); err != nil {
		return nil, err
	}
	var err error
	if b.normalize, err = normalizerFunc(tj.Normalizer); err != nil {
		return nil, err
	}
	if tj.PreTokenizer == nil {
		return nil, fmt.Errorf("pre_tokenizer is null, byte-level BPE needs ByteLevel")
	}
	if err := b.addStages(*tj.PreTokenizer); err != nil {
		return nil, err
	}
	if !b.hasByteStage() {
		return nil, fmt.Errorf("pre_tokenizer has no ByteLevel stage")
	}
	if err := b.addAdded(tj); err != nil {
		return nil, err
	}
	if err := b.addTemplate(tj.PostProcessor); err != nil {
		return nil, err
	}
	b.texts = make(map[int][]byte, len(b.vocab)+len(tj.AddedTokens))
	for tok, id := range b.vocab {
		b.texts[id] = decodeByteRunes(tok)
	}
	for _, a := range tj.AddedTokens {
		b.texts[a.ID] = []byte(a.Content)
	}
	return b, nil
}

// addMerges checks the vocabulary holds every byte symbol, then ranks the merges by position.
func (b *BPE) addMerges(merges []json.RawMessage) error {
	for c, r := range byteRunes {
		if _, ok := b.vocab[string(r)]; !ok {
			return fmt.Errorf("vocabulary has no token for byte %#02x", c)
		}
	}
	for i, raw := range merges {
		left, right, err := parseMerge(raw)
		if err != nil {
			return fmt.Errorf("merge %d: %w", i, err)
		}
		if _, ok := b.vocab[left+right]; !ok {
			return fmt.Errorf("merge %d: %q is not in the vocabulary", i, left+right)
		}
		if _, dup := b.merges[[2]string{left, right}]; !dup {
			b.merges[[2]string{left, right}] = i
		}
	}
	return nil
}

func parseMerge(raw json.RawMessage) (left, right string, err error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		l, r, ok := strings.Cut(s, " ")
		if !ok || r == "" || strings.Contains(r, " ") {
			return "", "", fmt.Errorf("%q is not two tokens separated by one space", s)
		}
		return l, r, nil
	}
	var pair []string
	if json.Unmarshal(raw, &pair) != nil || len(pair) != 2 {
		return "", "", fmt.Errorf("%s is neither a string nor a pair", raw)
	}
	return pair[0], pair[1], nil
}

func normalizerFunc(n *bpeNormalizerJSON) (func(string) string, error) {
	if n == nil {
		return func(s string) string { return s }, nil
	}
	forms := map[string]norm.Form{"NFC": norm.NFC, "NFD": norm.NFD, "NFKC": norm.NFKC, "NFKD": norm.NFKD}
	if f, ok := forms[n.Type]; ok {
		return f.String, nil
	}
	if n.Type != "Sequence" {
		return nil, fmt.Errorf("normalizer %q is not supported", n.Type)
	}
	var steps []func(string) string
	for _, sub := range n.Normalizers {
		f, err := normalizerFunc(&sub)
		if err != nil {
			return nil, err
		}
		steps = append(steps, f)
	}
	return func(s string) string {
		for _, f := range steps {
			s = f(s)
		}
		return s
	}, nil
}

func (b *BPE) addStages(p bpePreTokenizerJSON) error {
	switch p.Type {
	case "Sequence":
		for _, sub := range p.Pretokenizers {
			if err := b.addStages(sub); err != nil {
				return err
			}
		}
	case "ByteLevel":
		st := bpeSplit{bytes: true, prefixSpace: p.AddPrefixSpace}
		if p.UseRegex == nil || *p.UseRegex {
			re, err := newSplitter(gpt2Pattern)
			if err != nil {
				return err
			}
			st.re = re
		}
		b.splits = append(b.splits, st)
	// Digits isolates digit runs, or each digit, as tokenizers does with char::is_numeric,
	// which is \p{N}.
	case "Digits":
		pattern := `\p{N}+`
		if p.IndividualDigits {
			pattern = `\p{N}`
		}
		re, err := newSplitter(pattern)
		if err != nil {
			return err
		}
		b.splits = append(b.splits, bpeSplit{re: re})
	case "Split":
		if p.Behavior != "Isolated" || p.Invert || p.Pattern == nil {
			return fmt.Errorf("Split pre-tokenizer must be Isolated, not inverted, with a pattern")
		}
		pattern := ""
		switch {
		case p.Pattern.Regex != nil:
			pattern = *p.Pattern.Regex
		case p.Pattern.String != nil:
			pattern = regexp.QuoteMeta(*p.Pattern.String)
		default:
			return fmt.Errorf("Split pattern has neither Regex nor String")
		}
		re, err := newSplitter(pattern)
		if err != nil {
			return err
		}
		b.splits = append(b.splits, bpeSplit{re: re})
	default:
		return fmt.Errorf("pre_tokenizer %q is not supported", p.Type)
	}
	return nil
}

func (b *BPE) hasByteStage() bool {
	for _, s := range b.splits {
		if s.bytes {
			return true
		}
	}
	return false
}

func (b *BPE) addAdded(tj *bpeJSON) error {
	for _, a := range tj.AddedTokens {
		if a.Content == "" || a.ID < 0 || a.LStrip || a.RStrip {
			return fmt.Errorf("added token %q (id %d) is empty, negative or strips whitespace", a.Content, a.ID)
		}
		if id, ok := b.vocab[a.Content]; ok && id != a.ID {
			return fmt.Errorf("added token %q is id %d but the vocabulary says %d", a.Content, a.ID, id)
		}
		t := addedToken{content: a.Content, id: int32(a.ID), singleWord: a.SingleWord}
		if a.Normalized {
			t.content = b.normalize(a.Content)
			b.normalized = append(b.normalized, t)
		} else {
			b.raw = append(b.raw, t)
		}
	}
	return nil
}

func (b *BPE) addTemplate(p *bpePostProcessorJSON) error {
	switch {
	case p == nil, p.Type == "ByteLevel":
		return nil
	case p.Type != "TemplateProcessing":
		return fmt.Errorf("post_processor %q is not supported", p.Type)
	}
	seenSeq := false
	for _, item := range p.Single {
		switch {
		case item.Sequence != nil && item.Sequence.ID == "A" && !seenSeq:
			seenSeq = true
		case item.SpecialToken != nil:
			st, ok := p.SpecialTokens[item.SpecialToken.ID]
			if !ok {
				return fmt.Errorf("template names special token %q the post_processor does not define", item.SpecialToken.ID)
			}
			if seenSeq {
				b.suffix = append(b.suffix, st.IDs...)
			} else {
				b.prefix = append(b.prefix, st.IDs...)
			}
		default:
			return fmt.Errorf("template item is not a special token or sequence A, once")
		}
	}
	if !seenSeq {
		return fmt.Errorf("single template has no sequence A")
	}
	return nil
}

// Encode returns the token ids of text, wrapped by the tokenizer's single-sequence
// template when it has one.
func (b *BPE) Encode(text string) []int {
	ids := append([]int(nil), b.prefix...)
	for _, seg := range cut(text, b.raw) {
		if seg.added {
			ids = append(ids, int(seg.id))
			continue
		}
		for _, sub := range cut(b.normalize(seg.text), b.normalized) {
			if sub.added {
				ids = append(ids, int(sub.id))
				continue
			}
			for _, word := range b.preTokenize(sub.text) {
				ids = b.appendWord(ids, word)
			}
		}
	}
	return append(ids, b.suffix...)
}

// preTokenize runs every stage over the pieces the one before produced. Pieces leave
// byte-mapped once a ByteLevel stage has run.
func (b *BPE) preTokenize(text string) []string {
	pieces := []string{text}
	for _, st := range b.splits {
		var next []string
		for _, p := range pieces {
			if st.prefixSpace && !strings.HasPrefix(p, " ") {
				p = " " + p
			}
			parts := []string{p}
			if st.re != nil {
				parts = st.re.split(p)
			}
			for _, part := range parts {
				if st.bytes {
					part = encodeByteRunes(part)
				}
				next = append(next, part)
			}
		}
		pieces = next
	}
	return pieces
}

// appendWord merges word, its symbols one byte character each, by rank: the lowest-ranked
// adjacent pair first, the leftmost on a tie.
func (b *BPE) appendWord(ids []int, word string) []int {
	if word == "" {
		return ids
	}
	if id, ok := b.vocab[word]; ok && b.ignoreMerges {
		return append(ids, id)
	}
	symbols := make([]string, 0, len(word))
	for _, r := range word {
		symbols = append(symbols, string(r))
	}
	for len(symbols) > 1 {
		best, at := -1, -1
		for i := 0; i+1 < len(symbols); i++ {
			if rank, ok := b.merges[[2]string{symbols[i], symbols[i+1]}]; ok && (best < 0 || rank < best) {
				best, at = rank, i
			}
		}
		if at < 0 {
			break
		}
		symbols[at] += symbols[at+1]
		symbols = append(symbols[:at+1], symbols[at+2:]...)
	}
	for _, s := range symbols {
		ids = append(ids, b.vocab[s])
	}
	return ids
}

// Decode returns the text of ids. Added and special tokens decode to their content, and
// an id the tokenizer does not know is skipped. Bytes that are not valid UTF-8 become
// U+FFFD, one per maximal invalid subsequence, as Rust's lossy conversion does.
func (b *BPE) Decode(ids []int) string {
	var buf []byte
	for _, id := range ids {
		buf = append(buf, b.texts[id]...)
	}
	return lossyUTF8(buf)
}

func lossyUTF8(buf []byte) string {
	if utf8.Valid(buf) {
		return string(buf)
	}
	var sb strings.Builder
	for len(buf) > 0 {
		r, size := utf8.DecodeRune(buf)
		if r != utf8.RuneError || size > 1 {
			sb.WriteRune(r)
			buf = buf[size:]
			continue
		}
		sb.WriteRune(utf8.RuneError)
		buf = buf[invalidPrefixLen(buf):]
	}
	return sb.String()
}

// invalidPrefixLen is the length of the longest prefix of buf that begins a valid UTF-8
// sequence but is not complete, at least 1.
func invalidPrefixLen(buf []byte) int {
	lead := buf[0]
	need, lo, hi := 0, byte(0x80), byte(0xBF)
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		need = 2
	case lead == 0xE0:
		need, lo = 3, 0xA0
	case lead >= 0xE1 && lead <= 0xEC, lead == 0xEE, lead == 0xEF:
		need = 3
	case lead == 0xED:
		need, hi = 3, 0x9F
	case lead == 0xF0:
		need, lo = 4, 0x90
	case lead >= 0xF1 && lead <= 0xF3:
		need = 4
	case lead == 0xF4:
		need, hi = 4, 0x8F
	default:
		return 1
	}
	n := 1
	for n < need && n < len(buf) {
		c := buf[n]
		if c < lo || c > hi {
			break
		}
		lo, hi = 0x80, 0xBF
		n++
	}
	return n
}

// byteRunes maps each byte to the printable character GPT-2 writes it as: printable
// Latin-1 stays, the rest move to U+0100 and up in byte order.
var byteRunes, runeBytes = func() (r [256]rune, back map[rune]byte) {
	back = make(map[rune]byte, 256)
	next := rune(256)
	for b := range 256 {
		c := rune(b)
		if !(b >= '!' && b <= '~' || b >= 0xA1 && b <= 0xAC || b >= 0xAE) {
			c, next = next, next+1
		}
		r[b], back[c] = c, byte(b)
	}
	return r, back
}()

func encodeByteRunes(s string) string {
	var sb strings.Builder
	for i := range len(s) {
		sb.WriteRune(byteRunes[s[i]])
	}
	return sb.String()
}

// decodeByteRunes maps a token's characters back to bytes; a character outside the byte
// map is kept as its UTF-8 encoding.
func decodeByteRunes(tok string) []byte {
	out := make([]byte, 0, len(tok))
	for _, r := range tok {
		if c, ok := runeBytes[r]; ok {
			out = append(out, c)
		} else {
			out = utf8.AppendRune(out, r)
		}
	}
	return out
}

// splitter is a pre-tokenizer regex with Isolated behavior. When the pattern ends in
// the lookahead tail `\s+(?!\S)|\s+`, which Go's regexp cannot express, head holds the
// rest and the tail applies where head does not match.
type splitter struct {
	head    *regexp.Regexp
	hasTail bool
}

func newSplitter(pattern string) (*splitter, error) {
	s := &splitter{}
	if head, ok := strings.CutSuffix(pattern, lookaheadTail); ok {
		pattern, s.hasTail = head, true
	}
	if strings.Contains(pattern, "(?!") || strings.Contains(pattern, "(?=") || strings.Contains(pattern, "(?<") {
		return nil, fmt.Errorf("pattern %q uses lookaround other than the trailing `\\s+(?!\\S)|\\s+`", pattern)
	}
	if pattern == "" {
		return s, nil
	}
	pattern, err := unicodeSpace(pattern)
	if err != nil {
		return nil, err
	}
	re, err := regexp.Compile(`\A(?:` + pattern + `)`)
	if err != nil {
		return nil, fmt.Errorf("pre-tokenizer pattern: %w", err)
	}
	s.head = re
	return s, nil
}

// unicodeSpace rewrites \s and \S to the Unicode White_Space set.
func unicodeSpace(p string) (string, error) {
	var sb strings.Builder
	inClass := false
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '\\' && i+1 < len(p):
			i++
			switch p[i] {
			case 's':
				if inClass {
					sb.WriteString(whiteSpace)
				} else {
					sb.WriteString("[" + whiteSpace + "]")
				}
			case 'S':
				if inClass {
					return "", fmt.Errorf("pattern has \\S inside a character class")
				}
				sb.WriteString("[^" + whiteSpace + "]")
			default:
				sb.WriteByte('\\')
				sb.WriteByte(p[i])
			}
			continue
		case c == '[' && !inClass:
			inClass = true
		case c == ']' && inClass:
			inClass = false
		}
		sb.WriteByte(c)
	}
	return sb.String(), nil
}

// split returns the pieces of text: each match, and each stretch no match covers.
func (s *splitter) split(text string) []string {
	var out []string
	gap := 0
	for p := 0; p < len(text); {
		n := s.matchLen(text[p:])
		if n == 0 {
			_, size := utf8.DecodeRuneInString(text[p:])
			p += size
			continue
		}
		if gap < p {
			out = append(out, text[gap:p])
		}
		out = append(out, text[p:p+n])
		p += n
		gap = p
	}
	if gap < len(text) {
		out = append(out, text[gap:])
	}
	return out
}

func (s *splitter) matchLen(rest string) int {
	if s.head != nil {
		if loc := s.head.FindStringIndex(rest); loc != nil && loc[1] > 0 {
			return loc[1]
		}
	}
	if !s.hasTail {
		return 0
	}
	// \s+(?!\S) takes a whitespace run less its last character unless the run ends
	// the text; \s+ takes the lone character the lookahead rejected.
	end, last := 0, 0
	for end < len(rest) {
		r, size := utf8.DecodeRuneInString(rest[end:])
		if !unicode.IsSpace(r) {
			break
		}
		last = end
		end += size
	}
	switch {
	case end == 0:
		return 0
	case end == len(rest), last == 0:
		return end
	}
	return last
}
