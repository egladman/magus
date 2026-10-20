package distill

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gonum.org/v1/gonum/mat"
)

// planted draws n rows of dims around a mean, spread with standard deviation 5 along
// u1 and 2 along u2, plus 0.1 of isotropic noise.
func planted(rng *rand.Rand, n int) (rows [][]float32, u1, u2 []float64) {
	const dims = 6
	u1 = []float64{0, 0.6, 0, -0.8, 0, 0} // largest loading negative: the fit must flip it
	u2 = []float64{0.6, 0, 0, 0, 0.8, 0}
	mean := []float64{1, 2, 3, 4, 5, 6}
	rows = make([][]float32, n)
	for i := range rows {
		a, b := 5*rng.NormFloat64(), 2*rng.NormFloat64()
		r := make([]float32, dims)
		for j := range r {
			r[j] = float32(mean[j] + a*u1[j] + b*u2[j] + 0.1*rng.NormFloat64())
		}
		rows[i] = r
	}
	return rows, u1, u2
}

func TestPCARecoversPlantedAxes(t *testing.T) {
	rows, u1, u2 := planted(rand.New(rand.NewPCG(21, 0)), 5000)
	p, err := FitPCA(rows, 2)
	require.NoError(t, err)
	assert.Equal(t, 2, p.Dims())
	assert.InDelta(t, -1, dot(p.Components[0], u1), 1e-3)
	assert.InDelta(t, 1, math.Abs(dot(p.Components[1], u2)), 1e-3)
	assert.InEpsilon(t, 25, p.Variances[0], 0.05)
	assert.InEpsilon(t, 4, p.Variances[1], 0.05)
	assert.InDelta(t, 0, dot(p.Components[0], p.Components[1]), 1e-12)
	for c, comp := range p.Components {
		assert.InDelta(t, 1, dot(comp, comp), 1e-12)
		big := 0
		for j := range comp {
			if math.Abs(comp[j]) > math.Abs(comp[big]) {
				big = j
			}
		}
		assert.Positive(t, comp[big], "component %d's largest loading is negative", c)
	}
	// The mean projects to the origin.
	mean := make([]float32, len(p.Mean))
	for j, x := range p.Mean {
		mean[j] = float32(x)
	}
	assert.InDeltaSlice(t, []float64{0, 0}, p.Project(mean), 1e-5)
}

func TestPCAVariancesMatchSVD(t *testing.T) {
	rng := rand.New(rand.NewPCG(22, 0))
	const n, dims = 300, 10
	rows := make([][]float32, n)
	for i := range rows {
		rows[i] = make([]float32, dims)
		for j := range rows[i] {
			rows[i][j] = float32(rng.NormFloat64() * float64(j+1))
		}
	}
	p, err := FitPCA(rows, dims)
	require.NoError(t, err)

	x := mat.NewDense(n, dims, nil)
	for i, r := range rows {
		for j, v := range r {
			x.Set(i, j, float64(v)-p.Mean[j])
		}
	}
	var svd mat.SVD
	require.True(t, svd.Factorize(x, mat.SVDThin))
	for c, s := range svd.Values(nil) {
		assert.InEpsilon(t, s*s/(n-1), p.Variances[c], 1e-9, "component %d", c)
	}
}

func TestPCAIsBitIdenticalAcrossChunks(t *testing.T) {
	// More rows than covChunk, so the covariance sums over two rank-k updates.
	rows, _, _ := planted(rand.New(rand.NewPCG(23, 0)), covChunk+700)
	a, err := FitPCA(rows, 3)
	require.NoError(t, err)
	b, err := FitPCA(rows, 3)
	require.NoError(t, err)
	assert.Equal(t, bits(a.Mean), bits(b.Mean))
	for c := range a.Components {
		assert.Equal(t, bits(a.Components[c]), bits(b.Components[c]))
	}
	assert.Equal(t, bits(a.Variances), bits(b.Variances))
}

func bits(x []float64) []uint64 {
	out := make([]uint64, len(x))
	for i, v := range x {
		out[i] = math.Float64bits(v)
	}
	return out
}

func TestPCARefuses(t *testing.T) {
	_, err := FitPCA([][]float32{{1, 2}}, 1)
	assert.ErrorContains(t, err, "at least 2")
	_, err = FitPCA([][]float32{{1, 2}, {3, 4}}, 3)
	assert.ErrorContains(t, err, "3 components of 2 dims")
	_, err = FitPCA([][]float32{{1, 2}, {3}}, 1)
	assert.ErrorContains(t, err, "row 1 has 1 dims")
	_, err = FitPCA([][]float32{{1, 2}, {float32(math.NaN()), 4}}, 1)
	assert.ErrorContains(t, err, "non-finite")
}

func TestPCASampleByHashIgnoresOrder(t *testing.T) {
	ids := make([]string, 100)
	for i := range ids {
		ids[i] = fmt.Sprint("text-", i)
	}
	got := SampleByHash(ids, 10, "pca/v1:")
	require.Len(t, got, 10)
	assert.True(t, slices.IsSorted(got))

	rev := slices.Clone(ids)
	slices.Reverse(rev)
	var a, b []string
	for _, i := range got {
		a = append(a, ids[i])
	}
	for _, i := range SampleByHash(rev, 10, "pca/v1:") {
		b = append(b, rev[i])
	}
	assert.ElementsMatch(t, a, b)
	assert.NotEqual(t, got, SampleByHash(ids, 10, "pca/v2:"))
	assert.Len(t, SampleByHash(ids, 500, "pca/v1:"), 100)
}
