package eval

import (
	"math"
	"testing"

	"github.com/stretchr/testify/suite"
)

type PassKSuite struct{ suite.Suite }

func TestPassK(t *testing.T) { suite.Run(t, new(PassKSuite)) }

func (s *PassKSuite) TestEstimatorMatchesTheClosedForm() {
	s.Equal(0.0, PassAtK(20, 0, 1))
	s.Equal(1.0, PassAtK(20, 20, 10))
	s.InDelta(0.25, PassAtK(20, 5, 1), 1e-12)
	// 1 - C(15,5)/C(20,5)
	s.InDelta(1-3003.0/15504.0, PassAtK(20, 5, 5), 1e-12)
	s.Equal(1.0, PassAtK(20, 16, 5))
	s.Equal(0.0, PassAtK(5, 1, 6))
	s.False(math.IsNaN(PassAtK(20, 3, 10)))
}

func (s *PassKSuite) TestAllPassNeedsEveryNamedTestOk() {
	out := "ok    test \"a\"\nFAIL  test \"b\"\n      std.assert: x\n---\n1 passed, 1 failed, 0 skipped\n"
	s.True(AllPass(out, []string{"a"}))
	s.False(AllPass(out, []string{"a", "b"}))
	s.False(AllPass("[error] boom", []string{"a"}))
	s.False(AllPass(out, nil))
}

func (s *PassKSuite) TestSeatbeltDeniesNetworkAndLimitsWrites() {
	p := Seatbelt("/scratch", "/home/u")
	s.Contains(p, "(deny network*)")
	s.Contains(p, `(subpath "/scratch")`)
	s.Contains(p, `"/home/u/.cache/magus/buzz-bytecode"`)
}
