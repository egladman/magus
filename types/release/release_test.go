package release

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type StageSuite struct{ suite.Suite }

func TestStageSuite(t *testing.T) { suite.Run(t, new(StageSuite)) }

func (s *StageSuite) TestNoTagIsUnknownNeverPreOneByGuess() {
	s.False(Stage{}.Known())
}

func (s *StageSuite) TestATagIsKnown() {
	s.True(Stage{Tag: "v0.4.0"}.Known())
}
