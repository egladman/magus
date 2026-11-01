package vcs

import (
	"context"
	"errors"
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
	"github.com/egladman/magus/types"
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

func (s *missingCommitsSuite) TestADifferentCommitSharingASubjectIsMissing() {
	s.repo.Commit("edit top", map[string]string{"other.txt": "unrelated\n"})

	s.Equal(1, s.missing())
}

func (s *missingCommitsSuite) TestADifferentEditOfTheSameFileSharingASubjectIsMissing() {
	s.repo.Commit("edit top", map[string]string{"lock": "1\n2\n3\n4\n5\n6\n7\n8\nnine\n"})

	s.Equal(1, s.missing())
}

func (s *missingCommitsSuite) TestAFixupFoldedIntoItsCommitIsHeld() {
	s.repo.Git("switch", "-q", "side")
	s.repo.Commit("fixup! edit top", map[string]string{"lock": "one\n2\n3\n4\n5\n6\n7\n8\nnine\n"})
	s.repo.Git("switch", "-q", s.main)
	s.repo.Commit("edit top", map[string]string{"lock": "one\n2\n3\n4\n5\n6\n7\n8\nnine\n"})
	s.repo.Commit("edit middle", map[string]string{"lock": "one\n2\n3\n4\nfive\n6\n7\n8\nnine\n"})

	s.Zero(s.missing())
}

func (s *missingCommitsSuite) TestACommitAndItsRevertLeaveNothingMissing() {
	s.repo.Git("switch", "-q", "side")
	s.repo.Git("revert", "--no-edit", "HEAD")
	s.repo.Git("switch", "-q", s.main)

	s.Zero(s.missing())
}

// treeless declines DiffTrees, as hg, jj and sapling do.
type treeless struct{ gitVCS }

func (treeless) DiffTrees(context.Context, string, string, string) ([]string, error) {
	return nil, &types.VCSUnsupportedError{VCS: "treeless", Capability: types.CapTreeReporter}
}

// brokenTrees has a tree diff that fails rather than declines.
type brokenTrees struct{ gitVCS }

func (brokenTrees) DiffTrees(context.Context, string, string, string) ([]string, error) {
	return nil, errors.New("diff-tree failed")
}

func (s *missingCommitsSuite) TestASquashLandingADeletionIsHeldByEveryBackend() {
	s.repo.Git("switch", "-q", "side")
	s.repo.Git("rm", "-q", "lock")
	s.repo.Git("commit", "-q", "-m", "drop the lock")
	s.repo.Git("switch", "-q", s.main)
	s.repo.Git("rm", "-q", "lock")
	s.repo.Git("commit", "-q", "-m", "squash")

	s.Zero(s.missing())
	got, err := missingCommits(s.T().Context(), treeless{}, s.repo.Root(), s.main, "side")
	s.Require().NoError(err)
	s.Zero(got)
}

func (s *missingCommitsSuite) TestATreeDiffThatFailsProvesNothing() {
	s.repo.Commit("squash", map[string]string{"lock": "one\n2\n3\n4\n5\n6\n7\n8\n9\n"})

	s.False(contentOnBranch(s.T().Context(), brokenTrees{}, s.repo.Root(), s.main, "side"))
}

func (s *missingCommitsSuite) TestAnUnknownRevisionIsAnError() {
	_, err := gitVCS{}.MissingCommits(s.T().Context(), s.repo.Root(), s.main, "no-such-revision")

	s.Error(err)
}

func (s *missingCommitsSuite) missingOn(branch string) int {
	got, err := gitVCS{}.MissingCommits(s.T().Context(), s.repo.Root(), s.main, branch)
	s.Require().NoError(err)
	return got
}

// onBranch runs commit on a new branch off main, then returns to main.
func (s *missingCommitsSuite) onBranch(name string, commit func()) {
	s.repo.Git("switch", "-q", "-c", name, s.main)
	commit()
	s.repo.Git("switch", "-q", s.main)
}

func (s *missingCommitsSuite) TestAPureRenameSharingASubjectIsMissing() {
	s.onBranch("moved", func() {
		s.repo.Git("mv", "lock", "moved-lock")
		s.repo.Git("commit", "-q", "-m", "tidy")
	})
	s.repo.Commit("tidy", map[string]string{"other.txt": "unrelated\n"})

	s.Equal(1, s.missingOn("moved"))
}

func (s *missingCommitsSuite) TestAModeChangeSharingASubjectIsMissing() {
	s.onBranch("runnable", func() {
		s.Require().NoError(os.Chmod(filepath.Join(s.repo.Root(), "lock"), 0o755))
		s.repo.Git("add", "lock")
		s.repo.Git("commit", "-q", "-m", "tidy")
	})
	s.repo.Commit("tidy", map[string]string{"other.txt": "unrelated\n"})

	s.Equal(1, s.missingOn("runnable"))
}

func (s *missingCommitsSuite) TestADifferentEditOfABinarySharingASubjectIsMissing() {
	s.onBranch("art", func() { s.repo.Commit("add art", map[string]string{"art.bin": "\x00ours"}) })
	s.repo.Commit("add art", map[string]string{"art.bin": "\x00theirs"})

	s.Equal(1, s.missingOn("art"))
}

func (s *missingCommitsSuite) TestAReplayOfABinaryIsHeldAfterTheBranchEditedIt() {
	s.onBranch("art", func() { s.repo.Commit("add art", map[string]string{"art.bin": "\x00ours"}) })
	s.repo.Git("cherry-pick", "art")
	s.repo.Commit("redraw", map[string]string{"art.bin": "\x00redrawn"})

	s.Zero(s.missingOn("art"))
}

// idlessBinaries reports every binary as hg and sl can, with no blob ids.
type idlessBinaries struct{ gitVCS }

func (idlessBinaries) RangeDiff(context.Context, string, string, string, []string) (string, error) {
	return "diff --git a/art.bin b/art.bin\nBinary file art.bin has changed\n", nil
}

func (s *missingCommitsSuite) TestABinaryWithNoBlobIDsMatchesNoOtherCommit() {
	ours := types.Commit{ID: "ours", Parents: []string{"p"}}
	theirs := types.Commit{ID: "theirs", Parents: []string{"p"}}

	held, err := heldByCopy(s.T().Context(), idlessBinaries{}, s.repo.Root(), ours, []types.Commit{theirs})

	s.Require().NoError(err)
	s.False(held)
}
