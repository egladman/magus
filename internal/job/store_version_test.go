package job

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/types/agent"
)

type modelTierSourceJobSuite struct {
	suite.Suite
}

func TestModelTierSourceJobSuite(t *testing.T) { suite.Run(t, new(modelTierSourceJobSuite)) }

func (s *modelTierSourceJobSuite) entry(text string) migrate.Entry {
	var e migrate.Entry
	s.Require().NoError(json.Unmarshal([]byte(text), &e))
	return e
}

func (s *modelTierSourceJobSuite) source(e migrate.Entry) string {
	var status struct {
		ModelTier struct {
			Source string `json:"source"`
		} `json:"modelTier"`
	}
	s.Require().NoError(json.Unmarshal(e["status"], &status))
	return status.ModelTier.Source
}

func (s *modelTierSourceJobSuite) TestAStoredRowBecomesTheJobSpelling() {
	got, err := modelTierSourceJob(s.entry(`{"spec":{},"status":{"modelTier":{"name":"bounded","source":"Row"}}}`))

	s.Require().NoError(err)
	s.Equal(wireTierSource.wire[agent.ModelTierSourceJob], s.source(got))
}

func (s *modelTierSourceJobSuite) TestAnotherSourceIsKept() {
	got, err := modelTierSourceJob(s.entry(`{"spec":{},"status":{"modelTier":{"source":"Matrix"}}}`))

	s.Require().NoError(err)
	s.Equal("Matrix", s.source(got))
}

func (s *modelTierSourceJobSuite) TestAnEntryWithNoStatusIsKept() {
	got, err := modelTierSourceJob(s.entry(`{"spec":{}}`))

	s.Require().NoError(err)
	s.NotContains(got, "status")
}

func (s *modelTierSourceJobSuite) TestAnEntryWithNoModelTierIsKept() {
	got, err := modelTierSourceJob(s.entry(`{"spec":{},"status":{"created":"2026-11-13T00:00:00Z"}}`))

	s.Require().NoError(err)
	s.Empty(s.source(got))
}
