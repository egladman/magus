package wordclass

import (
	"compress/gzip"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/libs/proofread/internal/conllu"
)

// ewt reads the EWT test split the trainer wrote.
func ewt(tb testing.TB) []conllu.Sentence {
	tb.Helper()

	f, err := os.Open("testdata/en_ewt-ud-test.conllu.gz")
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		tb.Fatal(err)
	}

	sentences, err := conllu.Read(zr)
	if err != nil {
		tb.Fatal(err)
	}

	return sentences
}

func BenchmarkTag(b *testing.B) {
	gold := ewt(b)

	sentences := make([][]string, len(gold))
	for i, s := range gold {
		sentences[i] = Split(s.Text)
	}

	embedded()
	b.ReportAllocs()

	for b.Loop() {
		for _, words := range sentences {
			Tag(words)
		}
	}
}

func TestTagAccuracy(t *testing.T) {
	var tokens, upos, xpos int
	for _, s := range ewt(t) {
		for i, got := range Tag(s.Forms()) {
			tokens++
			if got.Class.String() == s.Words[i].UPOS {
				upos++
			}
			if got.Penn == s.Words[i].XPOS {
				xpos++
			}
		}
	}
	if got, want := (Accuracy{Words: tokens, UPOS: upos, XPOS: xpos}), embedded().Accuracy; got != want {
		t.Errorf("tagged %+v right; the trainer counted %+v", got, want)
	}
	uposAcc, xposAcc := float64(upos)/float64(tokens), float64(xpos)/float64(tokens)
	t.Logf("EWT test: UPOS %.4f, XPOS %.4f over %d tokens", uposAcc, xposAcc, tokens)
	if uposAcc < 0.93 {
		t.Errorf("UPOS accuracy %.4f, want >= 0.93", uposAcc)
	}
}

func TestTagEmpty(t *testing.T) {
	for _, words := range [][]string{nil, {}} {
		if got := Tag(words); len(got) != 0 {
			t.Errorf("Tag(%#v) = %v, want empty", words, got)
		}
	}
}

func TestTagPunctuation(t *testing.T) {
	got := Tag([]string{"Wait", ",", "what", "?", "(", "Really", ")", "."})
	for _, i := range []int{1, 3, 4, 6, 7} {
		if got[i].Class != Punctuation {
			t.Errorf("word %d: %v, want PUNCT", i, got[i])
		}
	}
	want := map[int]string{1: ",", 4: "-LRB-", 6: "-RRB-", 7: "."}
	for i, penn := range want {
		if got[i].Penn != penn {
			t.Errorf("word %d: Penn %q, want %q", i, got[i].Penn, penn)
		}
	}
}

func TestTagContractions(t *testing.T) {
	got := Tag([]string{"I", "do", "n't", "think", "it", "'s", "ready", "."})
	want := []Word{
		{"I", Pronoun, "PRP"}, {"do", Auxiliary, "VBP"}, {"n't", Particle, "RB"}, {"think", Verb, "VB"},
		{"it", Pronoun, "PRP"}, {"'s", Auxiliary, "VBZ"}, {"ready", Adjective, "JJ"}, {".", Punctuation, "."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTagParticiples(t *testing.T) {
	got := Tag([]string{"The", "report", "was", "written", "by", "the", "team", "."})
	if got[3] != (Word{"written", Verb, "VBN"}) {
		t.Errorf("written: %v, want VERB VBN", got[3])
	}
	got = Tag([]string{"Running", "the", "tests", "takes", "time", "."})
	if got[0] != (Word{"Running", Verb, "VBG"}) {
		t.Errorf("Running: %v, want VERB VBG", got[0])
	}
}

func TestTagDeterministic(t *testing.T) {
	words := strings.Fields("Colorless green ideas sleep furiously , said the linguist in 1957 .")
	want := Tag(words)
	var wg sync.WaitGroup
	results := make([][]Word, 8)
	for i := range results {
		wg.Go(func() { results[i] = Tag(words) })
	}
	wg.Wait()
	for i, got := range results {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("call %d: %v, want %v", i, got, want)
		}
	}
}
