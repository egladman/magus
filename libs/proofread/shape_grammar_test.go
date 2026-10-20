package proofread

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/proofread/internal/wordclass"
)

// grammarCount counts feature's matches in text, split as [MeasureShape]
// splits it, whatever its length.
func grammarCount(text, feature string) int {
	n := 0

	for _, sentence := range splitSentences(maskSpans(text)) {
		s := tagSentence(sentence)

		for _, f := range grammarFeatures {
			for w := range s.words {
				if f.name == feature && f.matches(s, w) {
					n++
				}
			}
		}
	}

	return n
}

func TestGrammarFeatures(t *testing.T) {
	cases := map[string][]struct {
		text string
		want int
	}{
		"participial_clause": {
			{"Running the tests, she found a bug.", 1},
			{"The build failed, leaving the cache stale.", 1},
			{"Built in a week, the tool still works.", 1},
			// A gerund subject, a progressive and a list of participles.
			{"Running the tests takes time.", 0},
			{"She was running the tests.", 0},
			{"It was tested, reviewed and merged.", 0},
		},
		"nominalization": {
			{"The implementation needs a decision.", 2},
			{"Its readiness and complexity hurt performance.", 3},
			{"The implementations differ.", 1},
			// "nation" and "city" are under the stem floor; "mention",
			// "function" and "version" are listed.
			{"The nation and the city keep a mention of the function and its version.", 0},
		},
		"agentless_passive": {
			{"The cache was rebuilt.", 1},
			{"The fix has not been merged yet.", 1},
			{"It got merged on Friday.", 1},
			{"The cache was rebuilt by CI.", 0},
			{"It's been a while.", 0},
			{"She has merged it.", 0},
		},
		"phrasal_coordination": {
			{"Tests and benchmarks run nightly.", 1},
			{"The result is fast and safe.", 1},
			{"The result is fast or safe.", 0},
			{"The cat and the dog sleep.", 0},
			{"It ran and stopped.", 0},
		},
		"downtoner": {
			{"It is slightly slower and nearly done.", 2},
			{"It only runs once.", 1},
			{"It is the only way.", 0},
		},
		"concessive_though": {
			{"Though it works, it is slow.", 1},
			{"It is slow, although it works.", 1},
			{"It works, though.", 0},
		},
		"present_participle_postmodifier": {
			{"A job running every night failed.", 1},
			{"The job's running now.", 0},
			{"The job is running now.", 0},
		},
	}

	for feature, examples := range cases {
		t.Run(feature, func(t *testing.T) {
			for _, ex := range examples {
				if got := grammarCount(ex.text, feature); got != ex.want {
					s := tagSentence(strings.Fields(ex.text))
					t.Errorf("%q: %d matches, want %d; tagged %v", ex.text, got, ex.want, s.tags)
				}
			}
		})
	}
}

func TestGrammarFeaturesAreNamedOnce(t *testing.T) {
	seen := map[string]bool{}

	for _, f := range grammarFeatures {
		if seen[f.name] {
			t.Errorf("%s is listed twice", f.name)
		}

		seen[f.name] = true
	}
}

func TestTemplateRate(t *testing.T) {
	cycle := []wordclass.Class{wordclass.Determiner, wordclass.Noun, wordclass.Verb, wordclass.Punctuation}

	cases := map[string]struct {
		sequence []wordclass.Class
		want     float64
	}{
		// Five 4-grams; the first and the last are both DET NOUN VERB PUNCT.
		"one sentence shape twice": {slices.Concat(cycle, cycle), 0.4},
		"no repeat": {
			[]wordclass.Class{
				wordclass.Determiner, wordclass.Noun, wordclass.Verb, wordclass.Adverb,
				wordclass.Determiner, wordclass.Adjective, wordclass.Noun,
			}, 0,
		},
		"under one 4-gram": {cycle[:3], 0},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := templateRate(tc.sequence); got != tc.want {
				t.Errorf("templateRate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTagCompressionIsLowerForRepeatedSyntax(t *testing.T) {
	var repeated, varied []wordclass.Class

	for i := range 200 {
		repeated = append(repeated, wordclass.Class(1+i%4))
		varied = append(varied, wordclass.Class(1+(i*7+i*i/3)%17))
	}

	r, v := tagCompression(repeated), tagCompression(varied)
	if r <= 0 || r >= v || v >= 1 {
		t.Errorf("tagCompression = %v repeated, %v varied, want 0 < repeated < varied < 1", r, v)
	}

	if got := tagCompression(nil); got != 0 {
		t.Errorf("tagCompression(nil) = %v, want 0", got)
	}
}

func TestMeasureShapeMeasuresTemplatesOfOneSentenceShape(t *testing.T) {
	// 67 sentences of DET NOUN VERB PUNCT: every 4-gram repeats.
	got := MeasureShape(repeat("The cat sat.", 67), KindReference)

	if got.TemplateRate != 1 || got.TagCompression <= 0 || got.TagCompression >= 0.1 {
		t.Errorf("MeasureShape = template rate %v, tag compression %v, want 1 and under 0.1",
			got.TemplateRate, got.TagCompression)
	}

	if len(got.Grammar) != len(grammarFeatures) || got.Grammar["nominalization"] != 0 {
		t.Errorf("Grammar = %v, want every feature, none matched", got.Grammar)
	}
}

func TestMeasureShapeReadsTheGrammarOfAParagraph(t *testing.T) {
	got := shapeFixture(t, "grammar.txt")

	// Over 300 words, checked by hand:
	//   - participial clauses: "Reading those logs,", ", writing down" and
	//     "Looking back,";
	//   - nominalizations: documentation, assumptions, convenience and
	//     permission;
	//   - agentless passives: "was rebuilt", "had been ignored" and "were
	//     fixed within a day";
	//   - phrasal coordination: none; "stale caches and flaky network
	//     fetches" joins a noun to an adjective;
	//   - downtoners: nearly, slightly and only;
	//   - concessives: though and although;
	//   - postmodifiers: "a job failing", "a week tracing" and "the person
	//     reading".
	want := map[string]float64{
		"participial_clause": 10, "nominalization": 13.33, "agentless_passive": 10, "phrasal_coordination": 0,
		"downtoner": 10, "concessive_though": 6.67, "present_participle_postmodifier": 10,
	}
	if got.Words != 300 || !maps.Equal(got.Grammar, want) {
		t.Errorf("MeasureShape = %d words, grammar %v, want 300 and %v", got.Words, got.Grammar, want)
	}

	if got.TemplateRate != 0.287 || got.TagCompression != 0.2189 {
		t.Errorf("MeasureShape = template rate %v, tag compression %v, want 0.287 and 0.2189",
			got.TemplateRate, got.TagCompression)
	}
}
