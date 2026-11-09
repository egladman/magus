package calibrate

import (
	"slices"
	"testing"

	"github.com/egladman/magus/libs/proofread"
)

func pairedShapes(n int, owner, rewrite func(i int) float64) ([]proofread.Shape, []proofread.Shape) {
	var o, x []proofread.Shape

	for i := range n {
		o = append(o, proofread.Shape{Words: 100, Triads: owner(i)})
		x = append(x, proofread.Shape{Words: 100, Triads: rewrite(i)})
	}

	return o, x
}

func triads(seps []PairedSeparation) (PairedSeparation, bool) {
	i := slices.IndexFunc(seps, func(s PairedSeparation) bool { return s.Feature == "triads_per_1000" })
	if i < 0 {
		return PairedSeparation{}, false
	}

	return seps[i], true
}

func TestSeparatePairsFindsAFeatureEveryRewriteRaises(t *testing.T) {
	o, x := pairedShapes(40, func(i int) float64 { return float64(i % 10) }, func(i int) float64 { return 20 + float64(i%10) })

	s, ok := triads(SeparatePairs(o, x))
	if !ok || s.Higher != 1 || s.Cut != 9 || s.Detected != 1 || s.FalseAlarms != 0 || !s.Separates {
		t.Errorf("triads = %+v, %v, want every rewrite higher and past the cut", s, ok)
	}
}

func TestSeparatePairsCutsLowWhenRewritesLower(t *testing.T) {
	o, x := pairedShapes(40, func(i int) float64 { return 10 + float64(i%10) }, func(int) float64 { return 0 })

	s, _ := triads(SeparatePairs(o, x))
	if s.Higher != 0 || s.Cut != 10 || s.Detected != 1 || !s.Separates {
		t.Errorf("triads = %+v, want the cut at the owners' low end", s)
	}
}

func TestSeparatePairsFindsNoSeparationInUnchangedPairs(t *testing.T) {
	o, x := pairedShapes(40, func(i int) float64 { return float64(i) }, func(i int) float64 { return float64(i) })

	s, _ := triads(SeparatePairs(o, x))
	if s.Higher != 0.5 || s.Detected > s.FalseAlarms || s.Separates {
		t.Errorf("triads = %+v, want no movement", s)
	}
}

func TestSeparatePairsSeparatesNothingUnderTwentyPairs(t *testing.T) {
	o, x := pairedShapes(MinCutPairs-1, func(int) float64 { return 0 }, func(int) float64 { return 50 })

	if s, _ := triads(SeparatePairs(o, x)); s.Separates {
		t.Errorf("triads = %+v, want no separation on %d pairs", s, MinCutPairs-1)
	}
}

func TestSeparatePairsSkipsAPairEitherSideWithholds(t *testing.T) {
	o, x := pairedShapes(6, func(int) float64 { return 0 }, func(int) float64 { return 1 })
	x[0].Withheld = []string{"triads_per_1000"}

	if s, _ := triads(SeparatePairs(o, x)); s.Pairs != 5 {
		t.Errorf("triads pairs = %d, want 5", s.Pairs)
	}
}

func TestSeparatePairsPanicsOnUnevenSides(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("SeparatePairs accepted two owner texts and one rewrite")
		}
	}()

	SeparatePairs(make([]proofread.Shape, 2), make([]proofread.Shape, 1))
}
