package proofread

import (
	"cmp"
	"math"
	"slices"
	"testing"
)

func shapes(n int, build func(i int) Shape) []Shape {
	out := make([]Shape, n)
	for i := range out {
		out[i] = build(i)
	}

	return out
}

func separationNamed(t *testing.T, seps []Separation, feature Feature) Separation {
	t.Helper()

	i := slices.IndexFunc(seps, func(s Separation) bool { return s.Feature == feature })
	if i < 0 {
		t.Fatalf("no separation for %s in %+v", feature, seps)
	}

	return seps[i]
}

func TestSeparateScoresPerfectSeparation(t *testing.T) {
	high := shapes(5, func(i int) Shape { return Shape{Words: 300, MATTR: 0.7 + float64(i)/100} })
	low := shapes(6, func(i int) Shape { return Shape{Words: 300, MATTR: 0.5 + float64(i)/100} })

	if got := separationNamed(t, Separate(high, low), "mattr"); got != (Separation{"mattr", 1, 5, 6}) {
		t.Errorf("owner higher: %+v", got)
	}

	if got := separationNamed(t, Separate(low, high), "mattr"); got != (Separation{"mattr", 0, 6, 5}) {
		t.Errorf("owner lower: %+v", got)
	}
}

func TestSeparateScoresIdenticalSetsAtHalf(t *testing.T) {
	set := shapes(5, func(i int) Shape {
		return Shape{MATTR: float64(i), Triads: float64(5 - i), Punctuation: map[string]float64{"comma": float64(i % 2)}}
	})

	seps := Separate(set, set)
	if len(seps) == 0 {
		t.Fatal("no separations")
	}

	for _, s := range seps {
		if s.AUC != 0.5 {
			t.Errorf("%s: AUC %v, want 0.5", s.Feature, s.AUC)
		}
	}
}

func TestSeparateCountsTiesHalf(t *testing.T) {
	owner := shapes(5, func(int) Shape { return Shape{Triads: 1} })
	other := shapes(5, func(i int) Shape { return Shape{Triads: float64(min(i, 1))} })

	// 5 wins over the one 0, and 20 ties at half each, of 25 pairs.
	if got := separationNamed(t, Separate(owner, other), "triads_per_1000"); got.AUC != 0.6 {
		t.Errorf("AUC = %v, want 0.6", got.AUC)
	}
}

func TestSeparateLeavesOutWithheldTexts(t *testing.T) {
	owner := shapes(7, func(i int) Shape {
		if i >= 5 {
			return Shape{Withheld: []string{"mattr", "function_words"}}
		}

		return Shape{MATTR: 0.9, FunctionWords: map[string]float64{"the": 60}}
	})
	other := shapes(5, func(int) Shape { return Shape{MATTR: 0.5, FunctionWords: map[string]float64{"the": 40}} })
	seps := Separate(owner, other)

	for _, feature := range []Feature{"mattr", "function_words.the"} {
		if got := separationNamed(t, seps, feature); got != (Separation{feature, 1, 5, 5}) {
			t.Errorf("%+v, want the withheld texts left out", got)
		}
	}

	if got := separationNamed(t, seps, "triads_per_1000"); got.OwnerTexts != 7 {
		t.Errorf("triads counted %d owner texts, want 7: withholding mattr withholds nothing else", got.OwnerTexts)
	}
}

func TestSeparateNeedsFiveTextsASide(t *testing.T) {
	five := shapes(5, func(int) Shape { return Shape{MATTR: 1} })
	four := shapes(4, func(int) Shape { return Shape{MATTR: 0} })

	if got := Separate(five, four); got == nil || len(got) != 0 {
		t.Errorf("4 other texts: %+v, want an empty result", got)
	}

	if got := Separate(four, five); got == nil || len(got) != 0 {
		t.Errorf("4 owner texts: %+v, want an empty result", got)
	}

	withheld := slices.Concat(four, []Shape{{Withheld: []string{"mattr"}}})
	if got := Separate(five, withheld); slices.ContainsFunc(got, func(s Separation) bool { return s.Feature == "mattr" }) {
		t.Errorf("mattr scored from 4 unwithheld texts: %+v", got)
	}
}

func TestSeparateFlattensMaps(t *testing.T) {
	owner := shapes(5, func(i int) Shape {
		return Shape{Punctuation: map[string]float64{"semicolon": 2, "comma": float64(i)}, Layout: map[string]float64{"bold": 1}}
	})
	other := shapes(5, func(i int) Shape {
		return Shape{Words: 10 * i, Sentences: i, Punctuation: map[string]float64{"comma": float64(i)}}
	})
	seps := Separate(owner, other)

	want := map[Feature]float64{"punctuation.semicolon": 1, "layout.bold": 1, "punctuation.comma": 0.5}
	for feature, auc := range want {
		if got := separationNamed(t, seps, feature); got.AUC != auc || got.OwnerTexts != 5 || got.OtherTexts != 5 {
			t.Errorf("%+v, want AUC %v over 5 and 5", got, auc)
		}
	}

	for _, s := range seps {
		switch s.Feature {
		case "words", "sentences", "withheld", "punctuation", "layout":
			t.Errorf("%s scored as a feature", s.Feature)
		}
	}
}

func TestSeparateSortsByDistanceFromHalfThenName(t *testing.T) {
	owner := shapes(5, func(i int) Shape { return Shape{MATTR: 0, Triads: 1, HapaxRatio: float64(i)} })
	other := shapes(5, func(i int) Shape { return Shape{MATTR: 1, Triads: 0, HapaxRatio: float64(i)} })
	seps := Separate(owner, other)

	if seps[0].Feature != "mattr" || seps[1].Feature != "triads_per_1000" {
		t.Errorf("first two = %+v, want mattr (AUC 0) then triads_per_1000 (AUC 1)", seps[:2])
	}

	if !slices.IsSortedFunc(seps, func(a, b Separation) int {
		return cmp.Or(cmp.Compare(math.Abs(b.AUC-0.5), math.Abs(a.AUC-0.5)), cmp.Compare(a.Feature, b.Feature))
	}) {
		t.Errorf("not sorted: %+v", seps)
	}
}
