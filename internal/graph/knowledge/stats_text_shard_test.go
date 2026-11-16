package knowledge

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type graphBuildShardSuite struct{ suite.Suite }

func TestGraphBuildShard(t *testing.T) { suite.Run(t, new(graphBuildShardSuite)) }

func (s *graphBuildShardSuite) TestNarrowedCoverageDoesNotFailASymbolRefresh() {
	s.NoError(GraphBuildShardFailure(s.T().Context(), []string{"@coverage: coverage profile omits 2 director(ies): run test unnarrowed"}))
}

func (s *graphBuildShardSuite) TestAnInvalidCoverageProfileStillFails() {
	s.ErrorContains(GraphBuildShardFailure(s.T().Context(), []string{"@coverage: invalid coverage mode"}), "invalid coverage mode")
}

func (s *graphBuildShardSuite) TestAnotherShardFailureIsNotHiddenByPartialCoverage() {
	err := GraphBuildShardFailure(s.T().Context(), []string{"@coverage: coverage profile omits 2 director(ies)", "@symbols: index unreadable"})
	s.ErrorContains(err, "@symbols: index unreadable")
}
