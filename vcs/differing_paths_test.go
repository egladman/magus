package vcs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
)

type differingPathsSuite struct {
	suite.Suite
	repo *testkit.Repo
	a, b string
}

func TestDifferingPaths(t *testing.T) { suite.Run(t, new(differingPathsSuite)) }

func (s *differingPathsSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{
		"same.txt":    "same\n",
		"edited.txt":  "before\n",
		"removed.txt": "here\n",
	})
	s.a = s.repo.Head()
	s.repo.Commit("change", map[string]string{"edited.txt": "after\n", "added.txt": "new\n"})
	s.repo.Git("rm", "-q", "removed.txt")
	s.repo.Git("commit", "-q", "-m", "remove")
	s.b = s.repo.Head()
}

func (s *differingPathsSuite) paths() []string {
	return []string{"same.txt", "./edited.txt", "removed.txt", "added.txt", "on-neither.txt"}
}

func (s *differingPathsSuite) TestATreeDiffNamesWhatDiffers() {
	got, err := DifferingPaths(s.T().Context(), gitVCS{}, s.repo.Root(), s.a, s.b, s.paths())

	s.Require().NoError(err)
	s.Equal([]string{"edited.txt", "removed.txt", "added.txt"}, got)
}

func (s *differingPathsSuite) TestABackendThatDeclinesTheTreeDiffGivesTheSameAnswer() {
	withTree, err := DifferingPaths(s.T().Context(), gitVCS{}, s.repo.Root(), s.a, s.b, s.paths())
	s.Require().NoError(err)

	withReads, err := DifferingPaths(s.T().Context(), treeless{}, s.repo.Root(), s.a, s.b, s.paths())

	s.Require().NoError(err)
	s.Equal(withTree, withReads)
}

func (s *differingPathsSuite) TestATreeDiffThatFailsIsAnError() {
	_, err := DifferingPaths(s.T().Context(), brokenTrees{}, s.repo.Root(), s.a, s.b, s.paths())

	s.Error(err)
}

func (s *differingPathsSuite) TestAContextThatEndedIsAnErrorOnEveryBackend() {
	spent, cancel := context.WithCancel(s.T().Context())
	cancel()

	_, err := DifferingPaths(spent, treeless{}, s.repo.Root(), s.a, s.b, s.paths())

	s.ErrorIs(err, context.Canceled)
}

func (s *differingPathsSuite) TestNoPathsAsksNothing() {
	got, err := DifferingPaths(s.T().Context(), brokenTrees{}, s.repo.Root(), s.a, s.b, nil)

	s.Require().NoError(err)
	s.Empty(got)
}
