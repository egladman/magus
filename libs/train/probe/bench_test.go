package probe

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type BenchSuite struct{ suite.Suite }

func TestBench(t *testing.T) { suite.Run(t, new(BenchSuite)) }

func (s *BenchSuite) TestParseLlamaBenchNamesEachTest() {
	got, err := ParseLlamaBench([]byte(`[{"n_prompt":512,"n_gen":0,"avg_ts":900.5},{"n_prompt":0,"n_gen":128,"avg_ts":60.25}]`))
	s.Require().NoError(err)
	s.Require().Len(got, 2)
	s.Equal("pp512", got[0].Test)
	s.Equal("tg128", got[1].Test)
	s.InDelta(60.25, got[1].AvgTS, 1e-9)
}

func (s *BenchSuite) TestParseLlamaBenchRefusesAnEmptyArray() {
	_, err := ParseLlamaBench([]byte(`[]`))
	s.ErrorContains(err, "no tests")
}
