package teacher

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/xdg"
)

const potionTokenizer = "potion-code-16M-v2/tokenizer.json"

// pinnedPotionTokenizer is where `embed fetch` writes potion's tokenizer.json: the cache
// root joined with the path inputs.jsonl pins.
func pinnedPotionTokenizer(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "inputs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(raw)) {
		var in struct{ Name, Path string }
		if err := json.Unmarshal([]byte(line), &in); err != nil {
			t.Fatal(err)
		}
		if in.Name == potionTokenizer {
			dir, err := xdg.CacheDir()
			if err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "magus-embed", in.Path)
		}
	}
	t.Fatalf("inputs.jsonl pins no %s", potionTokenizer)
	return ""
}

// The expected pieces come from applying BERT's basic tokenizer and greedy longest-match
// WordPiece by hand against potion's vocabulary; only their ids come from the file.
func TestPotionWordPiecePieces(t *testing.T) {
	path := pinnedPotionTokenizer(t)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		t.Skipf("%s is not fetched; run `magus run fetch libs/embed/train -- %s`", path, potionTokenizer)
	}
	w, err := LoadTokenizerJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ text, pieces string }{
		{"Hello, World!", "hello , world !"},
		{"The quick brown fox jumps over the lazy dog.", "the quick brown fox jumps over the lazy dog ."},
		{"How do I read a file line by line in Go?", "how do i read a file line by line in go ?"},
		{"Retrieval ranks documents by their similarity to a query.",
			"retrieval ranks documents by their similarity to a query ."},
		{"cache invalidation is one of the two hard problems", "cache invalidation is one of the two hard problems"},
		{"Café crème brûlée naïve résumé", "cafe cre ##me br ##ule ##e naive resume"},
		{"中文分词测试 and English", "中 文 分 [UNK] 测 试 and english"},
		{"emoji 🚀 rocket 🎉 party", "emoji [UNK] rocket [UNK] party"},
		{"tabs\tand\nnewlines", "tabs and newlines"},
		{"   leading and trailing spaces   ", "leading and trailing spaces"},
		{"func (w *WordPiece) Encode(text string, maxLen int) []int32 {",
			"func ( w * word ##piece ) encode ( text string , maxlen int ) [ ] int32 {"},
		{`return nil, fmt.Errorf("worker: join: %w", err)`,
			`return nil , fmt . errorf ( " worker : join : % w " , err )`},
		{"if err != nil { return err }", "if err ! = nil { return err }"},
		{"SELECT id, name FROM users WHERE created_at", "select id , name from users where created _ at"},
		{`import "github.com/egladman/magus/libs/embed"`,
			`import " github . com / egl ##ad ##man / mag ##us / libs / embed "`},
		{"const x = arr.map((v) => v * 2).filter(Boolean);",
			"const x = arr . map ( ( v ) = > v * 2 ) . filter ( boolean ) ;"},
		{"async function fetchData() { await fetch(url); }",
			"async function fetchdata ( ) { await fetch ( url ) ; }"},
		{"pub fn new() -> Self { Self { inner: Vec::new() } }",
			"pub fn new ( ) - > self { self { inner : vec : : new ( ) } }"},
		{"def parse_config(path: str) -> dict:", "def parse _ config ( path : str ) - > dict :"},
		{"HTTPServerTimeout 0xFF", "httpserver ##time ##out 0xff"},
		// [UNK] matches before normalizing, so only its exact spelling is the token; [PAD]
		// matches after, in any case, but only as a word of its own.
		{"[UNK] literal and [unk] lowercase", "[UNK] literal and [ unk ] lowercase"},
		{"[PAD] padding [pad] x[pad]y", "[PAD] padding [PAD] x [ pad ] y"},
		{strings.Repeat("x", 101), "[UNK]"},
	}
	for _, c := range cases {
		var want []int32
		for _, p := range strings.Fields(c.pieces) {
			id, ok := w.vocab[p]
			if !ok {
				t.Fatalf("%q: piece %q is not in the vocabulary", c.text, p)
			}
			want = append(want, id)
		}
		if got := w.Encode(c.text, 512); !slices.Equal(got, want) {
			t.Errorf("Encode(%q)\n got  %v\n want %v (%s)", c.text, got, want, c.pieces)
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
		{"## pieces continue a lowercased word", "GoLang go", 512, "[CLS] go ##lang go [SEP]"},
		{"a word with an unmatched tail is one [UNK]", "playx", 512, "[CLS] [UNK] [SEP]"},
		{"special tokens match in the raw text", "x[SEP]y [MASK]", 512, "[CLS] x [SEP] y [MASK] [SEP]"},
		{"special tokens are case sensitive", "[sep]", 512, "[CLS] [UNK] [UNK] [UNK] [SEP]"},
		{"CJK ideographs stand alone", "中国x", 512, "[CLS] 中 国 x [SEP]"},
		{"format and control characters vanish", "play​ing x\vy\x00z", 512, "[CLS] play ##ing [UNK] [SEP]"},
		{"tab, newline and no-break space split", "x\ty\nz x", 512, "[CLS] x y z x [SEP]"},
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
