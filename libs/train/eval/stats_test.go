package eval

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type StatsSuite struct{ suite.Suite }

func TestStats(t *testing.T) { suite.Run(t, new(StatsSuite)) }

func (s *StatsSuite) TestFillGivesTheMeanAndItsTInterval() {
	u := NewUnits()
	for i, v := range []float64{1, 0, 1, 1, 0, 1, 1, 1} {
		u.Add(string(rune('a'+i)), "g", v)
	}
	var r Row
	u.Fill(&r)
	s.Require().NotNil(r.Value)
	s.InDelta(0.75, *r.Value, 1e-9)
	s.Require().NotNil(r.Low)
	s.Less(*r.Low, 0.75)
	s.Greater(*r.High, 0.75)
	s.Equal(8, r.N)
	s.Equal(1, r.Groups)
}

func (s *StatsSuite) TestFillWithOneUnitHasNoInterval() {
	u := NewUnits()
	u.Add("a", "g", 1)
	var r Row
	u.Fill(&r)
	s.Nil(r.Low)
	s.Equal("fewer than 2 units", r.Interval)
	var none Row
	NewUnits().Fill(&none)
	s.Nil(none.Value)
	s.NotEmpty(none.NotScored)
}

func (s *StatsSuite) TestPValueClustersByGroupAndIsFixedBySeed() {
	a, b := NewUnits(), NewUnits()
	for i := range 40 {
		id := string(rune('A' + i))
		g := string(rune('a' + i/4))
		a.Add(id, g, 1)
		b.Add(id, g, 0)
	}
	p := a.PValue(b)
	s.Require().NotNil(p)
	s.Equal(*p, *a.PValue(b))
	s.Less(*p, 0.01)
	s.Nil(a.PValue(NewUnits()))
}

func (s *StatsSuite) TestJackknifeDeletesOneGroupAtATime() {
	groups := []string{"a", "a", "b", "b", "c", "c", "d", "d"}
	vals := []float64{1, 1, 2, 2, 3, 3, 4, 4}
	mean := func(keep []int) float64 {
		var t float64
		for _, i := range keep {
			t += vals[i]
		}
		return t / float64(len(keep))
	}
	est, pseudo := Jackknife(groups, mean)
	s.InDelta(2.5, est, 1e-9)
	s.Len(pseudo.Value, 4)
	// For a mean, the pseudo-value of a group is that group's own mean.
	s.InDelta(1, pseudo.Value["a"], 1e-9)
	s.InDelta(4, pseudo.Value["d"], 1e-9)
	var r Row
	JackknifeRow(&r, est, pseudo)
	s.InDelta(2.5, *r.Value, 1e-9)
	s.NotNil(r.Low)
}

func (s *StatsSuite) TestQuantileInterpolates() {
	s.InDelta(2.5, Quantile([]float64{4, 1, 3, 2}, 0.5), 1e-9)
	s.InDelta(3.85, Quantile([]float64{1, 2, 3, 4}, 0.95), 1e-9)
}
