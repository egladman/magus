package job

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/tier"
)

type jobRecordReadSuite struct {
	suite.Suite
}

func TestJobRecordRead(t *testing.T) { suite.Run(t, new(jobRecordReadSuite)) }

func (s *jobRecordReadSuite) TestAFlatRowSourceMigratesToJob() {
	st := tempStore(s.T(), s.T().TempDir())
	st.notices = &bytes.Buffer{}
	plantRaw(s.T(), st, `{"api_version":"safetyscissors.job/v1alpha1"}`+"\n"+
		`{"id":"a","state":"running","created":1,"updated":1,"model_tier":{"name":"bounded","rank":1,"skills":"short","source":"row"}}`)

	f, err := st.Read()

	s.Require().NoError(err)
	s.Require().Len(f.Jobs, 1)
	s.Equal(&tier.Assignment{Name: "bounded", Rank: 1, Skills: tier.SkillVariantShort, Source: tier.SourceJob}, f.Jobs[0].ModelTier)
}
