package rankeval

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reference values come from outside the code under test:
//   - exact p-values and kappas from exact rational arithmetic (Python
//     fractions.Fraction) over every sign pattern or label pair;
//   - t quantiles and t tail areas from Simpson quadrature of the Student t
//     density with bisection (Python, math.lgamma), checked against the closed
//     forms at df 1 and 2;
//   - the sleep interval and p-value against R's published t.test(extra ~ group,
//     data = sleep, paired = TRUE) output;
//   - topic set sizes from Sakai's normal approximation and, independently, the
//     exact noncentral t power by quadrature over the chi-square density; both
//     give the same n in every case pinned.

// Twelve paired scores in multiples of 1/8, so every sign-flipped sum is exact
// in float64 and ties are real ties. Differences: 1/4 -1/8 3/8 1/8 -1/8 3/8 1/4
// 0 3/8 -1/8 1/4 1/4, mean 5/32.
var (
	twelveA = []float64{0.75, 0.5, 0.875, 0.625, 0.25, 1.0, 0.5, 0.375, 0.875, 0.625, 0.5, 0.75}
	twelveB = []float64{0.5, 0.625, 0.5, 0.5, 0.375, 0.625, 0.25, 0.375, 0.5, 0.75, 0.25, 0.5}
)

// Student's sleep data (Cushny and Peebles; R datasets::sleep): extra hours of
// sleep for ten patients under each of two drugs.
var (
	sleepDrug1 = []float64{0.7, -1.6, -0.2, -1.2, -0.1, 3.4, 3.7, 0.8, 0.0, 2.0}
	sleepDrug2 = []float64{1.9, 0.8, 1.1, 0.1, -0.1, 4.4, 5.5, 1.6, 4.6, 3.4}
)

// exactSignFlipP enumerates all 2^len(sums) sign patterns, the identity among them.
func exactSignFlipP(sums []float64) float64 {
	var observed float64
	for _, s := range sums {
		observed += s
	}
	hits := 0
	for mask := range 1 << len(sums) {
		var sum float64
		for i, s := range sums {
			if mask>>i&1 == 1 {
				sum -= s
			} else {
				sum += s
			}
		}
		if math.Abs(sum) >= math.Abs(observed) {
			hits++
		}
	}
	return float64(hits) / float64(int(1)<<len(sums))
}

func TestPairedRandomizationMatchesExactEnumeration(t *testing.T) {
	d := make([]float64, len(twelveA))
	for i := range d {
		d[i] = twelveA[i] - twelveB[i]
	}
	exact := exactSignFlipP(d)
	require.Equal(t, 152.0/4096, exact, "Python Fraction enumeration: 152 of 4096 patterns")

	for _, seed := range []uint64{1, 2, 3} {
		mean, p := pairedRandomization(twelveA, twelveB, 200_000, seed)
		assert.Equal(t, 5.0/32, mean)
		assert.InDelta(t, exact, p, 0.005, "seed %d", seed)
	}
}

func TestPairedRandomizationSleep(t *testing.T) {
	mean, p := pairedRandomization(sleepDrug1, sleepDrug2, 200_000, 7)
	assert.InDelta(t, -1.58, mean, 1e-12)
	// Only the all-negative and all-positive patterns reach |sum| = 15.8, each
	// twice over the zero difference: 4 of 1024.
	assert.InDelta(t, 4.0/1024, p, 0.005)
}

func TestPairedRandomizationDeterministic(t *testing.T) {
	_, p1 := pairedRandomization(twelveA, twelveB, 10_000, 42)
	_, p2 := pairedRandomization(twelveA, twelveB, 10_000, 42)
	_, p3 := pairedRandomization(twelveA, twelveB, 10_000, 43)
	assert.Equal(t, p1, p2)
	assert.NotEqual(t, p1, p3)

	same := []float64{0.3, 0.6, 0.9}
	mean, p := pairedRandomization(same, same, 1_000, 1)
	assert.Zero(t, mean)
	assert.Equal(t, 1.0, p, "every flip of all-zero differences ties the observed 0")

	// A shift so large no flip but the identity and its negation reaches it still
	// gets p >= 1/(samples+1), never 0.
	_, p = pairedRandomization([]float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, make([]float64, 20), 1_000, 1)
	assert.Equal(t, 1.0/1001, p)
}

func TestPairedRandomizationClustered(t *testing.T) {
	clusters := []string{"s1", "s1", "s2", "s3", "s3", "s3", "s4", "s5", "s5", "s6", "s7", "s7"}
	sums := map[string]float64{}
	var order []string
	for i, c := range clusters {
		if _, ok := sums[c]; !ok {
			order = append(order, c)
		}
		sums[c] += twelveA[i] - twelveB[i]
	}
	flat := make([]float64, len(order))
	for i, c := range order {
		flat[i] = sums[c]
	}
	exact := exactSignFlipP(flat)
	require.Equal(t, 6.0/128, exact, "Python Fraction enumeration over 7 clusters: 6 of 128")

	mean, p := clusteredRandomization(twelveA, twelveB, clusters, 200_000, 1)
	assert.Equal(t, 5.0/32, mean, "the mean stays over pairs")
	assert.InDelta(t, exact, p, 0.005)

	singletons := make([]string, len(twelveA))
	for i := range singletons {
		singletons[i] = string(rune('a' + i))
	}
	_, pc := clusteredRandomization(twelveA, twelveB, singletons, 50_000, 9)
	_, pp := pairedRandomization(twelveA, twelveB, 50_000, 9)
	assert.Equal(t, pp, pc, "one pair per cluster is the paired test")
}

func TestPairedRandomizationPanics(t *testing.T) {
	assert.Panics(t, func() { pairedRandomization([]float64{1}, []float64{1, 2}, 10, 1) })
	assert.Panics(t, func() { pairedRandomization(nil, nil, 10, 1) })
	assert.Panics(t, func() { pairedRandomization([]float64{math.NaN()}, []float64{1}, 10, 1) })
	assert.Panics(t, func() { pairedRandomization([]float64{1}, []float64{math.Inf(1)}, 10, 1) })
	assert.Panics(t, func() { pairedRandomization([]float64{1}, []float64{0}, 0, 1) })
	assert.Panics(t, func() { clusteredRandomization([]float64{1, 2}, []float64{0, 0}, []string{"a"}, 10, 1) })
}

func TestPairedTIntervalSleep(t *testing.T) {
	// R prints mean difference -1.58 and 95 percent CI -2.4598858 -0.7001142; the
	// literals are the quadrature t(0.975, 9) = 2.262157162798248 applied to
	// sd = sqrt(1702/1125).
	mean, lo, hi := pairedTInterval(sleepDrug1, sleepDrug2, 0.95)
	assert.InDelta(t, -1.58, mean, 1e-12)
	assert.InDelta(t, -2.4598857632769993, lo, 1e-9)
	assert.InDelta(t, -0.7001142367230009, hi, 1e-9)

	// t(0.95, 9) = 1.8331129326562747 by quadrature; se = sqrt(1702/1125/10).
	se := math.Sqrt(1702.0 / 1125 / 10)
	_, lo90, hi90 := pairedTInterval(sleepDrug1, sleepDrug2, 0.90)
	assert.InDelta(t, -1.58-1.8331129326562747*se, lo90, 1e-9)
	assert.InDelta(t, -1.58+1.8331129326562747*se, hi90, 1e-9)

	// Cohen's d_z = -1.58 / sqrt(1702/1125).
	assert.InDelta(t, -1.2845575625910546, effectSize(sleepDrug1, sleepDrug2), 1e-12)

	assert.Panics(t, func() { pairedTInterval([]float64{1}, []float64{0}, 0.95) })
	assert.Panics(t, func() { pairedTInterval(sleepDrug1, sleepDrug2, 1) })
	assert.Panics(t, func() { effectSize([]float64{1}, []float64{0}) })
}

func TestPairedTIntervalStudentT(t *testing.T) {
	// Closed forms: df 1 is Cauchy, t = tan(pi(p-1/2)); df 2 is t = (2p-1)/sqrt(2p(1-p)).
	assert.InDelta(t, math.Tan(math.Pi*0.475), studentTQuantile(0.025, 1), 1e-10)
	assert.InDelta(t, 0.95/math.Sqrt(2*0.975*0.025), studentTQuantile(0.025, 2), 1e-12)
	for df, want := range map[float64]float64{
		9:   2.262157162798248,
		11:  2.2009851600915926,
		30:  2.0422724563012595,
		100: 1.9839715185235733,
	} {
		assert.InDelta(t, want, studentTQuantile(0.025, df), 1e-9, "df %v", df)
		assert.InDelta(t, -want, studentTQuantile(0.975, df), 1e-9, "df %v", df)
	}
	// R reports t = -4.0621, p-value = 0.002833 for the paired sleep test.
	assert.InDelta(t, 0.002833, 2*studentTUpper(4.062127683382037, 9), 5e-7)
	assert.Equal(t, 0.5, studentTUpper(0, 7))
}

func TestNonInferior(t *testing.T) {
	// Drug 2 against drug 1: mean difference +1.58. The one-sided p-values are
	// quadrature t tail areas at (1.58 + margin) / se with 9 df.
	lower, p := nonInferior(sleepDrug2, sleepDrug1, 0.5)
	assert.InDelta(t, 0.7001142367230009, lower, 1e-9)
	assert.InDelta(t, 0.00023190270447970285, p, 1e-9)
	_, p = nonInferior(sleepDrug2, sleepDrug1, 0)
	assert.InDelta(t, 0.001416445098695096, p, 1e-9, "half R's two-sided 0.002833")

	// The interval rule and the test agree at every margin: lower > -margin
	// exactly when p < 0.025.
	for _, margin := range []float64{0, 0.5, 2, 2.45, 2.47, 3} {
		lower, p := nonInferior(sleepDrug1, sleepDrug2, margin)
		assert.Equal(t, lower > -margin, p < 0.025, "margin %v", margin)
	}
	_, p = nonInferior(sleepDrug1, sleepDrug2, 2.4598857632769993)
	assert.InDelta(t, 0.025, p, 1e-9, "the margin at the interval's lower end")

	assert.Panics(t, func() { nonInferior(sleepDrug1, sleepDrug2, -0.1) })
	assert.Panics(t, func() { nonInferior(sleepDrug1, sleepDrug2, math.NaN()) })
}

// holmRejects is Holm's procedure as published: sort ascending, reject while
// p_(i) <= alpha/(m-i+1) (1-based i), stop at the first acceptance.
func holmRejects(ps []float64, alpha float64) []bool {
	order := make([]int, len(ps))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(i, j int) int {
		switch {
		case ps[i] < ps[j]:
			return -1
		case ps[i] > ps[j]:
			return 1
		}
		return 0
	})
	rejected := make([]bool, len(ps))
	for k, i := range order {
		if ps[i] > alpha/float64(len(ps)-k) {
			break
		}
		rejected[i] = true
	}
	return rejected
}

func TestHolm(t *testing.T) {
	// Sorted 0.005 0.01 0.03 0.04 times 4 3 2 1: 0.02 0.03 0.06 0.04, then the
	// running max lifts 0.04 to 0.06.
	assert.InDeltaSlice(t, []float64{0.03, 0.06, 0.06, 0.02}, holm([]float64{0.01, 0.04, 0.03, 0.005}), 1e-15)
	assert.Equal(t, []float64{1, 1}, holm([]float64{0.5, 0.6}))
	assert.Equal(t, []float64{0.2}, holm([]float64{0.2}))
	assert.InDeltaSlice(t, []float64{0.06, 0.06, 0.5}, holm([]float64{0.02, 0.02, 0.5}), 1e-15, "tied p-values")
	assert.Empty(t, holm(nil))

	rng := rand.New(rand.NewPCG(5, 0))
	for range 500 {
		ps := make([]float64, 1+rng.IntN(8))
		for i := range ps {
			ps[i] = rng.Float64() * rng.Float64() * rng.Float64()
		}
		adjusted := holm(ps)
		for _, alpha := range []float64{0.01, 0.05, 0.1} {
			want := holmRejects(ps, alpha)
			for i := range ps {
				assert.Equal(t, want[i], adjusted[i] <= alpha, "ps %v alpha %v", ps, alpha)
			}
		}
	}

	assert.Panics(t, func() { holm([]float64{1.2}) })
	assert.Panics(t, func() { holm([]float64{math.NaN()}) })
}

func TestTopicSetSize(t *testing.T) {
	cases := []struct {
		name                    string
		sd, delta, alpha, power float64
		want                    int
	}{
		// minDt = 0.5 at alpha .05, beta .20: Sakai's estimate 33.3 rounds up to 34,
		// whose exact noncentral t power is 0.8077775 (G*Power prints the same) and
		// n = 33's is below 0.80.
		{"minDt 0.5", 1, 0.5, 0.05, 0.8, 34},
		// The holdout sizing in the fix plan, with the t correction its normal-only
		// formula leaves out (155 and 242 there).
		{"sd .20 delta .045", 0.20, 0.045, 0.05, 0.8, 157},
		{"sd .25 delta .045", 0.25, 0.045, 0.05, 0.8, 245},
		{"sd .25 delta .05", 0.25, 0.05, 0.05, 0.8, 199},
		{"alpha .01 power .9", 1, 0.2, 0.01, 0.9, 376},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, topicSetSize(c.sd, c.delta, c.alpha, c.power), c.name)
	}
	assert.Equal(t, 2, topicSetSize(0.01, 1, 0.05, 0.8), "a huge effect still needs 2 topics")

	assert.Panics(t, func() { topicSetSize(0, 0.05, 0.05, 0.8) })
	assert.Panics(t, func() { topicSetSize(0.2, math.Inf(1), 0.05, 0.8) })
	assert.Panics(t, func() { topicSetSize(0.2, 0.05, 0, 0.8) })
	assert.Panics(t, func() { topicSetSize(0.2, 0.05, 0.05, 1) })
}

func TestKappa(t *testing.T) {
	// 50 items: yes/yes 20, yes/no 5, no/yes 10, no/no 15. p_o = 0.7, p_e = 0.5.
	var a, b []int
	for _, cell := range []struct{ x, y, n int }{{1, 1, 20}, {1, 0, 5}, {0, 1, 10}, {0, 0, 15}} {
		for range cell.n {
			a = append(a, cell.x)
			b = append(b, cell.y)
		}
	}
	assert.InDelta(t, 0.4, cohenKappa(a, b), 1e-15)
	assert.InDelta(t, 0.4, weightedKappa(a, b), 1e-15, "two grades: linear weights are the unweighted ones")

	// Grades 0-2, Fraction arithmetic: kappa 3/10, linear-weighted 6/13.
	ga := []int{2, 2, 1, 0, 1, 2, 0, 0, 1, 2, 1, 0, 2, 1, 0}
	gb := []int{2, 1, 1, 0, 2, 2, 0, 1, 1, 1, 0, 0, 2, 2, 1}
	assert.InDelta(t, 0.3, cohenKappa(ga, gb), 1e-15)
	assert.InDelta(t, 6.0/13, weightedKappa(ga, gb), 1e-15)

	// Weights are scale-free: grades 0 and 2 alone weigh as one step.
	assert.InDelta(t, 8.0/13, weightedKappa([]int{0, 2, 2, 0, 2}, []int{0, 2, 0, 0, 2}), 1e-15)
	assert.InDelta(t, 8.0/13, cohenKappa([]int{0, 2, 2, 0, 2}, []int{0, 2, 0, 0, 2}), 1e-15)

	assert.Equal(t, 1.0, cohenKappa(ga, ga))
	assert.Equal(t, 1.0, weightedKappa(ga, ga))
	assert.True(t, math.IsNaN(cohenKappa([]int{1, 1}, []int{1, 1})), "p_e = 1 leaves kappa undefined")

	assert.Panics(t, func() { cohenKappa([]int{1}, []int{1, 2}) })
	assert.Panics(t, func() { weightedKappa(nil, nil) })
}
