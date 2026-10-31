package main

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

type ForkJudgedPathSuite struct {
	suite.Suite
	store *job.Store
}

func TestForkJudgedPathSuite(t *testing.T) {
	suite.Run(t, new(ForkJudgedPathSuite))
}

func (s *ForkJudgedPathSuite) SetupTest() {
	root := testkit.NewRepository(s.T(), map[string]string{"magus.yaml": "{}\n"}).Root()
	s.store = job.NewStore(job.Location{StateBase: s.T().TempDir(), CacheDir: s.T().TempDir(), Root: root})
}

func (s *ForkJudgedPathSuite) seed(state types.JobState) {
	_, err := s.store.Update(s.T().Context(), "w", func(u *types.Job) {
		*u = types.Job{ID: "w", State: state, ReadOnly: true}
	})
	s.Require().NoError(err)
}

func (s *ForkJudgedPathSuite) TestALiveRowIsJudgedAsAnUpdate() {
	s.seed(types.StateDeclared)
	s.True(noteForkJudgedAsUpdate(s.T().Context(), s.store, "w"))
}

func (s *ForkJudgedPathSuite) TestAnEndedRowIsJudgedWhole() {
	s.seed(types.StateNoReturn)
	s.False(noteForkJudgedAsUpdate(s.T().Context(), s.store, "w"))
}

func (s *ForkJudgedPathSuite) TestAnUnknownRowIsJudgedWhole() {
	s.False(noteForkJudgedAsUpdate(s.T().Context(), s.store, "nobody"))
}
