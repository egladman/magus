package check

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type TierSuite struct{ suite.Suite }

func TestTier(t *testing.T) { suite.Run(t, new(TierSuite)) }

// A tier nobody proved bounds nothing, so it must not rank as trivial.
func (s *TierSuite) TestAnUnknownTierRanksAsFull() {
	s.Equal(RiskFull.Rank(), Tier("").Rank())
	s.Equal(RiskFull.Rank(), Tier("bogus").Rank())
}

func (s *TierSuite) TestTiersRankLowestFirst() {
	s.Less(RiskTrivial.Rank(), RiskMechanical.Rank())
	s.Less(RiskMechanical.Rank(), RiskScoped.Rank())
	s.Less(RiskScoped.Rank(), RiskFull.Rank())
}

func (s *TierSuite) TestAPathLineNamesItsClass() {
	e := TierEvidence{Path: "a/a.go", Class: "code", Tier: RiskScoped, Why: "Go package fx/a"}
	s.Equal("a/a.go: scoped (code: Go package fx/a)", e.Line())
}

func (s *TierSuite) TestAGateStepLineNamesTheStep() {
	e := TierEvidence{Project: ".", Target: "buzz-test", Tier: RiskScoped, Why: "skipped"}
	s.Equal(". buzz-test: scoped (skipped)", e.Line())
}

func (s *TierSuite) TestALineWithNoSubjectIsTheTierAlone() {
	e := TierEvidence{Tier: RiskFull, Why: "module libs/x is no project's root"}
	s.Equal("full (module libs/x is no project's root)", e.Line())
}
