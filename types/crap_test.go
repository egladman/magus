package types

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type CrapSuite struct{ suite.Suite }

func TestCrapSuite(t *testing.T) { suite.Run(t, new(CrapSuite)) }

func (s *CrapSuite) TestScoreOfAFullyCoveredFunctionIsItsComplexity() {
	s.InDelta(7.0, CrapScore(7, 1), 1e-9)
}

func (s *CrapSuite) TestScoreOfAnUncoveredFunctionIsComplexitySquaredPlusComplexity() {
	s.InDelta(30.0, CrapScore(5, 0), 1e-9)
}

func (s *CrapSuite) TestScoreAtHalfCoverageCubesTheMiss() {
	s.InDelta(4*0.125+2, CrapScore(2, 0.5), 1e-9)
}

func (s *CrapSuite) TestGapIsDecidedWhenItsWorstCaseIsWithinTheThreshold() {
	s.True(FunctionGap{Complexity: 2}.Decided(10))
}

func (s *CrapSuite) TestGapIsDecidedAtTheThresholdExactly() {
	s.True(FunctionGap{Complexity: 3}.Decided(12))
}

func (s *CrapSuite) TestGapIsOpenWhenItsWorstCaseExceedsTheThreshold() {
	s.False(FunctionGap{Complexity: 3}.Decided(10))
}

func (s *CrapSuite) TestGapWithUnknownComplexityDecidesNothing() {
	s.False(FunctionGap{Complexity: 0}.Decided(10))
}

func (s *CrapSuite) TestCoverageIsCoveredOverCoverable() {
	s.InDelta(0.75, CrapFunction{Covered: 3, Coverable: 4}.Coverage(), 1e-9)
}

func (s *CrapSuite) TestCoverageOfAFunctionWithNothingCoverableIsZero() {
	s.Zero(CrapFunction{Covered: 0, Coverable: 0}.Coverage())
}

func (s *CrapSuite) TestPastIsStrictlyAboveTheThreshold() {
	s.False(CrapFunction{Score: 10}.Past(10))
	s.True(CrapFunction{Score: 10.5}.Past(10))
}
