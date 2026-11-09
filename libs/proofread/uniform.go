package proofread

import (
	"fmt"
	"strings"
)

// RuleUniform reports a text more even than its author writes: on two or
// more spread measures it sits past the author's own 5th percentile.
const RuleUniform Rule = "uniform"

// minUniformFeatures is how many spread measures must sit past the author's
// range before uniform reports. One alone is ordinary variation.
const minUniformFeatures = 2

// uniformFeature is one spread measure uniform holds a text to. even is the
// direction a text moves as it evens out.
type uniformFeature struct {
	name  Feature
	label string
	even  string
	lower bool
}

// uniformFeatures are the spreads ADR 0037's paired calibration kept for
// uniform. Only opener entropy moved there; the item measures had too few
// lists to measure.
var uniformFeatures = []uniformFeature{
	{"item_words_sd", "the spread of list items' lengths", "items of one length", true},
	{"item_opener_share", "list items on one opening word", "items on one opener", false},
	{"opener_class_entropy", "the variety of what sentences open on", "sentences opening alike", true},
}

var uniformChecks = []check{{rule: RuleUniform, reads: proseFormats, judge: uniform}}

var uniformTexts = map[Rule]ruleText{
	RuleUniform: {
		code:      "PRF1041",
		dimension: DimensionStance,
		catches: "a text more even than its author writes: list items of one length, items on one opening " +
			"word, or sentences that all open on one part of speech, past the author's own 5th percentile " +
			"on two or more of them",
		why: "A text cleared of every finding comes out evenly shaped, and that evenness is itself a mark of " +
			"machine-written text. Runs only with a voice file, whose windows hold the author's own range of " +
			"each measure; a text is held to that range, never to a shared one, and nothing is added to make " +
			"a text uneven. A paired calibration of one author's texts against rewrites that cleared every " +
			"finding found the rewrites narrowed what sentences open on, but evened out neither sentence nor " +
			"paragraph length, so those are not read; it had too few lists to measure the item spreads. " +
			"The author's ranges come from 200-word windows, so a text under 200 words is not judged alone; " +
			"`proofread --set` pools short texts, such as a branch's commits, into one. " +
			"No firing has been labeled, so it ships off.",
	},
}

// uniform reports a text whose spreads sit past its author's range on
// minUniformFeatures or more. It abstains without a voice, on a kind with no
// ranges, and on a measure the text is too short for. Under 200 words it
// abstains too: [JudgeSet] pools short texts.
func uniform(in input) []Finding {
	if in.opts.voice == nil || in.spec.Voice == "" {
		return nil
	}

	ranges := in.opts.voice.Kinds[in.spec.Voice].Ranges
	if len(ranges) == 0 {
		return nil
	}

	// The author's ranges come from 200-word windows; a shorter text reads
	// more even than any window would, so it is judged in a set instead.
	s := MeasureShape(strings.Join(in.source, "\n"), in.spec.Voice)
	if s.Words < voiceWindowWords {
		return nil
	}

	var past []string

	for _, f := range uniformFeatures {
		r, ok := ranges[f.name]
		if !ok {
			continue
		}

		v, ok := readRangeFeature(s, f.name)
		if !ok || (f.lower && v >= r.P5) || (!f.lower && v <= r.P95) {
			continue
		}

		bound, side := r.P5, "under"
		if !f.lower {
			bound, side = r.P95, "over"
		}

		past = append(past, fmt.Sprintf("%s (%s is %s; 95%% of your own writing is not %s %s)", f.even, f.label,
			formatValue(v, 2), side, formatValue(bound, 2)))
	}

	if len(past) < minUniformFeatures {
		return nil
	}

	return []Finding{{Message: "This reads more even than your own writing does: " + strings.Join(past, "; ") +
		". Keep the specifics and lengths the text had; do not add variation for its own sake."}}
}
