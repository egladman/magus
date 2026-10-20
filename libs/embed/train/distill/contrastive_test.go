package distill

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContrastiveGradientMatchesFiniteDifferences(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tau      float64
		prefixes []int
	}{
		{"tau 0.05, matryoshka", 0.05, []int{3, 8}},
		{"tau 1, one prefix", 1, []int{8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewPCG(12, 0))
			m := randomModel(t, rng, 9, 8)
			pairs := randomPairs(rng, 5, 9)
			worst := checkGradient(t, m, nil, pairs, gradOptions(8, tc.prefixes, tc.tau, 0.7))
			t.Logf("worst scaled error %.3g", worst)
		})
	}
}

func TestContrastiveLossOnOrthonormalPairs(t *testing.T) {
	// q_i and d_i lie along basis vector i: z_ii = 1/τ and z_ij = 0, so each row's
	// loss is log(e^{1/τ} + n − 1) − 1/τ.
	const n, tau = 4, 0.5
	q := make([][]float64, n)
	d := make([][]float64, n)
	for i := range n {
		q[i], d[i] = make([]float64, n), make([]float64, n)
		q[i][i], d[i][i] = 3, 0.5
	}
	zero := func() [][]float64 {
		z := make([][]float64, n)
		for i := range z {
			z[i] = make([]float64, n)
		}
		return z
	}
	got := mnrlLoss(q, d, zero(), zero(), []int{n}, tau, 1, 1)
	want := math.Log(math.Exp(1/tau)+n-1) - 1/tau
	assert.InDelta(t, want, got, 1e-12)
}

func TestContrastiveBatchesNeverRepeatAText(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 0))
	base := randomPairs(rng, 30, 12)
	var pairs []Pair
	for range 5 { // upsampled like the Buzz pairs: every pair five times
		pairs = append(pairs, base...)
	}
	var selfClash int
	for _, p := range pairs {
		if p.Query.key() == p.Doc.key() {
			selfClash++
		}
	}
	batches, dropped := batchPairs(rand.New(rand.NewPCG(1, 1)), pairs, 8)
	var total int
	for _, b := range batches {
		assert.GreaterOrEqual(t, len(b), 2)
		assert.LessOrEqual(t, len(b), 8)
		seen := map[uint64]bool{}
		for _, p := range b {
			for _, k := range []uint64{p.Query.key(), p.Doc.key()} {
				assert.False(t, seen[k], "a text repeats within a batch")
				seen[k] = true
			}
		}
		total += len(b)
	}
	assert.Equal(t, len(pairs), total+dropped, "a pair is neither batched nor counted dropped")
	assert.GreaterOrEqual(t, dropped, selfClash)
	assert.Less(t, dropped, len(pairs)/10)
	again, _ := batchPairs(rand.New(rand.NewPCG(1, 1)), pairs, 8)
	assert.Equal(t, batches, again)

	// Five copies of one pair and nothing else: they can only go one to a batch.
	only := []Pair{base[0], base[0], base[0], base[0], base[0]}
	batches, dropped = batchPairs(rand.New(rand.NewPCG(1, 1)), only, 8)
	assert.Empty(t, batches)
	assert.Equal(t, 5, dropped)
}
