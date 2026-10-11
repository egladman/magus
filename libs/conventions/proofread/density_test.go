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
		{"a clause naming an issue", padded(14, "The worker reads the key, closing #12, keeping the order."), nil},
		{"an open task item", padded(14, "The worker reads the key.") +
			"\n\n- [ ] Have you read the key, using the lock, keeping the order?", nil},
		{"a tail closed by a period", padded(14, "It fails, causing retries. It stops, wasting time."),
			[]string{"2 present participial clauses in 218 words, 9.2 per 1000"}},
		{"a tail ending a list item", padded(14, "") + "\n\n- Fixed the race, ensuring stable output\n" +
			"- Moved the key, keeping old names", []string{"2 present participial clauses in 222 words, 9.0 per 1000"}},
		{"a noun in a series", padded(14, "") + "\n\n- Uses timeout constants, logging methods, and restart helpers.\n" +
			"- Shows short names, ranking scores, and links.", nil},
		{"a noun before the and closing a series", padded(14, "The guide gives the commands, timing notes, and "+
			"build tips. The page lists the flags, caching rules, and limits."), nil},
		{"a noun ending a list item", padded(14, "") + "\n\n- Phase one: bare fixes, logging setup\n" +
			"- Phase two: modern packaging, logging cleanup", nil},
		{"a noun closing a parenthesis", padded(14, "It covers ranges (negative ranges, crossing zero) and "+
			"inputs (empty input, missing keys)."), nil},
		{"a gerund subject after a comma", padded(14, "In the cache, caching is the default. After a miss, "+
			"clicking `Retry` should hash again."), nil},
		{"a bold label", padded(14, "") + "\n\n- **Adding keys**: tests one key, two keys\n" +
			"- **Saving keys**: tests the store, the cache", nil},
		{"a comma inside the opener's parenthesis", padded(14, "") + "\n\n- Troubleshooting for build quirks " +
			"(case issues, stale keys)\n- Tracing for slow runs (cold caches, long hashes)", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := densityMessages(tc.text, KindReference, RuleParticiples); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("participles = %q, want %q", got, tc.want)
			}
		})
	}
}

// The rule cannot tell a participle from a gerund, so the finding quotes each
// clause it counted, in text order, for the reader to check.
func TestParticiplesNamesTheClausesItCounted(t *testing.T) {
	text := padded(14, "Holding the lock, the worker reads the key, using the lock it holds for the whole run.")

	var got []string

	for _, f := range JudgeText(text, KindReference, WithOnly(RuleParticiples)) {
		got = append(got, f.Message)
	}

	want := []string{"2 present participial clauses in 227 words, 8.8 per 1000 (the cap is 5): " +
		"'Holding the lock', 'using the lock it holds for ...'. Give each -ing clause its own sentence with a " +
		"subject, or cut the clause."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("participles = %q, want %q", got, want)
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
