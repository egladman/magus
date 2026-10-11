package proofread

import (
	"reflect"
	"strings"
	"testing"
)

// plain is a sentence of 15 words with no participle and no nominalization.
const plain = "The cache stores each blob under a key that the run derives from its inputs. "

// padded is n plain sentences, 15n words, then tail.
func padded(n int, tail string) string { return strings.Repeat(plain, n) + tail }

// densityMessages renders each finding of rule as its message's count and
// rate, the part a case pins.
func densityMessages(text string, kind Kind, rule Rule) []string {
	var out []string

	for _, f := range JudgeText(text, kind) {
		if f.Rule == rule {
			head, _, _ := strings.Cut(f.Message, " (")
			out = append(out, head)
		}
	}

	return out
}

func TestParticiplesReportsATextOverTheRate(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"two tails in 220 words", padded(14, "The worker reads the key, using the lock it holds, keeping the order."),
			[]string{"2 present participial clauses in 223 words, 9.0 per 1000"}},
		{"an opening clause", padded(14, "Holding the lock, the worker reads the key. Using it, the run hashes."),
			[]string{"2 present participial clauses in 223 words, 9.0 per 1000"}},
		{"one in 220 words is under the cap", padded(14, "The worker reads the key, using the lock it holds."), nil},
		{"a short text is not judged", "The worker reads the key, using the lock, keeping the order, hashing it.", nil},
		{"a series of gerunds", padded(14, "It covers parsing, linking, running and testing. It covers reading, writing."), nil},
		{"a gerund subject", padded(14, "Running the suite takes a minute. Reading the key takes less."), nil},
		{"a preposition", padded(14, "It reads every input, including the lock, according to the key."), nil},
		{"a noun after the comma", padded(14, "It returns a key, a string, nothing else, a warning."), nil},
		{"in code", padded(14, "It runs `go test, using the cache, keeping the key`."), nil},
		{"an -ing word ending its clause", padded(14, "The key it reads is stable, unchanging. It is fixed, unyielding."), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := densityMessages(tc.text, KindReference, RuleParticiples); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("participles = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNominalizationsReportsATextOverTheRate(t *testing.T) {
	nominal := "The implementation of the validation requires the configuration and the documentation. "

	cases := []struct {
		name string
		text string
		want []string
	}{
		{"verbs turned into nouns", padded(13, strings.Repeat(nominal, 2)),
			[]string{"8 nominalizations in 217 words, 36.9 per 1000"}},
		{"under the cap", padded(13, nominal), nil},
		{"a short text is not judged", strings.Repeat(nominal, 5), nil},
		{"technical nouns", padded(13, strings.Repeat("The function returns the exception, the comment and the "+
			"environment, then the document. ", 3)), nil},
		{"identifiers and code", padded(13, strings.Repeat("It reads `implementation`, getConfiguration and "+
			"user_validation now. ", 3)), nil},
		{"a heading is a label", "## Implementation, validation, configuration and documentation\n\n" + padded(14, ""), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := densityMessages(tc.text, KindReference, RuleNominalizations); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("nominalizations = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDensityRulesAdviseOnRunningProseOnly(t *testing.T) {
	every := []Kind{
		KindDocComment, KindReference, KindGuide, KindChangeDescription, KindAgentInstructions, KindReviewReply,
		KindIssue,
	}
	text := "fix(cache): keep the key stable\n" +
		padded(14, "The worker reads the key, using the lock it holds, keeping the order.")
	advise := map[Kind]Decision{
		KindReference: DecisionAdvise, KindGuide: DecisionAdvise, KindChangeDescription: DecisionAdvise,
		KindIssue: DecisionAdvise,
	}

	if got := decisions(RuleParticiples, text, nil, every...); !reflect.DeepEqual(got, advise) {
		t.Errorf("participles judged %v, want %v", got, advise)
	}
}

func TestDensityFindingNamesNoSpan(t *testing.T) {
	text := padded(14, "The worker reads the key, using the lock it holds, keeping the order.")

	for _, f := range JudgeText(text, KindReference, WithOnly(RuleParticiples)) {
		if f.Match != "" || f.Line != 0 || f.Replacements != nil {
			t.Errorf("finding = %#v, want one for the whole text", f)
		}
	}
}
