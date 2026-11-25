package loop

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/suite"
)

type PlanSuite struct{ suite.Suite }

func TestLoopPlan(t *testing.T) { suite.Run(t, new(PlanSuite)) }

func (s *PlanSuite) plan(layers, start, end, passes int) Plan {
	p, err := NewPlan(layers, start, end, passes)
	s.Require().NoError(err)
	return p
}

func (s *PlanSuite) TestSourceRepeatsTheLoopBlock() {
	p := s.plan(6, 2, 4, 3)
	s.Equal(10, p.Applications())
	var got []int
	for i := range p.Applications() {
		src, err := p.Source(i)
		s.Require().NoError(err)
		got = append(got, src)
	}
	s.Equal([]int{0, 1, 2, 3, 2, 3, 2, 3, 4, 5}, got)
}

func (s *PlanSuite) TestTargetsInvertSource() {
	p := s.plan(6, 2, 4, 3)
	for src := range p.Layers() {
		targets, err := p.Targets(src)
		s.Require().NoError(err)
		for _, t := range targets {
			back, err := p.Source(t)
			s.Require().NoError(err)
			s.Equal(src, back)
		}
	}
	got, err := p.Targets(3)
	s.Require().NoError(err)
	s.Equal([]int{3, 5, 7}, got)
	got, err = p.Targets(5)
	s.Require().NoError(err)
	s.Equal([]int{9}, got)
}

func (s *PlanSuite) TestOnePassIsTheIdentity() {
	p := s.plan(6, 2, 4, 1)
	s.Equal(6, p.Applications())
	for i := range 6 {
		src, err := p.Source(i)
		s.Require().NoError(err)
		s.Equal(i, src)
	}
}

func (s *PlanSuite) TestRefusesZeroPasses() {
	_, err := NewPlan(6, 2, 4, 0)
	s.ErrorIs(err, ErrPasses)
}

func (s *PlanSuite) TestRefusesAnEmptyLoop() {
	_, err := NewPlan(6, 3, 3, 2)
	s.ErrorIs(err, ErrEmptyLoop)
}

func (s *PlanSuite) TestRefusesALoopPastTheLastLayer() {
	_, err := NewPlan(6, 2, 7, 2)
	s.ErrorIs(err, ErrPastEnd)
}

func (s *PlanSuite) TestRefusesNoPrelude() {
	_, err := NewPlan(6, 0, 4, 2)
	s.ErrorIs(err, ErrNoPrelude)
}

func (s *PlanSuite) TestRefusesNoCoda() {
	_, err := NewPlan(6, 2, 6, 2)
	s.ErrorIs(err, ErrNoCoda)
}

func (s *PlanSuite) TestIndexOutOfRange() {
	p := s.plan(6, 2, 4, 3)
	_, err := p.Source(10)
	s.ErrorIs(err, ErrIndex)
	_, err = p.Source(-1)
	s.ErrorIs(err, ErrIndex)
	_, err = p.Targets(6)
	s.ErrorIs(err, ErrIndex)
}

func (s *PlanSuite) TestTheZeroPlanUnrollsNothing() {
	dir := s.T().TempDir()
	err := UnrollSafetensors(filepath.Join(dir, "out"), filepath.Join(dir, "in"), Plan{})
	s.ErrorIs(err, ErrPasses)
}

// ForwardSuite runs a toy residual stack, h = h + tanh(W_l h + b_l). Looping the shared
// block in memory must match walking the unrolled file bit for bit. That equality is what
// makes the unrolled file weight sharing.
type ForwardSuite struct{ suite.Suite }

func TestLoopForward(t *testing.T) { suite.Run(t, new(ForwardSuite)) }

const toyDim = 5

type toyLayer struct{ w, b []float64 }

func (l toyLayer) apply(h []float64) []float64 {
	out := make([]float64, len(h))
	for r := range h {
		acc := l.b[r]
		for c, x := range h {
			acc += l.w[r*len(h)+c] * x
		}
		out[r] = h[r] + math.Tanh(acc)
	}
	return out
}

func toyLayers(n int) []toyLayer {
	rng := rand.New(rand.NewPCG(7, 11))
	out := make([]toyLayer, n)
	for i := range out {
		out[i] = toyLayer{w: make([]float64, toyDim*toyDim), b: make([]float64, toyDim)}
		for j := range out[i].w {
			out[i].w[j] = rng.NormFloat64() / toyDim
		}
		for j := range out[i].b {
			out[i].b[j] = rng.NormFloat64() / 10
		}
	}
	return out
}

func f64Bytes(v []float64) []byte {
	out := make([]byte, 8*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint64(out[8*i:], math.Float64bits(x))
	}
	return out
}

func f64s(b []byte) []float64 {
	out := make([]float64, len(b)/8)
	for i := range out {
		out[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[8*i:]))
	}
	return out
}

func looped(layers []toyLayer, p Plan, h []float64) []float64 {
	for l := range p.Start() {
		h = layers[l].apply(h)
	}
	for range p.Passes() {
		for l := p.Start(); l < p.End(); l++ {
			h = layers[l].apply(h)
		}
	}
	for l := p.End(); l < p.Layers(); l++ {
		h = layers[l].apply(h)
	}
	return h
}

func (s *ForwardSuite) TestLoopedEqualsUnrolled() {
	p, err := NewPlan(6, 2, 4, 3)
	s.Require().NoError(err)
	layers := toyLayers(p.Layers())
	var tensors []stTensor
	for i, l := range layers {
		prefix := layerPrefix + strconv.Itoa(i) + "."
		tensors = append(tensors,
			stTensor{name: prefix + "mlp.weight", dtype: "F64", shape: []int64{toyDim, toyDim}, data: f64Bytes(l.w)},
			stTensor{name: prefix + "mlp.bias", dtype: "F64", shape: []int64{toyDim}, data: f64Bytes(l.b)})
	}
	dir := s.T().TempDir()
	src, dst := filepath.Join(dir, "src.safetensors"), filepath.Join(dir, "dst.safetensors")
	s.Require().NoError(writeTestSafetensors(src, tensors, nil))
	s.Require().NoError(UnrollSafetensors(dst, src, p))

	got := readTestSafetensors(s.T(), dst)
	h0 := []float64{0.3, -1.2, 0.7, 2.0, -0.4}
	h := h0
	for i := range p.Applications() {
		prefix := layerPrefix + strconv.Itoa(i) + "."
		h = toyLayer{w: f64s(got[prefix+"mlp.weight"].data), b: f64s(got[prefix+"mlp.bias"].data)}.apply(h)
	}
	want := looped(layers, p, h0)
	s.Len(h, len(h0))
	s.Equal(want, h)

	once, err := NewPlan(6, 2, 4, 1)
	s.Require().NoError(err)
	s.NotEqual(looped(layers, once, h0), h, "three passes must differ from one, or the loop did not run")
}
