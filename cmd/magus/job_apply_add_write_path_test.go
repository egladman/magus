package main

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

type jobApplyAddWritePathSuite struct {
	suite.Suite
	root  string
	store *job.Store
}

func TestJobApplyAddWritePath(t *testing.T) { suite.Run(t, new(jobApplyAddWritePathSuite)) }

func (s *jobApplyAddWritePathSuite) SetupTest() {
	row := leaseRow("w", "")
	row.State, row.WritePaths = types.StateRunning, []string{"a.go"}
	row.Check = &types.LeaseCheck{Target: "test", Project: "."}
	row.Validation = row.Check.String()
	root, cacheDir := execFixture(s.T(), row)
	s.root = root
	s.store = job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: cacheDir, Root: root})
}

func (s *jobApplyAddWritePathSuite) apply(add ...string) []job.Applied {
	rec, err := widenedDeclaration(s.store, "w", add)
	s.Require().NoError(err)
	applied, err := job.Apply(s.T().Context(), s.store, []types.Declaration{rec}, globalCfg.Jobs, nil, func() string { return "" }, false)
	s.Require().NoError(err)
	return applied
}

func (s *jobApplyAddWritePathSuite) TestARunningRowWidenedStaysRunningWithTheNewPath() {
	applied := s.apply("b.go", "c.go")

	s.Require().Len(applied, 1)
	s.Equal(types.StateRunning, applied[0].Next.State)
	s.Equal([]string{"a.go", "b.go", "c.go"}, applied[0].Next.WritePaths)
	rows, err := s.store.List()
	s.Require().NoError(err)
	s.Equal(types.StateRunning, rows[0].State)
	s.Equal([]string{"a.go", "b.go", "c.go"}, rows[0].WritePaths)
}

func (s *jobApplyAddWritePathSuite) TestAPathTheRowHoldsChangesNothing() {
	applied := s.apply("a.go")

	s.Require().Len(applied, 1)
	s.Empty(applied[0].Changed)
}

func (s *jobApplyAddWritePathSuite) TestNamingRecordsAsWellIsAUsageError() {
	s.Require().Error(jobApply(s.T().Context(), s.root, []string{"w", "--add-write-path", "b.go", "-f", "-"}))
}

func (s *jobApplyAddWritePathSuite) TestWithoutAnIdIsAUsageError() {
	s.Require().Error(jobApply(s.T().Context(), s.root, []string{"--add-write-path", "b.go"}))
}

func (s *jobApplyAddWritePathSuite) TestAnUnknownJobIsRefused() {
	_, err := widenedDeclaration(s.store, "nope", []string{"b.go"})

	s.Require().Error(err)
}
