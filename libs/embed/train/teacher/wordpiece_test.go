package teacher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// potionFixture is testdata/potion-reference-ids.json: potion-code-16M-v2's tokenizer.json
// with its vocabulary cut to the ids the cases use, and the ids Hugging Face tokenizers
// 0.22.2 gives each case. Greedy longest match over that subset picks what it picks over
// the whole vocabulary, since every piece it chose is kept and no longer one is added.
type potionFixture struct {
	VocabSize int `json:"vocab_size"`
	Model     struct {
		Vocab map[string]int32 `json:"vocab"`
	} `json:"model"`
	Cases []struct {
		Text string  `json:"text"`
		IDs  []int32 `json:"ids"`
	} `json:"cases"`
}

// writePotionTokenizer writes the fixture as a tokenizer.json with ids 0 to vocab_size-1:
// an id the cases never use holds a token starting with NUL, which normalizing removes
// from every text, so it never matches.
func writePotionTokenizer(t *testing.T, raw []byte, fx potionFixture) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	vocab := make(map[string]int32, fx.VocabSize)
	used := map[int32]bool{}
	for tok, id := range fx.Model.Vocab {
		vocab[tok], used[id] = id, true
	}
	for id := range int32(fx.VocabSize) {
		if !used[id] {
			vocab[fmt.Sprintf("\x00%d", id)] = id
		}
	}
	doc["model"].(map[string]any)["vocab"] = vocab
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tokenizer.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPotionWordPieceMatchesReferenceIDs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "potion-reference-ids.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx potionFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	w, err := LoadTokenizerJSON(writePotionTokenizer(t, raw, fx))
	if err != nil {
		t.Fatal(err)
	}
	if len(fx.Cases) < 50 {
		t.Fatalf("fixture holds %d cases, want at least 50", len(fx.Cases))
	}
	for _, c := range fx.Cases {
		if got := w.Encode(c.Text, 512); !slices.Equal(got, c.IDs) {
			t.Errorf("Encode(%q)\n got  %v\n want %v", c.Text, got, c.IDs)
		}
	}
}

func TestLoadTokenizerJSONRefusesAnotherPipeline(t *testing.T) {
	const base = `{"added_tokens": [{"id": 1, "content": "[UNK]", "normalized": false}],
		"normalizer": {"type": "BertNormalizer", "clean_text": true, "handle_chinese_chars": true, "strip_accents": null, "lowercase": true},
		"pre_tokenizer": {"type": "BertPreTokenizer"}, "post_processor": %s,
		"model": {"type": "WordPiece", "unk_token": "[UNK]", "continuing_subword_prefix": "##", "max_input_chars_per_word": 100,
			"vocab": %s}}`
	dense := `{"a": 0, "[UNK]": 1}`
	cases := []struct{ name, post, vocab, want string }{
		{"a template post-processor", `{"type": "TemplateProcessing"}`, dense, "post_processor"},
		{"ids with a gap", "null", `{"a": 0, "[UNK]": 2}`, "token ids are not 0 to 1"},
		{"an added token at another id", "null", `{"[UNK]": 0, "a": 1}`, `added token "[UNK]" is not id 1`},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "tokenizer.json")
		if err := os.WriteFile(path, []byte(fmt.Sprintf(base, c.post, c.vocab)), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadTokenizerJSON(path); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to name %q", c.name, err, c.want)
		}
	}
	path := filepath.Join(t.TempDir(), "tokenizer.json")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(base, "null", dense)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTokenizerJSON(path); err != nil {
		t.Errorf("the base tokenizer.json: %v", err)
	}
}

var pieceVocab = []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]", "[MASK]", "hello", ",", "!", "$", "5", "cafe", "play",
	"##ing", "un", "##aff", "##able", "中", "国", "a", "##a", "x", "y", "z", "_", "go", "##lang"}

func TestWordPieceEncode(t *testing.T) {
	w, err := NewWordPiece(pieceVocab)
	if err != nil {
		t.Fatal(err)
	}
	words := func(ids []int32) []string {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = pieceVocab[id]
		}
		return out
	}
	cases := []struct {
		name, text string
		maxLen     int
		want       string
	}{
		{"punctuation splits and unknown words", "Hello, World!", 512, "[CLS] hello , [UNK] ! [SEP]"},
		{"accents strip after lowercasing", "CAFÉ Playing", 512, "[CLS] cafe play ##ing [SEP]"},
		{"greedy longest match", "unaffable", 512, "[CLS] un ##aff ##able [SEP]"},
		{"a word with an unmatched tail is one [UNK]", "playx", 512, "[CLS] [UNK] [SEP]"},
		{"special tokens match in the raw text", "x[SEP]y [MASK]", 512, "[CLS] x [SEP] y [MASK] [SEP]"},
		{"special tokens are case sensitive", "[sep]", 512, "[CLS] [UNK] [UNK] [UNK] [SEP]"},
		{"CJK ideographs stand alone", "中国x", 512, "[CLS] 中 国 x [SEP]"},
		{"format and control characters vanish", "play​ing x\vy\x00z", 512, "[CLS] play ##ing [UNK] [SEP]"},
		{"tab, newline and no-break space split", "x\ty\nz x", 512, "[CLS] x y z x [SEP]"},
		{"ASCII symbols are punctuation", "$5_go", 512, "[CLS] $ 5 _ go [SEP]"},
		{"words over 100 runes are [UNK]", strings.Repeat("a", 101) + " " + strings.Repeat("a", 100), 512,
			"[CLS] [UNK] a" + strings.Repeat(" ##a", 99) + " [SEP]"},
		{"truncation keeps [SEP]", "hello hello hello", 4, "[CLS] hello hello [SEP]"},
		{"empty text", "", 512, "[CLS] [SEP]"},
	}
	for _, c := range cases {
		got := strings.Join(words(w.Encode(c.text, c.maxLen)), " ")
		if got != c.want {
			t.Errorf("%s: Encode(%q)\n got  %s\n want %s", c.name, c.text, got, c.want)
		}
	}
}

func TestNewWordPieceNeedsSpecialTokens(t *testing.T) {
	if _, err := NewWordPiece(slices.DeleteFunc(slices.Clone(pieceVocab), func(s string) bool { return s == "[SEP]" })); err == nil {
		t.Error("a vocabulary without [SEP] was accepted")
	}
}
