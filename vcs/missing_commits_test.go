package vcs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
)

const lockLines = "1\n2\n3\n4\n5\n6\n7\n8\n9\n"

type missingCommitsSuite struct {
	suite.Suite
	repo   *testkit.Repo
	called string
	main   string
}

func TestMissingCommits(t *testing.T) { suite.Run(t, new(missingCommitsSuite)) }

func (s *missingCommitsSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{
		".gitattributes": "lock merge=magus\n",
		"lock":           lockLines,
	})
	s.called = filepath.Join(s.T().TempDir(), "driver-called")
	s.repo.Git("config", "merge.magus.driver", fmt.Sprintf("touch %q; exit 9", s.called))
	s.main = s.repo.Git("symbolic-ref", "--short", "HEAD")
	s.repo.Git("switch", "-q", "-c", "side")
	s.repo.Commit("edit top", map[string]string{"lock": "one\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Git("switch", "-q", s.main)
}

func (s *missingCommitsSuite) missing() int {
	got, err := gitVCS{}.MissingCommits(s.T().Context(), s.repo.Root(), s.main, "side")
	s.Require().NoError(err)
	return got
}

func (s *missingCommitsSuite) TestABranchWithNoCopyIsMissingItsCommits() {
	s.Equal(1, s.missing())
}

func (s *missingCommitsSuite) TestAnAncestorIsHeld() {
	s.repo.Git("merge", "-q", "--ff-only", "side")

	s.Zero(s.missing())
}

func (s *missingCommitsSuite) TestASquashIsHeldByContent() {
	s.repo.Commit("squash", map[string]string{"lock": "one\n2\n3\n4\n5\n6\n7\n8\n9\n"})

	s.Zero(s.missing())
}

func (s *missingCommitsSuite) TestAReplayIsHeldAfterTheBranchEditedTheFile() {
	s.repo.Git("cherry-pick", "side")
	s.repo.Commit("edit bottom", map[string]string{"lock": "one\n2\n3\n4\n5\n6\n7\n8\nnine\n"})

	s.Zero(s.missing())
}

func (s *missingCommitsSuite) TestRevertedWorkReadsAsHeld() {
	s.repo.Git("cherry-pick", "side")
	s.repo.Git("revert", "--no-edit", "HEAD")

	s.Zero(s.missing())
}

func (s *missingCommitsSuite) TestARewrittenSquashLaterEditedReadsAsMissing() {
	s.repo.Commit("squash", map[string]string{"lock": "one\n2\n3\n4\n5\n6\n7\n8\n9\n"})
	s.repo.Commit("edit bottom", map[string]string{"lock": "one\n2\n3\n4\n5\n6\n7\n8\nnine\n"})

	s.Equal(1, s.missing())
}

func (s *missingCommitsSuite) TestNoDriverRunsAndNoTempFileAppears() {
	s.repo.Commit("edit bottom", map[string]string{"lock": "1\n2\n3\n4\n5\n6\n7\n8\nnine\n"})

	s.Equal(1, s.missing())

	s.NoFileExists(s.called)
	leftovers, err := filepath.Glob(filepath.Join(s.repo.Root(), ".merge_file_*"))
	s.Require().NoError(err)
	s.Empty(leftovers)
}

func (s *missingCommitsSuite) TestABranchFarAheadCostsNoGitCallPerCommit() {
	for i := range 5 {
		s.repo.Commit(fmt.Sprintf("ahead %d", i), map[string]string{"ahead.txt": strconv.Itoa(i)})
	}
	real, err := exec.LookPath("git")
	s.Require().NoError(err)
	shim := s.T().TempDir()
	calls := filepath.Join(shim, "calls")
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %q\nexec %q \"$@\"\n", calls, real)
	s.Require().NoError(os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755))
	s.T().Setenv("PATH", fmt.Sprintf("%s%c%s", shim, os.PathListSeparator, os.Getenv("PATH")))

	s.Equal(1, s.missing())

	logged, err := os.ReadFile(calls)
	s.Require().NoError(err)
	logs := 0
	for line := range strings.Lines(string(logged)) {
		if slices.Contains(strings.Fields(line), "log") {
			logs++
		}
	}
	s.Equal(2, logs, "one log per side of the range:\n%s", logged)
}

func (s *missingCommitsSuite) TestAnUnknownRevisionIsAnError() {
	_, err := gitVCS{}.MissingCommits(s.T().Context(), s.repo.Root(), s.main, "no-such-revision")

	s.Error(err)
}
