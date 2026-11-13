package job

import (
	"bytes"
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

func (s *jobRecordReadSuite) TestAFlatRowSourceMigratesToJob() {
	st := tempStore(s.T(), s.T().TempDir())
	st.notices, st.migrations = &bytes.Buffer{}, jobMigrations
	plantRaw(s.T(), st, `{"api_version":"magus.job/v1alpha1"}`+"\n"+
		`{"id":"a","state":"running","created":1,"updated":1,"model_tier":{"name":"bounded","rank":1,"skills":"short","source":"row"}}`)

	f, err := st.read()

	s.Require().NoError(err)
	s.Require().Len(f.Jobs, 1)
	s.Equal(&agent.JobModelTier{Name: "bounded", Rank: 1, Skills: agent.SkillVariantShort, Source: agent.ModelTierSourceJob}, f.Jobs[0].ModelTier)
}

func (s *jobRecordReadSuite) TestAnEmptySpellingReadsEmpty() {
	got, err := s.record("").job()

	s.Require().NoError(err)
	s.Empty(got.ModelTier.Source)
}
