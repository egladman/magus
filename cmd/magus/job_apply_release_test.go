package main

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
)

type jobApplyReleaseWritePathSuite struct {
	suite.Suite
	root  string
	store *job.Store
}

func TestJobApplyReleaseWritePath(t *testing.T) { suite.Run(t, new(jobApplyReleaseWritePathSuite)) }

func (s *jobApplyReleaseWritePathSuite) SetupTest() {
	row := leaseRow("w", "")
	row.State, row.WritePaths = types.StateRunning, []string{"a.go", "b.go", "c.go#Run"}
	row.Check = &types.LeaseCheck{Target: "test", Project: "."}
	row.Validation = row.Check.String()
	row.Goals = []types.Goal{{ID: "g", Kind: types.GoalKindPaths, Paths: []string{"a.go"}}}
	root, cacheDir := execFixture(s.T(), row)
	s.root = root
	s.store = job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: cacheDir, Root: root})
}

func (s *jobApplyReleaseWritePathSuite) release(paths ...string) types.Declaration {
	held, err := widenedDeclaration(s.store, "w", nil)
	s.Require().NoError(err)
	rec, err := narrowedDeclaration(held, "w", paths)
	s.Require().NoError(err)
	return rec
}

func (s *jobApplyReleaseWritePathSuite) TestAReleasedPathLeavesTheRestOfTheSpec() {
	rec := s.release("b.go")

	s.Equal([]string{"a.go", "c.go#Run"}, rec.WritePaths)
	s.Len(rec.Goals, 1)
	s.NotNil(rec.Check)
}

func (s *jobApplyReleaseWritePathSuite) TestADeclarationClaimIsReleasedByItsWholeValue() {
	s.Equal([]string{"a.go", "b.go"}, s.release("c.go#Run").WritePaths)
}

func (s *jobApplyReleaseWritePathSuite) TestAPathTheJobLacksIsAUsageError() {
	held, err := widenedDeclaration(s.store, "w", nil)
	s.Require().NoError(err)

	_, err = narrowedDeclaration(held, "w", []string{"nope.go"})

	s.Require().Error(err)
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *jobApplyReleaseWritePathSuite) TestNamingRecordsAsWellIsAUsageError() {
	s.Require().Error(jobApply(s.T().Context(), s.root, []string{"w", "--release-write-path", "b.go", "--stdin"}))
}

func (s *jobApplyReleaseWritePathSuite) TestWithoutAnIdIsAUsageError() {
	s.Require().Error(jobApply(s.T().Context(), s.root, []string{"--release-write-path", "b.go"}))
}

func (s *jobApplyReleaseWritePathSuite) TestAClaimWithACommaIsOnePath() {
	var paths claimListFlag

	s.Require().NoError(paths.Set("a.go#Run,Stop"))
	s.Require().NoError(paths.Set("b.go,c.go"))

	s.Equal(claimListFlag{"a.go#Run,Stop", "b.go", "c.go"}, paths)
}
