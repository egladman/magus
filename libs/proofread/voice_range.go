package proofread

import (
	"fmt"
	"slices"
)

// minRangeWindows is the fewest windows with data for a feature that a
// [Range] is taken from. Under it the feature has no range, and a rule
// reading it keeps its default.
const minRangeWindows = 5

// Range is an author's 5th and 95th percentile of one feature over the
// windows [BuildVoice] pools, and how many windows had data for it.
type Range struct {
	P5      float64 `json:"p5"`
	P95     float64 `json:"p95"`
	Windows int     `json:"windows"`
}

// rangeFeature is one feature a voice keeps a [Range] of.
type rangeFeature struct {
	name Feature
	read func(Shape) (float64, bool)
}

// rangeFeatures are the features a budget or the uniform rule reads, the
// ones the paired calibration in ADR 0037 kept.
var rangeFeatures = []rangeFeature{
	{"triads_per_1000", func(s Shape) (float64, bool) { return s.Triads, s.Words > 0 }},
	{"grammar_per_1000.participial_clause", grammarRate("participial_clause")},
	{"item_words_sd", withheldUnless("item_words_sd", func(s Shape) float64 { return s.ItemWordsStdDev })},
	{"item_opener_share", withheldUnless("item_opener_share", func(s Shape) float64 { return s.ItemOpenerShare })},
	{"opener_class_entropy", withheldUnless("opener_class_entropy", func(s Shape) float64 { return s.OpenerClassEntropy })},
}

func grammarRate(key string) func(Shape) (float64, bool) {
	return func(s Shape) (float64, bool) {
		v, ok := s.Grammar[key]

		return v, ok
	}
}

func withheldUnless(name string, value func(Shape) float64) func(Shape) (float64, bool) {
	return func(s Shape) (float64, bool) { return value(s), !slices.Contains(s.Withheld, name) }
}

// readRangeFeature reads the named range feature from s.
func readRangeFeature(s Shape, name Feature) (float64, bool) {
	i := slices.IndexFunc(rangeFeatures, func(f rangeFeature) bool { return f.name == name })
	if i < 0 {
		return 0, false
	}

	return rangeFeatures[i].read(s)
}

// buildRanges pools texts into windows as voice-drift does. It returns each
// range feature's percentiles over the windows with data for it, and nil
// when no feature has [minRangeWindows].
func buildRanges(kind Kind, texts []string) map[Feature]Range {
	values := map[Feature][]float64{}

	for _, s := range MeasureWindowShapes(texts, kind, voiceWindowWords, VoiceSeed) {
		for _, f := range rangeFeatures {
			if v, ok := f.read(s); ok {
				values[f.name] = append(values[f.name], v)
			}
		}
	}

	var out map[Feature]Range

	for name, vs := range values {
		if len(vs) < minRangeWindows {
			continue
		}

		if out == nil {
			out = map[Feature]Range{}
		}

		slices.Sort(vs)
		out[name] = Range{P5: round4(percentile(vs, 0.05)), P95: round4(percentile(vs, 0.95)), Windows: len(vs)}
	}

	return out
}

func checkRanges(kind Kind, ranges map[Feature]Range) error {
	for name, r := range ranges {
		switch {
		case !slices.ContainsFunc(rangeFeatures, func(f rangeFeature) bool { return f.name == name }):
			return fmt.Errorf("%s: no range is kept of feature %q", kind, name)
		case r.Windows < minRangeWindows:
			return fmt.Errorf("%s: range of %s comes from %d windows, want %d or more", kind, name, r.Windows, minRangeWindows)
		case r.P5 > r.P95:
			return fmt.Errorf("%s: range of %s has p5 %v over p95 %v", kind, name, r.P5, r.P95)
		}
	}

	return nil
}
