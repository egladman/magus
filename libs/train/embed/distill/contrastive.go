package distill

import (
	"math"
	"math/rand/v2"
	"slices"
)

// mnrlLoss is the Matryoshka MNRL (InfoNCE) loss L2 = Σ_m mean_i [logsumexp_j z_ij −
// z_ii] over prefixes m, with z_ij = a_i·b_j/τ, a_i = q_i[:m]/‖q_i[:m]‖ and b_j the
// same of d_j: each query against every doc of the batch, its own doc the positive.
// It adds scale·∂L2/∂q and scale·∂L2/∂d into dq and dd. A zero prefix makes it NaN.
func mnrlLoss(q, d, dq, dd [][]float64, prefixes []int, tau, scale float64, workers int) float64 {
	n := len(q)
	a, b := make([][]float64, n), make([][]float64, n)
	na, nb := make([]float64, n), make([]float64, n)
	z := make([][]float64, n) // reused as dz once a row's loss is taken
	rowLoss := make([]float64, n)
	for i := range n {
		z[i] = make([]float64, n)
	}
	var loss float64
	for _, m := range prefixes {
		for i := range n {
			a[i], na[i] = unit(q[i][:m])
			b[i], nb[i] = unit(d[i][:m])
		}
		crossEntropyRows(z, rowLoss, a, b, tau, workers)
		for _, l := range rowLoss {
			loss += l
		}
		k := scale / tau
		parallel(n, workers, func(_, lo, hi int) {
			da := make([]float64, m)
			for i := lo; i < hi; i++ {
				clear(da)
				for j := range n {
					axpy(da, z[i][j], b[j])
				}
				projectInto(dq[i][:m], a[i], da, k/na[i])
			}
		})
		parallel(n, workers, func(_, lo, hi int) {
			db := make([]float64, m)
			for j := lo; j < hi; j++ {
				clear(db)
				for i := range n {
					axpy(db, z[i][j], a[i])
				}
				projectInto(dd[j][:m], b[j], db, k/nb[j])
			}
		})
	}
	return loss / float64(n)
}

// crossEntropyRows is the forward pass of one prefix: row i's logits z_ij = a_i·b_j/τ,
// its softmax cross-entropy against the positive j = i into rowLoss[i], computed as
// logsumexp about the row max, then ∂(mean loss)/∂z_ij = (softmax_ij − [i = j])/n
// over z[i].
func crossEntropyRows(z [][]float64, rowLoss []float64, a, b [][]float64, tau float64, workers int) {
	n := len(z)
	parallel(n, workers, func(_, lo, hi int) {
		for i := lo; i < hi; i++ {
			zi := z[i]
			top := math.Inf(-1)
			for j := range n {
				zi[j] = dot(a[i], b[j]) / tau
				top = max(top, zi[j])
			}
			var sum float64
			for _, x := range zi {
				sum += math.Exp(x - top)
			}
			rowLoss[i] = top + math.Log(sum) - zi[i]
			for j, x := range zi {
				zi[j] = math.Exp(x-top) / sum / float64(n)
			}
			zi[i] -= 1 / float64(n)
		}
	})
}

// unit is x/‖x‖ and ‖x‖.
func unit(x []float64) ([]float64, float64) {
	n := math.Sqrt(dot(x, x))
	u := make([]float64, len(x))
	for j, v := range x {
		u[j] = v / n
	}
	return u, n
}

// projectInto adds k·(I − uuᵀ)·g into dst: the chain rule through x/‖x‖ for unit u,
// with k carrying 1/‖x‖.
func projectInto(dst, u, g []float64, k float64) {
	ug := dot(u, g)
	for j := range dst {
		dst[j] += k * (g[j] - u[j]*ug)
	}
}

func axpy(dst []float64, k float64, x []float64) {
	for j, v := range x {
		dst[j] += k * v
	}
}

// batchPairs shuffles pairs and cuts them into batches of up to size in which no
// query or doc repeats, so no in-batch negative is secretly a positive. A pair that
// would repeat one waits for the next batch it fits. A batch of fewer than two pairs
// has no negative and is dropped, as is a pair whose query and doc bag the same; the
// count of dropped pairs is returned. Copies of one upsampled pair that are still
// waiting when the rest run out can only go one to a batch, so they are what drops.
func batchPairs(rng *rand.Rand, pairs []Pair, size int) ([][]Pair, int) {
	if len(pairs) == 0 {
		return nil, 0
	}
	order := slices.Clone(pairs)
	rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	b := &noDuplicates{size: size, seen: map[uint64]bool{}}
	for _, p := range order {
		k := keyedPair{p: p, q: p.Query.key(), d: p.Doc.key()}
		if !b.fits(k) {
			b.carry = append(b.carry, k)
			continue
		}
		b.add(k)
		if len(b.cur) == size {
			b.flush()
		}
	}
	// Each flush takes cur's pairs out for good, so this ends; what stays in carry is
	// only pairs whose query bag is their doc bag, which never fit.
	for len(b.cur) > 0 {
		b.flush()
	}
	dropped := len(pairs)
	for _, batch := range b.out {
		dropped -= len(batch)
	}
	return b.out, dropped
}

type keyedPair struct {
	p    Pair
	q, d uint64
}

// noDuplicates fills batches of up to size pairs in which no query or doc bag repeats,
// carrying a pair that does not fit cur to the next batch it fits.
type noDuplicates struct {
	size  int
	out   [][]Pair
	carry []keyedPair
	cur   []Pair
	seen  map[uint64]bool
}

func (b *noDuplicates) fits(k keyedPair) bool { return !b.seen[k.q] && !b.seen[k.d] && k.q != k.d }

func (b *noDuplicates) add(k keyedPair) {
	b.cur = append(b.cur, k.p)
	b.seen[k.q], b.seen[k.d] = true, true
}

// flush closes cur, keeping it only with two pairs or more, and opens the next batch
// with every carried pair that fits it.
func (b *noDuplicates) flush() {
	if len(b.cur) >= 2 {
		b.out = append(b.out, b.cur)
	}
	b.cur, b.seen = nil, map[uint64]bool{}
	waiting := b.carry[:0:0]
	for _, k := range b.carry {
		if len(b.cur) < b.size && b.fits(k) {
			b.add(k)
		} else {
			waiting = append(waiting, k)
		}
	}
	b.carry = waiting
}
