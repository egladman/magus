package tier

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type AssignmentSuite struct{ suite.Suite }

func TestAssignment(t *testing.T) { suite.Run(t, new(AssignmentSuite)) }

func (s *AssignmentSuite) TestTheForkLineNamesTheReasonAndSite() {
	tier := Assignment{Name: "routine", Source: SourceMatrix, Reason: "small", Site: "magusfile.buzz:20"}
	s.Equal(`routine (rank 0), matrix: "small" at magusfile.buzz:20`, tier.String())
}

func (s *AssignmentSuite) TestTheForkLineNamesAReasonAlone() {
	tier := Assignment{Name: "bounded", Rank: 1, Source: SourceDefault, Reason: "the declared default"}
	s.Equal(`bounded (rank 1), default: "the declared default"`, tier.String())
}

func (s *AssignmentSuite) TestTheForkLineNamesTheSourceAlone() {
	s.Equal("design (rank 2), job", Assignment{Name: "design", Rank: 2, Source: SourceJob}.String())
}

func (s *AssignmentSuite) TestTheForkLineSaysWhichTiersShareAnID() {
	tier := Assignment{Name: "frontier", Rank: 3, Source: SourceJob, Shared: []string{"codex runs design and frontier on one id, gpt-6-astra"}}
	s.Equal("frontier (rank 3), job; codex runs design and frontier on one id, gpt-6-astra", tier.String())
}
