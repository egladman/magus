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

func (s *ChangeRiskSuite) TestCoverageIsCoveredOverCoverable() {
	s.InDelta(0.75, ChangeRiskFunction{Covered: 3, Coverable: 4}.Coverage(), 1e-9)
}

func (s *ChangeRiskSuite) TestCoverageOfAFunctionWithNothingCoverableIsZero() {
	s.Zero(ChangeRiskFunction{Covered: 0, Coverable: 0}.Coverage())
}

