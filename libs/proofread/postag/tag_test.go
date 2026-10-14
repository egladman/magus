package postag

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func BenchmarkTag(b *testing.B) {
	texts := ewtTexts(b)

	sentences := make([][]string, len(texts))
	for i, text := range texts {
		sentences[i] = Tokenize(text)
	}

	loadModel()
	b.ReportAllocs()

	for b.Loop() {
		for _, words := range sentences {
			Tag(words)
		}
	}
}

type goldToken struct{ form, upos, xpos string }

func readGold(t *testing.T, path string) [][]goldToken {
	t.Helper()
	gz, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	var sentences [][]goldToken
	var cur []goldToken
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if len(cur) > 0 {
				sentences = append(sentences, cur)
			}
			cur = nil
			continue
		}
		cols := strings.Split(line, "\t")
		if strings.HasPrefix(line, "#") || len(cols) != 10 || strings.ContainsAny(cols[0], "-.") {
			continue
		}
		cur = append(cur, goldToken{form: cols[1], upos: cols[3], xpos: cols[4]})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(cur) > 0 {
		sentences = append(sentences, cur)
	}
	return sentences
}

func TestTagAccuracy(t *testing.T) {
	var tokens, upos, xpos int
	for _, s := range readGold(t, "testdata/en_ewt-ud-test.conllu.gz") {
		words := make([]string, len(s))
		for i, tok := range s {
			words[i] = tok.form
		}
		for i, got := range Tag(words) {
			tokens++
			if got.UPOS.String() == s[i].upos {
				upos++
			}
			if string(got.XPOS) == s[i].xpos {
				xpos++
			}
		}
	}
	m := loadModel()
	if tokens != m.testTokens || upos != m.testUPOS || xpos != m.testXPOS {
		t.Errorf("tagged %d tokens, %d UPOS and %d XPOS right; the trainer counted %d, %d and %d",
			tokens, upos, xpos, m.testTokens, m.testUPOS, m.testXPOS)
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
		if got[i].UPOS != PUNCT {
			t.Errorf("word %d: %v, want PUNCT", i, got[i])
		}
	}
	want := map[int]XPOS{1: COMMA, 4: "-LRB-", 6: "-RRB-", 7: "."}
	for i, xpos := range want {
		if got[i].XPOS != xpos {
			t.Errorf("word %d: XPOS %q, want %q", i, got[i].XPOS, xpos)
		}
	}
}

func TestTagContractions(t *testing.T) {
	got := Tag([]string{"I", "do", "n't", "think", "it", "'s", "ready", "."})
	want := []Tagged{
		{"I", PRON, "PRP"}, {"do", AUX, VBP}, {"n't", PART, "RB"}, {"think", VERB, VB},
		{"it", PRON, "PRP"}, {"'s", AUX, VBZ}, {"ready", ADJ, "JJ"}, {".", PUNCT, "."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTagParticiples(t *testing.T) {
	got := Tag([]string{"The", "report", "was", "written", "by", "the", "team", "."})
	if got[3] != (Tagged{"written", VERB, VBN}) {
		t.Errorf("written: %v, want VERB VBN", got[3])
	}
	got = Tag([]string{"Running", "the", "tests", "takes", "time", "."})
	if got[0] != (Tagged{"Running", VERB, VBG}) {
		t.Errorf("Running: %v, want VERB VBG", got[0])
	}
}

func TestTagDeterministic(t *testing.T) {
	words := strings.Fields("Colorless green ideas sleep furiously , said the linguist in 1957 .")
	want := Tag(words)
	var wg sync.WaitGroup
	results := make([][]Tagged, 8)
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
