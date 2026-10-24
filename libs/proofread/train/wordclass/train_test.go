package wordclass

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"slices"
	"testing"

	"github.com/egladman/magus/libs/proofread/internal/wordclass"
)

// gunzip reads one of the files Run writes beside the wordclass package.
func gunzip(tb testing.TB, path string) []byte {
	tb.Helper()

	f, err := os.Open(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		tb.Fatal(err)
	}

	raw, err := io.ReadAll(zr)
	if err != nil {
		tb.Fatal(err)
	}

	return raw
}

// ewt reads the EWT test split Run wrote.
func ewt(tb testing.TB) []sentence {
	tb.Helper()

	sentences, err := readSentences(bytes.NewReader(gunzip(tb, "../../internal/wordclass/testdata/en_ewt-ud-test.conllu.gz")))
	if err != nil {
		tb.Fatal(err)
	}

	return sentences
}

func BenchmarkTag(b *testing.B) {
	gold := ewt(b)

	sentences := make([][]string, len(gold))
	for i, s := range gold {
		sentences[i] = wordclass.Split(s.text)
	}

	wordclass.Tag(nil)
	b.ReportAllocs()

	for b.Loop() {
		for _, words := range sentences {
			wordclass.Tag(words)
		}
	}
}

func TestTagAccuracy(t *testing.T) {
	var m wordclass.Model
	if err := m.UnmarshalBinary(gunzip(t, "../../internal/wordclass/gen/model.gz")); err != nil {
		t.Fatal(err)
	}

	got, classes := evaluate(&m, ewt(t))
	if got != m.Accuracy {
		t.Errorf("tagged %+v right; the trainer counted %+v", got, m.Accuracy)
	}

	uposAcc, xposAcc := float64(got.UPOS)/float64(got.Words), float64(got.XPOS)/float64(got.Words)
	t.Logf("EWT test: UPOS %.4f, XPOS %.4f over %d tokens", uposAcc, xposAcc, got.Words)

	for _, s := range classes.scores() {
		t.Log(s)
	}

	if uposAcc < 0.93 {
		t.Errorf("UPOS accuracy %.4f, want >= 0.93", uposAcc)
	}
}

func TestConfusionScores(t *testing.T) {
	c := confusion{
		{gold: "NOUN", predicted: "NOUN"}: 8,
		{gold: "NOUN", predicted: "VERB"}: 2,
		{gold: "VERB", predicted: "VERB"}: 3,
		{gold: "VERB", predicted: "NOUN"}: 1,
		{gold: "INTJ", predicted: "NOUN"}: 1,
	}

	got := c.scores()
	want := []classScore{
		{upos: "INTJ", correct: 0, predicted: 0, gold: 1},
		{upos: "NOUN", correct: 8, predicted: 10, gold: 10},
		{upos: "VERB", correct: 3, predicted: 5, gold: 4},
	}

	if !slices.Equal(got, want) {
		t.Errorf("scores() = %+v, want %+v", got, want)
	}

	if got, want := want[2].String(), "VERB  precision 0.6000 (3 of 5) recall 0.7500 (3 of 4)"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func BenchmarkSplit(b *testing.B) {
	sentences := ewt(b)
	b.ReportAllocs()

	for b.Loop() {
		for _, s := range sentences {
			wordclass.Split(s.text)
		}
	}
}

// TestSplitAgreesWithEWT reads each EWT test sentence's text and counts the
// sentences Split splits exactly as EWT did, once EWT's split hyphenated
// words are joined back, since Split keeps them whole.
func TestSplitAgreesWithEWT(t *testing.T) {
	sentences := ewt(t)

	exact := 0
	for _, s := range sentences {
		var forms []string
		attached, joining := false, false
		for _, w := range s.words {
			glued := !w.spaceAfter
			switch {
			case joining:
				forms[len(forms)-1] += w.form
			case w.form == "-" && attached && glued:
				forms[len(forms)-1] += w.form
			default:
				forms = append(forms, w.form)
			}
			joining = w.form == "-" && attached && glued
			attached = glued
		}

		if got := wordclass.Split(s.text); slices.Equal(got, forms) {
			exact++
		} else if testing.Verbose() {
			t.Logf("%q\n got %q\nwant %q", s.text, got, forms)
		}
	}
	share := float64(exact) / float64(len(sentences))
	t.Logf("Split matches EWT on %d of %d test sentences (%.4f)", exact, len(sentences), share)
	// The misses left are web text: typos EWT splits ("dont"), slashes between
	// words and emoticons.
	if share < 0.89 {
		t.Errorf("Split matches EWT on %.4f of its test sentences, want >= 0.89", share)
	}
}
