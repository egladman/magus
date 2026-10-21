package distill

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
)

// gradChunks is how many pieces a batch's forward and backward passes split into. It
// is fixed, not the worker count, so the sums reduce in the same order however many
// workers run them, and a run is bit-identical at any Workers.
const gradChunks = 16

// validSalt picks the early-stopping texts: IDHash(validSalt, id) % validModulus == 0,
// about 2% of them.
const (
	validSalt    = "embed-valid/v1:"
	validModulus = 50
)

// Model is the student: each token t has a direction e_t and a weight
// ω_t = softplus(β_t), and a text with token counts n_t embeds to
// s = Σ n_t·ω_t·e_t / Σ n_t.
type Model struct {
	dims int
	vec  []float64 // row-major, len(beta)×dims
	beta []float64
}

// NewModel starts a student from per-token directions and positive weights: e_t is
// dirs[t] scaled to unit length and β_t = softplus⁻¹(weights[t]). A zero direction is
// an error, since it could never learn a gradient through the norm.
func NewModel(dirs [][]float64, weights []float64) (*Model, error) {
	if len(dirs) == 0 || len(dirs) != len(weights) {
		return nil, fmt.Errorf("new model: %d directions and %d weights", len(dirs), len(weights))
	}
	dims := len(dirs[0])
	if dims < 1 {
		return nil, errors.New("new model: directions have no dims")
	}
	m := &Model{dims: dims, vec: make([]float64, len(dirs)*dims), beta: make([]float64, len(dirs))}
	for t, d := range dirs {
		if len(d) != dims {
			return nil, fmt.Errorf("new model: direction %d has %d dims, direction 0 %d", t, len(d), dims)
		}
		n := math.Sqrt(dot(d, d))
		if !(n > 0) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("new model: direction %d has norm %v", t, n)
		}
		row := m.row(t)
		for j, x := range d {
			row[j] = x / n
		}
		w := weights[t]
		if !(w > 0) || math.IsInf(w, 0) {
			return nil, fmt.Errorf("new model: weight %d is %v, not positive and finite", t, w)
		}
		m.beta[t] = softplusInv(w)
	}
	return m, nil
}

// Dims is the length of every direction.
func (m *Model) Dims() int { return m.dims }

// Len is the number of tokens.
func (m *Model) Len() int { return len(m.beta) }

// Weight is ω_t.
func (m *Model) Weight(t int) float64 { return softplus(m.beta[t]) }

// Direction is e_t. The slice aliases the model.
func (m *Model) Direction(t int) []float64 { return m.row(t) }

// Embed is s for b.
func (m *Model) Embed(b Bag) []float64 {
	s := make([]float64, m.dims)
	m.embedInto(s, b)
	return s
}

// FitScale multiplies every weight by the α > 0 that minimizes Σ ‖α·s(x) − y(x)‖²
// over examples, α = Σ s·y / Σ s·s, and returns α. SIF weights are relative: they
// say nothing of the targets' scale, so without this the MSE spends its first epochs
// learning one global factor. The weights' ratios, and so every cosine, are unchanged.
func (m *Model) FitScale(examples []Example) (float64, error) {
	var sy, ss float64
	for _, ex := range examples {
		if err := m.checkBag(ex.Bag); err != nil {
			return 0, fmt.Errorf("fit scale: example %q: %w", ex.ID, err)
		}
		if len(ex.Target) != m.dims {
			return 0, fmt.Errorf("fit scale: example %q has a %d-dim target, the model %d", ex.ID, len(ex.Target), m.dims)
		}
		s := m.Embed(ex.Bag)
		sy += dot(s, ex.Target)
		ss += dot(s, s)
	}
	alpha := sy / ss
	if !(alpha > 0) || math.IsInf(alpha, 0) {
		return 0, fmt.Errorf("fit scale: the least-squares scale is %v, so the directions do not point toward the targets", alpha)
	}
	for t := range m.beta {
		m.beta[t] = softplusInv(alpha * softplus(m.beta[t]))
	}
	return alpha, nil
}

// Clone copies the model.
func (m *Model) Clone() *Model {
	return &Model{dims: m.dims, vec: slices.Clone(m.vec), beta: slices.Clone(m.beta)}
}

func (m *Model) row(t int) []float64 { return m.vec[t*m.dims : (t+1)*m.dims : (t+1)*m.dims] }

func (m *Model) embedInto(s []float64, b Bag) {
	clear(s)
	n := b.total()
	for i, t := range b.Rows {
		k := b.Counts[i] * softplus(m.beta[t]) / n
		for j, e := range m.row(int(t)) {
			s[j] += k * e
		}
	}
}

// grads accumulates a sparse gradient over the rows a batch touches.
type grads struct {
	dims int
	slot map[int32]int
	rows []int32
	vec  []float64
	beta []float64
}

func newGrads(dims int) *grads { return &grads{dims: dims, slot: map[int32]int{}} }

func (g *grads) at(t int32) int {
	if i, ok := g.slot[t]; ok {
		return i
	}
	i := len(g.rows)
	g.slot[t] = i
	g.rows = append(g.rows, t)
	g.vec = append(g.vec, make([]float64, g.dims)...)
	g.beta = append(g.beta, 0)
	return i
}

// backward adds ∂L/∂e_t = ds·n_t·ω_t/N and ∂L/∂β_t = (ds·e_t)·n_t·σ(β_t)/N for every
// token of b, given ds = ∂L/∂s.
func (m *Model) backward(g *grads, b Bag, ds []float64) {
	n := b.total()
	for i, t := range b.Rows {
		c := b.Counts[i] / n
		e := m.row(int(t))
		k := g.at(t)
		gv := g.vec[k*g.dims : (k+1)*g.dims]
		w := c * softplus(m.beta[t])
		for j, d := range ds {
			gv[j] += d * w
		}
		g.beta[k] += dot(ds, e) * c * sigmoid(m.beta[t])
	}
}

// reduce sums parts in order into one gradient over the union of their rows, rows
// ascending.
func reduce(dims int, parts []*grads) *grads {
	out := newGrads(dims)
	var rows []int32
	for _, p := range parts {
		rows = append(rows, p.rows...)
	}
	slices.Sort(rows)
	for _, t := range slices.Compact(rows) {
		out.at(t)
	}
	for _, p := range parts {
		for i, t := range p.rows {
			k := out.slot[t]
			ov := out.vec[k*dims : (k+1)*dims]
			for j, x := range p.vec[i*dims : (i+1)*dims] {
				ov[j] += x
			}
			out.beta[k] += p.beta[i]
		}
	}
	return out
}

// prefixCoef is Σ_{m ∈ prefixes, j < m} 1/m for each dim j: how much the Matryoshka
// sum weighs dim j's squared error.
func prefixCoef(dims int, prefixes []int) []float64 {
	c := make([]float64, dims)
	for _, m := range prefixes {
		for j := range m {
			c[j] += 1 / float64(m)
		}
	}
	return c
}

// mseLoss is L1 = mean over the batch of Σ_m ‖s_{:m} − y_{:m}‖²/m. It writes ∂L1/∂s
// into ds when ds is not nil.
func mseLoss(s, y, ds [][]float64, coef []float64) float64 {
	var loss float64
	inv := 1 / float64(len(s))
	for i := range s {
		var l float64
		for j, c := range coef {
			r := s[i][j] - y[i][j]
			l += c * r * r
			if ds != nil {
				ds[i][j] += inv * 2 * c * r
			}
		}
		loss += l
	}
	return loss * inv
}

// TrainOptions configures Train. Every field is required; DefaultTrainOptions gives
// the design's values.
type TrainOptions struct {
	Seed         uint64
	Epochs       int
	LearningRate float64
	MSEBatch     int     // texts per MSE batch
	PairBatch    int     // pairs per MNRL batch, at least 2
	Lambda       float64 // weight of the MNRL term; 0 trains MSE alone
	Prefixes     []int   // Matryoshka prefixes, ascending, the last the model's dims
	Temperature  float64 // MNRL τ
	Workers      int     // goroutines for the forward and backward passes
	Patience     int     // epochs without a held-out improvement before training stops
}

// DefaultTrainOptions is section 1.5's configuration for a model of dims: lazy Adam at
// 5e-3, MSE batches of 1,024 texts, MNRL batches of 512 pairs at τ = 0.05 with λ = 0.5,
// 3 epochs with a patience of 1, and the prefixes of {64, 128, 256} below dims, then
// dims.
func DefaultTrainOptions(dims int) TrainOptions {
	var prefixes []int
	for _, m := range []int{64, 128} {
		if m < dims {
			prefixes = append(prefixes, m)
		}
	}
	return TrainOptions{
		Epochs: 3, LearningRate: 5e-3, MSEBatch: 1024, PairBatch: 512, Lambda: 0.5,
		Prefixes: append(prefixes, dims), Temperature: 0.05, Workers: 6, Patience: 1,
	}
}

func (o TrainOptions) validate(dims int, examples, pairs int) error {
	switch {
	case o.Epochs < 1:
		return fmt.Errorf("epochs %d is below 1", o.Epochs)
	case !(o.LearningRate > 0):
		return fmt.Errorf("learning rate %v is not positive", o.LearningRate)
	case examples > 0 && o.MSEBatch < 1:
		return fmt.Errorf("MSE batch %d is below 1", o.MSEBatch)
	case pairs > 0 && o.PairBatch < 2:
		return fmt.Errorf("pair batch %d is below 2, which leaves MNRL no negative", o.PairBatch)
	case !(o.Lambda >= 0):
		return fmt.Errorf("lambda %v is negative", o.Lambda)
	case pairs > 0 && o.Lambda == 0:
		return errors.New("pairs were given but lambda is 0, so pass no pairs to train MSE alone")
	case !(o.Temperature > 0):
		return fmt.Errorf("temperature %v is not positive", o.Temperature)
	case o.Workers < 1:
		return fmt.Errorf("workers %d is below 1", o.Workers)
	case o.Patience < 1:
		return fmt.Errorf("patience %d is below 1", o.Patience)
	case len(o.Prefixes) == 0 || o.Prefixes[len(o.Prefixes)-1] != dims:
		return fmt.Errorf("prefixes %v must end at the model's %d dims", o.Prefixes, dims)
	case examples == 0 && pairs == 0:
		return errors.New("no examples and no pairs")
	}
	for i, m := range o.Prefixes {
		if m < 1 || i > 0 && m <= o.Prefixes[i-1] {
			return fmt.Errorf("prefixes %v are not positive and ascending", o.Prefixes)
		}
	}
	return nil
}

// Epoch is one epoch's mean losses. Valid is the MSE on the held-out texts, NaN when
// none are held out. DroppedPairs is how many pairs no batch of the epoch could take.
type Epoch struct {
	MSE, MNRL, Loss, Valid float64
	DroppedPairs           int
}

// Report is what Train did.
type Report struct {
	Epochs    []Epoch
	Steps     int
	Train     int // MSE examples trained on
	Held      int // MSE examples held out for early stopping
	Pairs     int
	BestEpoch int // the epoch whose parameters the model ends with, zero-based
}

// checkTrainingData checks every example's bag and target and every pair's bags
// against m.
func (m *Model) checkTrainingData(examples []Example, pairs []Pair) error {
	for _, ex := range examples {
		if err := m.checkBag(ex.Bag); err != nil {
			return fmt.Errorf("example %q: %w", ex.ID, err)
		}
		if len(ex.Target) != m.dims {
			return fmt.Errorf("example %q has a %d-dim target, the model %d", ex.ID, len(ex.Target), m.dims)
		}
	}
	for i, p := range pairs {
		if err := errors.Join(m.checkBag(p.Query), m.checkBag(p.Doc)); err != nil {
			return fmt.Errorf("pair %d: %w", i, err)
		}
	}
	return nil
}

// holdOut splits examples into the training split and the validation split, the
// examples whose IDHash under validSalt falls on the held-out residue.
func holdOut(examples []Example) (train, held []Example) {
	for _, ex := range examples {
		if IDHash(validSalt, ex.ID)%validModulus == 0 {
			held = append(held, ex)
		} else {
			train = append(train, ex)
		}
	}
	return train, held
}

// epoch runs one epoch of lazy Adam steps, each on one MSE batch and one pair batch,
// cycling the set with fewer batches, and counts its steps in rep. The epoch's losses
// are means over its steps; its Valid is left for the caller.
func (tr *trainer) epoch(rep *Report, epoch int, train []Example, pairs []Pair) (Epoch, error) {
	opts := tr.opts
	rng := rand.New(rand.NewPCG(opts.Seed, uint64(epoch)))
	exBatches := batchExamples(rng, train, opts.MSEBatch)
	pairBatches, dropped := batchPairs(rng, pairs, opts.PairBatch)
	steps := max(len(exBatches), len(pairBatches))
	if steps == 0 {
		return Epoch{}, fmt.Errorf("train: epoch %d has no batch: all %d pairs clash within a batch", epoch, dropped)
	}
	ep := Epoch{DroppedPairs: dropped}
	for k := range steps {
		var exb []Example
		var pb []Pair
		if len(exBatches) > 0 {
			exb = exBatches[k%len(exBatches)]
		}
		if len(pairBatches) > 0 {
			pb = pairBatches[k%len(pairBatches)]
		}
		l1, l2, g := tr.lossAndGrad(exb, pb)
		loss := l1 + opts.Lambda*l2
		if math.IsNaN(loss) || math.IsInf(loss, 0) {
			return Epoch{}, fmt.Errorf("train: epoch %d step %d: loss is %v", epoch, k, loss)
		}
		tr.adam.step(tr.m, g, opts.LearningRate)
		ep.MSE += l1
		ep.MNRL += l2
		ep.Loss += loss
		rep.Steps++
	}
	ep.MSE /= float64(steps)
	ep.MNRL /= float64(steps)
	ep.Loss /= float64(steps)
	return ep, nil
}

// Train fits m to examples (L1) and pairs (λ·L2) with lazy Adam. Each step takes one
// MSE batch and one pair batch; an epoch runs as many steps as the larger set has
// batches, cycling the smaller. Examples whose IDHash under validSalt falls on the
// held-out residue are kept out of training; when there are any, training stops after
// Patience epochs in a row whose held-out MSE does not improve, and m keeps the best
// epoch's parameters. One seed decides every shuffle, and the result is bit-identical
// for the same inputs, options and machine, at any Workers.
func Train(m *Model, examples []Example, pairs []Pair, opts TrainOptions) (Report, error) {
	if err := opts.validate(m.dims, len(examples), len(pairs)); err != nil {
		return Report{}, fmt.Errorf("train: %w", err)
	}
	if err := m.checkTrainingData(examples, pairs); err != nil {
		return Report{}, fmt.Errorf("train: %w", err)
	}
	train, held := holdOut(examples)
	if len(train) == 0 && len(pairs) == 0 {
		return Report{}, errors.New("train: every example is held out and there are no pairs")
	}

	tr := &trainer{m: m, opts: opts, coef: prefixCoef(m.dims, opts.Prefixes), adam: newAdam(m)}
	rep := Report{Train: len(train), Held: len(held), Pairs: len(pairs), BestEpoch: -1}
	best, bestValid := m.Clone(), math.Inf(1)
	for epoch := range opts.Epochs {
		ep, err := tr.epoch(&rep, epoch, train, pairs)
		if err != nil {
			return rep, err
		}
		ep.Valid = math.NaN()
		if len(held) > 0 {
			ep.Valid = tr.validLoss(held)
		}
		rep.Epochs = append(rep.Epochs, ep)
		if len(held) == 0 {
			rep.BestEpoch = epoch
			continue
		}
		if !(ep.Valid < bestValid) {
			if epoch-rep.BestEpoch >= opts.Patience {
				break
			}
			continue
		}
		bestValid, rep.BestEpoch = ep.Valid, epoch
		best = m.Clone()
	}
	if len(held) > 0 {
		*m = *best
	}
	return rep, nil
}

func (m *Model) checkBag(b Bag) error {
	if len(b.Rows) == 0 || len(b.Rows) != len(b.Counts) {
		return fmt.Errorf("bag has %d rows and %d counts", len(b.Rows), len(b.Counts))
	}
	for i, t := range b.Rows {
		if t < 0 || int(t) >= m.Len() || i > 0 && t <= b.Rows[i-1] {
			return fmt.Errorf("bag row %d is out of range or out of order", t)
		}
		if !(b.Counts[i] > 0) || math.IsInf(b.Counts[i], 0) {
			return fmt.Errorf("bag row %d has count %v", t, b.Counts[i])
		}
	}
	return nil
}

func batchExamples(rng *rand.Rand, ex []Example, size int) [][]Example {
	if len(ex) == 0 {
		return nil
	}
	order := slices.Clone(ex)
	rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	var out [][]Example
	for lo := 0; lo < len(order); lo += size {
		out = append(out, order[lo:min(lo+size, len(order))])
	}
	return out
}

type trainer struct {
	m    *Model
	opts TrainOptions
	coef []float64
	adam *adam
}

// lossAndGrad is L1 over exb and L2 over pb, and the gradient of L1 + λ·L2.
func (tr *trainer) lossAndGrad(exb []Example, pb []Pair) (l1, l2 float64, g *grads) {
	var bags []Bag
	for _, ex := range exb {
		bags = append(bags, ex.Bag)
	}
	for _, p := range pb {
		bags = append(bags, p.Query)
	}
	for _, p := range pb {
		bags = append(bags, p.Doc)
	}
	s := tr.forward(bags)
	ds := make([][]float64, len(s))
	for i := range ds {
		ds[i] = make([]float64, tr.m.dims)
	}
	if len(exb) > 0 {
		y := make([][]float64, len(exb))
		for i, ex := range exb {
			y[i] = ex.Target
		}
		l1 = mseLoss(s[:len(exb)], y, ds[:len(exb)], tr.coef)
	}
	if len(pb) > 0 {
		q, d := len(exb), len(exb)+len(pb)
		l2 = mnrlLoss(s[q:d], s[d:], ds[q:d], ds[d:], tr.opts.Prefixes, tr.opts.Temperature, tr.opts.Lambda, tr.opts.Workers)
	}
	return l1, l2, tr.backward(bags, ds)
}

func (tr *trainer) validLoss(held []Example) float64 {
	var sum float64
	for lo := 0; lo < len(held); lo += tr.opts.MSEBatch {
		part := held[lo:min(lo+tr.opts.MSEBatch, len(held))]
		bags := make([]Bag, len(part))
		y := make([][]float64, len(part))
		for i, ex := range part {
			bags[i], y[i] = ex.Bag, ex.Target
		}
		sum += mseLoss(tr.forward(bags), y, nil, tr.coef) * float64(len(part))
	}
	return sum / float64(len(held))
}

func (tr *trainer) forward(bags []Bag) [][]float64 {
	s := make([][]float64, len(bags))
	parallel(len(bags), tr.opts.Workers, func(_, lo, hi int) {
		for i := lo; i < hi; i++ {
			s[i] = tr.m.Embed(bags[i])
		}
	})
	return s
}

func (tr *trainer) backward(bags []Bag, ds [][]float64) *grads {
	parts := make([]*grads, gradChunks)
	parallel(len(bags), tr.opts.Workers, func(c, lo, hi int) {
		g := newGrads(tr.m.dims)
		for i := lo; i < hi; i++ {
			tr.m.backward(g, bags[i], ds[i])
		}
		parts[c] = g
	})
	return reduce(tr.m.dims, slices.DeleteFunc(parts, func(g *grads) bool { return g == nil }))
}

// parallel splits [0, n) into gradChunks contiguous chunks and runs fn on each across
// at most workers goroutines. Chunk c always covers the same range for the same n.
func parallel(n, workers int, fn func(c, lo, hi int)) {
	next := make(chan int, gradChunks)
	for c := range gradChunks {
		next <- c
	}
	close(next)
	var wg sync.WaitGroup
	for range min(workers, gradChunks) {
		wg.Go(func() {
			for c := range next {
				lo, hi := c*n/gradChunks, (c+1)*n/gradChunks
				if lo < hi {
					fn(c, lo, hi)
				}
			}
		})
	}
	wg.Wait()
}

// adam is lazy Adam: a step updates the moments and parameters of only the rows the
// batch touched, with bias correction from the global step count.
type adam struct {
	t          int
	mVec, vVec []float64
	mB, vB     []float64
}

const (
	adamB1  = 0.9
	adamB2  = 0.999
	adamEps = 1e-8
)

func newAdam(m *Model) *adam {
	return &adam{
		mVec: make([]float64, len(m.vec)), vVec: make([]float64, len(m.vec)),
		mB: make([]float64, len(m.beta)), vB: make([]float64, len(m.beta)),
	}
}

func (a *adam) step(m *Model, g *grads, lr float64) {
	a.t++
	c1 := 1 - math.Pow(adamB1, float64(a.t))
	c2 := 1 - math.Pow(adamB2, float64(a.t))
	upd := func(p, mo, ve *float64, gr float64) {
		*mo = adamB1**mo + (1-adamB1)*gr
		*ve = adamB2**ve + (1-adamB2)*gr*gr
		*p -= lr * (*mo / c1) / (math.Sqrt(*ve/c2) + adamEps)
	}
	d := m.dims
	for k, t := range g.rows {
		base := int(t) * d
		for j, gr := range g.vec[k*d : (k+1)*d] {
			upd(&m.vec[base+j], &a.mVec[base+j], &a.vVec[base+j], gr)
		}
		upd(&m.beta[t], &a.mB[t], &a.vB[t], g.beta[k])
	}
}

func dot(a, b []float64) float64 {
	var s float64
	for i, x := range a {
		// The explicit conversion rounds the product, so no architecture fuses it
		// into the add and the table's bytes match everywhere.
		s = float64(x*b[i]) + s
	}
	return s
}

// softplus is log(1 + eˣ), computed without overflow.
func softplus(x float64) float64 {
	if x > 0 {
		return x + math.Log1p(math.Exp(-x))
	}
	return math.Log1p(math.Exp(x))
}

// softplusInv is log(eʷ − 1) for w > 0, computed without overflow.
func softplusInv(w float64) float64 {
	if w > 30 {
		return w + math.Log(-math.Expm1(-w))
	}
	return math.Log(math.Expm1(w))
}

func sigmoid(x float64) float64 {
	if x >= 0 {
		return 1 / (1 + math.Exp(-x))
	}
	e := math.Exp(x)
	return e / (1 + e)
}
