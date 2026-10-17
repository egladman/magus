package distill

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/textembed"
	"github.com/egladman/magus/internal/textrank"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gradTol bounds |analytic − numeric| / max(1, |analytic|, |numeric|) for every
// parameter. Central differences at gradEps leave a truncation error near eps²·f”'
// and a rounding error near 1e-16·|f|/eps, both far below it.
const (
	gradEps = 1e-6
	gradTol = 1e-7
)

func randomModel(t *testing.T, rng *rand.Rand, tokens, dims int) *Model {
	t.Helper()
	dirs := make([][]float64, tokens)
	weights := make([]float64, tokens)
	for i := range dirs {
		dirs[i] = make([]float64, dims)
		for j := range dirs[i] {
			dirs[i][j] = rng.NormFloat64()
		}
		weights[i] = 0.2 + rng.Float64()
	}
	m, err := NewModel(dirs, weights)
	require.NoError(t, err)
	// Unit directions are only the start; scale rows apart so the check sees varied norms.
	for i := range m.vec {
		m.vec[i] *= 0.5 + rng.Float64()
	}
	return m
}

// randomBag draws distinct rows from [0, tokens−1), leaving the last token untouched so
// the check also sees a zero gradient.
func randomBag(rng *rand.Rand, tokens int) Bag {
	var b Bag
	for r := range tokens - 1 {
		if rng.IntN(2) == 0 || r == 0 && rng.IntN(2) == 0 {
			continue
		}
		b.Rows = append(b.Rows, int32(r))
		b.Counts = append(b.Counts, float64(1+rng.IntN(3)))
	}
	if len(b.Rows) == 0 {
		b.Rows, b.Counts = []int32{int32(rng.IntN(tokens - 1))}, []float64{2}
	}
	return b
}

func randomExamples(rng *rand.Rand, n, tokens, dims int) []Example {
	out := make([]Example, n)
	for i := range out {
		y := make([]float64, dims)
		for j := range y {
			y[j] = 0.3 * rng.NormFloat64()
		}
		out[i] = Example{ID: fmt.Sprint(i), Bag: randomBag(rng, tokens), Target: y}
	}
	return out
}

func randomPairs(rng *rand.Rand, n, tokens int) []Pair {
	out := make([]Pair, n)
	for i := range out {
		out[i] = Pair{Query: randomBag(rng, tokens), Doc: randomBag(rng, tokens)}
	}
	return out
}

// checkGradient compares lossAndGrad's gradient of L1 + λ·L2 with central differences
// over every direction entry and every β, and returns the largest scaled error.
func checkGradient(t *testing.T, m *Model, ex []Example, pairs []Pair, opts TrainOptions) float64 {
	t.Helper()
	tr := &trainer{m: m, opts: opts, coef: prefixCoef(m.dims, opts.Prefixes)}
	loss := func() float64 {
		l1, l2, _ := tr.lossAndGrad(ex, pairs)
		return l1 + opts.Lambda*l2
	}
	_, _, g := tr.lossAndGrad(ex, pairs)
	analyticVec := make([]float64, len(m.vec))
	analyticBeta := make([]float64, len(m.beta))
	for k, r := range g.rows {
		copy(analyticVec[int(r)*m.dims:], g.vec[k*m.dims:(k+1)*m.dims])
		analyticBeta[r] = g.beta[k]
	}
	var worst float64
	check := func(name string, p *float64, analytic float64) {
		orig := *p
		*p = orig + gradEps
		up := loss()
		*p = orig - gradEps
		down := loss()
		*p = orig
		numeric := (up - down) / (2 * gradEps)
		e := math.Abs(analytic-numeric) / max(1, math.Abs(analytic), math.Abs(numeric))
		worst = max(worst, e)
		assert.LessOrEqual(t, e, gradTol, "%s: analytic %.12g, numeric %.12g", name, analytic, numeric)
	}
	for i := range m.vec {
		check(fmt.Sprintf("e[%d][%d]", i/m.dims, i%m.dims), &m.vec[i], analyticVec[i])
	}
	for i := range m.beta {
		check(fmt.Sprintf("beta[%d]", i), &m.beta[i], analyticBeta[i])
	}
	return worst
}

func gradOptions(dims int, prefixes []int, tau, lambda float64) TrainOptions {
	return TrainOptions{
		Seed: 1, Epochs: 1, LearningRate: 1e-2, MSEBatch: 64, PairBatch: 64,
		Lambda: lambda, Prefixes: prefixes, Temperature: tau, Workers: 3, Patience: 1,
	}
}

func TestTrainMSEGradientMatchesFiniteDifferences(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 0))
	m := randomModel(t, rng, 7, 8)
	ex := randomExamples(rng, 5, 7, 8)
	worst := checkGradient(t, m, ex, nil, gradOptions(8, []int{2, 4, 8}, 0.05, 0))
	t.Logf("worst scaled error %.3g", worst)
}

func TestTrainJointGradientMatchesFiniteDifferences(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 0))
	m := randomModel(t, rng, 8, 8)
	ex := randomExamples(rng, 4, 8, 8)
	pairs := randomPairs(rng, 4, 8)
	worst := checkGradient(t, m, ex, pairs, gradOptions(8, []int{4, 8}, 0.05, 0.5))
	t.Logf("worst scaled error %.3g", worst)
}

func TestTrainMSELossOnKnownValues(t *testing.T) {
	// One dim of error 1 in dim 0 counts once per prefix: 1/2 + 1/4.
	s := [][]float64{{1, 0, 0, 0}}
	y := [][]float64{{0, 0, 0, 0}}
	assert.InDelta(t, 0.75, mseLoss(s, y, nil, prefixCoef(4, []int{2, 4})), 1e-15)
}

func TestTrainSoftplusInverts(t *testing.T) {
	for _, w := range []float64{1e-8, 1e-4, 0.3, 1, 7, 29.9, 30.1, 400} {
		assert.InEpsilon(t, w, softplus(softplusInv(w)), 1e-12, "w=%v", w)
	}
	assert.InDelta(t, 0.5, sigmoid(0), 0)
	assert.False(t, math.IsNaN(sigmoid(-800)) || math.IsNaN(sigmoid(800)))
}

func TestTrainRefusesBadOptions(t *testing.T) {
	rng := rand.New(rand.NewPCG(14, 0))
	m := randomModel(t, rng, 5, 8)
	ex := randomExamples(rng, 3, 5, 8)
	pairs := randomPairs(rng, 3, 5)
	good := gradOptions(8, []int{4, 8}, 0.05, 0.5)
	for _, tc := range []struct {
		name  string
		edit  func(*TrainOptions)
		pairs []Pair
		want  string
	}{
		{"pairs without lambda", func(o *TrainOptions) { o.Lambda = 0 }, pairs, "lambda is 0"},
		{"pair batch of one", func(o *TrainOptions) { o.PairBatch = 1 }, pairs, "no negative"},
		{"prefixes short of dims", func(o *TrainOptions) { o.Prefixes = []int{4} }, nil, "must end"},
		{"prefixes descending", func(o *TrainOptions) { o.Prefixes = []int{6, 4, 8} }, nil, "ascending"},
		{"no workers", func(o *TrainOptions) { o.Workers = 0 }, nil, "workers"},
		{"no epochs", func(o *TrainOptions) { o.Epochs = 0 }, nil, "epochs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := good
			tc.edit(&o)
			_, err := Train(m.Clone(), ex, tc.pairs, o)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	bad := []Example{{ID: "x", Bag: Bag{Rows: []int32{9}, Counts: []float64{1}}, Target: make([]float64, 8)}}
	_, err := Train(m.Clone(), bad, nil, good)
	assert.ErrorContains(t, err, "out of range")
	_, err = Train(m.Clone(), nil, []Pair{pairs[0], pairs[0], pairs[0]}, good)
	assert.ErrorContains(t, err, "has no batch")
}

func TestTrainLossFallsAndIsBitIdentical(t *testing.T) {
	rng := rand.New(rand.NewPCG(15, 0))
	m := randomModel(t, rng, 40, 16)
	ex := randomExamples(rng, 300, 40, 16)
	pairs := randomPairs(rng, 120, 40)
	opts := gradOptions(16, []int{8, 16}, 0.1, 0.25)
	opts.Epochs, opts.MSEBatch, opts.PairBatch = 6, 32, 16

	run := func(workers int) (*Model, Report) {
		o := opts
		o.Workers = workers
		mm := m.Clone()
		rep, err := Train(mm, ex, pairs, o)
		require.NoError(t, err)
		return mm, rep
	}
	a, rep := run(6)
	b, _ := run(6)
	c, _ := run(1)
	first, last := rep.Epochs[0], rep.Epochs[len(rep.Epochs)-1]
	t.Logf("epochs %d, loss %.5f -> %.5f, held %d, best epoch %d", len(rep.Epochs), first.Loss, last.Loss, rep.Held, rep.BestEpoch)
	assert.Less(t, last.Loss, first.Loss)
	assert.Less(t, last.MNRL, first.MNRL)
	assert.Equal(t, 300, rep.Train+rep.Held)
	assert.Positive(t, rep.Held)
	assert.True(t, bitEqual(a, b), "two runs with one seed differ")
	assert.True(t, bitEqual(a, c), "one worker and six workers differ")

	o := opts
	o.Seed = 2
	d := m.Clone()
	_, err := Train(d, ex, pairs, o)
	require.NoError(t, err)
	assert.False(t, bitEqual(a, d), "another seed trained the same model")
}

func bitEqual(a, b *Model) bool {
	if len(a.vec) != len(b.vec) || len(a.beta) != len(b.beta) {
		return false
	}
	for i := range a.vec {
		if math.Float64bits(a.vec[i]) != math.Float64bits(b.vec[i]) {
			return false
		}
	}
	for i := range a.beta {
		if math.Float64bits(a.beta[i]) != math.Float64bits(b.beta[i]) {
			return false
		}
	}
	return true
}

func TestTrainEarlyStoppingKeepsTheBestEpoch(t *testing.T) {
	// A learning rate this high overshoots, so the held-out MSE rises within a few
	// epochs and training stops there with the best epoch's parameters.
	rng := rand.New(rand.NewPCG(16, 0))
	m := randomModel(t, rng, 30, 8)
	ex := randomExamples(rng, 400, 30, 8)
	opts := gradOptions(8, []int{8}, 0.05, 0)
	opts.Epochs, opts.MSEBatch, opts.LearningRate = 40, 400, 0.5
	rep, err := Train(m, ex, nil, opts)
	require.NoError(t, err)
	require.Less(t, len(rep.Epochs), 40, "training never stopped early")
	best := rep.Epochs[rep.BestEpoch].Valid
	assert.Equal(t, len(rep.Epochs)-2, rep.BestEpoch)
	assert.GreaterOrEqual(t, rep.Epochs[len(rep.Epochs)-1].Valid, best)

	var held []Example
	for _, e := range ex {
		if IDHash(validSalt, e.ID)%validModulus == 0 {
			held = append(held, e)
		}
	}
	tr := &trainer{m: m, opts: opts, coef: prefixCoef(8, opts.Prefixes)}
	assert.Equal(t, math.Float64bits(best), math.Float64bits(tr.validLoss(held)), "the model is not the best epoch's")
}

func TestTrainFitScaleFindsTheLeastSquaresScale(t *testing.T) {
	rng := rand.New(rand.NewPCG(18, 0))
	m := randomModel(t, rng, 10, 8)
	ex := randomExamples(rng, 30, 10, 8)
	// Targets along the student's own embeddings, so a positive scale exists.
	for i := range ex {
		s := m.Embed(ex[i].Bag)
		for j := range s {
			ex[i].Target[j] = 3*s[j] + 0.01*ex[i].Target[j]
		}
	}
	w0 := []float64{m.Weight(0), m.Weight(1)}
	tr := &trainer{m: m, opts: gradOptions(8, []int{8}, 1, 0), coef: prefixCoef(8, []int{8})}
	alpha, err := m.FitScale(ex)
	require.NoError(t, err)
	assert.InDelta(t, 3, alpha, 0.05)
	assert.InEpsilon(t, w0[0]/w0[1], m.Weight(0)/m.Weight(1), 1e-9, "the weights' ratios moved")
	at := tr.validLoss(ex)
	for _, k := range []float64{0.999, 1.001} {
		mm := m.Clone()
		for i := range mm.beta {
			mm.beta[i] = softplusInv(k * softplus(mm.beta[i]))
		}
		tr.m = mm
		assert.Greater(t, tr.validLoss(ex), at, "scale x%v beats the least-squares one", k)
	}

	for i := range ex {
		for j := range ex[i].Target {
			ex[i].Target[j] = -ex[i].Target[j]
		}
	}
	_, err = m.FitScale(ex)
	assert.ErrorContains(t, err, "do not point toward the targets")
}

func TestTrainNewModelRefusesZeroDirections(t *testing.T) {
	_, err := NewModel([][]float64{{0, 0}}, []float64{1})
	assert.ErrorContains(t, err, "norm")
	_, err = NewModel([][]float64{{1, 0}}, []float64{0})
	assert.ErrorContains(t, err, "not positive")
	m, err := NewModel([][]float64{{3, 4}}, []float64{0.25})
	require.NoError(t, err)
	assert.InDeltaSlice(t, []float64{0.6, 0.8}, m.Direction(0), 1e-15)
	assert.InDelta(t, 0.25, m.Weight(0), 1e-15)
}

// toyTeacher is a nonlinear stand-in for a transformer: a text embeds to the
// normalized tanh of its tokens' hidden vectors weighed by a hidden importance, so
// the student has both directions and weights to learn.
type toyTeacher struct {
	hidden     map[string][]float64
	importance map[string]float64
}

func newToyTeacher(rng *rand.Rand, words []string, dims int) *toyTeacher {
	tt := &toyTeacher{hidden: map[string][]float64{}, importance: map[string]float64{}}
	for _, w := range words {
		h := make([]float64, dims)
		for j := range h {
			h[j] = rng.NormFloat64()
		}
		tt.hidden[w], tt.importance[w] = h, 0.1+3*rng.Float64()
	}
	return tt
}

func (tt *toyTeacher) embed(text string) []float32 {
	var dims int
	for _, h := range tt.hidden {
		dims = len(h)
		break
	}
	acc := make([]float64, dims)
	for _, w := range strings.Fields(text) {
		for j, x := range tt.hidden[w] {
			acc[j] += tt.importance[w] * x
		}
	}
	var n float64
	for j := range acc {
		acc[j] = math.Tanh(0.3 * acc[j])
		n += acc[j] * acc[j]
	}
	out := make([]float32, dims)
	for j := range acc {
		out[j] = float32(acc[j] / math.Sqrt(n))
	}
	return out
}

// TestTrainDistillsAToyTeacher runs the whole pipeline on a toy: vocabulary, the
// teacher over every token, PCA fitted on the texts' targets, SIF init, joint training,
// then the int8 table, read back and embedded by the runtime.
func TestTrainDistillsAToyTeacher(t *testing.T) {
	rng := rand.New(rand.NewPCG(41, 0))
	words := make([]string, 80)
	for i := range words {
		words[i] = fmt.Sprintf("w%03d", i)
	}
	teacher := newToyTeacher(rng, words, 96)
	docs, pairText := toyTexts(rng, words)
	v, err := BuildVocab(docs, VocabOptions{MinCount: 2, MaxBytes: 32, Size: 64, Forced: []string{"w079"}})
	require.NoError(t, err)
	m, pca, targets := toyStudent(t, v, teacher, docs)

	ex, empty, err := Examples(v, docs, func(id string) ([]float64, bool) {
		y, ok := targets[id]
		if !ok {
			return nil, false
		}
		return pca.Project(y), true
	})
	require.NoError(t, err)
	assert.Less(t, empty, 15, "too many toy texts hold no vocabulary token")
	pairs, _, err := BagPairs(v, pairText)
	require.NoError(t, err)

	opts := DefaultTrainOptions(64)
	opts.Seed, opts.Epochs, opts.Patience = 5, 40, 3
	opts.MSEBatch, opts.PairBatch, opts.LearningRate = 64, 32, 3e-3
	// The toy's MSE runs about 300 times below its MNRL; at the design's λ = 0.5 the
	// MNRL gradient swamps L1 under Adam and the held-out MSE only rises.
	opts.Lambda = 0.002
	assert.Equal(t, []int{64}, opts.Prefixes)
	tr := &trainer{m: m, opts: opts, coef: prefixCoef(64, opts.Prefixes)}
	sifScale := tr.validLoss(ex)
	alpha, err := m.FitScale(ex)
	require.NoError(t, err)
	before := tr.validLoss(ex)
	assert.Less(t, before, sifScale)

	rep, err := Train(m, ex, pairs, opts)
	require.NoError(t, err)
	after := tr.validLoss(ex)
	first, best := rep.Epochs[0], rep.Epochs[rep.BestEpoch]
	t.Logf("MSE: SIF scale %.5f, x%.2f %.5f, trained %.5f; held-out %.5f -> %.5f; MNRL %.3f -> %.3f; %d epochs, best %d",
		sifScale, alpha, before, after, first.Valid, best.Valid, first.MNRL, best.MNRL, len(rep.Epochs), rep.BestEpoch)
	assert.Less(t, after, 0.75*before, "training cut the base distill's MSE by less than a quarter")
	assert.Less(t, best.Valid, first.Valid)
	assert.Less(t, best.MNRL, 0.6*first.MNRL)
	assertTableMatchesStudent(t, m, v, docs[:200])
}

// toyTexts draws 1500 texts of 3 to 12 words, skewed toward low indexes, and pairs
// every third text with its first two words as the query.
func toyTexts(rng *rand.Rand, words []string) (textsFixture, pairsFixture) {
	var docs textsFixture
	var pairs pairsFixture
	for i := range 1500 {
		var ws []string
		for range 3 + rng.IntN(10) {
			// Squaring skews the draw toward low indexes, a rough Zipf.
			f := rng.Float64()
			ws = append(ws, words[int(f*f*float64(len(words)))])
		}
		body := strings.Join(ws, " ")
		docs = append(docs, Text{ID: fmt.Sprint("t", i), Body: body})
		if i%3 == 0 {
			pairs = append(pairs, TextPair{Source: "toy", Query: strings.Join(ws[:2], " "), Doc: body})
		}
	}
	return docs, pairs
}

// toyStudent embeds every text with teacher, fits a 64-dim PCA on a hash sample of
// those targets, and starts the student from the projected token vectors with count
// SIF weights. targets maps each text id to its teacher vector.
func toyStudent(t *testing.T, v *Vocab, teacher *toyTeacher, docs textsFixture) (*Model, *PCA, map[string][]float32) {
	t.Helper()
	targets := map[string][]float32{}
	ids := make([]string, len(docs))
	for i, c := range docs {
		targets[c.ID], ids[i] = teacher.embed(c.Body), c.ID
	}
	var sample [][]float32
	for _, i := range SampleByHash(ids, 1000, "pca/v1:") {
		sample = append(sample, targets[ids[i]])
	}
	pca, err := FitPCA(sample, 64)
	require.NoError(t, err)

	dirs := make([][]float64, v.Len())
	for i, tok := range v.Tokens {
		dirs[i] = pca.Project(teacher.embed(tok))
	}
	sif, err := CountSIF(v, 1e-3)
	require.NoError(t, err)
	m, err := NewModel(dirs, sif)
	require.NoError(t, err)
	return m, pca, targets
}

// assertTableMatchesStudent writes m as the int8 table, reads it back, and checks that
// the runtime's embedding of each doc stays within cosine 0.99 of the student's.
func assertTableMatchesStudent(t *testing.T, m *Model, v *Vocab, docs textsFixture) {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, WriteTable(&buf, m, v, testProvenance()))
	tbl, err := textembed.ReadTable(buf.Bytes())
	require.NoError(t, err)
	var worst float64 = 1
	for _, c := range docs {
		q, ok := tbl.Embed([]textembed.Field{{Tokens: textrank.Tokens(c.Body), Weight: 1}})
		require.True(t, ok)
		worst = min(worst, cosine8(q.Q, m.Embed(v.Bag(c.Body))))
	}
	t.Logf("table %d bytes, digest %s, worst runtime-vs-student cosine %.5f", buf.Len(), tbl.Digest(), worst)
	assert.GreaterOrEqual(t, worst, 0.99)
}
