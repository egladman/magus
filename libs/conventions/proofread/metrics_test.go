package proofread

import "testing"

func TestMeasureCountsWordsSentencesAndGrade(t *testing.T) {
	got := Measure("The cat sat. The cat sat on the mat today.", KindReference)

	want := Metrics{
		Words: 10, Sentences: 2, SentenceWordsMean: 5, SentenceWordsSD: 2, LongestSentence: 7, Syllables: 11,
		FleschKincaidGrade: 0.39*5 + 11.8*1.1 - 15.59, FleschReadingEase: 206.835 - 1.015*5 - 84.6*1.1,
	}
	want.FleschKincaidGrade, want.FleschReadingEase = round2(want.FleschKincaidGrade), round2(want.FleschReadingEase)

	if got != want {
		t.Errorf("Measure:\n got %+v\nwant %+v", got, want)
	}
}

func TestMeasureMasksWhatIsNotProse(t *testing.T) {
	plain := Measure("Run the code now.", KindReference)

	cases := map[string]string{
		"a code span":   "Run the `magus affected --impact internal/cache` now.",
		"a fence":       "Run the code now.\n\n```sh\nmagus run go-build . --no-cache\n```\n",
		"a heading":     "## Configuration reference\n\nRun the code now.",
		"a table":       "Run the code now.\n\n| Flag | Meaning |\n| ---- | ------- |\n| -x | extraordinary |\n",
		"a URL":         "Run the code now. https://example.com/documentation/configuration",
		"front matter":  "---\ntitle: Configuration\n---\n\nRun the code now.",
		"an HTML block": "<!-- generated automatically -->\nRun the code now.",
	}

	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Measure(text, KindReference); got != plain {
				t.Errorf("Measure:\n got %+v\nwant %+v", got, plain)
			}
		})
	}
}

func TestMeasureEndsASentenceAtItsParagraphOrItem(t *testing.T) {
	got := Measure("- one two three\n- four five\n\nsix seven", KindChangeDescription)
	if got.Sentences != 3 || got.Words != 7 || got.LongestSentence != 3 {
		t.Errorf("Measure = %+v, want 3 sentences of 7 words, the longest 3", got)
	}
}

func TestMeasureOfNoProseIsZero(t *testing.T) {
	if got := Measure("```\ncode\n```\n", KindReference); got != (Metrics{}) {
		t.Errorf("Measure = %+v, want zero", got)
	}
}

func TestSyllablesEstimatesAWord(t *testing.T) {
	cases := map[string]int{
		"cat": 1, "the": 1, "cache": 1, "table": 2, "little": 2, "release": 2, "reading": 2,
		"configuration": 5, "every": 3, "rhythm": 1, "42": 1, "you're": 1, "(code)": 1,
	}

	for word, want := range cases {
		if got := Syllables(word); got != want {
			t.Errorf("Syllables(%q) = %d, want %d", word, got, want)
		}
	}
}
