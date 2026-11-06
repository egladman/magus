package types

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type ChangeRiskSuite struct{ suite.Suite }

func TestChangeRiskSuite(t *testing.T) { suite.Run(t, new(ChangeRiskSuite)) }

func (s *ChangeRiskSuite) TestScoreOfAFullyCoveredFunctionIsItsComplexity() {
	s.InDelta(7.0, ChangeRiskScore(7, 1), 1e-9)
}

func (s *ChangeRiskSuite) TestScoreOfAnUncoveredFunctionIsComplexitySquaredPlusComplexity() {
	s.InDelta(30.0, ChangeRiskScore(5, 0), 1e-9)
}

func (s *ChangeRiskSuite) TestScoreAtHalfCoverageCubesTheMiss() {
	s.InDelta(4*0.125+2, ChangeRiskScore(2, 0.5), 1e-9)
}

func (s *ChangeRiskSuite) TestGapIsDecidedWhenItsWorstCaseIsWithinTheThreshold() {
	s.True(FunctionGap{Complexity: 2}.Decided(10))
}

func (s *ChangeRiskSuite) TestGapIsDecidedAtTheThresholdExactly() {
	s.True(FunctionGap{Complexity: 3}.Decided(12))
}

func (s *ChangeRiskSuite) TestGapIsOpenWhenItsWorstCaseExceedsTheThreshold() {
	s.False(FunctionGap{Complexity: 3}.Decided(10))
}

func (s *ChangeRiskSuite) TestGapWithUnknownComplexityDecidesNothing() {
	s.False(FunctionGap{Complexity: 0}.Decided(10))
}

func (s *ChangeRiskSuite) TestCoverageIsCoveredOverCoverable() {
	s.InDelta(0.75, ChangeRiskFunction{Covered: 3, Coverable: 4}.Coverage(), 1e-9)
}

func (s *ChangeRiskSuite) TestCoverageOfAFunctionWithNothingCoverableIsZero() {
	s.Zero(ChangeRiskFunction{Covered: 0, Coverable: 0}.Coverage())
}

func (s *ChangeRiskSuite) TestPastIsStrictlyAboveTheThreshold() {
	s.False(ChangeRiskFunction{Score: 10}.Past(10))
	s.True(ChangeRiskFunction{Score: 10.5}.Past(10))
}

func (s *ChangeRiskSuite) TestAShrunkFunctionNoWorseThanAtBaseIsNotCharged() {
	s.False(ChangeRiskFunction{Score: 20, BaseScore: 30}.Charged(10))
	s.False(ChangeRiskFunction{Score: 20, BaseScore: 20}.Charged(10))
}

func (s *ChangeRiskSuite) TestAFunctionWorseThanAtBaseAndPastTheThresholdIsCharged() {
	s.True(ChangeRiskFunction{Score: 20, BaseScore: 15}.Charged(10))
}

func (s *ChangeRiskSuite) TestAFunctionWithinTheThresholdIsNeverCharged() {
	s.False(ChangeRiskFunction{Score: 9, BaseScore: 1}.Charged(10))
}

func (s *ChangeRiskSuite) TestAFunctionWithNoBaseMeasureMeetsTheThresholdAlone() {
	s.True(ChangeRiskFunction{Score: 11, BaseReason: "it is new since base"}.Charged(10))
}
