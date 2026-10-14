package proofread

import (
	"math"
	"slices"
)

// WilsonLower is the lower end of the Wilson score interval on a proportion
// of correct successes in n trials, at the normal quantile z (1.96 for a
// two-sided 95% interval). It is 0 when n is 0, and it panics when correct is
// negative or exceeds n.
func WilsonLower(correct, n int, z float64) float64 {
	if correct < 0 || correct > n {
		panic("proofread: WilsonLower needs 0 <= correct <= n")
	}

	if n == 0 {
		return 0
	}

	return wilson(float64(correct)/float64(n), n, z)
}

// wilson is the lower end of the Wilson score interval on a proportion p
// observed over n > 0 trials, at the normal quantile z.
func wilson(p float64, n int, z float64) float64 {
	nf := float64(n)
	z2 := z * z
	center := p + z2/(2*nf)
	margin := z * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf))

	// The bound is never below 0; float rounding can put it a hair under at p = 0.
	return max(0, (center-margin)/(1+z2/nf))
}

// NominalAlpha is Krippendorff's alpha for nominal values. Each unit holds the
// values its coders gave it, a missing value left out. A unit with fewer than
// two values is not pairable and is skipped. It is false when alpha is
// undefined: fewer than two pairable values, or a single value throughout,
// where expected disagreement is 0.
func NominalAlpha(units [][]string) (float64, bool) {
	// The coincidence matrix as Krippendorff (2011) builds it: each ordered pair
	// of values within a unit adds 1/(m-1), m the unit's value count.
	coincide := map[[2]string]float64{}
	marginal := map[string]float64{}
	total := 0.0

	for _, u := range units {
		m := len(u)
		if m < 2 {
			continue
		}

		w := 1 / float64(m-1)

		for i, a := range u {
			for j, b := range u {
				if i != j {
					coincide[[2]string{a, b}] += w
				}
			}

			marginal[a]++
		}

		total += float64(m)
	}

	if total < 2 || len(marginal) < 2 {
		return 0, false
	}

	values := make([]string, 0, len(marginal))
	for v := range marginal {
		values = append(values, v)
	}

	slices.Sort(values)

	observed, expected := 0.0, 0.0

	for _, a := range values {
		for _, b := range values {
			if a == b {
				continue
			}

			observed += coincide[[2]string{a, b}]
			expected += marginal[a] * marginal[b]
		}
	}

	return 1 - (total-1)*observed/expected, true
}
