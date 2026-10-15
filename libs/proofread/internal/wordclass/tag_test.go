package wordclass

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

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
