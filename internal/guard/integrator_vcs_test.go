package guard

import (
	"context"
	"fmt"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// IntegratorVCSSuite pins what a landing checkout's integrator runs there.
// It rebases, cherry-picks and reverts on its own branch, and pushes nothing.
// Rows 498, 559, 1267 and the 11-07 restack deadlock.
type IntegratorVCSSuite struct {
	suite.Suite
	at      location
	deps    Dependencies
	repo    worktreeRepo
	row     types.Job
	landing string
}

func TestIntegratorVCSSuite(t *testing.T) { suite.Run(t, new(IntegratorVCSSuite)) }

func (s *IntegratorVCSSuite) SetupTest() {
	s.repo = newWorktreeRepository(s.T())
	s.landing = s.repo.add("land-job")
	s.row = narrowLease()
	s.row.ID = "land-job"
	s.row.Parent = "orchestrator"
	s.row.CheckoutRoot = s.landing
	s.row.Integrates = []string{"done-a"}
	s.integrateAs("orchestrator/integrate " + s.row.ID)
}

func (s *IntegratorVCSSuite) integrateAs(title string) {
	ctx, deps := integratorFixture(s.T(), s.row, title)
	s.at, s.deps = ledgerAt(s.T(), ctx), deps
	s.deps.callDir = s.landing
}

func (s *IntegratorVCSSuite) judge(command string) string {
	return denyLeaseScopedVCS(s.ledgerContext(), s.deps, s.row.ID, command)
}

func (s *IntegratorVCSSuite) TestRebasesItsCheckoutOntoItsBase() {
	s.repo.commit(s.landing)
	for _, command := range []string{
		"git rebase origin/main",
		"git rebase -q origin/main",
		"git rebase --onto origin/main HEAD~1",
		"git rebase done-a",
		"git rebase --continue",
		"git rebase --abort",
		"git rebase --skip",
	} {
		s.Empty(s.judge(command), "%q", command)
	}
}

func (s *IntegratorVCSSuite) TestCherryPicksAnIntegratedCommitOutsideItsWritePaths() {
	done := s.repo.add("done-a")
	picked := s.repo.commit(done)
	s.Empty(s.judge("git cherry-pick "+picked), "the integrator lands other rows' paths")
}

func (s *IntegratorVCSSuite) TestRevertsACommitOnItsBranch() {
	s.repo.commit(s.landing)
	s.Empty(s.judge("git revert --no-edit HEAD"))
}

func (s *IntegratorVCSSuite) TestKeepsItsRoleWhenResumedOnANewJob() {
	s.integrateAs("orchestrator/integrate an-earlier-integration")
	s.Empty(s.judge("git rebase origin/main"), "a continue keeps the first title; the row says it integrates")
}

func (s *IntegratorVCSSuite) TestIsRefusedARebaseOntoAnythingButItsBase() {
	reason := s.judge("git rebase some-other-branch")
	s.Contains(reason, "It rebases onto some-other-branch")
	s.Contains(reason, "origin/main")
}

func (s *IntegratorVCSSuite) TestIsRefusedARebaseThatRunsOrMovesMore() {
	for _, command := range []string{
		"git rebase",
		"git rebase -x 'make' origin/main",
		"git rebase --exec=make origin/main",
		"git rebase --update-refs origin/main",
		"git rebase --autostash origin/main",
		"git rebase --root",
		"git rebase origin/main done-a",
		"git rebase --onto origin/main HEAD~1 done-a",
	} {
		s.NotEmpty(s.judge(command), "%q", command)
	}
}

func (s *IntegratorVCSSuite) TestIsRefusedOutsideItsCheckout() {
	sibling := s.repo.add("sibling")
	for _, command := range []string{
		fmt.Sprintf("git -C %s rebase origin/main", s.repo.main),
		fmt.Sprintf("cd %s && git rebase origin/main", sibling),
		fmt.Sprintf("git -C %s cherry-pick HEAD", sibling),
		fmt.Sprintf("git -C %s revert HEAD", sibling),
	} {
		s.NotEmpty(s.judge(command), "%q", command)
	}
}

func (s *IntegratorVCSSuite) TestStillLeavesPushResetAndStashToTheRoot() {
	for _, command := range []string{"git push", "git push --force-with-lease", "git reset --hard HEAD", "git stash push -m wip", "git clean -fd"} {
		s.NotEmpty(s.judge(command), "%q", command)
	}
}

func (s *IntegratorVCSSuite) TestAChangeRoleWorkerIsRefusedARebase() {
	s.row.Integrates = nil
	s.integrateAs("orchestrator/feat " + s.row.ID)
	s.NotEmpty(s.judge("git rebase origin/main"))
}

func (s *IntegratorVCSSuite) TestTheRootHoldingTheRowIsRefusedARebase() {
	s.deps.caller.Agent = ""
	s.NotEmpty(s.judge("git rebase origin/main"))
}

func (s *IntegratorVCSSuite) TestMergesEachBranchALiteralLoopNames() {
	s.row.Integrates = []string{"done-a", "done-b"}
	s.integrateAs("orchestrator/integrate " + s.row.ID)
	s.Empty(s.judge(`for b in done-a done-b; do git merge --no-ff --no-edit "$b"; done`), "row 734")
}

func (s *IntegratorVCSSuite) TestIsRefusedALoopMergeOfABranchItDoesNotIntegrate() {
	s.NotEmpty(s.judge(`for b in done-a stray; do git merge --no-ff --no-edit "$b"; done`))
}

func (s *IntegratorVCSSuite) TestIsRefusedALoopMergeOverAListItCannotRead() {
	s.Contains(s.judge(`for b in $(cat branches); do git merge --no-ff --no-edit "$b"; done`), "literal")
}

func (s *IntegratorVCSSuite) TestInstallsTheHarnessInItsCheckout() {
	s.row.CheckoutRoot = ""
	s.integrateAs("orchestrator/integrate " + s.row.ID)
	s.deps.callDir = ""
	s.Empty(denyLeaseScopedHarness(s.ledgerContext(), s.deps, s.row.ID, "magus agent harness install").Say, "row 527")
	worker := s.deps
	worker.caller.Agent = ""
	s.NotEmpty(denyLeaseScopedHarness(s.ledgerContext(), worker, s.row.ID, "magus agent harness install").Say)
}

func (s *IntegratorVCSSuite) ledgerContext() context.Context { return contextWithLedger(s.T(), s.at) }

// IntegrationMergeSpellingsSuite pins how an integrate row names a branch.
type IntegrationMergeSpellingsSuite struct {
	suite.Suite
	row types.Job
}

func TestIntegrationMergeSpellingsSuite(t *testing.T) {
	suite.Run(t, new(IntegrationMergeSpellingsSuite))
}

func (s *IntegrationMergeSpellingsSuite) SetupTest() {
	s.row = types.Job{ID: "integrate", Integrates: []string{"done-a"}}
}

func (s *IntegrationMergeSpellingsSuite) merges(line string) bool {
	cmds, ok := ParseCommands(line)
	s.Require().True(ok, line)
	s.Require().Len(cmds, 1, line)
	return integrationMerge(cmds[0], s.row)
}

func (s *IntegrationMergeSpellingsSuite) TestABareBranchMerges() {
	s.True(s.merges("git merge done-a"))
}

func (s *IntegrationMergeSpellingsSuite) TestAnOriginBranchMerges() {
	s.True(s.merges("git merge --no-ff origin/done-a"))
}

func (s *IntegrationMergeSpellingsSuite) TestAHeadsRefMerges() {
	s.True(s.merges("git merge --no-ff --no-edit refs/heads/done-a"))
}

func (s *IntegrationMergeSpellingsSuite) TestARemoteRefMerges() {
	s.True(s.merges("git merge refs/remotes/origin/done-a"))
}

func (s *IntegrationMergeSpellingsSuite) TestAMessageMerges() {
	s.True(s.merges(`git merge --no-ff -m "merge done-a" done-a`))
	s.True(s.merges(`git merge --message="merge done-a" done-a`))
}

func (s *IntegrationMergeSpellingsSuite) TestAnUnlistedRefIsRefused() {
	s.False(s.merges("git merge origin/other"))
	s.False(s.merges("git merge refs/tags/done-a"))
}

func (s *IntegrationMergeSpellingsSuite) TestAnotherOptionIsRefused() {
	s.False(s.merges("git merge --squash done-a"))
	s.False(s.merges("git merge -X theirs done-a"))
}

func (s *IntegrationMergeSpellingsSuite) TestTwoOperandsAreRefused() {
	s.False(s.merges("git merge done-a origin/done-a"))
}

func (s *IntegrationMergeSpellingsSuite) TestADanglingMessageFlagIsRefused() {
	s.False(s.merges("git merge done-a -m"))
}
