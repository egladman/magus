package proofread

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func shapeFixture(t *testing.T, name string) Shape {
	t.Helper()

	text, err := os.ReadFile(filepath.Join("testdata", "shape", name))
	if err != nil {
		t.Fatal(err)
	}

	return MeasureShape(string(text), KindReference)
}

func repeat(words string, n int) string {
	return strings.TrimSpace(strings.Repeat(words+" ", n))
}

var (
	lexicalWithheld  = []string{"mattr", "word_entropy", "hapax_ratio", "lexical_density"}
	countWithheld    = []string{"function_words", "grammar_per_1000", "template_rate", "tag_compression"}
	sentenceWithheld = []string{"sentence_words_mean", "sentence_words_sd", "adjacent_overlap"}
)

func TestMeasureShapeCountsPunctuationPer100Words(t *testing.T) {
	got := shapeFixture(t, "punctuation.txt")

	// 10 words; the dashes are an em dash, a spaced hyphen and an en dash.
	want := map[string]float64{
		"colon": 10, "semicolon": 10, "paren": 10, "dash": 30, "comma": 10, "exclamation": 10, "question": 10,
	}
	if got.Words != 10 || !maps.Equal(got.Punctuation, want) {
		t.Errorf("MeasureShape = %d words, punctuation %v, want 10 and %v", got.Words, got.Punctuation, want)
	}
}

func TestMeasureShapeCountsLayoutPer100Words(t *testing.T) {
	got := shapeFixture(t, "layout.txt")

	// The heading holds no counted word: 5 words in the paragraph, 2 and 3 in
	// the items.
	want := map[string]float64{"heading": 10, "bold": 10, "list_item": 20, "code_span": 10}
	if got.Words != 10 || !maps.Equal(got.Layout, want) || got.ParagraphWordsMean != 3.33 {
		t.Errorf("MeasureShape = %d words, layout %v, %v per paragraph, want 10, %v and 3.33",
			got.Words, got.Layout, got.ParagraphWordsMean, want)
	}
}

func TestMeasureShapeCountsThreeItemListsOnly(t *testing.T) {
	got := shapeFixture(t, "triads.txt")

	// 25 words: "the cache, the graph, and the log" and "fast, small and safe"
	// are triads; "A, B, C, and D" has four items and "bread and butter" two.
	if got.Words != 25 || got.Triads != 80 {
		t.Errorf("MeasureShape = %d words, %v triads per 1000, want 25 and 80", got.Words, got.Triads)
	}
}

func TestMeasureShapeCountsContrasts(t *testing.T) {
	got := shapeFixture(t, "contrasts.txt")

	// 20 words: "not slow but", "isn't a bug, it's" and "It's not just".
	if got.Words != 20 || got.NotXButYPer1000 != 150 {
		t.Errorf("MeasureShape = %d words, %v contrasts per 1000, want 20 and 150", got.Words, got.NotXButYPer1000)
	}
}

func TestMeasureShapeCountsQuestionsItsParagraphAnswers(t *testing.T) {
	got := shapeFixture(t, "questions.txt")

	// 18 words. "Why does it race?" and "Is it safe?" are answered; "Is it
	// fast?" is followed by a question and "Why?" ends its paragraph: 2 in 18
	// words is 111.11 per 1000.
	if got.Words != 18 || got.RhetoricalQuestionsPer1000 != 111.11 {
		t.Errorf("MeasureShape = %d words, %v rhetorical questions per 1000, want 18 and 111.11",
			got.Words, got.RhetoricalQuestionsPer1000)
	}
}

func TestMeasureShapeAveragesAdjacentOverlap(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("testdata", "shape", "overlap.txt"))
	if err != nil {
		t.Fatal(err)
	}

	got, m := MeasureShape(string(text), KindReference), Measure(string(text), KindReference)

	// The pairs share 2 of 4 content words, then 0, 2 of 2 and 0: (0.5 + 0 +
	// 1 + 0) / 4.
	if got.AdjacentOverlap != 0.375 {
		t.Errorf("AdjacentOverlap = %v, want 0.375", got.AdjacentOverlap)
	}

	if got.Words != m.Words || got.Sentences != m.Sentences || got.SentenceWordsMean != m.SentenceWordsMean ||
		got.SentenceWordsStdDev != m.SentenceWordsSD {
		t.Errorf("MeasureShape = %+v, want the words and sentences of Measure %+v", got, m)
	}
}

func TestMeasureShapeMeasuresLexicalRichness(t *testing.T) {
	distinct := make([]string, 0, 50)
	for i := range 50 {
		distinct = append(distinct, fmt.Sprintf("a%d", i))
	}

	cases := map[string]struct {
		text                           string
		mattr, entropy, hapax, density float64
	}{
		// Every window holds two types of 50 words; "the" is a function word.
		"two words alternating": {repeat("the cat", 50), 0.04, 1, 0, 0.5},
		// The window at offset i holds 50-i distinct words, plus "the" when i
		// is over 0: (50 + 50 + 49 + ... + 1) / 51 windows / 50 = 0.5196.
		// The entropy is 50 * 0.01 * log2(100) + 0.5 * log2(2) = 3.8219.
		"fifty distinct words, then one": {strings.Join(distinct, " ") + " " + repeat("the", 50), 0.5196, 3.8219, 0.5, 0.5},
		// Code spans are no word to the lexical measures, so ten of them
		// change nothing.
		"code spans among them": {repeat("the cat", 50) + " " + repeat("`x`", 10), 0.04, 1, 0, 0.5},
		// A word of punctuation alone trims to nothing, which is no type.
		"spaced dashes among them": {repeat("the cat --", 50), 0.04, 1, 0, 0.5},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := MeasureShape(tc.text, KindReference)
			if got.MATTR != tc.mattr || got.WordEntropy != tc.entropy || got.HapaxRatio != tc.hapax ||
				got.LexicalDensity != tc.density {
				t.Errorf("MeasureShape = MATTR %v, entropy %v, hapax %v, density %v, want %v, %v, %v, %v",
					got.MATTR, got.WordEntropy, got.HapaxRatio, got.LexicalDensity, tc.mattr, tc.entropy, tc.hapax, tc.density)
			}
		})
	}
}

func TestMeasureShapeCountsFunctionWordsPer1000(t *testing.T) {
	got := MeasureShape(repeat("the cat", 100), KindReference)

	if len(got.FunctionWords) != len(shapeFunctionWords) || got.FunctionWords["the"] != 500 || got.FunctionWords["a"] != 0 {
		t.Errorf("FunctionWords = %v, want every listed word, the at 500 and a at 0", got.FunctionWords)
	}
}

func TestShapeFunctionWordsAreDistinct(t *testing.T) {
	if len(isShapeFunctionWord) != len(shapeFunctionWords) {
		t.Errorf("%d function words hold %d distinct ones", len(shapeFunctionWords), len(isShapeFunctionWord))
	}
}

func TestMeasureShapeWithholdsWhatTheTextIsTooShortFor(t *testing.T) {
	everything := slices.Concat(lexicalWithheld, countWithheld, sentenceWithheld)

	cases := map[string]struct {
		text string
		want []string
	}{
		"no prose":       {"```\ncode\n```\n", everything},
		"99 words":       {repeat("the cat", 49) + " the", everything},
		"100 words":      {repeat("the cat", 50), slices.Concat(countWithheld, sentenceWithheld)},
		"199 words":      {repeat("the cat", 99) + " the", slices.Concat(countWithheld, sentenceWithheld)},
		"200 words":      {repeat("the cat", 100), sentenceWithheld},
		"4 sentences":    {repeat("One two.", 4), everything},
		"5 sentences":    {repeat("One two.", 5), slices.Concat(lexicalWithheld, countWithheld)},
		"100 code spans": {repeat("`x`", 100), everything},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := MeasureShape(tc.text, KindReference)
			if !slices.Equal(got.Withheld, tc.want) {
				t.Errorf("Withheld = %v, want %v", got.Withheld, tc.want)
			}

			if slices.Contains(got.Withheld, "mattr") && (got.MATTR != 0 || got.WordEntropy != 0 || got.HapaxRatio != 0 ||
				got.LexicalDensity != 0) {
				t.Errorf("MeasureShape = %+v, want the withheld lexical measures zero", got)
			}

			if slices.Contains(got.Withheld, "function_words") && got.FunctionWords != nil {
				t.Errorf("FunctionWords = %v, want none", got.FunctionWords)
			}

			if slices.Contains(got.Withheld, "grammar_per_1000") &&
				(got.Grammar != nil || got.TemplateRate != 0 || got.TagCompression != 0) {
				t.Errorf("MeasureShape = grammar %v, template rate %v, tag compression %v, want none",
					got.Grammar, got.TemplateRate, got.TagCompression)
			}
		})
	}
}

func TestMeasureShapeOfFiveSentences(t *testing.T) {
	// "one" is a function word, so each sentence's content is {two}.
	got := MeasureShape(repeat("One two.", 5), KindReference)
	if got.SentenceWordsMean != 2 || got.SentenceWordsStdDev != 0 || got.AdjacentOverlap != 1 {
		t.Errorf("MeasureShape = %+v, want 2 words per sentence, no spread and full overlap", got)
	}
}

func TestMeasureShapeSkipsWhatIsNotProse(t *testing.T) {
	plain := MeasureShape("Run the code now.", KindReference)

	cases := map[string]string{
		"a fence":       "Run the code now.\n\n```sh\nmagus run go-build . --no-cache, now; really: yes!\n```\n",
		"a URL":         "Run the code now. https://example.com/a,b;c:d",
		"front matter":  "---\ntitle: Configuration, again\n---\n\nRun the code now.",
		"a table":       "Run the code now.\n\n| Flag | Meaning |\n| ---- | ------- |\n| -x | a, b and c |\n",
		"an HTML block": "<!-- generated: automatically -->\nRun the code now.",
	}

	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if got := MeasureShape(text, KindReference); !reflect.DeepEqual(got, plain) {
				t.Errorf("MeasureShape:\n got %+v\nwant %+v", got, plain)
			}
		})
	}
}

func TestMeasureShapeCountsACodeSpanAsOneWordOfNoPunctuation(t *testing.T) {
	got := MeasureShape("Run `a, b; c: d` now.", KindReference)

	if got.Words != 3 || got.Layout["code_span"] != 33.33 || got.Punctuation["comma"] != 0 || got.Punctuation["colon"] != 0 {
		t.Errorf("MeasureShape = %+v, want 3 words, one code span and no punctuation", got)
	}
}
