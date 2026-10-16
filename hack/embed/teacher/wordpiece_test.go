package teacher

import (
	"slices"
	"strings"
	"testing"
)

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
