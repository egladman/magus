package proofread

import (
	"math"
	"slices"
	"strings"
	"testing"
)

// commitMessageMinimum is MINIMUMS["commit-message"] in
// hack/policy/proofread.buzz, whose test holds the two equal.
const commitMessageMinimum = 20

// gradeFindings returns the [RuleGrade] findings in found.
func gradeFindings(found []Finding) []Finding {
	var out []Finding

	for _, f := range found {
		if f.Rule == RuleGrade {
			out = append(out, f)
		}
	}

	return out
}

func TestGradeWeighsAdviseFindingsAgainstLength(t *testing.T) {
	filler := Finding{Rule: RuleFiller, Decision: DecisionAdvise, Weight: 0.65}
	deny := Finding{Rule: RuleTerms, Decision: DecisionDeny}

	cases := []struct {
		name  string
		found []Finding
		words int
		want  float64
	}{
		{"no finding", nil, 40, 100},
		{"a deny finding weighs nothing", []Finding{deny}, 40, 100},
		{"one finding in 100 words", []Finding{filler}, 100, 67.5},
		{"one finding in 0 words", []Finding{filler}, 0, 35},
		{"two findings in 0 words floor at 0", []Finding{filler, filler}, 0, 0},
		{"1000 words", []Finding{filler}, 1000, 100 - 65.0/11},
		{"5000 words grade as 1000", []Finding{filler}, 5000, 100 - 65.0/11},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Grade(tc.found, tc.words); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("Grade = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGradeOfALongTextStopsGrowingAt1000Words(t *testing.T) {
	text := func(words int) string {
		var b strings.Builder

		b.WriteString("The cache simply keeps it.\n\n")

		for range (words - 5) / 5 {
			b.WriteString("The cache keeps the key. ")
		}

		return b.String()
	}

	var got []string

	for _, words := range []int{1000, 5000} {
		found := JudgeText(text(words), KindReference, WithMinimum(101))
		graded := gradeFindings(found)

		if len(graded) != 1 || !slices.ContainsFunc(found, func(f Finding) bool { return f.Rule == RuleFiller }) {
			t.Fatalf("%d words: want one filler finding and one grade finding, got %+v", words, found)
		}

		got = append(got, graded[0].Message)
	}

	if got[0] != got[1] {
		t.Errorf("1000 words: %q\n5000 words: %q", got[0], got[1])
	}
}

// A commit subject is the shortest text proofread grades, so it carries the
// risk that a grade only tracks length.
func TestAShortCommitPassesOneFindingOfItsStrongestRuleAndFailsTwo(t *testing.T) {
	shipEvidence(t)

	strongest := RuleSubjectMood

	for _, c := range checks {
		if c.defaultDecision(KindCommitMessage) == DecisionAdvise && weights[c.rule] > weights[strongest] {
			t.Fatalf("%s weighs %v, past %s: test the strongest rule", c.rule, weights[c.rule], strongest)
		}
	}

	one := "Added a flag so the cache key stays stable across worker runs"
	if n := countWords(one); n > 12 {
		t.Fatalf("%d words, want at most 12", n)
	}

	found := JudgeText(one, KindCommitMessage, WithMinimum(commitMessageMinimum))
	if len(found) != 1 || found[0].Rule != strongest || found[0].Weight != weights[strongest] {
		t.Errorf("one finding of %s: got %+v", strongest, found)
	}

	twice := []Finding{found[0], found[0]}
	if g := Grade(twice, countWords(one)); g >= commitMessageMinimum {
		t.Errorf("two findings of %s grade %v, want under %d", strongest, g, commitMessageMinimum)
	}
}

func TestTheGradeFindingNamesTheMinimumAndTheCostliestRules(t *testing.T) {
	const text = "It simply holds. It really holds. It basically holds, and it may well hold."

	found := JudgeText(text, KindReference, WithMinimum(90))

	graded := gradeFindings(found)
	if len(graded) != 1 || graded[0].Decision != DecisionDeny || graded[0].Weight != 0 {
		t.Fatalf("want one deny grade finding, got %+v", found)
	}

	msg := graded[0].Message
	if !strings.Contains(msg, "under the minimum of 90") || !strings.Contains(msg, "filler") {
		t.Errorf("message = %q", msg)
	}

	if got := JudgeText(text, KindReference); len(gradeFindings(got)) != 0 {
		t.Errorf("no minimum grades nothing, got %+v", gradeFindings(got))
	}

	off := WithDecisions(map[Rule]Decision{RuleGrade: DecisionOff})
	if got := JudgeText(text, KindReference, WithMinimum(90), off); len(gradeFindings(got)) != 0 {
		t.Errorf("a table setting grade off grades nothing, got %+v", gradeFindings(got))
	}
}
