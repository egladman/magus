package guard

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// UnloadableCommitSuite judges commits while the working tree's
// magusfile does not load and the approved one still does.
type UnloadableCommitSuite struct {
	suite.Suite
	layers   ctxLayers
	cacheDir string
	deps     Dependencies
}

func TestUnloadableCommitSuite(t *testing.T) {
	suite.Run(t, new(UnloadableCommitSuite))
}

func (s *UnloadableCommitSuite) SetupTest() {
	s.layers, s.cacheDir = spawnLayers(s.T())
	approved := (&commandRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAllow}}).rule()
	s.deps = Dependencies{
		LoadFailure:         errMissingMember,
		ApprovedCommandRule: func(context.Context) (workspace.CommandRule, error) { return approved, nil },
	}
}

func (s *UnloadableCommitSuite) judge(line string) types.GuardJudgment {
	return Judge(s.ctx(), s.deps, Request{Input: line, Host: "claude-code", Session: line})
}

// commitSay is the verdict a commit spelled verb opens with.
func commitSay(verb string) string {
	return fmt.Sprintf("`%s` waits until this checkout's magusfile loads; regenerate what the change's sources produce with `%s`, then commit the outputs with the source.",
		verb, hint.Affected.With("generate:rw"))
}

func (s *UnloadableCommitSuite) TestAGitCommitWaitsForTheLoad() {
	stored := shortDeny(s.T(), s.cacheDir, s.judge("git commit -m x"), denyRulePolicyUnloaded, commitSay("git commit"))
	s.Contains(stored, "The working tree's magusfile does not load:\n  "+errMissingMember.Error())
}

func (s *UnloadableCommitSuite) TestAnHgCommitWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("hg commit -m x"), denyRulePolicyUnloaded, commitSay("hg commit"))
}

func (s *UnloadableCommitSuite) TestAJJCommitWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("jj commit -m x"), denyRulePolicyUnloaded, commitSay("jj commit"))
}

func (s *UnloadableCommitSuite) TestACherryPickContinueWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("git cherry-pick --continue"), denyRulePolicyUnloaded, commitSay("git cherry-pick --continue"))
}

func (s *UnloadableCommitSuite) TestAMergeContinueWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("git merge --continue"), denyRulePolicyUnloaded, commitSay("git merge --continue"))
}

func (s *UnloadableCommitSuite) TestARebaseContinueWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("git rebase --continue"), denyRulePolicyUnloaded, commitSay("git rebase --continue"))
}

func (s *UnloadableCommitSuite) TestARevertContinueWaitsForTheLoad() {
	shortDeny(s.T(), s.cacheDir, s.judge("git revert --continue"), denyRulePolicyUnloaded, commitSay("git revert --continue"))
}

func (s *UnloadableCommitSuite) TestAnAbortPasses() {
	v := s.judge("git cherry-pick --abort")
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *UnloadableCommitSuite) TestAnEditStandsOnTheApprovedRule() {
	v := s.judge("touch notes.txt")
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *UnloadableCommitSuite) TestACommitPassesOnceTheTreeLoads() {
	s.deps.LoadFailure = nil
	v := s.judge("git commit -m x")
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *UnloadableCommitSuite) TestACommitInAnotherCheckoutPasses() {
	v := s.judge(fmt.Sprintf("git -C %s commit -m x", shellQuoted(s.T().TempDir())))
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *UnloadableCommitSuite) ctx() context.Context { return s.layers.of(s.T()) }
