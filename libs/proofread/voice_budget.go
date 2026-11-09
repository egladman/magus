package proofread

import (
	"fmt"
	"math"
	"strings"
)

// budget is how many times a counting rule lets a text show its habit
// before it reports, and where that number came from.
type budget struct {
	allowed int
	// fromVoice reports whether the author's own texts set allowed.
	fromVoice bool
}

// voiceBudget scales the author's 95th percentile of feature, a rate per
// 1000 words, to in's words, rounded down. A voice never lowers a budget,
// so defaultAllowed stands with no range or a lower one.
func voiceBudget(in input, feature Feature, defaultAllowed int) budget {
	out := budget{allowed: defaultAllowed}

	if in.opts.voice == nil || in.spec.Voice == "" {
		return out
	}

	r, ok := in.opts.voice.Kinds[in.spec.Voice].Ranges[feature]
	if !ok {
		return out
	}

	words := Measure(strings.Join(in.source, "\n"), in.spec.Voice).Words

	if own := int(math.Floor(r.P95 * float64(words) / 1000)); own > defaultAllowed {
		out = budget{allowed: own, fromVoice: true}
	}

	return out
}

// note is the clause a finding adds to say a voice set its budget.
func (b budget) note() string {
	if !b.fromVoice {
		return ""
	}

	return fmt.Sprintf(" Your own texts of this length allow %d; this one has more.", b.allowed)
}
