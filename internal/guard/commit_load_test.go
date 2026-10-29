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
	ctx      context.Context
	cacheDir string
	deps     Dependencies
}

func TestUnloadableCommitSuite(t *testing.T) {
	suite.Run(t, new(UnloadableCommitSuite))
}

func (s *UnloadableCommitSuite) SetupTest() {
	s.ctx, s.cacheDir = spawnFixture(s.T())
	approved := (&commandRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAllow}}).rule()
	s.deps = Dependencies{
		LoadFailure:         errMissingMember,
		ApprovedCommandRule: func(context.Context) (workspace.CommandRule, error) { return approved, nil },
	}
}

func (s *UnloadableCommitSuite) judge(line string) types.GuardJudgment {
	return Judge(s.ctx, s.deps, Request{Input: line, Host: "claude-code", Session: line})
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
