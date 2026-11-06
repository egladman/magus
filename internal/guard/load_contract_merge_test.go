package guard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

// MergeOfBuildSuite puts a branch with its own load-contract change
// mid-merge of a commit a ./magus was built at.
type MergeOfBuildSuite struct {
	suite.Suite
	root, base, decls, head string
}

func TestMergeOfBuildSuite(t *testing.T) {
	suite.Run(t, new(MergeOfBuildSuite))
}

func (s *MergeOfBuildSuite) SetupTest() {
	s.root, s.base, s.decls, _ = contractCheckout(s.T())
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "magus"), []byte("#!/bin/sh\n"), 0o755))
	s.git("checkout", "-q", "-b", "branch")
	dir := filepath.Join(s.root, "internal/spell/gen/decls")
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "branch.buzz"), []byte("export fun y() > void {}\n"), 0o644))
	s.git("add", "-A")
	s.git("commit", "-q", "-m", "branch")
	s.head = s.git("rev-parse", "HEAD")
	s.git("merge", "-q", "--no-commit", "--no-ff", s.decls)
}

func (s *MergeOfBuildSuite) git(args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", s.root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
	s.Require().NoError(err, "%s", out)
	return strings.TrimSpace(string(out))
}

func (s *MergeOfBuildSuite) judge(built, line string) string {
	deps := Dependencies{Binary: filepath.Join(s.root, "magus"), BinaryCommit: built, BinaryVersion: "v0-test",
		Revision: func(context.Context, string, string) string { return s.head }}
	at := location{workspace: s.root, dir: s.root, cacheDir: s.T().TempDir(), policies: magusRepoAt.policies}
	return denyContractSkew(s.T().Context(), deps, at, line, DialectBash)
}

func (s *MergeOfBuildSuite) TestResolveRunsWhileTheIncomingSideIsTheBuild() {
	s.Empty(s.judge(s.decls, "./magus vcs resolve"))
}

func (s *MergeOfBuildSuite) TestAddRunsWhileTheIncomingSideIsTheBuild() {
	s.Empty(s.judge(s.decls, "git add internal/spell/gen/decls/branch.buzz"))
}

func (s *MergeOfBuildSuite) TestTheMergeCommitRunsWhileTheIncomingSideIsTheBuild() {
	s.Empty(s.judge(s.decls, `git commit -m "merge main"`))
}

func (s *MergeOfBuildSuite) TestAnAmendIsStillRefused() {
	s.NotEmpty(s.judge(s.decls, "git commit --amend --no-edit"))
}

func (s *MergeOfBuildSuite) TestAnyOtherChangeIsStillRefused() {
	s.NotEmpty(s.judge(s.decls, "touch notes.txt"))
}

func (s *MergeOfBuildSuite) TestAGitOutsidePathIsRefused() {
	s.NotEmpty(s.judge(s.decls, "./git add internal/spell/gen/decls/branch.buzz"))
	s.NotEmpty(s.judge(s.decls, `/tmp/x/git commit -m "merge main"`))
}

func (s *MergeOfBuildSuite) TestAMagusOutsideThisCheckoutIsRefused() {
	s.NotEmpty(s.judge(s.decls, "/tmp/x/magus vcs resolve"))
}

func (s *MergeOfBuildSuite) TestTheCommitIsRefusedWhenTheBuildIsNeitherSide() {
	s.NotEmpty(s.judge(s.base, `git commit -m "merge main"`))
}
