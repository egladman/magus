package teacher

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The expected ids are worked out by hand from the tokenizers specification over the
// fixtures: byte b has id b, merges follow the fixture's merge order.

func loadFixture(t *testing.T, name string) *BPE {
	t.Helper()
	b, err := LoadBPE(filepath.Join("testdata", "bpe", name))
	if err != nil {
		t.Fatalf("LoadBPE(%s): %v", name, err)
	}
	return b
}

func TestBPEEncodeOLMoStyle(t *testing.T) {
	b := loadFixture(t, "olmo.json")
	const cls, sep = 266, 267
	cases := []struct {
		name, text string
		want       []int
	}{
		{"merge chain to one token", "hello", []int{cls, 259, sep}},
		{"leading space joins the word", "hello world", []int{cls, 259, 264, sep}},
		{"merge order by rank", "world", []int{cls, 119, 261, 263, sep}},
		{"unmerged letters stay bytes", "Hello", []int{cls, 72, 101, 257, 111, sep}},
		{"two byte character merges", "héllo", []int{cls, 104, 265, 257, 111, sep}},
		{"contraction splits off", "it's", []int{cls, 105, 116, 39, 115, sep}},
		{"digits take no leading space from letters", "a1 2", []int{cls, 97, 49, 32, 50, sep}},
		{"double space leaves one for the word", "hello  world", []int{cls, 259, 32, 264, sep}},
		{"newlines split one at a time before a word", "a\n\nb", []int{cls, 97, 10, 10, 98, sep}},
		{"added token in the text", "hello[SEP]world", []int{cls, 259, sep, 119, 261, 263, sep}},
		{"empty text", "", []int{cls, sep}},
	}
	for _, c := range cases {
		if got := b.Encode(c.text); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Encode(%q) = %v, want %v", c.name, c.text, got, c.want)
		}
	}
}

func TestBPEEncodeQwenStyle(t *testing.T) {
	b := loadFixture(t, "qwen.json")
	cases := []struct {
		name, text string
		want       []int
	}{
		{"word and spaced word", "hi hi", []int{256, 257}},
		{"contraction is case-insensitive", "HI'S", []int{72, 73, 39, 83}},
		{"digits one at a time", "x123", []int{120, 49, 50, 51}},
		{"NFC composes before bytes", "é", []int{258}},
		{"added token outside the vocabulary", "hi<|endoftext|>hi", []int{256, 259, 256}},
		{"punctuation takes trailing newlines", "hi!\n\nhi", []int{256, 33, 10, 10, 256}},
		{"trailing whitespace is one match", "hi  ", []int{256, 32, 32}},
		{"blank run with a newline", "hi \nx", []int{256, 32, 10, 120}},
		{"no template", "", nil},
	}
	for _, c := range cases {
		if got := b.Encode(c.text); !reflect.DeepEqual(got, c.want) && (len(got) != 0 || len(c.want) != 0) {
			t.Errorf("%s: Encode(%q) = %v, want %v", c.name, c.text, got, c.want)
		}
	}
}

func TestBPEDecode(t *testing.T) {
	olmo := loadFixture(t, "olmo.json")
	qwen := loadFixture(t, "qwen.json")
	cases := []struct {
		name string
		b    *BPE
		ids  []int
		want string
	}{
		{"merged tokens", olmo, []int{259, 264}, "hello world"},
		{"special tokens keep their content", olmo, []int{266, 259, 267}, "[CLS]hello[SEP]"},
		{"two byte ids make one character", olmo, []int{195, 169}, "é"},
		{"truncated sequence is one replacement", olmo, []int{0xE2, 0x82}, "�"},
		{"stray continuation bytes are one each", olmo, []int{0x80, 0x80}, "��"},
		{"lead byte that cannot start a sequence", olmo, []int{0xC0, 0x41}, "�A"},
		{"unknown id is skipped", olmo, []int{259, 9999, 264}, "hello world"},
		{"added token beyond the vocabulary", qwen, []int{256, 259, 257}, "hi<|endoftext|> hi"},
		{"empty", qwen, nil, ""},
	}
	for _, c := range cases {
		if got := c.b.Decode(c.ids); got != c.want {
			t.Errorf("%s: Decode(%v) = %q, want %q", c.name, c.ids, got, c.want)
		}
	}
}

func TestBPERoundTrip(t *testing.T) {
	b := loadFixture(t, "qwen.json")
	for _, text := range []string{"hi hi", "x123!\n\n  hi ", "café 世界 <|im_start|>hi", "\U0001F600"} {
		if got := b.Decode(b.Encode(text)); got != text {
			t.Errorf("Decode(Encode(%q)) = %q", text, got)
		}
	}
}

func TestBPEByteMap(t *testing.T) {
	// Spec anchors: space is U+0120, newline U+010A, NUL U+0100, DEL U+0121, NBSP U+00A0 stays.
	for b, want := range map[byte]rune{' ': 0x120, '\n': 0x10A, 0: 0x100, 0x7F: 0x121, 0xA0: 0x142, 0xAD: 0x143, 'A': 'A', 0xA1: 0xA1} {
		if byteRunes[b] != want {
			t.Errorf("byteRunes[%#02x] = %U, want %U", b, byteRunes[b], want)
		}
	}
}

func TestSplitterUnicodeWhitespace(t *testing.T) {
	s, err := newSplitter(gpt2Pattern)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		text string
		want []string
	}{
		{"a  b", []string{"a", " ", " ", "b"}},
		{"a　b", []string{"a", "　", "b"}},
		{"ab   ", []string{"ab", "   "}},
		{"ab   cd", []string{"ab", "  ", " cd"}},
		{"\t\v x", []string{"\t\v", " x"}},
	}
	for _, c := range cases {
		if got := s.split(c.text); !reflect.DeepEqual(got, c.want) {
			t.Errorf("split(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestSplitterKeepsUnmatchedText(t *testing.T) {
	s, err := newSplitter(`\p{N}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ab", "1", "2", "c"}
	if got := s.split("ab12c"); !reflect.DeepEqual(got, want) {
		t.Errorf("split = %q, want %q", got, want)
	}
}

func TestLoadBPERefuses(t *testing.T) {
	olmo, err := os.ReadFile(filepath.Join("testdata", "bpe", "olmo.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, from, to, want string
	}{
		{"another model", `"type": "BPE"`, `"type": "WordPiece"`, "not BPE"},
		{"byte fallback", `"byte_fallback": false`, `"byte_fallback": true`, "byte_fallback"},
		{"a byte missing from the vocabulary", `"Ā": 0,`, ``, "no token for byte 0"},
		{"a merge to nowhere", `"Ã ©"`, `"Ã Ã"`, "not in the vocabulary"},
		{"a one-token merge", `"Ã ©"`, `"Ã"`, "two tokens"},
		{"an added token at the wrong id", `"id": 266`, `"id": 300`, "vocabulary says 266"},
		{"a stripping added token", `"lstrip": false`, `"lstrip": true`, "strips whitespace"},
		{"another decoder", `"decoder": {"type": "ByteLevel"`, `"decoder": {"type": "Metaspace"`, "decoder"},
		{"another pre-tokenizer", `"type": "ByteLevel", "add_prefix_space": false`, `"type": "Metaspace", "add_prefix_space": false`, "pre_tokenizer"},
		{"another post-processor", `"TemplateProcessing"`, `"RobertaProcessing"`, "post_processor"},
		{"another normalizer", `"normalizer": null`, `"normalizer": {"type": "Lowercase"}`, "normalizer"},
	}
	for _, c := range cases {
		if !strings.Contains(string(olmo), c.from) {
			t.Fatalf("%s: fixture lacks %q", c.name, c.from)
		}
		path := filepath.Join(t.TempDir(), "tokenizer.json")
		if err := os.WriteFile(path, []byte(strings.Replace(string(olmo), c.from, c.to, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBPE(path); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: LoadBPE error = %v, want it to mention %q", c.name, err, c.want)
		}
	}
	if _, err := LoadBPE(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("LoadBPE of a missing file: no error")
	}
}

func TestNewSplitterRefusesOtherLookaround(t *testing.T) {
	if _, err := newSplitter(`a(?=b)|\s+`); err == nil {
		t.Error("lookahead outside the tail: no error")
	}
}

func digitsSplit(t *testing.T, individual bool, text string) []string {
	t.Helper()
	var b BPE
	if err := b.addStages(bpePreTokenizerJSON{Type: "Digits", IndividualDigits: individual}); err != nil {
		t.Fatal(err)
	}
	return b.splits[0].re.split(text)
}

func TestDigitsPreTokenizerIsolatesEachDigit(t *testing.T) {
	want := []string{"ab", "1", "2", "3", "c"}
	if got := digitsSplit(t, true, "ab123c"); !reflect.DeepEqual(got, want) {
		t.Errorf("split = %q, want %q", got, want)
	}
}

func TestDigitsPreTokenizerKeepsADigitRunWhole(t *testing.T) {
	want := []string{"ab", "123", "c"}
	if got := digitsSplit(t, false, "ab123c"); !reflect.DeepEqual(got, want) {
		t.Errorf("split = %q, want %q", got, want)
	}
}
