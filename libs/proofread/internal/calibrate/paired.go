package calibrate

import (
	"cmp"
	"math"
	"slices"

	"github.com/egladman/magus/libs/proofread"
)

// PairedFalseAlarmPercent is the share of owner texts a [PairedSeparation]'s
// cut lets past, the rate voice-drift holds to.
const PairedFalseAlarmPercent = 5

// MinCutPairs is the fewest pairs a cut at [PairedFalseAlarmPercent] can sit
// below the owners' maximum on; under it no feature separates.
const MinCutPairs = 100 / PairedFalseAlarmPercent

// PairedSeparation is how one [proofread.Shape] feature moves from an owner's
// texts to rewrites of the same texts.
type PairedSeparation struct {
	Feature proofread.Feature `json:"feature"`
	// Pairs is the pairs both sides of which count toward the feature.
	Pairs int `json:"pairs"`
	// Higher is the share of pairs whose rewrite measures higher than its
	// original, ties counted half: 0.5 is no movement.
	Higher float64 `json:"higher"`
	// Cut is the owner value no more than [PairedFalseAlarmPercent] of the
	// owner texts pass in the direction the rewrites moved.
	Cut float64 `json:"cut"`
	// FalseAlarms and Detected are the shares of owner texts and of rewrites
	// past the cut.
	FalseAlarms float64 `json:"false_alarms"`
	Detected    float64 `json:"detected"`
	// Separates reports whether Pairs reach [MinCutPairs] and the 95% Wilson
	// lower bound on Detected is above FalseAlarms and the 5% rate.
	Separates bool `json:"separates"`
}

// SeparatePairs scores each feature by how it moves from owner[i] to
// other[i], one text before and after a rewrite. Features are the ones
// [Separate] reads. A pair counts unless either side withholds the feature,
// and a feature on fewer than [MinSeparationTexts] pairs is left out.
// Features that separate sort first. It panics when the lengths differ.
func SeparatePairs(owner, other []proofread.Shape) []PairedSeparation {
	if len(owner) != len(other) {
		panic("calibrate: SeparatePairs needs one rewrite per owner text")
	}

	names := map[string]string{}
	out := []PairedSeparation{}

	var ownerTexts, otherTexts []flatShape

	for i := range owner {
		o, x := flattenShapes(owner[i:i+1], names), flattenShapes(other[i:i+1], names)
		if len(o) == 1 && len(x) == 1 {
			ownerTexts, otherTexts = append(ownerTexts, o[0]), append(otherTexts, x[0])
		}
	}

	for name, top := range names {
		var ov, xv []float64

		for i := range ownerTexts {
			o, x := ownerTexts[i], otherTexts[i]
			if o.withheld[top] || o.withheld[name] || x.withheld[top] || x.withheld[name] {
				continue
			}

			ov, xv = append(ov, o.values[name]), append(xv, x.values[name])
		}

		if len(ov) < MinSeparationTexts {
			continue
		}

		out = append(out, pairedSeparation(proofread.Feature(name), ov, xv))
	}

	slices.SortFunc(out, func(a, b PairedSeparation) int {
		return cmp.Or(compareBool(b.Separates, a.Separates), cmp.Compare(b.Detected, a.Detected),
			cmp.Compare(a.Feature, b.Feature))
	})

	return out
}

func pairedSeparation(feature proofread.Feature, ov, xv []float64) PairedSeparation {
	var higher float64

	for i := range ov {
		switch {
		case xv[i] > ov[i]:
			higher++
		case xv[i] == ov[i]:
			higher += 0.5
		}
	}

	s := PairedSeparation{Feature: feature, Pairs: len(ov), Higher: round3(higher / float64(len(ov)))}

	sorted := slices.Sorted(slices.Values(ov))
	allowed := len(sorted) * PairedFalseAlarmPercent / 100

	past := func(v float64) bool { return v < s.Cut }
	if s.Higher >= 0.5 {
		s.Cut, past = sorted[len(sorted)-1-allowed], func(v float64) bool { return v > s.Cut }
	} else {
		s.Cut = sorted[allowed]
	}

	alarms, detected := countPast(ov, past), countPast(xv, past)
	s.FalseAlarms = round3(float64(alarms) / float64(len(ov)))
	s.Detected = round3(float64(detected) / float64(len(xv)))
	s.Separates = len(ov) >= MinCutPairs &&
		WilsonLower(detected, len(xv), z95) > max(s.FalseAlarms, PairedFalseAlarmPercent/100.0)

	return s
}

func countPast(values []float64, past func(float64) bool) int {
	var n int

	for _, v := range values {
		if past(v) {
			n++
		}
	}

	return n
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }
