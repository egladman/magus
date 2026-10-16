package distill

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"

	"gonum.org/v1/gonum/mat"
)

// covChunk is how many centered rows one rank-k update of the covariance takes. It
// fixes the order of the float sums, so it is part of what makes a fit reproducible.
const covChunk = 4096

// PCA is a fitted basis: x maps to Components·(x − Mean).
type PCA struct {
	Mean       []float64
	Components [][]float64 // k orthonormal rows of the input dims, variance descending
	Variances  []float64   // the eigenvalue of each component
}

// FitPCA fits the k leading principal components of rows with gonum's EigenSym on
// their sample covariance. Each component's sign is fixed so its largest-magnitude
// loading, the first on a tie, is positive. The fit is bit-identical for the same rows
// in the same order on one machine.
func FitPCA(rows [][]float32, k int) (*PCA, error) {
	if len(rows) < 2 {
		return nil, fmt.Errorf("pca: %d rows; a covariance needs at least 2", len(rows))
	}
	dims := len(rows[0])
	if k < 1 || k > dims {
		return nil, fmt.Errorf("pca: %d components of %d dims", k, dims)
	}
	mean := make([]float64, dims)
	for i, r := range rows {
		if len(r) != dims {
			return nil, fmt.Errorf("pca: row %d has %d dims, row 0 %d", i, len(r), dims)
		}
		for j, x := range r {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return nil, fmt.Errorf("pca: row %d holds a non-finite value", i)
			}
			mean[j] += float64(x)
		}
	}
	for j := range mean {
		mean[j] /= float64(len(rows))
	}

	cov := mat.NewSymDense(dims, nil)
	chunk := mat.NewDense(min(covChunk, len(rows)), dims, nil)
	for lo := 0; lo < len(rows); lo += covChunk {
		part := rows[lo:min(lo+covChunk, len(rows))]
		c := chunk.Slice(0, len(part), 0, dims).(*mat.Dense)
		for i, r := range part {
			for j, x := range r {
				c.Set(i, j, float64(x)-mean[j])
			}
		}
		cov.SymRankK(cov, 1, c.T())
	}
	cov.ScaleSym(1/float64(len(rows)-1), cov)

	var es mat.EigenSym
	if !es.Factorize(cov, true) {
		return nil, errors.New("pca: the eigendecomposition did not converge")
	}
	vals := es.Values(nil)
	var vecs mat.Dense
	es.VectorsTo(&vecs)

	p := &PCA{Mean: mean, Components: make([][]float64, k), Variances: make([]float64, k)}
	for c := range k {
		col := len(vals) - 1 - c // EigenSym returns ascending eigenvalues
		comp := mat.Col(nil, col, &vecs)
		big := 0
		for j, x := range comp {
			if math.Abs(x) > math.Abs(comp[big]) {
				big = j
			}
		}
		if comp[big] < 0 {
			for j := range comp {
				comp[j] = -comp[j]
			}
		}
		p.Components[c] = comp
		p.Variances[c] = vals[col]
	}
	return p, nil
}

// Dims is the number of components.
func (p *PCA) Dims() int { return len(p.Components) }

// Project maps x into the basis. x must have the fitted input dims.
func (p *PCA) Project(x []float32) []float64 {
	if len(x) != len(p.Mean) {
		panic(fmt.Sprintf("distill: project %d dims through a PCA fitted on %d", len(x), len(p.Mean)))
	}
	out := make([]float64, len(p.Components))
	for c, comp := range p.Components {
		var s float64
		for j, w := range comp {
			s += w * (float64(x[j]) - p.Mean[j])
		}
		out[c] = s
	}
	return out
}

// SampleByHash picks the k ids with the smallest IDHash(salt, id), returned as indexes
// into ids in ascending order. The choice depends on the ids alone, not their order.
// k at least len(ids) picks every index.
func SampleByHash(ids []string, k int, salt string) []int {
	idx := make([]int, len(ids))
	hs := make([]uint64, len(ids))
	for i, id := range ids {
		idx[i], hs[i] = i, IDHash(salt, id)
	}
	if k < len(ids) {
		slices.SortFunc(idx, func(a, b int) int {
			if c := cmp.Compare(hs[a], hs[b]); c != 0 {
				return c
			}
			return cmp.Compare(ids[a], ids[b])
		})
		idx = idx[:max(k, 0)]
		slices.Sort(idx)
	}
	return idx
}
