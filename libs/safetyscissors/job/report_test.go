package job

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ReportSuite struct{ gradingSuite }

func TestReportSuite(t *testing.T) { suite.Run(t, new(ReportSuite)) }

func (s *ReportSuite) TestAReportNamesWhoRanIt() {
	s.declare(checkGoalAccepting("tests", BasisReported))

	_, err := s.store.ReportRun(s.T().Context(), Report{Job: "j1", Goal: "tests", Check: testCheck(), By: " "})

	refused, ok := errors.AsType[*ReportError](err)
	s.Require().True(ok, err)
	s.Equal(ReportError{Job: "j1", Goal: "tests"}, *refused)
}

func (s *ReportSuite) TestAReportOfAnUndeclaredGoalIsRefused() {
	s.declare(checkGoalAccepting("tests", BasisReported))

	_, err := s.store.ReportRun(s.T().Context(), Report{Job: "j1", Goal: "nope", Check: testCheck(), By: "worker"})

	refused, ok := errors.AsType[*GoalError](err)
	s.Require().True(ok, err)
	s.Empty(refused.Kind)
}

func (s *ReportSuite) TestAReportOfAPathsGoalIsRefused() {
	s.declare(Goal{ID: "docs", Kind: GoalKindPaths, Expect: ExpectChanged, Paths: []string{"docs"}})

	_, err := s.store.ReportRun(s.T().Context(), Report{Job: "j1", Goal: "docs", By: "worker"})

	refused, ok := errors.AsType[*GoalError](err)
	s.Require().True(ok, err)
	s.Equal(GoalKindPaths, refused.Kind)
}

func (s *ReportSuite) TestTheLeasesOwnCheckIsAGoalThatAcceptsOnlyAWitnessedRun() {
	_, err := s.store.Update(s.T().Context(), "j1", func(j *Job) { j.Check = testCheck() })
	s.Require().NoError(err)
	ref, err := s.store.ReportRun(s.T().Context(), Report{Job: "j1", Goal: PrimaryGoalID, Check: testCheck(), By: "worker"})
	s.Require().NoError(err)

	g, err := s.store.Grade(s.T().Context(), "j1", map[string]RunRef{PrimaryGoalID: ref})

	s.Require().NoError(err)
	s.Require().Len(g.Goals, 1)
	s.Contains(g.Goals[0].Violations, "the run is reported and this goal accepts only [witnessed]")
}

func (s *ReportSuite) TestATreeThatCannotBeDigestedIsATreeError() {
	s.declare(checkGoalAccepting("tests", BasisReported))
	cause := errors.New("no revision")
	st := s.open(WithTree(func(context.Context) (string, error) { return "", cause }))

	_, err := st.ReportRun(s.T().Context(), Report{Job: "j1", Goal: "tests", Check: testCheck(), By: "worker"})

	refused, ok := errors.AsType[*TreeError](err)
	s.Require().True(ok, err)
	s.Equal("for the report", refused.When)
	s.ErrorIs(err, cause)
}
