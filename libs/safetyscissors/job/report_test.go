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
	s.declare(checkPostconditionAccepting("tests", BasisReported))

	_, err := s.store.ReportRun(s.T().Context(), Report{Job: "j1", Postcondition: "tests", Check: testCheck(), By: " "})

	refused, ok := errors.AsType[*ReportError](err)
	s.Require().True(ok, err)
	s.Equal(ReportError{Job: "j1", Postcondition: "tests"}, *refused)
}

func (s *ReportSuite) TestAReportOfAnUndeclaredPostconditionIsRefused() {
	s.declare(checkPostconditionAccepting("tests", BasisReported))

	_, err := s.store.ReportRun(s.T().Context(), Report{Job: "j1", Postcondition: "nope", Check: testCheck(), By: "worker"})

	refused, ok := errors.AsType[*PostconditionError](err)
	s.Require().True(ok, err)
	s.Empty(refused.Kind)
}

func (s *ReportSuite) TestAReportOfAPathsPostconditionIsRefused() {
	s.declare(Postcondition{ID: "docs", Kind: PostconditionKindPaths, Expect: ExpectChanged, Paths: []string{"docs"}})

	_, err := s.store.ReportRun(s.T().Context(), Report{Job: "j1", Postcondition: "docs", By: "worker"})

	refused, ok := errors.AsType[*PostconditionError](err)
	s.Require().True(ok, err)
	s.Equal(PostconditionKindPaths, refused.Kind)
}

func (s *ReportSuite) TestTheLeasesOwnCheckIsAPostconditionThatAcceptsOnlyAWitnessedRun() {
	_, err := s.store.Update(s.T().Context(), "j1", func(j *Job) { j.Check = testCheck() })
	s.Require().NoError(err)
	ref, err := s.store.ReportRun(s.T().Context(), Report{Job: "j1", Postcondition: PrimaryPostconditionID, Check: testCheck(), By: "worker"})
	s.Require().NoError(err)

	g, err := s.store.Grade(s.T().Context(), "j1", map[string]RunRef{PrimaryPostconditionID: ref})

	s.Require().NoError(err)
	s.Require().Len(g.Postconditions, 1)
	s.Contains(g.Postconditions[0].Violations, "the run is reported and this postcondition accepts only [witnessed]")
}

func (s *ReportSuite) TestATreeThatCannotBeDigestedIsATreeError() {
	s.declare(checkPostconditionAccepting("tests", BasisReported))
	cause := errors.New("no revision")
	st := s.open(WithTree(func(context.Context) (string, error) { return "", cause }))

	_, err := st.ReportRun(s.T().Context(), Report{Job: "j1", Postcondition: "tests", Check: testCheck(), By: "worker"})

	refused, ok := errors.AsType[*TreeError](err)
	s.Require().True(ok, err)
	s.Equal("for the report", refused.When)
	s.ErrorIs(err, cause)
}
