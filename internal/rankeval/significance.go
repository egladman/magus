package rankeval

import (
	"cmp"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
)

// PairedRandomization is the two-sided paired randomization (sign-flip) test of
// a against b, one pair per query (Smucker, Allan and Carterette, CIKM 2007;
// Urbano, Lima and Hanjalic, SIGIR 2019). meanDelta is mean(a-b). Of samples
// random sign flips of the differences, the hits are those whose |sum| reaches
// the observed |sum|; p is (hits+1)/(samples+1), never 0 (Phipson and Smyth,
// SAGMB 2010). A PCG stream seeded with seed draws the flips, so a seed fixes p.
// It panics when a and b differ in length, are empty or hold a NaN or an
// infinity, or when samples < 1.
func PairedRandomization(a, b []float64, samples int, seed uint64) (meanDelta, p float64) {
	d := differences(a, b, 1)
	return signFlip(d, len(d), samples, seed)
}

// ClusteredRandomization is PairedRandomization with one sign per cluster:
// every pair sharing a cluster id, such as the queries of one session, flips
// together, so correlated rows do not count as independent evidence. meanDelta
// is still the mean over pairs. It panics as PairedRandomization does, and when
// clusters is not as long as a.
func ClusteredRandomization(a, b []float64, clusters []string, samples int, seed uint64) (meanDelta, p float64) {
	d := differences(a, b, 1)
	if len(clusters) != len(d) {
		panic(fmt.Sprintf("%d cluster ids for %d pairs", len(clusters), len(d)))
	}
	index := make(map[string]int)
	var sums []float64
	for i, c := range clusters {
		j, ok := index[c]
		if !ok {
			j = len(sums)
			index[c] = j
			sums = append(sums, 0)
		}
		sums[j] += d[i]
	}
	return signFlip(sums, len(d), samples, seed)
}

// signFlip tests whether the cluster sums are symmetric about 0. Under that null
// hypothesis each sum is as likely negated, so the 2^len(sums) sign patterns
// are equally likely; samples of them are drawn, one PCG bit per sum.
func signFlip(sums []float64, pairs, samples int, seed uint64) (meanDelta, p float64) {
	if samples < 1 {
		panic(fmt.Sprintf("%d randomization samples", samples))
	}
	var observed, scale float64
	for _, s := range sums {
		observed += s
		scale += math.Abs(s)
	}
	// A flipped sum equal to the observed one in exact arithmetic can land an ulp
	// below it; without the tolerance that tie would not count.
	threshold := math.Abs(observed) - 1e-9*scale
	src := rand.NewPCG(seed, 0)
	hits := 0
	for range samples {
		var sum float64
		var bits uint64
		for i, s := range sums {
			if i%64 == 0 {
				bits = src.Uint64()
			}
			if bits&1 == 1 {
				sum -= s
			} else {
				sum += s
			}
			bits >>= 1
		}
		if math.Abs(sum) >= threshold {
			hits++
		}
	}
	return observed / float64(pairs), float64(hits+1) / float64(samples+1)
}

// PairedTInterval is the Student t confidence interval for mean(a-b) at the
// given confidence, such as 0.95: mean +- t(1-(1-confidence)/2, n-1) * sd/sqrt(n),
// with sd the sample standard deviation of the differences (Student 1908). It
// panics on fewer than 2 pairs, on input PairedRandomization rejects, and on a
// confidence outside (0, 1).
func PairedTInterval(a, b []float64, confidence float64) (mean, lo, hi float64) {
	if math.IsNaN(confidence) || confidence <= 0 || confidence >= 1 {
		panic(fmt.Sprintf("confidence %v is outside (0, 1)", confidence))
	}
	d := differences(a, b, 2)
	mean, sd := meanSD(d)
	half := studentTQuantile((1-confidence)/2, float64(len(d)-1)) * sd / math.Sqrt(float64(len(d)))
	return mean, mean - half, mean + half
}

// EffectSize is the standardized mean difference of paired scores, mean(a-b) /
// sd(a-b), Cohen's d_z (Cohen 1988), with sd the sample standard deviation. It is
// +-Inf when every difference is the same nonzero value and NaN when all are 0.
// It panics as PairedTInterval does.
func EffectSize(a, b []float64) float64 {
	mean, sd := meanSD(differences(a, b, 2))
	return mean / sd
}

// NonInferior tests H0: mean(a-b) <= -margin against H1: mean(a-b) > -margin
// with a one-sided paired t-test. lower is the lower end of the 95%
// PairedTInterval and p the test's p-value; lower > -margin exactly when p <
// 0.025, the convention that the lower bound of the two-sided 95% interval
// clears the margin (ICH E9, 1998). Pass the p of several guards through Holm.
// It panics as PairedTInterval does, and on a negative or non-finite margin.
func NonInferior(a, b []float64, margin float64) (lower, p float64) {
	if math.IsNaN(margin) || margin < 0 || math.IsInf(margin, 1) {
		panic(fmt.Sprintf("non-inferiority margin %v is not a finite value >= 0", margin))
	}
	d := differences(a, b, 2)
	mean, sd := meanSD(d)
	se := sd / math.Sqrt(float64(len(d)))
	df := float64(len(d) - 1)
	return mean - studentTQuantile(0.025, df)*se, studentTUpper((mean+margin)/se, df)
}

// Holm returns Holm's step-down adjusted p-values (Holm, Scand. J. Statistics
// 1979), in the order of ps: sorted ascending, the i-th smallest (0-based) of m
// becomes min(1, (m-i) * p), raised to the largest adjusted value before it.
// Rejecting every hypothesis whose adjusted p is <= alpha controls the
// family-wise error rate at alpha (Carterette, TOIS 2012, for IR). It panics on
// a p outside [0, 1].
func Holm(ps []float64) []float64 {
	for _, p := range ps {
		if math.IsNaN(p) || p < 0 || p > 1 {
			panic(fmt.Sprintf("p-value %v is outside [0, 1]", p))
		}
	}
	order := make([]int, len(ps))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(i, j int) int { return cmp.Compare(ps[i], ps[j]) })
	adjusted := make([]float64, len(ps))
	var running float64
	for rank, i := range order {
		running = max(running, min(1, float64(len(ps)-rank)*ps[i]))
		adjusted[i] = running
	}
	return adjusted
}

// TopicSetSize is the number of topics a paired t-test at two-sided level alpha
// needs to detect a true mean difference delta with the given power, when the
// differences have standard deviation sd (Sakai, "Topic set size design",
// Information Retrieval Journal 2016, after Nagata 2003). It is the smallest
// n >= 2 whose power, by the normal approximation to the noncentral t Sakai
// uses, reaches power; the search starts at his estimate
// ((z_{alpha/2} + z_{1-power}) / (delta/sd))^2 + z_{alpha/2}^2/2, z_P the upper
// P point of the standard normal. It panics unless
// sd and delta are finite and > 0 and alpha and power lie in (0, 1).
func TopicSetSize(sd, delta, alpha, power float64) int {
	for _, v := range []float64{sd, delta} {
		if math.IsNaN(v) || v <= 0 || math.IsInf(v, 1) {
			panic(fmt.Sprintf("topic set size needs a finite sd and delta > 0, got %v and %v", sd, delta))
		}
	}
	for _, v := range []float64{alpha, power} {
		if math.IsNaN(v) || v <= 0 || v >= 1 {
			panic(fmt.Sprintf("topic set size needs alpha and power in (0, 1), got %v and %v", alpha, power))
		}
	}
	effect := delta / sd
	za := normalQuantile(1 - alpha/2)
	start := math.Pow((za+normalQuantile(power))/effect, 2) + za*za/2
	n := max(2, int(math.Ceil(start)))
	for pairedTPower(n, effect, alpha) < power {
		n++
	}
	for n > 2 && pairedTPower(n-1, effect, alpha) >= power {
		n--
	}
	return n
}

// pairedTPower is the two-sided power at n topics and standardized effect
// delta/sd, approximating the noncentral t with noncentrality sqrt(n)*effect by
// `P(T' <= t) ~ Phi((t(1-1/4f) - lambda) / sqrt(1 + t^2/2f))`, `f = n-1` (Sakai 2016,
// eq. for the paired t-test).
func pairedTPower(n int, effect, alpha float64) float64 {
	f := float64(n - 1)
	lambda := math.Sqrt(float64(n)) * effect
	t := studentTQuantile(alpha/2, f)
	scale := math.Sqrt(1 + t*t/(2*f))
	shrink := 1 - 1/(4*f)
	return 1 - normalCDF((t*shrink-lambda)/scale) + normalCDF((-t*shrink-lambda)/scale)
}

// CohenKappa is Cohen's kappa between two raters' labels of the same items
// (Cohen 1960): `(p_o - p_e) / (1 - p_e)`, with p_e from each rater's marginals.
// It is NaN when p_e is 1, as when both raters give every item one label. It
// panics when a and b differ in length or are empty.
func CohenKappa(a, b []int) float64 {
	return kappa(a, b, func(x, y int) int64 {
		if x == y {
			return 0
		}
		return 1
	})
}

// WeightedKappa is Cohen's weighted kappa with linear weights (Cohen 1968) for
// ordinal grades: `1 - sum(w*observed) / sum(w*expected)`, with disagreement
// weight |x-y| on the grade values. It equals scikit-learn's
// cohen_kappa_score(weights="linear") when the observed grades are consecutive
// integers. Its NaN and panic cases are CohenKappa's.
func WeightedKappa(a, b []int) float64 {
	return kappa(a, b, func(x, y int) int64 {
		if x > y {
			return int64(x - y)
		}
		return int64(y - x)
	})
}

// kappa is `1 - observed/expected` disagreement, kept in integers until the one
// division: observed is n*sum(w(a_i, b_i)), expected sum(w(x, y)*count_a(x)*count_b(y)).
func kappa(a, b []int, weight func(x, y int) int64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		panic(fmt.Sprintf("kappa needs two equal, nonempty label lists, got %d and %d", len(a), len(b)))
	}
	countA, countB := map[int]int64{}, map[int]int64{}
	var observed int64
	for i := range a {
		countA[a[i]]++
		countB[b[i]]++
		observed += weight(a[i], b[i])
	}
	var expected int64
	for x, cx := range countA {
		for y, cy := range countB {
			expected += weight(x, y) * cx * cy
		}
	}
	return 1 - float64(observed*int64(len(a)))/float64(expected)
}

// differences is a-b, checked: equal lengths, at least minimum pairs, finite.
func differences(a, b []float64, minimum int) []float64 {
	if len(a) != len(b) {
		panic(fmt.Sprintf("paired scores differ in length: %d and %d", len(a), len(b)))
	}
	if len(a) < minimum {
		panic(fmt.Sprintf("%d score pairs, want at least %d", len(a), minimum))
	}
	d := make([]float64, len(a))
	for i := range a {
		d[i] = a[i] - b[i]
		if math.IsNaN(d[i]) || math.IsInf(d[i], 0) {
			panic(fmt.Sprintf("pair %d is not finite: %v and %v", i, a[i], b[i]))
		}
	}
	return d
}

// meanSD is the mean and the sample (n-1) standard deviation, in two passes so a
// large mean does not cancel the variance.
func meanSD(d []float64) (mean, sd float64) {
	for _, v := range d {
		mean += v
	}
	mean /= float64(len(d))
	var ss float64
	for _, v := range d {
		ss += (v - mean) * (v - mean)
	}
	return mean, math.Sqrt(ss / float64(len(d)-1))
}

func normalCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }

func normalQuantile(p float64) float64 { return math.Sqrt2 * math.Erfinv(2*p-1) }

// studentTUpper is P(T > t) for Student's t with df degrees of freedom, through
// the regularized incomplete beta: P(|T| > t) = I_{df/(df+t^2)}(df/2, 1/2).
func studentTUpper(t, df float64) float64 {
	if t < 0 {
		return 1 - studentTUpper(-t, df)
	}
	return 0.5 * regularizedBeta(df/(df+t*t), df/2, 0.5)
}

// studentTQuantile is the t with P(T > t) = upper, found by bisection on
// studentTUpper, which is monotone; bisection costs more calls than Newton but
// cannot overshoot in the heavy tails of small df.
func studentTQuantile(upper, df float64) float64 {
	if upper > 0.5 {
		return -studentTQuantile(1-upper, df)
	}
	lo, hi := 0.0, 1.0
	for studentTUpper(hi, df) > upper {
		lo, hi = hi, 2*hi
	}
	for range 200 {
		mid := lo + (hi-lo)/2
		if mid == lo || mid == hi {
			break
		}
		if studentTUpper(mid, df) > upper {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo + (hi-lo)/2
}

// regularizedBeta is I_x(a, b) by the continued fraction of Numerical Recipes
// (Press et al., 3rd ed., 6.4), evaluated with modified Lentz on whichever of
// x and 1-x converges fast.
func regularizedBeta(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lga, _ := math.Lgamma(a)
	lgb, _ := math.Lgamma(b)
	lgab, _ := math.Lgamma(a + b)
	front := math.Exp(lgab - lga - lgb + a*math.Log(x) + b*math.Log1p(-x))
	if x < (a+1)/(a+b+2) {
		return front * betaFraction(x, a, b) / a
	}
	return 1 - front*betaFraction(1-x, b, a)/b
}

func betaFraction(x, a, b float64) float64 {
	const tiny = 1e-300
	clamp := func(v float64) float64 {
		if math.Abs(v) < tiny {
			return tiny
		}
		return v
	}
	c := 1.0
	d := 1 / clamp(1-(a+b)*x/(a+1))
	h := d
	for m := 1.0; m <= 10000; m++ {
		even := m * (b - m) * x / ((a + 2*m - 1) * (a + 2*m))
		d = 1 / clamp(1+even*d)
		c = clamp(1 + even/c)
		h *= d * c
		odd := -(a + m) * (a + b + m) * x / ((a + 2*m) * (a + 2*m + 1))
		d = 1 / clamp(1+odd*d)
		c = clamp(1 + odd/c)
		delta := d * c
		h *= delta
		if math.Abs(delta-1) < 1e-15 {
			return h
		}
	}
	panic(fmt.Sprintf("incomplete beta did not converge at x=%v a=%v b=%v", x, a, b))
}
