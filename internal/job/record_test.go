package job

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
)

type jobRecordReadSuite struct {
	suite.Suite
}

func TestJobRecordRead(t *testing.T) { suite.Run(t, new(jobRecordReadSuite)) }

func (s *jobRecordReadSuite) record(source string) jobRecord {
	r := recordJob(types.Job{ID: "a", State: types.StateDeclared,
		ModelTier: &agent.JobModelTier{Name: "bounded", Source: agent.ModelTierSourceJob}}, nil)
	r.Status.ModelTier.Source = source
	return r
}

func (s *jobRecordReadSuite) TestAnUnlistedStoredSpellingIsRefused() {
	_, err := s.record("Row").job()

	s.Require().Error(err)
	s.ErrorContains(err, `job a: "Row" is not a stored ModelTierSource`)
}

func (s *jobRecordReadSuite) TestAListedSpellingReadsAsItsValue() {
	got, err := s.record(wireTierSource.spell(agent.ModelTierSourceMatrix)).job()

	s.Require().NoError(err)
	s.Equal(agent.ModelTierSourceMatrix, got.ModelTier.Source)
}

func (s *jobRecordReadSuite) TestAnEmptySpellingReadsEmpty() {
	got, err := s.record("").job()

	s.Require().NoError(err)
	s.Empty(got.ModelTier.Source)
}
