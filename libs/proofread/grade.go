package proofread

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
)

// RuleGrade reports a text whose [Grade] is under the minimum the caller set
// with [WithMinimum].
const RuleGrade Rule = "grade"

// A text's findings are weighed against 1 + words/gradeWordStep, with words
// capped at gradeWordCap, so padding a long text buys no headroom.
const (
	gradeWordStep = 100
	gradeWordCap  = 1000
)

// gradeCheck has no judge: [input.grade] reports it once every other rule has.
// A diagnostic's rules all deny, so it has no grade.
var gradeCheck = check{
	rule: RuleGrade, on: slices.DeleteFunc(slices.Clone(kinds), func(k Kind) bool { return k == KindDiagnostic }),
}

var gradeTexts = map[Rule]ruleText{
	RuleGrade: {
		code:      "PRF1100",
		dimension: DimensionStructure,
		catches:   "a text whose grade, out of 100, is under the minimum its kind is held to",
		why: "An advise finding fails nothing alone, so twenty of them read the same as one. The grade " +
			"is 100 less 100 times the summed weight of a text's advise findings over 1 + words/100, words " +
			"capped at 1000, floored at 0. A finding weighs its rule's Wilson 95% lower bound on precision " +
			"in its latest labeling round, so a rule right 4 times in 5 costs four times one right 1 time " +
			"in 5, and a rule no round measured costs nothing. A deny finding fails a text whatever its " +
			"grade. The minimum is the caller's policy, set per kind in the decisions table; proofread " +
			"ships none.",
	},
}

// WithMinimum grades the judged text and reports a [RuleGrade] finding when
// its [Grade] is under minimum. Without it, nothing is graded.
func WithMinimum(minimum int) Option { return func(o *options) { o.minimum = &minimum } }

// Grade is a text's grade out of 100, given its findings and its count of
// prose words: 100 less 100 times the findings' summed Weight over 1 +
// words/100, with words capped at 1000, floored at 0. A text past 1000 words
// grades as one of 1000. Only advise findings carry a Weight.
func Grade(found []Finding, words int) float64 {
	cost := 0.0
	for _, f := range found {
		cost += f.Weight
	}

	return max(0, 100-points(cost, words))
}

// points is what a summed weight of cost takes from the grade of a text of
// words words.
func points(cost float64, words int) float64 {
	return 100 * cost / (1 + float64(min(words, gradeWordCap))/gradeWordStep)
}

// grade is the [RuleGrade] finding for found, the findings of the text in
// holds, when a minimum is set, the rule runs and the grade is under it.
func (in input) grade(found []Finding) (Finding, bool) {
	d := in.opts.decide(gradeCheck, in.kind)
	if in.opts.minimum == nil || d == DecisionOff {
		return Finding{}, false
	}

	words := in.words()
	minimum := *in.opts.minimum

	g := Grade(found, words)
	if g >= float64(minimum) {
		return Finding{}, false
	}

	return in.opts.settle(gradeCheck, Finding{Message: gradeMessage(found, words, g, minimum)}, d), true
}

// words counts the prose the rules read, or the whole text of a kind that
// holds no prose lines.
func (in input) words() int {
	if len(in.prose) == 0 {
		return countWords(in.text)
	}

	n := 0
	for _, ln := range in.prose {
		n += countWords(ln.body())
	}

	return n
}

// gradeMessage names the grade, the minimum and the three rules whose
// findings cost the most, so the reader knows what to fix first.
func gradeMessage(found []Finding, words int, g float64, minimum int) string {
	cost := map[Rule]float64{}

	for _, f := range found {
		if f.Weight > 0 {
			cost[f.Rule] += f.Weight
		}
	}

	rules := slices.SortedFunc(maps.Keys(cost), func(a, b Rule) int {
		return cmp.Or(cmp.Compare(cost[b], cost[a]), cmp.Compare(a, b))
	})

	parts := make([]string, 0, 3)
	for _, r := range rules[:min(3, len(rules))] {
		parts = append(parts, fmt.Sprintf("%s %.1f", r, points(cost[r], words)))
	}

	return fmt.Sprintf("Grade %.1f is under the minimum of %d. The findings that cost the most: %s. Fix those "+
		"first.", math.Floor(g*10)/10, minimum, strings.Join(parts, ", "))
}
