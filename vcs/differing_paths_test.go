package vcs

import (
	"context"
	"fmt"
	"io/fs"
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

type unreadableRevision struct {
	treeless
	revision string
}

func (r unreadableRevision) ReadFileAt(ctx context.Context, root, rev, path string) (string, error) {
	if r.revision == "" || rev == r.revision {
		return "", fmt.Errorf("read revision: %w", fs.ErrPermission)
	}
	return r.gitVCS.ReadFileAt(ctx, root, rev, path)
}

func (s *differingPathsSuite) TestTwoFailedReadsDoNotProveEqualContent() {
	got, err := DifferingPaths(s.T().Context(), unreadableRevision{}, s.repo.Root(), s.a, s.b, []string{"same.txt"})
	s.ErrorIs(err, fs.ErrPermission)
	s.Nil(got)
}

func (s *differingPathsSuite) TestAFailedEarlierReadDoesNotProveADeletion() {
	got, err := DifferingPaths(s.T().Context(), unreadableRevision{revision: s.a}, s.repo.Root(), s.a, s.b, []string{"same.txt"})
	s.ErrorIs(err, fs.ErrPermission)
	s.Nil(got)
}

func (s *differingPathsSuite) TestAFailedLaterReadDoesNotProveAnAddition() {
	got, err := DifferingPaths(s.T().Context(), unreadableRevision{revision: s.b}, s.repo.Root(), s.a, s.b, []string{"same.txt"})
	s.ErrorIs(err, fs.ErrPermission)
	s.Nil(got)
}

func (s *differingPathsSuite) TestAMissingRevisionPathHasATypedError() {
	_, err := (gitVCS{}).ReadFileAt(s.T().Context(), s.repo.Root(), s.a, "on-neither.txt")
	s.ErrorIs(err, ErrObjectMissing)
}

func (s *differingPathsSuite) TestAnInvalidRevisionIsNotAMissingPath() {
	_, err := (gitVCS{}).ReadFileAt(s.T().Context(), s.repo.Root(), "not-a-revision", "same.txt")
	s.Error(err)
	s.NotErrorIs(err, ErrObjectMissing)
}

// pairwise answers as git does without comparing many trees at once, as
// hg, jj and sapling do.
type pairwise struct{ revisionReader }

func (s *differingPathsSuite) byRevision() map[string][]string {
	return map[string][]string{s.a: s.paths(), s.b: s.paths(), "f00dfeed": s.paths()}
}

func (s *differingPathsSuite) TestEachRevisionIsComparedToTheHead() {
	got, err := DifferingPathsByRevision(s.T().Context(), gitVCS{}, s.repo.Root(), s.b, s.byRevision())

	s.Require().NoError(err)
	s.Equal(map[string][]string{s.a: {"edited.txt", "removed.txt", "added.txt"}, s.b: nil}, got)
}

func (s *differingPathsSuite) TestARevisionIsComparedOnlyOnItsOwnPaths() {
	got, err := DifferingPathsByRevision(s.T().Context(), gitVCS{}, s.repo.Root(), s.b, map[string][]string{s.a: {"same.txt", "added.txt"}})

	s.Require().NoError(err)
	s.Equal(map[string][]string{s.a: {"added.txt"}}, got)
}

func (s *differingPathsSuite) TestTwoNamesForOneCommitAreBothAnswered() {
	got, err := DifferingPathsByRevision(s.T().Context(), gitVCS{}, s.repo.Root(), s.b, map[string][]string{s.a: {"edited.txt"}, s.a[:12]: {"edited.txt"}})

	s.Require().NoError(err)
	s.Equal(map[string][]string{s.a: {"edited.txt"}, s.a[:12]: {"edited.txt"}}, got)
}

func (s *differingPathsSuite) TestAModeChangeAloneDiffers() {
	s.repo.Git("update-index", "--chmod=+x", "same.txt")
	s.repo.Git("commit", "-q", "-m", "mode")

	got, err := DifferingPathsByRevision(s.T().Context(), gitVCS{}, s.repo.Root(), s.repo.Head(), map[string][]string{s.b: {"same.txt"}})

	s.Require().NoError(err)
	s.Equal(map[string][]string{s.b: {"same.txt"}}, got)
}

func (s *differingPathsSuite) TestABackendAskedARevisionAtATimeGivesTheSameAnswer() {
	batched, err := DifferingPathsByRevision(s.T().Context(), gitVCS{}, s.repo.Root(), s.b, s.byRevision())
	s.Require().NoError(err)

	paired, err := DifferingPathsByRevision(s.T().Context(), pairwise{gitVCS{}}, s.repo.Root(), s.b, s.byRevision())

	s.Require().NoError(err)
	s.Equal(batched, paired)
}

func (s *differingPathsSuite) TestAHeadTheRepositoryLacksIsAnError() {
	_, err := DifferingPathsByRevision(s.T().Context(), gitVCS{}, s.repo.Root(), "f00dfeed", s.byRevision())

	s.ErrorIs(err, ErrObjectMissing)
}

func (s *differingPathsSuite) TestAHeadARevisionAtATimeLacksIsAnError() {
	_, err := DifferingPathsByRevision(s.T().Context(), pairwise{gitVCS{}}, s.repo.Root(), "f00dfeed", s.byRevision())

	s.Error(err)
}

func (s *differingPathsSuite) TestAnEndedContextIsAnErrorForManyRevisions() {
	spent, cancel := context.WithCancel(s.T().Context())
	cancel()

	_, err := DifferingPathsByRevision(spent, gitVCS{}, s.repo.Root(), s.b, s.byRevision())

	s.Error(err)
}

func (s *differingPathsSuite) TestAnEndedContextIsAnErrorARevisionAtATime() {
	spent, cancel := context.WithCancel(s.T().Context())
	cancel()

	_, err := DifferingPathsByRevision(spent, pairwise{gitVCS{}}, s.repo.Root(), s.b, s.byRevision())

	s.Error(err)
}

func (s *differingPathsSuite) TestNoRevisionsAskNothing() {
	got, err := DifferingPathsByRevision(s.T().Context(), brokenTrees{}, s.repo.Root(), "f00dfeed", nil)

	s.Require().NoError(err)
	s.Empty(got)
}
